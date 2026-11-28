package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/trail"
)

func runUpdate(c *runtimeCtx) error {
	if !updateOptsOf(c).jsonOut {
		_, err := execUpdate(c, c.out)
		return err
	}
	var buf bytes.Buffer
	behind, err := execUpdate(c, &buf)
	var code exitCode
	if err != nil && (!errors.As(err, &code) || code != 2) {
		return err
	}
	if jerr := writeUpdateJSON(c, behind, err == nil, buf.String()); jerr != nil {
		return jerr
	}
	return err
}

func execUpdate(c *runtimeCtx, out io.Writer) (bool, error) {
	// A dry run of update is its --check: it reports, and changes nothing —
	// no download, no binary replaced, no pull. It says
	// what a real update would do instead.
	o := updateOptsOf(c)
	dry := o.dryRun
	check := o.check || dry
	repo := c.repo()
	if repo == "" {
		// A release binary has no source to build from: the newest release is
		// downloaded and verified instead.
		args := installPassArgs(c.homeFlag, c.home)
		// The BUG-079 note, when this run wrote the installed binary, is
		// printed by UpdateFromRelease itself: only there is "replaced" known.
		err := harness.UpdateFromRelease(c.home, c.src.Version, check, args, out)
		if errors.Is(err, harness.ErrNewerRelease) {
			if dry {
				fmt.Fprintln(out, "[dry-run] update would download that release, check it against its SHA256SUMS, replace the binary and reinstall")
				return true, nil
			}
			return true, exitCode(2)
		}
		if err != nil {
			return false, fmt.Errorf("update: %w", err)
		}
		return false, nil
	}
	if err := knownCheckout(c.repoFlag, c.home, repo); err != nil {
		return false, err
	}
	err := update(repo, c.home, check, c.homeFlag, out)
	var code exitCode
	if errors.As(err, &code) && code == 2 {
		if dry {
			fmt.Fprintln(out, "[dry-run] update would pull those commits, rebuild and reinstall")
			return true, nil
		}
		return true, err
	}
	return false, err
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
	// an archive, never a path: neither is handed to trail.SameDir, which would
	// resolve it against the working directory.
	switch o := st.Origin(); o.Kind {
	case harness.OriginRelease:
		return fmt.Errorf("this home was installed from the release binary %s, not from a checkout; pass --repo %s to build and install from it", o.Release, repo)
	case harness.OriginZip:
		return fmt.Errorf("this home was installed from a zip (sha256 %s), not from a checkout; pass --repo %s to build and install from it", o.ShortSum(), repo)
	}
	if trail.SameDir(st.Repo, repo) {
		return nil
	}
	return fmt.Errorf("checkout %s is not the installed one (%s); pass --repo %s to build and install from it", repo, st.Repo, repo)
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

// zipPlaceWithoutSource answers status and update at a zip place when the
// harness source did not resolve: a development build carries no harness of
// its own, and a zip place records no tree, so outside a checkout neither
// verb could run and the place could not even be told how it moves
// (BUG-203). A path the operator named keeps its own error (D15).
func (c *runtimeCtx) zipPlaceWithoutSource() (handled bool, err error) {
	if c.cmd != "status" && c.cmd != "update" {
		return false, nil
	}
	if c.repoFlag != "" || os.Getenv("FYR_REPO") != "" {
		return false, nil
	}
	st, found, err := harness.LoadState(c.home)
	if err != nil || !found || st.Origin().Kind != harness.OriginZip {
		return false, nil
	}
	if c.cmd == "status" {
		if c.wantsJSON() {
			return true, statusWithoutSourceJSON(st, c.home, c.out)
		}
		return true, statusWithoutSource(st, c.home, c.out)
	}
	return true, fmt.Errorf("this home was installed from a zip (sha256 %s) and %s is a development build, which no zip replaces, so update has nothing to move: "+
		"download a newer release's zip, its SHA256SUMS and its release binary for this machine, and run that release binary's `fyr update --from-zip <file>` (README § Install from a zip); "+
		"or build fyr in a checkout and run `fyr install --repo <checkout>`, which makes this a checkout place that `fyr update` moves",
		st.Origin().ShortSum(), buildVersion())
}

// statusWithoutSource is status at a zip place read from the manifest alone:
// the zip is not kept, so there is nothing to compare against anyway.
func statusWithoutSource(st harness.State, home string, out io.Writer) error {
	fmt.Fprintf(out, "source     none — %s is a development build, which carries no harness, and the zip this home was installed from is not kept; `fyr install --repo <checkout>` makes it a checkout place\n", buildVersion())
	fmt.Fprintf(out, "installed  %s from %s (%d files, manifest ~/%s/state.json)\n", st.InstalledAt, installedFrom(st, st.Origin()), len(st.Files), harness.StateDir)
	binaryLines(home, "", harness.RepoInfo{}, out)
	retroLine(home, out)
	fmt.Fprintf(out, "agy        ~/%s  (rules/AGENTS.md, hooks.json, skills/, agents/, workflows/, rules/)\n", harness.AgyRoot)
	return zipDrift(home, st, out)
}

// refuseDevelZipOverPath refuses `update --from-zip` by a development build
// at a place installed from a tree: the zip would replace that tree's record
// with an archive's hash and keep the running binary, which leaves a place
// no verb of this build can update or report on (BUG-203, borken). A
// manifest that cannot be read refuses too: the check failing is not
// evidence the place is not a checkout's.
func refuseDevelZipOverPath(home string) error {
	if harness.IsReleaseVersion(buildVersion()) {
		return nil
	}
	// It runs after migrateStateDir, so only a dry run gets here with the
	// manifest still in ~/.config/sh-agent, which it does not read: it
	// refuses rather than pass a place the real run would refuse (P3's
	// reviews).
	if _, err := os.Lstat(filepath.Join(home, harness.StateDir)); errors.Is(err, os.ErrNotExist) && harness.StateDirNotice(home) != "" {
		return fmt.Errorf("this home's manifest is still in ~/.config/sh-agent, which a dry run does not move, so whether a tree was installed here cannot be told: a development build's --from-zip --dry-run refuses until `fyr install --repo <checkout>` migrates it")
	}
	st, found, err := harness.LoadState(home)
	if err != nil {
		return fmt.Errorf("reading the manifest before --from-zip: %w", err)
	}
	if !found || st.Origin().Kind != harness.OriginPath || st.Repo == "" {
		return nil
	}
	return fmt.Errorf("this home was installed from %s and %s is a development build, which no zip replaces: --from-zip would leave this place with no tree to update from; "+
		"run `fyr update` to move a checkout, or `fyr install --repo %s` to reinstall from it", harness.ShellArg(st.Repo), buildVersion(), harness.ShellArg(st.Repo))
}

// develZipNote is the line --from-zip adds for a development build: the
// harness moved, the binary did not, and how it moves.
func develZipNote(prefix string, out io.Writer) {
	if harness.IsReleaseVersion(buildVersion()) {
		return
	}
	fmt.Fprintf(out, "%sthe binary did not move: %s is a development build; build fyr in a checkout and run `fyr install --repo <checkout>` to move it, which makes this a checkout place\n", prefix, buildVersion())
}

// zipAdvice is how a zip place moves, as status and diff say it: a release
// binary repeats --from-zip with a newer zip; a development build, which no
// zip moves, goes back to a checkout (BUG-203).
func zipAdvice() string {
	if harness.IsReleaseVersion(buildVersion()) {
		return "run update --from-zip <file> again to change it"
	}
	return "a development build never moves by a zip: run `fyr install --repo <checkout>` to make it a checkout place"
}

func dryRunZipParent(home, parent string) (string, error) {
	if err := models.CheckHomeDir(home); err != nil {
		return "", err
	}
	fi, err := os.Lstat(parent)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return "", nil
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return "", fmt.Errorf("%s is not a directory", parent)
	}
	return parent, nil
}

