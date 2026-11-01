package main

import (
	"debug/buildinfo"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kesokaj/fyr/internal/auto"
	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/harness"
)

func status(src harness.Source, home string, out io.Writer) error {
	repo := src.Dir
	// Read first: the source line says when this home came from a zip, which
	// the embedded files or a checkout beside it do not describe (ADR-038).
	st, found, err := harness.LoadState(home)
	if err != nil {
		return err
	}
	origin := st.Origin()
	fromZip := found && origin.Kind == harness.OriginZip
	zipNote := ""
	if fromZip {
		zipNote = "; this home was installed from a zip — " + zipAdvice()
	}
	info := sourceLine(src, zipNote, out)

	if !found {
		fmt.Fprintln(out, "installed  nothing — run `fyr install`")
		return exitCode(1)
	}
	fmt.Fprintf(out, "installed  %s from %s (%d files, manifest ~/.config/fyr/state.json)\n", st.InstalledAt, installedFrom(st, origin), len(st.Files))
	binaryLines(home, repo, info, out)
	retroLine(home, out)
	fmt.Fprintln(out, "dream      "+dream.StatusLine(home))
	fmt.Fprintln(out, "auto       "+auto.StatusLine(auto.Options{Home: home}))

	inv, err := harness.LoadInventory(src)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "repo has   %d skills, %d agents, %d commands, %d rules\n", len(inv.Skills), len(inv.Agents), len(inv.Commands), len(inv.Rules))
	fmt.Fprintf(out, "agy        ~/%s  (rules/AGENTS.md, skills/, agents/, workflows/, rules/)\n", harness.AgyRoot)

	if fromZip {
		return zipDrift(home, st, out)
	}
	return driftLines(src, home, out)
}

// sourceLine prints where the harness comes from and returns the
// checkout's git state, empty for the embedded files.
func sourceLine(src harness.Source, zipNote string, out io.Writer) harness.RepoInfo {
	repo := src.Dir
	var info harness.RepoInfo
	switch {
	case repo == "":
		// The embedded files: no git state to show, and none is asked (D17).
		fmt.Fprintf(out, "source     embedded %s (release binary; update downloads the newest release%s)\n", src.Version, zipNote)
	default:
		info = harness.Inspect(repo)
		dirty := "clean"
		if info.StatusUnknown {
			// Never render an unreadable tree as clean: git failing to report is
			// not evidence of anything.
			dirty = "state unknown — git could not report it"
		} else if info.Dirty {
			dirty = "uncommitted changes"
		}
		if info.Commit == "" {
			fmt.Fprintf(out, "repo       %s (plain copy, not its own git checkout; update is refused%s)\n", repo, zipNote)
		} else {
			fmt.Fprintf(out, "repo       %s (%s @ %s, %s%s)\n", repo, info.Branch, info.Commit, dirty, zipNote)
		}
	}
	return info
}

// installedFrom names what the manifest says the home was installed from.
func installedFrom(st harness.State, origin harness.Origin) string {
	from := "commit " + st.Commit
	switch origin.Kind {
	case harness.OriginRelease:
		from = "release " + origin.Release
	case harness.OriginZip:
		from = "zip sha256:" + origin.ShortSum()
		if origin.Trust != "" { // a manifest from before the verdict was recorded has none
			from += " (" + origin.Trust + ")"
		}
	}
	return from
}

