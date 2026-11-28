package worker

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/kesokaj/fyr/internal/harness"
)

// The drafts copy's caps (D21).
const (
	maxDraft  = 1 << 20
	maxDrafts = 50
	draftsRel = ".agents/scratch/agents"
)

// draftName is the names copied: the lead puts a draft's path on a shell
// command line, so no name may hold a quote, a space or a metacharacter, or
// start like a flag (D138). With Options.Auto, autoDraftsName is copied too.
var draftName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,80}\.md$`)

const autoDraftsName = "auto-drafts.jsonl"

// drafts is step 8: the clone's draft records, gitignored and so not on
// its branch, copied into the lead's .agents/scratch/work/<stamp>/<name>/:
// regular files of one link only, named as draftName says, at most
// maxDrafts of at most maxDraft bytes, 0600. Both sides go through an os.Root, so no link leads the copy
// out of the clone or out of the lead's checkout. False means a copy that
// could not be made: the sandbox, which still holds the drafts, is kept.
func (w *worker) drafts() bool {
	src, err := os.OpenRoot(w.b.App())
	if err != nil {
		return true
	}
	defer src.Close()
	if st, err := src.Lstat(filepath.FromSlash(draftsRel)); errors.Is(err, fs.ErrNotExist) || err == nil && !st.IsDir() {
		return true // no drafts
	} else if err != nil {
		w.b.Note("the drafts were not copied: %v", err)
		return false
	}
	dir, err := src.OpenRoot(filepath.FromSlash(draftsRel))
	if err == nil {
		defer dir.Close()
	}
	var entries []os.DirEntry
	if err == nil {
		entries, err = readDir(dir)
	}
	if err != nil {
		w.b.Note("the drafts were not copied: %v", err)
		return false
	}
	return w.copyDrafts(dir, entries)
}

// copyDrafts writes each accepted entry, making the destination at the
// first one; the skipped are named in a note.
func (w *worker) copyDrafts(dir *os.Root, entries []os.DirEntry) bool {
	var dest *os.Root
	var skipped []string
	for _, e := range entries {
		data, ok := w.acceptedDraft(dir, e)
		if !ok {
			skipped = append(skipped, e.Name())
			continue
		}
		var err error
		dest, err = w.writeOneDraft(dest, e.Name(), data)
		if err != nil {
			w.b.Note("the drafts were not copied: %v", err)
			return false
		}
	}
	if len(skipped) > 0 {
		w.b.Note("drafts not copied (a name not matching %s, not a regular file of one link, over 1 MiB, or past %d): %s", draftName, maxDrafts, named(skipped))
	}
	return true
}

func (w *worker) acceptedDraft(dir *os.Root, e os.DirEntry) ([]byte, bool) {
	isAutoDraft := (w.o.Auto || w.o.DraftsOnly) && e.Name() == autoDraftsName
	if !draftName.MatchString(e.Name()) && !isAutoDraft {
		return nil, false
	}
	if w.res.Drafts == maxDrafts {
		return nil, false
	}
	return readDraft(dir, e)
}

func (w *worker) writeOneDraft(dest *os.Root, name string, data []byte) (*os.Root, error) {
	if dest == nil {
		var err error
		if dest, err = w.leadDir(); err != nil {
			return nil, err
		}
	}
	if err := writeDraft(dest, name, data); err != nil {
		return dest, err
	}
	w.res.Drafts++
	w.res.DraftsDir = w.draftsResultDir()
	return dest, nil
}

func readDir(dir *os.Root) ([]os.DirEntry, error) {
	f, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, err
}

// readDraft is e's content when e is a regular file with no other hard link
// (one could be the real token under another name), the same file when
// opened, of at most maxDraft bytes.
func readDraft(dir *os.Root, e os.DirEntry) ([]byte, bool) {
	named, err := dir.Lstat(e.Name())
	if err != nil || !named.Mode().IsRegular() {
		return nil, false
	}
	if st, ok := named.Sys().(*syscall.Stat_t); !ok || st.Nlink != 1 {
		return nil, false
	}
	f, err := dir.OpenFile(e.Name(), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !os.SameFile(st, named) {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, maxDraft+1))
	return data, err == nil && len(data) <= maxDraft
}

// draftsDest makes .agents/scratch/work/<stamp>/<name> in the lead's
// checkout through an os.Root on it: .agents, scratch, work and <stamp> are
// each an existing directory that is not a link (the workers of one run
// share <stamp>), or are made; <name> is made here, so nothing the session
// planted there is written through.
func draftsDest(root, stamp, name string) (*os.Root, error) {
	lead, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer lead.Close()
	work := filepath.Join(".agents", "scratch", "work")
	for _, s := range []struct {
		p     string
		fresh bool
	}{{".agents", false}, {filepath.Join(".agents", "scratch"), false}, {work, false},
		{filepath.Join(work, stamp), false}, {filepath.Join(work, stamp, name), true}} {
		st, err := lead.Lstat(s.p)
		switch {
		case err == nil && !s.fresh && st.IsDir():
			continue
		case err == nil:
			return nil, fmt.Errorf("%s exists and is not a directory this run made", s.p)
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
		if err := lead.Mkdir(s.p, 0o700); err != nil {
			return nil, err
		}
	}
	return lead.OpenRoot(filepath.Join(work, stamp, name))
}

// writeDraft writes one draft, never over an existing name.
func writeDraft(dest *os.Root, name string, data []byte) error {
	f, err := dest.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (w *worker) draftsResultDir() string {
	if w.o.DraftsOnly {
		return filepath.Join(w.deps.Root(w.o.Home, "sandbox"), "drafts", w.stamp, w.p.k.Name)
	}
	return filepath.ToSlash(filepath.Join(".agents", "scratch", "work", w.stamp, w.p.k.Name)) + "/"
}

func sandboxDraftsDest(home, stamp, name string) (*os.Root, error) {
	hr, err := os.OpenRoot(home)
	if err != nil {
		return nil, err
	}
	defer hr.Close()
	parts := append(strings.Split(filepath.ToSlash(harness.StateDir), "/"), "sandbox", "drafts", stamp, name)
	var cur string
	for idx, seg := range parts {
		if seg == "" || seg == "." {
			continue
		}
		if cur == "" {
			cur = seg
		} else {
			cur = filepath.Join(cur, seg)
		}
		fresh := idx == len(parts)-1
		st, err := hr.Lstat(cur)
		switch {
		case err == nil && !fresh && st.IsDir():
			continue
		case err == nil:
			return nil, fmt.Errorf("%s exists and is not a directory this run made", cur)
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
		if err := hr.Mkdir(cur, 0o700); err != nil {
			return nil, err
		}
	}
	return hr.OpenRoot(cur)
}

func (w *worker) leadDir() (*os.Root, error) {
	if w.dest == nil {
		var (
			d   *os.Root
			err error
		)
		if w.o.DraftsOnly {
			d, err = sandboxDraftsDest(w.o.Home, w.stamp, w.p.k.Name)
		} else {
			d, err = draftsDest(w.o.Root, w.stamp, w.p.k.Name)
		}
		if err != nil {
			return nil, err
		}
		w.dest = d
	}
	return w.dest, nil
}

func (w *worker) closeLeadDir() {
	if w.dest != nil {
		w.dest.Close()
		w.dest = nil
	}
}
