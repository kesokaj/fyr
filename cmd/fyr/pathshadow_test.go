package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// A verified release binary was installed to /usr/local/bin and then ignored,
// because two earlier PATH entries held other fyr binaries; install said it
// succeeded and status said drift none, both about a binary that was not the
// one the shell ran (BUG-078). install warns and status carries a `shadowed`
// line, and both go quiet when the earlier entry is a link to the same file.
func TestInstallAndStatusNameAPathShadow(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	installedBin := filepath.Join(home, harness.BinPath)

	// An earlier PATH entry holding a different fyr.
	earlier := t.TempDir()
	other := filepath.Join(earlier, "fyr")
	if err := os.WriteFile(other, []byte("#!/bin/sh\necho not-the-installed-one\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", earlier+string(os.PathListSeparator)+filepath.Dir(installedBin))

	var out bytes.Buffer
	if err := run([]string{"install", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	wantInstall := "warning: " + other + " comes before " + installedBin + " on PATH, so `fyr` runs that one"
	if !strings.Contains(out.String(), wantInstall) {
		t.Errorf("install missing %q:\n%s", wantInstall, out.String())
	}

	out.Reset()
	if err := run([]string{"status", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("status: %v\n%s", err, out.String())
	}
	wantStatus := "shadowed   " + other + " comes first on PATH, so `fyr` runs that, not the binary above"
	if !strings.Contains(out.String(), wantStatus) {
		t.Errorf("status missing %q:\n%s", wantStatus, out.String())
	}

	// The fix the operator applied: point the earlier entry at the installed
	// binary. It runs the same file, so neither command may still warn.
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(installedBin, other); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"status", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("status after symlink: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "shadowed") {
		t.Errorf("status still reports a shadow for a link to the installed binary:\n%s", out.String())
	}
}

// One situation, one message. When this home's bin dir is not on PATH at all,
// pathHint owns it and names what answers instead; the shadow warning stays
// quiet, so a scratch-home install — which this repo does constantly — does
// not print a warning the operator learns to skip past.
func TestOffPathInstallNamesWhatAnswersAndDoesNotWarnTwice(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	binDir := filepath.Join(home, filepath.Dir(harness.BinPath))

	elsewhere := t.TempDir()
	other := filepath.Join(elsewhere, "fyr")
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", elsewhere) // this home's bin dir is NOT on PATH

	var out bytes.Buffer
	if err := run([]string{"install", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	want := "note: " + binDir + " is not on PATH, so `fyr` runs " + other + " instead; put " + binDir + " ahead of it"
	if !strings.Contains(out.String(), want) {
		t.Errorf("install missing %q:\n%s", want, out.String())
	}
	// Not the generic "warning:" — setting PATH to one directory also drops
	// `go`, and install rightly warns about that. Only the shadow warning
	// must be absent.
	if strings.Contains(out.String(), "comes before") {
		t.Errorf("install warned twice about one situation:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"status", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("status: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "shadowed") {
		t.Errorf("status reported a shadow for a home whose bin dir is off PATH:\n%s", out.String())
	}

	// A dry run installs nothing, so it says nothing about PATH: on a fresh
	// home it would otherwise call the bin dir "not on PATH" whether it is or
	// not, a false diagnosis from a read-only mode.
	fresh := t.TempDir()
	out.Reset()
	if err := run([]string{"install", "--dry-run", "--repo", repo, "--home", fresh}, &out); err != nil {
		t.Fatalf("install --dry-run: %v\n%s", err, out.String())
	}
	// The bin-dir phrases only: PATH here is one directory, so install rightly
	// warns that `go` is not on it, and that warning is not what this pins.
	for _, claim := range []string{"add it so `fyr` is available everywhere", "so `fyr` runs", "comes before"} {
		if strings.Contains(out.String(), claim) {
			t.Errorf("install --dry-run made a PATH claim about a binary it did not write (%q):\n%s", claim, out.String())
		}
	}
}

// `update` replaces ~/.local/bin/fyr and nothing else. On devbox it was
// run by /usr/local/bin/fyr (v1.2.1), which stayed v1.2.1 while
// ~/.local/bin became v1.2.2, and bash's command hash kept running the old
// file (BUG-079). UpdateFromRelease prints the note itself when it REPLACED
// the binary (its positive case is beside it, in internal/harness). An update
// that installed nothing — up to date, --check, a fresh home — says nothing:
// there is nothing for the running file to be behind, and on the
// older-release branch the running file is the newer one (security review
// 2026-09-18, round 2).
func TestUpToDateUpdateSaysNothingAboutTheRunningBinary(t *testing.T) {
	t.Setenv("FYR_REPO", "")
	t.Chdir(t.TempDir())
	defer func(v string) { version = v }(version)
	version = "v1.0.0"

	// A gh that answers the one call the up-to-date path makes.
	ghDir := t.TempDir()
	script := "#!/bin/sh" + "\n" + "printf " + "'v1.0.0" + "\\n" + "'" + "\n"
	if err := os.WriteFile(filepath.Join(ghDir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// A PATH directory whose fyr IS this process's executable — the
	// shape of /usr/local/bin/fyr on devbox.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(self, filepath.Join(elsewhere, "fyr")); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	installed := filepath.Join(home, harness.BinPath)
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+elsewhere)

	for _, args := range [][]string{
		{"update", "--home", home},
		{"update", "--check", "--home", home},
	} {
		var out bytes.Buffer
		if err := run(args, &out); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		if !strings.Contains(out.String(), "up to date (v1.0.0)") {
			t.Fatalf("%v: expected the up-to-date path:\n%s", args, out.String())
		}
		if strings.Contains(out.String(), "not from the installed binary") {
			t.Errorf("%v installed nothing and still described the running binary as behind:\n%s", args, out.String())
		}
	}
}
