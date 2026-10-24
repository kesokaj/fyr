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
	"sort"
	"time"
)

// Install writes the planned files under home, removes files a previous
// install wrote that the plan no longer contains, and records the manifest.
// Writing is idempotent: an unchanged file is neither rewritten nor reported.
//
// The resync is also how an install from before ADR-062 sheds its Claude Code
// files: the old manifest lists ~/.claude/AGENTS.md, the CLAUDE.md stub and
// the skills, agents, commands and rules under ~/.claude, this plan names
// none of them, and each is removed by the one rule below.
func Install(src Source, home string, files []File, dryRun bool, out io.Writer) error {
	old, _, err := LoadState(home)
	if err != nil {
		return err
	}
	// Before any file is written, so a mode problem is seen first and never
	// interrupts a deployment (security review of the audit, round 2).
	TightenConfigDir(home, dryRun, out)

	counts := map[string]int{}
	var manifest []string
	// On a failed write the manifest is saved once as (every old entry still
	// on disk) ∪ (files written or seen so far), so nothing sh-agent wrote
	// is left unlisted for uninstall (audit 2026-09-30, IN2).
	saveOnError := func(werr error) error {
		if dryRun {
			return werr
		}
		listed := map[string]bool{}
		for _, rel := range manifest {
			listed[rel] = true
		}
		union := append([]string(nil), manifest...)
		for _, rel := range old.Files {
			if fi, err := os.Lstat(filepath.Join(home, rel)); err == nil && !listed[rel] && fi.Mode()&os.ModeSymlink == 0 && !underLink(home, rel) {
				union = append(union, rel)
			}
		}
		sort.Strings(union)
		old.Files = union
		if err := SaveState(home, old); err != nil {
			return fmt.Errorf("%w (and the manifest could not be saved: %v)", werr, err)
		}
		return werr
	}
	for _, f := range files {
		dest := filepath.Join(home, f.Rel)
		// A symlinked destination, or one under a linked directory, is never
		// read through, chmodded or replaced: it is the operator's, kept and
		// left out of the manifest, so uninstall never removes it (audit
		// 2026-09-30, IN3; security review of the fix).
		if fi, err := os.Lstat(dest); err == nil && fi.Mode()&os.ModeSymlink != 0 || underLink(home, f.Rel) {
			counts["kept"]++
			fmt.Fprintf(out, "keep    ~/%s (a symlink; sh-agent never writes through a link)\n", f.Rel)
			continue
		}
		existing, readErr := os.ReadFile(dest)
		switch {
		case readErr == nil && bytes.Equal(existing, f.Data):
			counts["unchanged"]++
			// Same bytes is not the same file: a deployed copy that someone
			// (or an older sh-agent) left group- or world-writable stays that
			// way forever on this branch, because nothing rewrites it. The
			// machine that HAS the problem is exactly the one that never gets
			// the fix (security review of v1.0.0, round 2, measured on a real
			// install). Correct the mode in place; the contents are already
			// right, and a chmod that fails is not worth failing an install
			// over — the next run with changed bytes will set it. The probe
			// runs on a dry run too, so it names the same `mode` line the
			// real run would (plan sh-agent-works-everywhere, D7, D8).
			if info, statErr := os.Stat(dest); statErr == nil && info.Mode().Perm() != f.Mode.Perm() {
				fmt.Fprintf(out, "mode    ~/%s (%04o -> %04o)\n", f.Rel, info.Mode().Perm(), f.Mode.Perm())
				if !dryRun {
					if err := os.Chmod(dest, f.Mode.Perm()); err != nil {
						fmt.Fprintf(out, "note: could not correct the mode of ~/%s (%v)\n", f.Rel, err)
					}
				}
			}
		default:
			counts["written"]++
			// Every run names what it changes, not only --dry-run: a file that
			// was not there is an `add`; one with other bytes an `update`; one
			// that exists but could not be read is written over as before and
			// the line says why (D6). The line prints AFTER the write, so a
			// write that fails — a directory at the path, which the rename
			// cannot replace — leaves no line claiming a change that did not
			// happen (review of Phase 1).
			line := "update  ~/" + f.Rel
			switch {
			case errors.Is(readErr, fs.ErrNotExist):
				line = "add     ~/" + f.Rel
			case readErr != nil:
				line = fmt.Sprintf("update  ~/%s (previous file unreadable: %v)", f.Rel, readErr)
			}
			if !dryRun {
				if err := WriteAtomic(dest, f.Data, f.Mode); err != nil {
					return saveOnError(fmt.Errorf("writing %s: %w", dest, err))
				}
			}
			fmt.Fprintln(out, line)
		}
		manifest = append(manifest, f.Rel)
	}

	// Resync: anything the previous install wrote that this one does not is
	// stale — a renamed skill, a deleted agent — and goes away, so the
	// deployed set is always exactly the repo's set.
	planned := map[string]bool{}
	for _, rel := range manifest {
		planned[rel] = true
	}
	for _, rel := range old.Files {
		if planned[rel] {
			continue
		}
		dest := filepath.Join(home, rel)
		fi, err := os.Lstat(dest)
		if err != nil || fi.Mode()&os.ModeSymlink != 0 || underLink(home, rel) {
			// Gone, or the operator's link now: not sh-agent's to remove.
			continue
		}
		counts["removed"]++
		fmt.Fprintf(out, "remove  ~/%s (no longer in the source)\n", rel)
		if dryRun {
			continue
		}
		if err := os.Remove(dest); err != nil {
			// The IN2 rule holds here too: the files written above stay
			// listed (code review round 2, 2026-10-01).
			return saveOnError(fmt.Errorf("removing stale %s: %w", dest, err))
		}
		pruneEmptyDirs(home, filepath.Dir(dest))
	}

	sort.Strings(manifest)
	// An embedded install records the release it came with and runs no git
	// command (D17); a tree on disk records its path and commit; a zip install
	// records the archive, never the unpack directory (ADR-038). One reader
	// of that value: OriginString (IN6).
	repo, commit := src.OriginString(), ""
	var info RepoInfo
	if src.Dir != "" && src.Origin == "" {
		info = Inspect(src.Dir) // empty for a plain copy: no commit of another repo is recorded
		commit = info.Commit
	}
	switch {
	case commit == "":
	case info.StatusUnknown:
		// Never record a bare commit for a tree git could not describe: the
		// files came from a working tree of unknown state, and the manifest
		// is what every later `status` compares against.
		commit += "+status-unknown"
	case info.Dirty:
		commit += "+dirty" // the files came from the working tree, not from this commit
	}
	st := State{Repo: repo, Commit: commit, InstalledAt: time.Now().Format(time.RFC3339), Vendor: VendorAntigravity, Files: manifest}
	if !dryRun {
		if err := SaveState(home, st); err != nil {
			return fmt.Errorf("saving manifest: %w", err)
		}
	}

	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}
	fmt.Fprintf(out, "%s%d written, %d unchanged, %d removed, %d kept — %d files managed\n",
		prefix, counts["written"], counts["unchanged"], counts["removed"], counts["kept"], len(manifest))
	return nil
}

