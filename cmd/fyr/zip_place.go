package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kesokaj/fyr/internal/harness"
)

// zipPlaceWithoutSource answers status and update at a zip place when the
// harness source did not resolve: a development build carries no harness of
// its own, and a zip place records no tree, so outside a checkout neither
// verb could run and the place could not even be told how it moves
// (BUG-203). A path the operator named keeps its own error (D15).
func (c *ctx) zipPlaceWithoutSource() (handled bool, err error) {
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
	fmt.Fprintf(out, "installed  %s from %s (%d files, manifest ~/.config/fyr/state.json)\n", st.InstalledAt, installedFrom(st, st.Origin()), len(st.Files))
	binaryLines(home, "", harness.RepoInfo{}, out)
	retroLine(home, out)
	fmt.Fprintf(out, "agy        ~/%s  (rules/AGENTS.md, skills/, agents/, workflows/, rules/)\n", harness.AgyRoot)
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
