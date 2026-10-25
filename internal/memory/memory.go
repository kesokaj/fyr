// Package memory is a project's memory store, .agents/memory/ at the
// repository root: one file per memory plus the MEMORY.md index. The CLI
// writes and reads it through `fyr memory`, so a memory is taken the
// same way on every machine (R10, plan 2026-09-30-parity-memory-dream).
package memory

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// The index caps: what `memory show` prints of MEMORY.md, so a session loads
// a bounded index however long the file grows.
const (
	maxLines = 200
	maxBytes = 25 * 1024
)

// lockWait is how long Add retries a live lock before refusing.
var lockWait = 5 * time.Second

var (
	slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	types  = map[string]bool{"user": true, "feedback": true, "project": true, "reference": true}
)

// Entry is one memory's frontmatter as the caller gives it.
type Entry struct {
	Type        string // user, feedback, project or reference
	Name        string // the slug: file name and index title
	Description string // one line
}

// refusal is a store the command will not touch, or input it will not take;
// the caller exits 2 with its message.
type refusal struct{ msg string }

func (r *refusal) Error() string { return r.msg }

func refuse(format string, a ...any) error { return &refusal{msg: fmt.Sprintf(format, a...)} }

// IsRefusal reports whether err is a refusal.
func IsRefusal(err error) bool {
	var r *refusal
	return errors.As(err, &r)
}

// Root is the main worktree's top level for the checkout that holds dir, so
// every worktree of a repository shares one store (D26).
func Root(dir string) (string, error) {
	out, err := trail.Command(dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", refuse("not a git checkout: %s", dir)
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return "", refuse("not a git checkout: %s", dir)
	}
	return filepath.Dir(common), nil
}

// trusted returns the store path of root after the checks every command
// makes: the store is the project's own directory, never a link a commit
// could carry and never content git tracks (memory-loop spec, ADR-043).
func trusted(root string) (string, error) {
	agents := filepath.Join(root, ".agents")
	store := filepath.Join(agents, "memory")
	for _, p := range []string{agents, store} {
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", refuse("%s is a symlink: the memory store must be the project's own directory", p)
		}
	}
	out, err := storeTracked(root).Output()
	if err != nil {
		return "", refuse("cannot ask git what it tracks under .agents/memory: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return "", refuse("git tracks files under .agents/memory: the store is the operator's own, never the repository's")
	}
	return store, nil
}

// Store returns root's store path after the trust checks every reader
// makes; `fyr dream` reads a project's memory through it.
func Store(root string) (string, error) { return trusted(root) }