// binaryLines prints the installed binary's build and whether it is the one
// an unqualified `fyr` runs.
func binaryLines(home, repo string, info harness.RepoInfo, out io.Writer) {
	// The line describes the installed file, not this process: status may
	// run from `go run` or from a build outside ~/.local/bin.
	binaryLine := "binary     unknown (installed file has no build info)"
	if bi, err := buildinfo.ReadFile(filepath.Join(home, harness.BinPath)); err == nil {
		rev := versionFromInfo(bi)
		if repo == "" {
			rev = releaseFromInfo(bi)
		}
		binaryLine = "binary     " + rev
		switch {
		case repo == "":
			// No checkout commit to compare against.
		case !binaryOlder(rev, info.Commit):
		case rev == "(devel)" || rev == "unknown":
			binaryLine += " — no build stamp; run `fyr update`"
		default:
			binaryLine += fmt.Sprintf(" — older than the repo (%s); run `fyr update`", info.Commit)
		}
	}
	fmt.Fprintln(out, binaryLine)
	// The line above describes the installed file; these say when that file is
	// not the one an unqualified `fyr` runs (BUG-078). Only when PATH
	// reaches the installed file at all: for a scratch home it never does, and
	// the operator's own home is the one where the distinction bites.
	if shadows, onPath := harness.PathShadows(os.Getenv("PATH"), filepath.Join(home, harness.BinPath)); onPath {
		for _, s := range shadows {
			fmt.Fprintf(out, "shadowed   %s comes first on PATH, so `fyr` runs that, not the binary above\n", s)
		}
	}
	// A crossing whose removal of the old binary was refused, or whose old
	// manifest never listed it, leaves it on PATH (plan fyr F2, F4; TR9).
	for _, old := range harness.OldBinariesOnPath(os.Getenv("PATH")) {
		fmt.Fprintf(out, "leftover   %s is the binary from before the rename to fyr; remove it by hand, and any unit or profile line that runs it\n", old)
	}
}

// retroLine prints when the last retro was stored, by the brain store's
// global retro records (ADR-087).
func retroLine(home string, out io.Writer) {
	if last, ok := brain.LastRetro(home); ok {
		days := int(time.Since(last).Hours() / 24)
		hint := ""
		if days > 14 {
			hint = " — past 14 days, run /retro at the end of the next session"
		}
		fmt.Fprintf(out, "retro      last proposal %s (%d days ago)%s\n", last.Format("2006-01-02"), days, hint)
	} else {
		fmt.Fprintln(out, "retro      none yet — run /retro at the end of a session with corrections")
	}
}

// zipDrift is status's drift check of a home installed from a zip.
func zipDrift(home string, st harness.State, out io.Writer) error {
	// The zip is not kept, so its content cannot be compared; say so and
	// check what can be checked — every file the install wrote is there.
	var missing []string
	for _, rel := range st.Files {
		if _, err := os.Lstat(filepath.Join(home, rel)); err != nil {
			missing = append(missing, rel)
		}
	}
	fmt.Fprintf(out, "drift      not compared — installed from a zip that is not kept; %d of %d manifest files present\n", len(st.Files)-len(missing), len(st.Files))
	for _, rel := range missing {
		fmt.Fprintf(out, "  missing  ~/%s\n", rel)
	}
	if len(missing) > 0 {
		return exitCode(1)
	}
	return nil
}

// driftLines compares the home against the plan of src and prints what
// differs; drift is exit 1.
func driftLines(src harness.Source, home string, out io.Writer) error {
	files, err := harness.Plan(src, nil)
	if err != nil {
		return err
	}
	report, err := harness.Status(home, files)
	if err != nil {
		return err
	}
	for _, e := range report.Entries {
		if e.State != "ok" && e.State != "kept" {
			fmt.Fprintf(out, "  %-8s ~/%s\n", e.State, e.Rel)
		}
	}
	if n := report.Counts["kept"]; n > 0 {
		// Not drift: no install changes them (plan fyr D32, IN32).
		fmt.Fprintf(out, "kept       %d files under ~/.claude that an install before fyr wrote for Claude Code; fyr never removes them, and the next install drops them from its manifest\n", n)
	}
	if !report.Drift() {
		fmt.Fprintf(out, "drift      none — %d files match the repo\n", report.Counts["ok"])
		return nil
	}
	fmt.Fprintf(out, "drift      %d missing, %d differ, %d stale — run `fyr diff`, then `fyr install`\n",
		report.Counts["missing"], report.Counts["differs"], report.Counts["stale"])
	return exitCode(1)
}
