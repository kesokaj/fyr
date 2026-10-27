package harness

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Install writes the planned files under home, removes files a previous
// install wrote that the plan no longer contains, and records the manifest.
// Writing is idempotent: an unchanged file is neither rewritten nor reported.
//
// An install from before ADR-062 listed Claude Code's files under ~/.claude
// (AGENTS.md, the CLAUDE.md stub, skills, agents, commands, rules). This plan
// names none of them, and the resync keeps every one where it is: they leave
// fyr's manifest and are never removed (plan fyr D32: the operator keeps
// Claude Code's files where they still use it). Any other stale entry, the
// old ~/.local/bin/sh-agent among them, goes by the one rule below.
func Install(src Source, home string, files []File, dryRun bool, out io.Writer) error {
	old, _, err := LoadState(home)
	if err != nil {
		return err
	}
	// Before any file is written, so a mode problem is seen first and never
	// interrupts a deployment (security review of the audit, round 2).
	tightenConfigDir(home, dryRun, out)

	r := &installRun{home: home, dryRun: dryRun, out: out, old: old, counts: map[string]int{}}
	for _, f := range files {
		if err := r.writeFile(f); err != nil {
			return err
		}
	}
	if err := r.resync(); err != nil {
		return err
	}
	if err := r.record(src); err != nil {
		return err
	}

	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}
	fmt.Fprintf(out, "%s%d written, %d unchanged, %d removed, %d kept — %d files managed\n",
		prefix, r.counts["written"], r.counts["unchanged"], r.counts["removed"], r.counts["kept"], len(r.manifest))
	return nil
}

// Entry is the state of one deployed path.
type Entry struct {
	Rel   string
	State string // ok | missing | differs | foreign | stale | kept
}

// Report is what `fyr status` prints.
type Report struct {
	Entries []Entry
	Counts  map[string]int
}

// Drift reports whether anything differs from the plan.
func (r Report) Drift() bool {
	return r.Counts["missing"]+r.Counts["differs"]+r.Counts["stale"] > 0
}

// Status compares the plan against home without changing anything.
func Status(home string, files []File) (Report, error) {
	st, _, err := LoadState(home)
	if err != nil {
		return Report{}, err
	}
	r := Report{Counts: map[string]int{}}
	planned := map[string]bool{}
	for _, f := range files {
		planned[f.Rel] = true
		state := "ok"
		// The link is looked at before anything is read: a link's target is
		// not fyr's to read, and one to a FIFO blocked the read (IN27).
		if fi, err := os.Lstat(filepath.Join(home, f.Rel)); err == nil && fi.Mode()&os.ModeSymlink != 0 || underLink(home, f.Rel) {
			state = "foreign" // the operator's link, or a file under one (IN3)
		} else {
			existing, err := readPlain(filepath.Join(home, f.Rel))
			switch {
			case errors.Is(err, os.ErrNotExist):
				state = "missing"
			case errors.Is(err, errNotRegular):
				state = "differs"
			case err != nil:
				return Report{}, err
			case !bytes.Equal(existing, f.Data):
				state = "differs"
			}
		}
		r.Counts[state]++
		r.Entries = append(r.Entries, Entry{Rel: f.Rel, State: state})
	}
	for _, rel := range st.Files {
		// The binary is compared by no one: status and diff plan without it,
		// since its bytes depend on the toolchain, not on the repo.
		if planned[rel] || rel == BinPath {
			continue
		}
		switch fyrOwns(home, rel) {
		case gone:
		case keptClaude:
			// Kept by every install and never removed (plan fyr D32): no
			// drift, since no install would change it (IN32).
			r.Counts["kept"]++
			r.Entries = append(r.Entries, Entry{Rel: rel, State: "kept"})
		default:
			r.Counts["stale"]++
			r.Entries = append(r.Entries, Entry{Rel: rel, State: "stale"})
		}
	}
	return r, nil
}

// errNotRegular is readPlain's answer for a FIFO, a device or a directory.
var errNotRegular = errors.New("not a regular file")

