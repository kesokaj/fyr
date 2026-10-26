package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

func runUpdate(c *ctx) error {
	// A dry run of update is its --check: it reports, and changes nothing —
	// no download, no binary replaced, no pull. It says
	// what a real update would do instead.
	dry := c.dryRun
	check := c.check || dry
	repo := c.repo()
	if repo == "" {
		// A release binary has no source to build from: the newest release is
		// downloaded and verified instead.
		args := installPassArgs(c.homeFlag, c.home)
		// The BUG-079 note, when this run wrote the installed binary, is
		// printed by UpdateFromRelease itself: only there is "replaced" known.
		err := harness.UpdateFromRelease(c.home, c.src.Version, check, args, c.out)
		if errors.Is(err, harness.ErrNewerRelease) {
			if dry {
				fmt.Fprintln(c.out, "[dry-run] update would download that release, check it against its SHA256SUMS, replace the binary and reinstall")
				return nil
			}
			return exitCode(2)
		}
		if err != nil {
			return fmt.Errorf("update: %w", err)
		}
		return nil
	}
	if err := knownCheckout(c.repoFlag, c.home, repo); err != nil {
		return err
	}
	err := update(repo, c.home, check, c.homeFlag, c.out)
	var code exitCode
	if dry && errors.As(err, &code) && code == 2 {
		fmt.Fprintln(c.out, "[dry-run] update would pull those commits, rebuild and reinstall")
		return nil
	}
	return err
}

func update(repo, home string, checkOnly bool, homeFlag string, out io.Writer) error {
	behind, err := harness.Behind(repo)
	if err != nil {
		return err
	}
	info := harness.Inspect(repo)
	if behind == 0 {
		fmt.Fprintf(out, "up to date with upstream (%s)\n", info.Commit)
	} else {
		fmt.Fprintf(out, "behind upstream by %d commit(s)\n", behind)
	}
	// A dirty checkout is never installed, on either path: `update` deploys
	// the WORKING TREE, not the fetched commit, so uncommitted work would reach
	// every vendor directory (BUG-046); `install` stays the deliberate way to
	// deploy a dirty tree. The check sits above the --check and --dry-run
	// returns, so the read-only mode refuses exactly what the real run refuses
	// (IN4).
	if info.Dirty || info.StatusUnknown {
		what := "has uncommitted changes"
		if info.StatusUnknown {
			what = "could not be checked for uncommitted changes (git did not report its status)"
		}
		return fmt.Errorf("%s %s and `update` installs the working tree, not the fetched commit; commit or stash them, or run `fyr install` to deploy them deliberately", repo, what)
	}
	if checkOnly {
		if behind == 0 {
			return nil
		}
		return exitCode(2)
	}
	if behind > 0 {
		if err := harness.Pull(repo, out); err != nil {
			return err
		}
		fmt.Fprintf(out, "pulled to %s\n", harness.Inspect(repo).Commit) // the tree moved: inspected again
	}
	if err := harness.Reinstall(repo, installPassArgs(homeFlag, home), out); err != nil {
		return err
	}
	// Reinstall wrote ~/.local/bin/fyr; the process running this update
	// may be another file (BUG-079).
	harness.NoteRunningElsewhere(home, out)
	return nil
}

// installPassArgs are the flags an update or a rebuilding install hands to
// the install it runs: the home it was told to use.
func installPassArgs(homeFlag, home string) []string {
	if homeFlag != "" {
		return []string{"--home", home}
	}
	return nil
}

// knownCheckout refuses to build and install from a checkout that was only
// discovered by walking up from the working directory when a different
// checkout is already installed: building runs that tree's code and deploys
// its context everywhere, so standing in a harness-shaped directory must not
// be enough. An explicit --repo or $FYR_REPO, or a first install, is.
func knownCheckout(repoFlag, home, repo string) error {
	if repoFlag != "" || os.Getenv("FYR_REPO") != "" {
		return nil
	}
	st, found, err := harness.LoadState(home)
	if err != nil {
		return fmt.Errorf("reading the manifest before building: %w", err)
	}
	if !found || st.Repo == "" {
		return nil
	}
	// A manifest written by an embedded or zip install records a release or
	// an archive, never a path: neither is handed to samePath, which would
	// resolve it against the working directory.
	switch o := st.Origin(); o.Kind {
	case harness.OriginRelease:
		return fmt.Errorf("this home was installed from the release binary %s, not from a checkout; pass --repo %s to build and install from it", o.Release, repo)
	case harness.OriginZip:
		return fmt.Errorf("this home was installed from a zip (sha256 %s), not from a checkout; pass --repo %s to build and install from it", o.ShortSum(), repo)
	}
	if samePath(st.Repo, repo) {
		return nil
	}
	return fmt.Errorf("checkout %s is not the installed one (%s); pass --repo %s to build and install from it", repo, st.Repo, repo)
}

func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ra == rb
}

// shouldRebuild decides whether install re-executes through a fresh build
// of the repo: yes whenever a toolchain is present and the repo is its own
// git checkout, unless this process is already the fresh build that
// Reinstall spawned. A plain copy is installed from the running binary.
func shouldRebuild(goOnPath, child, ownCheckout bool) bool {
	return goOnPath && !child && ownCheckout
}

// binaryOlder reports whether the running binary is not the repo's current
// build: unstamped (`go run`, or a build outside git) or stamped with a
// different revision. Prefixes are compared both ways because git's short
// hash length is not fixed.
func binaryOlder(rev, commit string) bool {
	rev = strings.TrimSuffix(rev, "+dirty")
	if rev == "" || commit == "" {
		return false
	}
	if rev == "(devel)" || rev == "unknown" {
		return true
	}
	return !strings.HasPrefix(rev, commit) && !strings.HasPrefix(commit, rev)
}

// selfExecutable returns the running binary so install can copy it to
// ~/.local/bin. On the normal path this is the build Reinstall just made;
// without a toolchain it is whatever binary is running, and status says so.
func selfExecutable() ([]byte, error) {
	p, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if p, err = filepath.EvalSymlinks(p); err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// pathHint says, after an install, whether the next unqualified `fyr`
// runs the binary just written — one message per situation. PATH reaching the
// file but only after an earlier entry is the BUG-078 case: install reports
// success and `status` reports drift none, both about a binary the next
// command will not run. PATH not reaching it is the old note, naming what
// answers instead when something does. Every fact comes from one PathShadows
// call, so the two halves cannot disagree on what "on PATH" means.
func pathHint(home string, out io.Writer) {
	bin := filepath.Join(home, harness.BinPath)
	binDir := filepath.Dir(bin)
	shadows, onPath := harness.PathShadows(os.Getenv("PATH"), bin)
	switch {
	case onPath:
		for _, s := range shadows {
			fmt.Fprintf(out, "warning: %s comes before %s on PATH, so `fyr` runs that one\n", s, bin)
		}
	case len(shadows) > 0:
		fmt.Fprintf(out, "note: %s is not on PATH, so `fyr` runs %s instead; put %s ahead of it\n", binDir, shadows[0], binDir)
	default:
		fmt.Fprintf(out, "note: %s is not on PATH; add it so `fyr` is available everywhere\n", binDir)
	}
}
