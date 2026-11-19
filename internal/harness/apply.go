package harness

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
		if isLinkedPath(home, f.Rel) {
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
		if isLinkedPath(home, f.Rel) {
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
// counted.
func claudeFile(home, rel string) bool {
	if !isClaudeRel(rel) {
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
	if _, err := os.Lstat(filepath.Join(home, rel)); err != nil {
		return gone
	}
	if isLinkedPath(home, rel) {
		return linked
	}
	return owned
}

func keepClaudeLine(n int) string {
	return fmt.Sprintf("keep    ~/.claude: %d files an install before fyr wrote for Claude Code; fyr never removes them, and they leave its manifest", n)
}