func prepareZipUnpackParent(home string, dryRun bool) (string, []string, error) {
	parent := filepath.Join(home, harness.StateDir)
	if dryRun {
		p, err := dryRunZipParent(home, parent)
		return p, nil, err
	}
	created, err := harness.MkdirAllTracked(parent)
	return parent, created, err
}

// updateFromZip installs the harness in the repository zip the operator
// downloaded, with the running binary: unpack, install, delete the unpacked
// copy (R8, R9's zip place; ADR-038). Nothing is fetched, and nothing in the
// zip replaces the binary: the running one is installed as in every install.
func updateFromZip(zipPath, home string, dryRun bool, out io.Writer) (err error) {
	// A dry run installs nothing and never creates ~/.config/fyr: when that
	// directory is absent the unpack goes into a zip-* directory beside the
	// zip instead, never the system temp directory (IN10), and that directory
	// is removed before this returns, as on a real run; the usage says so
	// (IN21). On a real run, created dirs are removed again on failure (BUG-093).
	parent, created, err := prepareZipUnpackParent(home, dryRun)
	if err != nil {
		return err
	}
	if len(created) > 0 {
		defer func() {
			if err != nil {
				harness.RemoveCreated(created)
			}
		}()
	}
	// The zip place is the fallback for machines that cannot take the normal
	// way in, so it accepts the zip it is given (ADR-041). A release zip with
	// its SHA256SUMS beside it is checked before a byte is written, and refused
	// if that check fails; without it, it is installed marked UNVERIFIED (R9,
	// ADR-053: the checksums are not signed, so the check is of integrity).
	verdict := &zipVerdict{zipPath: zipPath}
	dir, sum, err := harness.UnpackZip(zipPath, parent, verdict.check)
	if err != nil {
		return err
	}
	defer func() {
		if rerr := os.RemoveAll(dir); rerr != nil {
			fmt.Fprintf(out, "warning: could not remove the unpacked copy %s: %v\n", dir, rerr)
		}
	}()
	return installZip(zipPath, dir, sum, verdict.tag, parent, home, dryRun, out)
}

