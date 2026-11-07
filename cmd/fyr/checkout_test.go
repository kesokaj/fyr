package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestKnownCheckout(t *testing.T) {
	home := t.TempDir()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FYR_REPO", "")
	if err := knownCheckout("", home, repo); err != nil {
		t.Errorf("first install (no manifest) must pass: %v", err)
	}
	if err := harness.SaveState(home, harness.State{Repo: repo}); err != nil {
		t.Fatal(err)
	}
	if err := knownCheckout("", home, repo); err != nil {
		t.Errorf("same checkout must pass: %v", err)
	}
	other := t.TempDir()
	if err := knownCheckout("", home, other); err == nil {
		t.Error("a checkout found only by the directory walk must be refused when another is installed")
	}
	if err := knownCheckout(other, home, other); err != nil {
		t.Errorf("explicit --repo must pass: %v", err)
	}
	t.Setenv("FYR_REPO", other)
	if err := knownCheckout("", home, other); err != nil {
		t.Errorf("$FYR_REPO must pass: %v", err)
	}
}

// TestDryRunNeverBuilds: with a toolchain present, no child guard and a
// checkout-shaped repo, a dry run must report the rebuild instead of
// performing it, and write nothing.
func TestDryRunNeverBuilds(t *testing.T) {
	t.Setenv(harness.ChildEnv, "")
	// A checkout-SHAPED repo of its own, not this checkout (`../..`): when
	// the test runs inside a plain copy with no .git — an unpacked bundle, or
	// once a copy vendored inside a consumer project — `../..` is not a
	// checkout, the dry run rightly prints the plain-copy line, and the
	// assertion below failed for a shape the test never meant to cover
	// (styrman-crew BUG-058). IsCheckout asks only for a `.git` entry and
	// Inspect tolerates one git cannot read, so an empty directory is enough.
	repo := checkoutShaped(t)
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"install", "--dry-run", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	for _, want := range []string{"[dry-run] the binary would be rebuilt from", "[dry-run] "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
	// Reinstall removes its build directory itself, so its absence proves
	// nothing; what a rebuild would leave is an installed binary and a
	// child's output, and a dry run must leave neither.
	for _, rel := range []string{harness.BinPath, ".gemini", ".config"} {
		if _, err := os.Stat(filepath.Join(home, rel)); !os.IsNotExist(err) {
			t.Errorf("dry run wrote ~/%s: stat err = %v", rel, err)
		}
	}
	if strings.Contains(out.String(), "building fyr from") {
		t.Errorf("dry run ran a build:\n%s", out.String())
	}
}

// TestPlainCopyNeverRebuilds: an unpacked bundle's binary was built from
// the same tree, the tree may be read-only to the installing user, and it
// cannot be updated with git; install from it must deploy the running
// binary without a rebuild, without the no-toolchain warning, and
// without creating a scratch directory inside the copy (BUG-015).
func TestPlainCopyNeverRebuilds(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH: the rebuild path is unreachable, nothing to prove")
	}
	t.Setenv(harness.ChildEnv, "")
	os.Unsetenv(harness.ChildEnv)
	copyDir := plainCopy(t)
	home := t.TempDir()

	var out bytes.Buffer
	if err := run([]string{"install", "--repo", copyDir, "--home", home}, &out); err != nil {
		t.Fatalf("install from a plain copy: %v\n%s", err, out.String())
	}
	for _, never := range []string{"building fyr from", "would be rebuilt", "warning: go is not on PATH"} {
		if strings.Contains(out.String(), never) {
			t.Errorf("install from a plain copy printed %q:\n%s", never, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(copyDir, ".agents")); !os.IsNotExist(err) {
		t.Errorf("install created %s inside the plain copy (err=%v)", filepath.Join(copyDir, ".agents"), err)
	}
	if _, err := os.Stat(filepath.Join(home, harness.BinPath)); err != nil {
		t.Errorf("binary not installed from the plain copy: %v", err)
	}

	out.Reset()
	if err := run([]string{"install", "--dry-run", "--repo", copyDir, "--home", home}, &out); err != nil {
		t.Fatalf("dry run from a plain copy: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "would be rebuilt") {
		t.Errorf("dry run from a plain copy announced a rebuild:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[dry-run] plain copy") {
		t.Errorf("dry run from a plain copy must say so as a dry run:\n%s", out.String())
	}
}

// An embedded install's manifest records a release, not a path: knownCheckout
// says so and never resolves it as one (security review 2026-09-17).
func TestKnownCheckoutRefusesEmbeddedManifest(t *testing.T) {
	t.Setenv("FYR_REPO", "")
	home := t.TempDir()
	if err := harness.SaveState(home, harness.State{Repo: harness.EmbeddedPrefix + "v1.2.3"}); err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	err = knownCheckout("", home, repo)
	if err == nil || !strings.Contains(err.Error(), "installed from the release binary v1.2.3") {
		t.Errorf("knownCheckout = %v, want a refusal naming the release", err)
	}
	// Named explicitly, it is the operator's decision, as for any checkout.
	if err := knownCheckout(repo, home, repo); err != nil {
		t.Errorf("knownCheckout with --repo: %v", err)
	}
}

// TestRepoFlagWarnsOnVerbWithoutSource pins IN36 of the 2026-10-27 audit:
// every FlagSet accepts --repo, and a verb that never reads it says so on
// stderr and still runs with its exit status unchanged; a verb that reads it
// stays quiet. The table is the set of verbs that ignore it, so a verb added
// without source fails here until it is placed.
func TestRepoFlagWarnsOnVerbWithoutSource(t *testing.T) {
	want := map[string]bool{"version": true, "uninstall": true, "memory": true, "dream": true,
		"metrics": true, "record": true, "resume": true, "search": true, "index": true, "check": true, "git-check": true}
	for name, v := range verbs {
		if ignores := !v.source && !v.readsRepo; ignores != want[name] {
			t.Errorf("%s: ignores --repo = %v, want %v", name, ignores, want[name])
		}
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		warn string
	}{
		{[]string{"version", "--repo", repo}, "fyr: warning: --repo is ignored by version\n"},
		{[]string{"uninstall", "--dry-run", "--repo", repo, "--home", t.TempDir()}, "fyr: warning: --repo is ignored by uninstall\n"},
		{[]string{"list", "--repo", repo, "--home", t.TempDir()}, ""},
		{[]string{"version"}, ""},
	}
	for _, tc := range cases {
		var o, e bytes.Buffer
		stderr = &e
		err := run(tc.args, &o)
		stderr = os.Stderr
		if exitOf(err) != 0 || e.String() != tc.warn {
			t.Errorf("%q: exit %d, stderr %q, want exit 0 and %q (out %q)", tc.args, exitOf(err), e.String(), tc.warn, o.String())
		}
	}
}