// readPlain reads a deployed path only when it is a regular file: a FIFO
// would block the read and a device would stream, so neither is read
// (IN27). It judges the opened file, not the name (openPlain). A missing
// path is fs.ErrNotExist, as from os.ReadFile.
func readPlain(p string) ([]byte, error) { return openPlain(p) }

// Diff prints a unified diff for every file that differs, using the system
// diff so the output looks like every other diff the operator reads. It
// writes nothing anywhere, under the home or the system temp dir: the plan
// is compared in memory, and a file that differs reaches diff on its stdin
// (IN24, plan audit-fix D8).
func Diff(home string, files []File, out io.Writer) error {
	for _, f := range files {
		dest := filepath.Join(home, f.Rel)
		if fi, err := os.Lstat(dest); err == nil && fi.Mode()&os.ModeSymlink != 0 || underLink(home, f.Rel) {
			continue // the operator's link, or a file under one, is compared by no one (IN3)
		}
		existing, err := readPlain(dest)
		switch {
		case errors.Is(err, os.ErrNotExist):
			fmt.Fprintf(out, "missing ~/%s\n", f.Rel)
			continue
		case errors.Is(err, errNotRegular):
			fmt.Fprintf(out, "differs ~/%s (not a regular file; install replaces it)\n", f.Rel)
			continue
		case err != nil:
			return err
		case bytes.Equal(existing, f.Data):
			continue
		}
		if err := diffOne(f, dest, out); err != nil {
			return err
		}
	}
	return nil
}

// diffOne runs the system diff of the installed file against the plan's
// bytes, which it reads on stdin: no diff algorithm is written here, and no
// temp file either.
func diffOne(f File, dest string, out io.Writer) error {
	cmd := exec.Command("diff", "-u", "-L", "installed ~/"+f.Rel, "-L", "repo ~/"+f.Rel, dest, "-")
	cmd.Stdin = bytes.NewReader(f.Data)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() > 1 {
			return fmt.Errorf("diff %s: %w", f.Rel, err)
		}
	}
	return nil
}

// Uninstall removes every path in the manifest and nothing else, then the
// manifest itself. Directories left empty are removed on the way up.
func Uninstall(home string, dryRun bool, out io.Writer) error {
	st, found, err := LoadState(home)
	if err != nil {
		return err
	}
	if !found {
		fmt.Fprintln(out, "nothing installed (no manifest at ~/.config/fyr/state.json)")
		return nil
	}
	removed, claudeKept := 0, 0
	for _, rel := range st.Files {
		switch fyrOwns(home, rel) {
		case keptClaude:
			claudeKept++
			continue
		case gone:
			continue
		case linked:
			// An old manifest may name a path that is the operator's link
			// now, or sits under one: never fyr's to remove (IN3).
			fmt.Fprintf(out, "keep    ~/%s (a symlink; fyr never writes through a link)\n", rel)
			continue
		}
		dest := filepath.Join(home, rel)
		removed++
		if dryRun {
			fmt.Fprintf(out, "remove  ~/%s\n", rel)
			continue
		}
		if err := os.Remove(dest); err != nil {
			return fmt.Errorf("removing %s: %w", dest, err)
		}
		pruneEmptyDirs(home, filepath.Dir(dest))
	}
	if claudeKept > 0 {
		fmt.Fprintln(out, keepClaudeLine(claudeKept))
	}
	if dryRun {
		fmt.Fprintf(out, "[dry-run] would remove %d files and the manifest\n", removed)
		return nil
	}
	if err := removeState(home); err != nil {
		return err
	}
	fmt.Fprintf(out, "removed %d files and the manifest\n", removed)
	return nil
}

// claudeFile reports whether rel, an entry of a manifest an install before
// ADR-062 wrote, is one of Claude Code's files under ~/.claude that is still
// there. fyr never removes one (plan fyr D32); a file already gone is not
// counted. The entry is judged as the path it removes, cleaned (a hand-edited
// `./.claude/x` or `.gemini/../.claude/x` is the same file), and the first
// element without case, since a case-insensitive file system makes
// `.Claude` the same directory; on another, keeping it errs safe.
func claudeFile(home, rel string) bool {
	first, _, nested := strings.Cut(filepath.ToSlash(filepath.Clean(rel)), "/")
	if !nested || !strings.EqualFold(first, ".claude") {
		return false
	}
	_, err := os.Lstat(filepath.Join(home, rel))
	return err == nil
}