// Entry is the state of one deployed path.
type Entry struct {
	Rel   string
	State string // ok | missing | differs | foreign | stale
}

// Report is what `sh-agent status` prints.
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
		fi, lerr := os.Lstat(filepath.Join(home, f.Rel))
		existing, err := os.ReadFile(filepath.Join(home, f.Rel))
		state := "ok"
		switch {
		case lerr == nil && fi.Mode()&os.ModeSymlink != 0 || underLink(home, f.Rel):
			state = "foreign" // the operator's link, or a file under one (IN3)
		case errors.Is(err, os.ErrNotExist):
			state = "missing"
		case err != nil:
			return Report{}, err
		case bytes.Equal(existing, f.Data):
		default:
			state = "differs"
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
		if _, err := os.Lstat(filepath.Join(home, rel)); err == nil {
			r.Counts["stale"]++
			r.Entries = append(r.Entries, Entry{Rel: rel, State: "stale"})
		}
	}
	return r, nil
}

// Diff prints a unified diff for every file that differs, using the system
// diff so the output looks like every other diff the operator reads.
func Diff(home string, files []File, out io.Writer) error {
	for _, f := range files {
		dest := filepath.Join(home, f.Rel)
		if fi, err := os.Lstat(dest); err == nil && fi.Mode()&os.ModeSymlink != 0 || underLink(home, f.Rel) {
			continue // the operator's link, or a file under one, is compared by no one (IN3)
		}
		existing, err := os.ReadFile(dest)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(out, "missing ~/%s\n", f.Rel)
			continue
		}
		if err != nil {
			return err
		}
		if bytes.Equal(existing, f.Data) {
			continue
		}
		// Under the home's own runtime directory, never the system temp dir:
		// the rendered harness is the operator's, and this project keeps
		// generated files out of /tmp (security audit 2026-09-18).
		tmpDir, err := ConfigDir(home)
		if err != nil {
			return err
		}
		if err := diffOne(f, dest, tmpDir, out); err != nil {
			return err
		}
	}
	return nil
}

// diffOne renders one source file beside its installed copy and runs the
// system diff; the temp file goes on every path, including a failed write
// (reviews of the 2026-09-18 audit).
func diffOne(f File, dest, tmpDir string, out io.Writer) error {
	tmp, err := os.CreateTemp(tmpDir, "sh-agent-diff-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(f.Data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	cmd := exec.Command("diff", "-u", "-L", "installed ~/"+f.Rel, "-L", "repo ~/"+f.Rel, dest, tmp.Name())
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
		fmt.Fprintln(out, "nothing installed (no manifest at ~/.config/sh-agent/state.json)")
		return nil
	}
	removed := 0
	for _, rel := range st.Files {
		dest := filepath.Join(home, rel)
		fi, err := os.Lstat(dest)
		if err != nil {
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 || underLink(home, rel) {
			// An old manifest may name a path that is the operator's link
			// now, or sits under one: never sh-agent's to remove (IN3).
			fmt.Fprintf(out, "keep    ~/%s (a symlink; sh-agent never writes through a link)\n", rel)
			continue
		}
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
	if dryRun {
		fmt.Fprintf(out, "[dry-run] would remove %d files and the manifest\n", removed)
		return nil
	}
	if err := RemoveState(home); err != nil {
		return err
	}
	fmt.Fprintf(out, "removed %d files and the manifest\n", removed)
	return nil
}

// WriteAtomic writes through a temp file and rename, so a half-written
// context file is never visible and a running sh-agent binary can be
// replaced on Linux, where opening it for writing would fail with ETXTBSY.
func WriteAtomic(dest string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".sh-agent-*")
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