// zipVerdict is a zip's check against the SHA256SUMS beside it; tag is the
// release that file names once the check has passed.
type zipVerdict struct {
	zipPath, tag string
}

func (v *zipVerdict) check(sum string) error {
	tag, err := harness.VerifyZipSums(v.zipPath, sum)
	if errors.Is(err, harness.ErrZipNoSums) {
		return nil
	}
	if err != nil {
		return err
	}
	v.tag = tag
	return nil
}

// installZip installs the unpacked tree in dir, with the running binary, and
// on a real run sweeps the stale unpack directories under parent.
func installZip(zipPath, dir, sum, verifiedTag, parent, home string, dryRun bool, out io.Writer) error {
	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}
	trust := "UNVERIFIED — no SHA256SUMS beside it; trust rests on your own download"
	if verifiedTag != "" {
		trust = "CHECKED against the SHA256SUMS of release " + verifiedTag + " (integrity only)"
	}
	fmt.Fprintf(out, "%szip %s (sha256 %s, %s): installing its harness and the running binary %s; nothing is downloaded and nothing in the zip replaces the binary\n", prefix, zipPath, sum, trust, buildVersion())
	// The manifest keeps the verdict too, so status can tell a verified install
	// from an unverified one after the output has scrolled away.
	src := harness.Source{FS: os.DirFS(dir), Dir: dir, Origin: harness.ZipOrigin(sum, verifiedTag)}
	if err := installFiles(src, home, dryRun, out); err != nil {
		return err
	}
	develZipNote(prefix, out)
	// Only after a successful install: a refused or dry run changes nothing,
	// not even a stale leftover from an interrupted run.
	if !dryRun {
		harness.SweepStaleDirs(parent, "zip-", time.Hour, out)
	}
	return nil
}