// ownership is what fyr may do with a path a manifest lists.
type ownership int

const (
	owned      ownership = iota // on disk and fyr's: removed when no longer planned
	gone                        // not on disk
	linked                      // a symlink, or under a linked directory: the operator's (IN3)
	keptClaude                  // a ~/.claude file an install before ADR-062 wrote: kept (plan fyr D32)
)

// fyrOwns is the one judgement of a manifest entry, shared by install's
// resync and its save on error, uninstall and status, which each repeated
// the same three-way condition before (IN-S1).
func fyrOwns(home, rel string) ownership {
	if claudeFile(home, rel) {
		return keptClaude
	}
	fi, err := os.Lstat(filepath.Join(home, rel))
	switch {
	case err != nil:
		return gone
	case fi.Mode()&os.ModeSymlink != 0 || underLink(home, rel):
		return linked
	}
	return owned
}

func keepClaudeLine(n int) string {
	return fmt.Sprintf("keep    ~/.claude: %d files an install before fyr wrote for Claude Code; fyr never removes them, and they leave its manifest", n)
}

// WriteAtomic writes through a temp file and rename, so a half-written
// context file is never visible and a running fyr binary can be
// replaced on Linux, where opening it for writing would fail with ETXTBSY.
func WriteAtomic(dest string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".fyr-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return abandon(tmp, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return abandon(tmp, err)
	}
	// Sync before the rename: without it a crash between the two can leave
	// the rename durable and the bytes not, so the deployed file is a
	// zero-length or truncated context that no reader can tell from a real
	// one (security review of the settings write, 2026-09-16, finding 2 —
	// the same pattern was fixed there first).
	if err := tmp.Sync(); err != nil {
		return abandon(tmp, err)
	}
	// Close before the rename, as the settings writer always did: a close
	// failure then leaves dest untouched, so an error from this function
	// always means dest is unchanged; and on Linux a binary renamed into
	// place while still open cannot be executed until the close (ETXTBSY).
	// Round 2 of the 2026-09-30 review had these two lines the other way.
	if err := tmp.Close(); err != nil {
		return abandon(tmp, err)
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return abandon(tmp, err)
	}
	return nil
}

// abandon closes and removes the temp file of a failed write and returns the
// failure, naming the temp file when even its removal fails so nothing is
// left behind unseen (the settings writer said so before it shared this
// helper; code review 2026-09-30).
func abandon(tmp *os.File, err error) error {
	tmp.Close() // already closed on the rename branch, which is harmless; the file is discarded next
	if rm := os.Remove(tmp.Name()); rm != nil {
		return fmt.Errorf("%w; temp file %s left behind: %v", err, tmp.Name(), rm)
	}
	return err
}

// underLink reports whether any directory between home and home/rel is a
// symlink: a file under a linked directory is written, chmodded or removed
// through the link otherwise (security review 2026-09-30 of IN3).
func underLink(home, rel string) bool {
	dir := filepath.Dir(rel)
	for dir != "." && dir != "" && dir != string(filepath.Separator) {
		if fi, err := os.Lstat(filepath.Join(home, dir)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return true
		}
		dir = filepath.Dir(dir)
	}
	return false
}

// pruneEmptyDirs removes now-empty directories from dir upward, stopping at
// the vendor root and anything above it so uninstall never removes
// ~/.gemini/config itself. ~/.claude stays a stop too: the resync that sheds
// a pre-ADR-062 install's files there must leave the directory, which holds
// the operator's own settings.
func pruneEmptyDirs(home, dir string) {
	stop := map[string]bool{
		home:                                       true,
		filepath.Join(home, ".claude"):             true, // an install before ADR-062 wrote there
		filepath.Join(home, ".gemini"):             true,
		filepath.Join(home, AgyRoot):               true,
		filepath.Join(home, ".config"):             true,
		filepath.Join(home, ".local"):              true,
		filepath.Join(home, filepath.Dir(BinPath)): true,
	}
	for !stop[dir] && len(dir) > len(home) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
