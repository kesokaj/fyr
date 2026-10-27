package harness

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// installRun is one Install's state: the manifest the previous install
// recorded, the files this one has written or seen so far, and the counts
// its summary line reports.
type installRun struct {
	home     string
	dryRun   bool
	out      io.Writer
	old      State
	manifest []string
	counts   map[string]int
}

// saveOnError returns werr, the error that stops the install.
// On a failed write the manifest is saved once as (every old entry still
// on disk) ∪ (files written or seen so far), so nothing fyr wrote
// is left unlisted for uninstall (audit 2026-09-30, IN2).
func (r *installRun) saveOnError(werr error) error {
	if r.dryRun {
		return werr
	}
	listed := map[string]bool{}
	for _, rel := range r.manifest {
		listed[rel] = true
	}
	union := append([]string(nil), r.manifest...)
	for _, rel := range r.old.Files {
		if !listed[rel] && fyrOwns(r.home, rel) == owned {
			union = append(union, rel)
		}
	}
	sort.Strings(union)
	r.old.Files = union
	if err := SaveState(r.home, r.old); err != nil {
		return fmt.Errorf("%w (and the manifest could not be saved: %v)", werr, err)
	}
	return werr
}

// writeFile deploys one planned file and lists it in the manifest.
func (r *installRun) writeFile(f File) error {
	dest := filepath.Join(r.home, f.Rel)
	// A symlinked destination, or one under a linked directory, is never
	// read through, chmodded or replaced: it is the operator's, kept and
	// left out of the manifest, so uninstall never removes it (audit
	// 2026-09-30, IN3; security review of the fix).
	if fi, err := os.Lstat(dest); err == nil && fi.Mode()&os.ModeSymlink != 0 || underLink(r.home, f.Rel) {
		r.counts["kept"]++
		fmt.Fprintf(r.out, "keep    ~/%s (a symlink; fyr never writes through a link)\n", f.Rel)
		return nil
	}
	existing, readErr := readPlain(dest)
	switch {
	case readErr == nil && bytes.Equal(existing, f.Data):
		r.counts["unchanged"]++
		r.correctMode(f, dest)
	default:
		r.counts["written"]++
		if err := r.write(f, dest, readErr); err != nil {
			return err
		}
	}
	r.manifest = append(r.manifest, f.Rel)
	return nil
}

// correctMode sets an unchanged file's mode to the planned one.
func (r *installRun) correctMode(f File, dest string) {
	// Same bytes is not the same file: a deployed copy that someone
	// (or an older fyr) left group- or world-writable stays that
	// way forever on this branch, because nothing rewrites it. The
	// machine that HAS the problem is exactly the one that never gets
	// the fix (security review of v1.0.0, round 2, measured on a real
	// install). Correct the mode in place; the contents are already
	// right, and a chmod that fails is not worth failing an install
	// over — the next run with changed bytes will set it. The probe
	// runs on a dry run too, so it names the same `mode` line the
	// real run would (plan sh-agent-works-everywhere, D7, D8).
	if info, statErr := os.Stat(dest); statErr == nil && info.Mode().Perm() != f.Mode.Perm() {
		fmt.Fprintf(r.out, "mode    ~/%s (%04o -> %04o)\n", f.Rel, info.Mode().Perm(), f.Mode.Perm())
		if !r.dryRun {
			if err := os.Chmod(dest, f.Mode.Perm()); err != nil {
				fmt.Fprintf(r.out, "note: could not correct the mode of ~/%s (%v)\n", f.Rel, err)
			}
		}
	}
}

// write writes a new or changed file; readErr is why its bytes could not be
// compared.
func (r *installRun) write(f File, dest string, readErr error) error {
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
	if err := dirInTheWay(dest); err != nil {
		if r.dryRun {
			return fmt.Errorf("writing %s: %w", dest, err)
		}
		return r.saveOnError(fmt.Errorf("writing %s: %w", dest, err))
	}
	if !r.dryRun {
		if err := WriteAtomic(dest, f.Data, f.Mode); err != nil {
			return r.saveOnError(fmt.Errorf("writing %s: %w", dest, err))
		}
	}
	fmt.Fprintln(r.out, line)
	return nil
}

// resync removes what the previous install wrote that this one does not.
func (r *installRun) resync() error {
	// Resync: anything the previous install wrote that this one does not is
	// stale — a renamed skill, a deleted agent — and goes away, so the
	// deployed set is always exactly the repo's set.
	planned := map[string]bool{}
	for _, rel := range r.manifest {
		planned[rel] = true
	}
	claudeKept := 0
	for _, rel := range r.old.Files {
		if planned[rel] {
			continue
		}
		switch fyrOwns(r.home, rel) {
		case keptClaude:
			claudeKept++
			continue
		case gone, linked:
			// Gone, or the operator's link now: not fyr's to remove.
			continue
		}
		dest := filepath.Join(r.home, rel)
		r.counts["removed"]++
		fmt.Fprintf(r.out, "remove  ~/%s (no longer in the source)\n", rel)
		if r.dryRun {
			continue
		}
		if err := os.Remove(dest); err != nil {
			// The IN2 rule holds here too: the files written above stay
			// listed (code review round 2, 2026-10-01).
			return r.saveOnError(fmt.Errorf("removing stale %s: %w", dest, err))
		}
		pruneEmptyDirs(r.home, filepath.Dir(dest))
	}
	if claudeKept > 0 {
		r.counts["kept"] += claudeKept
		fmt.Fprintln(r.out, keepClaudeLine(claudeKept))
	}
	return nil
}

// record saves the manifest of this install from src.
func (r *installRun) record(src Source) error {
	sort.Strings(r.manifest)
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
	st := State{Repo: repo, Commit: commit, InstalledAt: time.Now().Format(time.RFC3339), Vendor: VendorAntigravity, Files: r.manifest}
	if !r.dryRun {
		if err := SaveState(r.home, st); err != nil {
			return fmt.Errorf("saving manifest: %w", err)
		}
	}
	return nil
}
