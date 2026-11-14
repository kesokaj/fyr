package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// TestReleaseBinaryWithoutCheckout: a release binary run where no harness tree
// is found installs the files built into it, and every command follows the
// per-command table in the release-binary plan (D13, D14, D15, D17). A
// development build in the same place keeps today's error.
func TestReleaseBinaryWithoutCheckout(t *testing.T) {
	t.Setenv("FYR_REPO", "")
	t.Setenv(harness.ChildEnv, "")
	t.Chdir(t.TempDir())
	home := t.TempDir()
	var out bytes.Buffer

	if err := run([]string{"install", "--home", home}, &out); err == nil || !strings.Contains(err.Error(), "harness repo not found") {
		t.Fatalf("development build, no tree: err = %v, want harness repo not found", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini")); !os.IsNotExist(err) {
		t.Fatalf("a refused install wrote into home: %v", err)
	}

	defer func(v string) { version = v }(version)
	version = "v9.9.9"

	// A release binary takes no tree at all, so --repo is refused whatever it
	// points at — and refused, not ignored (ADR-034).
	out.Reset()
	if err := run([]string{"install", "--repo", t.TempDir(), "--home", home}, &out); err == nil || !strings.Contains(err.Error(), "never a tree on disk") {
		t.Errorf("release binary, --repo at a non-tree: err = %v, want the refusal", err)
	}
	out.Reset()
	realTree := filepath.Join(t.TempDir(), "harness")
	for rel, body := range map[string]string{
		"context/AGENTS.md": "# ctx\n",
		"skills/s/SKILL.md": "---\nname: s\ndescription: \"x\"\n---\nbody\n",
	} {
		full := filepath.Join(realTree, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := run([]string{"install", "--repo", realTree, "--home", home}, &out); err == nil || !strings.Contains(err.Error(), "never a tree on disk") {
		t.Errorf("release binary, --repo at a real tree: err = %v, want the same refusal", err)
	}

	out.Reset()
	if err := run([]string{"install", "--home", home}, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "release binary v9.9.9") || strings.Contains(out.String(), "building") {
		t.Errorf("install output does not name the release binary, or built:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, harness.BinPath)); err != nil {
		t.Errorf("the running binary was not installed: %v", err)
	}
	st, found, err := harness.LoadState(home)
	if err != nil || !found || st.Repo != "embedded:v9.9.9" || st.Commit != "" {
		t.Errorf("state = %+v (found %v, err %v), want repo embedded:v9.9.9 and no commit", st, found, err)
	}

	out.Reset()
	if err := run([]string{"status", "--home", home}, &out); err != nil {
		t.Fatalf("status: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"source     embedded v9.9.9 (release binary; update downloads the newest release)",
		"from release v9.9.9",
		"binary     ",
		"drift      none",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "repo       ") {
		t.Errorf("status prints a repo line for an embedded install:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"diff", "--home", home}, &out); err != nil {
		t.Errorf("diff: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"list", "--home", home}, &out); err != nil || !strings.Contains(out.String(), "### Skills") {
		t.Errorf("list: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"render", "--home", home}, &out); err != nil || !strings.HasPrefix(out.String(), harness.Marker) {
		t.Errorf("render: %v\n%.200s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"adopt", "anything", "--home", home}, &out); err == nil || err.Error() != "adopt needs a harness checkout (`git clone git@github.com:kesokaj/fyr.git`, then run adopt from a build of it); this install is a release binary and carries no tree to adopt into" {
		t.Errorf("adopt: err = %v", err)
	}
	// `update` on a release binary goes to the HTTPS release flow (offline in tests).
	out.Reset()
	func() {
		setOfflineReleaseHTTP(t)
		t.Setenv("PATH", t.TempDir())
		if err := run([]string{"update", "--home", home}, &out); err == nil || !strings.Contains(err.Error(), "offline test network disabled") {
			t.Errorf("update offline: err = %v, want offline error", err)
		}
	}()
}

// TestReleaseBinaryUpdateCheckExitsTwo: on a release binary, `update --check`
// reports a newer release and exits 2, the same code a checkout that is behind
// gives (plan § Per command with an embedded source).
func TestReleaseBinaryUpdateCheckExitsTwo(t *testing.T) {
	t.Setenv("FYR_REPO", "")
	t.Chdir(t.TempDir())
	defer func(v string) { version = v }(version)
	version = "v1.0.0"

	serveLatestReleaseForTest(t, "v9.9.9", nil)
	t.Setenv("PATH", t.TempDir())

	var out bytes.Buffer
	err := run([]string{"update", "--check", "--home", t.TempDir()}, &out)
	var code exitCode
	if !errors.As(err, &code) || int(code) != 2 {
		t.Fatalf("err = %v, want exit code 2", err)
	}
	if !strings.Contains(out.String(), "newer release v9.9.9 available") {
		t.Errorf("output: %q", out.String())
	}
}

// The audit of 2026-09-18 (`reviews/2026-09-18-review-harness-merged.md`,
// findings 2, 8, 9): a plain copy is a real kind of place, and every refusal
// on it names the cause and the alternative.
func TestUpdateOnPlainCopyNamesTheAlternative(t *testing.T) {
	copy := plainCopyOf(t)
	home := t.TempDir()
	var out bytes.Buffer
	err := run([]string{"update", "--repo", copy, "--home", home}, &out)
	if err == nil {
		t.Fatalf("update on a plain copy succeeded:\n%s", out.String())
	}
	for _, want := range []string{"plain copy", "cannot be updated with git", "checkout", "release binary"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %v", want, err)
		}
	}
}

func TestAdoptOnPlainCopySaysWhereToCommit(t *testing.T) {
	copy := plainCopyOf(t)
	home := t.TempDir()
	src := filepath.Join(t.TempDir(), "widget")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: widget\ndescription: a widget\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"adopt", src, "--repo", copy, "--home", home}, &out); err != nil {
		t.Fatalf("adopt on a plain copy: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "commit and push the repo") {
		t.Errorf("a plain copy has no repository to commit to:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "checkout") {
		t.Errorf("the next step does not name a checkout:\n%s", out.String())
	}
}

func TestAdoptOnReleaseBinaryNamesTheAlternative(t *testing.T) {
	t.Setenv("FYR_REPO", "")
	t.Chdir(t.TempDir())
	defer func(v string) { version = v }(version)
	version = "v9.9.9"
	var out bytes.Buffer
	err := run([]string{"adopt", "widget", "--home", t.TempDir()}, &out)
	if err == nil {
		t.Fatal("adopt on a release binary succeeded")
	}
	for _, want := range []string{"release binary", "checkout", "git clone"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q: %v", want, err)
		}
	}
}

func TestStatusOnPlainCopyNamesTheRefusal(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	copy := plainCopyOf(t)
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"install", "--repo", copy, "--home", home}, &out); err != nil {
		t.Fatalf("install from a plain copy: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"status", "--repo", copy, "--home", home}, &out); err != nil {
		t.Fatalf("status: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "(plain copy, not its own git checkout; update is refused)") {
		t.Errorf("status does not say a plain copy cannot update:\n%s", out.String())
	}
}