// Add writes or replaces the memory e.Name with body, and its index line.
func Add(dir string, e Entry, body io.Reader, now time.Time) (string, error) {
	switch {
	case !types[e.Type]:
		return "", refuse("--type must be user, feedback, project or reference, not %q", e.Type)
	case !slugRe.MatchString(e.Name):
		return "", refuse("--name must match %s, not %q", slugRe, e.Name)
	case strings.TrimSpace(e.Description) == "":
		return "", refuse("--description is required")
	case strings.ContainsAny(e.Description, "\r\n"):
		return "", refuse("--description must be one line")
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", refuse("the memory's body comes on stdin and is empty")
	}
	root, err := Root(dir)
	if err != nil {
		return "", err
	}
	store, err := trusted(root)
	if err != nil {
		return "", err
	}
	rel := ".agents/memory/" + e.Name + ".md"
	// check-ignore reads the index and would start the project's fsmonitor:
	// it runs through the hardened builder too (BUG-165).
	if trail.Command(root, "check-ignore", "-q", rel).Run() != nil {
		return "", refuse("%s is not ignored by git: run /init-project first", rel)
	}

	// A full index is refused before anything is created; the count is checked
	// again under the lock, which is where the index is read for the write.
	if pre, err := readLines(filepath.Join(store, "MEMORY.md")); err == nil && len(pre) >= maxLines && !hasLine(pre, e.Name) {
		return "", refuse("MEMORY.md already holds %d lines, the most a session loads: run /dream to consolidate", len(pre))
	}

	// The index is read under the lock, so two writers never lose a line.
	if err := os.MkdirAll(store, 0o700); err != nil {
		return "", err
	}
	unlock, err := lock(filepath.Join(store, ".lock"))
	if err != nil {
		return "", err
	}
	defer unlock()

	index := filepath.Join(store, "MEMORY.md")
	lines, err := readLines(index)
	if err != nil {
		return "", err
	}
	line := fmt.Sprintf("- [%s](%s.md) — %s", e.Name, e.Name, e.Description)
	replaced := false
	for i, l := range lines {
		if linkTarget(e.Name).MatchString(l) {
			lines[i], replaced = line, true
		}
	}
	if !replaced {
		if len(lines)+1 > maxLines {
			return "", refuse("MEMORY.md already holds %d lines, the most a session loads: run /dream to consolidate", len(lines))
		}
		lines = append(lines, line)
	}

	desc, _ := json.Marshal(e.Description)
	file := fmt.Sprintf("---\nname: %s\ndescription: %s\nmetadata:\n  node_type: memory\n  type: %s\n  modified: %s\n---\n\n%s\n",
		e.Name, desc, e.Type, now.UTC().Format(time.RFC3339), text)
	path := filepath.Join(store, e.Name+".md")
	if err := harness.WriteAtomic(path, []byte(file), 0o600); err != nil {
		return "", err
	}
	if err := harness.WriteAtomic(index, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// linkTarget matches the index line whose link target is name's file, and
// only it: a description that mentions "(name.md)" is not a match (code review
// 2026-09-30).
func linkTarget(name string) *regexp.Regexp {
	return regexp.MustCompile(`^- \[[^\]]*\]\(` + regexp.QuoteMeta(name) + `\.md\)`)
}

func hasLine(lines []string, name string) bool {
	for _, l := range lines {
		if linkTarget(name).MatchString(l) {
			return true
		}
	}
	return false
}

// Show returns the index, capped at what a session loads.
func Show(dir string) (string, error) {
	root, err := Root(dir)
	if err != nil {
		return "", err
	}
	store, err := trusted(root)
	if err != nil {
		return "", err
	}
	lines, err := readLines(filepath.Join(store, "MEMORY.md"))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i, l := range lines {
		if i == maxLines || b.Len()+len(l)+1 > maxBytes {
			fmt.Fprintf(&b, "(… %d more lines in .agents/memory/MEMORY.md)\n", len(lines)-i)
			break
		}
		b.WriteString(l + "\n")
	}
	return b.String(), nil
}

// readLines returns the non-empty lines of path; a missing file is empty. A
// symlink is refused: the index is the store's own file, and through a link
// Show printed an outside file to the session and Add copied its lines into
// the store (the atomic write replaces the link; nothing was written outside;
// security review 2026-10-01).
func readLines(path string) ([]string, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, refuse("%s is a symlink: the index must be the store's own file", path)
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if l := strings.TrimRight(sc.Text(), "\r"); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, sc.Err()
}

// lock takes an exclusive flock on the store's lock file, retried for
// lockWait, then refused. The kernel drops the lock when the process ends, so
// a crashed writer never leaves a stale lock, and two waiters can never both
// take it (code review 2026-09-30: the pid-file lock had three races). The
// file itself stays; it is empty and gitignored with the store.
func lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, refuse("the memory store is busy: another fyr memory holds %s", path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// storeTracked is the git call that asks what the repository tracks under
// .agents/memory. The nightly dream runs it in every indexed project, so it
// goes through trail.Command: no index lock, no fsmonitor hook, no signature
// program, no lazy fetch (security review of the v2.0.0 gate, 2026-10-01;
// BUG-164, BUG-165).
func storeTracked(root string) *exec.Cmd {
	return trail.Command(root, "ls-files", "--", ".agents/memory")
}
