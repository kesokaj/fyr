package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestUpdateFromZipDryRunWritesNothing(t *testing.T) {
	isolate(t)
	zp := markedZip(t)
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"update", "--from-zip", zp, "--home", home, "--dry-run"}, &out); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "[dry-run] zip") {
		t.Errorf("dry run does not say it is one:\n%s", out.String())
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("dry run on a fresh home wrote %v", names)
	}
}

// A zip-* directory an interrupted run left behind is swept once it is an
// hour old; a fresh one, which may belong to a run in progress, is kept; a
// dry run sweeps nothing.
func TestUpdateFromZipSweepsStaleDirs(t *testing.T) {
	isolate(t)
	zp := markedZip(t)
	home := t.TempDir()
	cfg := filepath.Join(home, harness.StateDir)
	old, fresh := filepath.Join(cfg, "zip-old"), filepath.Join(cfg, "zip-fresh")
	// An old directory that is not an unpack directory (a feedback clone an
	// old version kept, say) is never the sweep's to remove.
	notOurs := filepath.Join(cfg, "feedback-old")
	for _, d := range []string{old, fresh, notOurs} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * time.Hour)
	for _, d := range []string{old, notOurs} {
		if err := os.Chtimes(d, past, past); err != nil {
			t.Fatal(err)
		}
	}
	if err := run([]string{"update", "--from-zip", zp, "--home", home, "--dry-run"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("a dry run swept %s", old)
	}
	if err := run([]string{"update", "--from-zip", zp, "--home", home}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Errorf("the stale %s was not swept", old)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("the fresh %s was swept: %v", fresh, err)
	}
	if _, err := os.Stat(notOurs); err != nil {
		t.Errorf("the sweep removed %s, which is not an unpack directory: %v", notOurs, err)
	}
}

// update --dry-run on a release binary changes nothing: it never downloads,
// never replaces the binary. It says what a real update would do.
func TestReleaseUpdateDryRunChangesNothing(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	t.Setenv("FYR_REPO", "")
	t.Chdir(t.TempDir())
	defer func(v string) { version = v }(version)
	version = "v1.0.0"
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "gh.log")
	script := "#!/bin/sh\n" + "echo \"$@\" >> " + log + "\n" + "printf 'v9.9.9\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"update", "--dry-run", "--home", home}, &out); err != nil {
		t.Fatalf("update --dry-run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "[dry-run]") || !strings.Contains(out.String(), "v9.9.9") {
		t.Errorf("dry run does not say what update would do:\n%s", out.String())
	}
	calls, _ := os.ReadFile(log)
	if strings.Contains(string(calls), "download") {
		t.Errorf("update --dry-run downloaded:\n%s", calls)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("update --dry-run wrote %d entries under the home", len(entries))
	}
}

// The sweep matches names, not a glob built from the home: a home whose path
// holds glob metacharacters sweeps its own stale directory and nobody else's.
func TestUpdateFromZipSweepWithGlobCharsInHome(t *testing.T) {
	isolate(t)
	zp := markedZip(t)
	base := t.TempDir()
	home, other := filepath.Join(base, "h[1]"), filepath.Join(base, "h1")
	past := time.Now().Add(-2 * time.Hour)
	var mine, theirs string
	for _, h := range []string{home, other} {
		d := filepath.Join(h, harness.StateDir, "zip-old")
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(d, past, past); err != nil {
			t.Fatal(err)
		}
		if h == home {
			mine = d
		} else {
			theirs = d
		}
	}
	if err := run([]string{"update", "--from-zip", zp, "--home", home}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mine); err == nil {
		t.Errorf("the home's own stale %s was not swept", mine)
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Errorf("another home's %s was swept", theirs)
	}
}

// A dry run on a fresh home (no ~/.config/fyr yet) unpacks beside the
// zip, never under the system temp directory, and removes that directory
// again: the zip's folder and TMPDIR hold nothing new afterwards.
func TestUpdateFromZipDryRunCleansTempDir(t *testing.T) {
	isolate(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	zp := markedZip(t)
	zdir := filepath.Dir(zp)
	beforeTmp, _ := os.ReadDir(tmp)
	beforeZip, _ := os.ReadDir(zdir)
	if err := run([]string{"update", "--from-zip", zp, "--home", t.TempDir(), "--dry-run"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	afterTmp, _ := os.ReadDir(tmp)
	if len(afterTmp) != len(beforeTmp) {
		t.Errorf("the dry run left %d new entries in TMPDIR", len(afterTmp)-len(beforeTmp))
	}
	afterZip, _ := os.ReadDir(zdir)
	if len(afterZip) != len(beforeZip) {
		var names []string
		for _, e := range afterZip {
			names = append(names, e.Name())
		}
		t.Errorf("the dry run left %d new entries beside the zip: %v", len(afterZip)-len(beforeZip), names)
	}
}

// update --dry-run on a checkout that is behind reports and changes nothing:
// no pull, no rebuild, no install.
func TestCheckoutUpdateDryRunChangesNothing(t *testing.T) {
	t.Setenv("FYR_REPO", "")
	repo, marker := gitFixture(t)
	base := filepath.Dir(repo)
	other := filepath.Join(base, "other")
	skill := filepath.Join(repo, "skills", "alpha", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("---\nname: alpha\ndescription: Alpha.\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "-C", repo, "add", "-A"},
		{"git", "-C", repo, "commit", "--quiet", "-m", "harness-shaped"},
		{"git", "-C", repo, "push", "--quiet"},
		{"git", "clone", "--quiet", filepath.Join(base, "origin.git"), other},
		{"git", "-C", other, "-c", "user.email=f@example.invalid", "-c", "user.name=f", "commit", "--quiet", "--allow-empty", "-m", "ahead"},
		{"git", "-C", other, "push", "--quiet", "origin", "HEAD:main"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Env = append(gitCleanEnv(), "GIT_CONFIG_GLOBAL="+filepath.Join(base, "gitconfig"), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	head := func() string {
		out, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
		return strings.TrimSpace(string(out))
	}
	before := head()
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"update", "--dry-run", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("update --dry-run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "behind upstream by 1") || !strings.Contains(out.String(), "[dry-run]") {
		t.Errorf("the dry run does not say what update would do:\n%s", out.String())
	}
	if head() != before {
		t.Errorf("update --dry-run pulled: HEAD moved from %s", before)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("update --dry-run ran an install")
	}
}

// The sweep runs only after a successful install: a run refused at any point
// (the unpack here; the install below) changes nothing, not even a stale leftover.
func TestUpdateFromZipRefusedRunSweepsNothing(t *testing.T) {
	isolate(t)
	home := t.TempDir()
	stale := filepath.Join(home, harness.StateDir, "zip-old")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatal(err)
	}
	notZip := filepath.Join(t.TempDir(), "x.zip")
	if err := os.WriteFile(notZip, []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"update", "--from-zip", notZip, "--home", home}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "not a readable zip") {
		t.Fatalf("err = %v, want the not-a-zip refusal", err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("a refused run swept %s", stale)
	}
}
