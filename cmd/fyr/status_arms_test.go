package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
)

// The arms of status that its split moves into helpers, pinned before the
// split (PLAN-2026-10-27-funclen Phase 6, D13).

// statusHome installs a plain copy of this checkout into a fresh home and
// returns both.
func statusHome(t *testing.T) (repo, home string) {
	t.Helper()
	repo, home = plainCopy(t), t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"install", "--repo", repo, "--home", home}, &out); err != nil {
		t.Fatalf("install: %v\n%s", err, out.String())
	}
	return repo, home
}

func statusOut(t *testing.T, repo, home string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	err := run([]string{"status", "--repo", repo, "--home", home}, &out)
	return out.String(), exitOf(err)
}

// A changed file is listed and counted as drift, with exit 1.
func TestStatusDriftLines(t *testing.T) {
	repo, home := statusHome(t)
	rel := filepath.Join(harness.AgyRoot, "rules", "AGENTS.md")
	if err := os.WriteFile(filepath.Join(home, rel), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := statusOut(t, repo, home)
	for _, want := range []string{"  differs  ~/" + rel + "\n", "drift      0 missing, 1 differ, 0 stale — run `fyr diff`, then `fyr install`\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
}

// A manifest entry under ~/.claude that an install before fyr wrote is kept,
// not drift.
func TestStatusKeptClaudeFiles(t *testing.T) {
	repo, home := statusHome(t)
	st, _, err := harness.LoadState(home)
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(".claude", "skills", "old", "SKILL.md")
	writeTestFile(t, filepath.Join(home, kept), "old\n")
	st.Files = append(st.Files, kept)
	if err := harness.SaveState(home, st); err != nil {
		t.Fatal(err)
	}
	out, code := statusOut(t, repo, home)
	want := "kept       1 files under ~/.claude that an install before fyr wrote for Claude Code; fyr never removes them, and the next install drops them from its manifest\n"
	if !strings.Contains(out, want) || !strings.Contains(out, "drift      none — ") || code != 0 {
		t.Errorf("exit %d, output lacks %q or no drift:\n%s", code, want, out)
	}
}

// A skill file Plan cannot read, and a deployed file Status cannot read,
// each fail status with the error.
func TestStatusPlanAndReadErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-0 file")
	}
	repo, home := statusHome(t)
	locked := filepath.Join(repo, "skills", "bash", "locked.txt")
	writeTestFile(t, locked, "x\n")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	if out, code := statusOut(t, repo, home); code != 1 || !strings.Contains(out, "repo has   ") {
		t.Errorf("plan error: exit %d\n%s", code, out)
	}
	var out bytes.Buffer
	err := run([]string{"status", "--repo", repo, "--home", home}, &out)
	if err == nil || !strings.Contains(err.Error(), "skill bash: ") {
		t.Errorf("plan error: %v", err)
	}
	os.Remove(locked)

	deployed := filepath.Join(home, harness.AgyRoot, "rules", "AGENTS.md")
	if err := os.Chmod(deployed, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(deployed, 0o644) })
	out.Reset()
	err = run([]string{"status", "--repo", repo, "--home", home}, &out)
	if err == nil || !strings.Contains(err.Error(), "permission denied") || strings.Contains(out.String(), "drift ") {
		t.Errorf("read error: %v\n%s", err, out.String())
	}
}

// The retro line: the brain store's global retro records alone, the old
// retros.jsonl beside them never counting (ADR-087), and the 14-day hint.
func TestStatusRetroLine(t *testing.T) {
	repo, home := statusHome(t)
	writeTestFile(t, filepath.Join(home, harness.StateDir, "retros.jsonl"), "{}\n")
	writeTestFile(t, filepath.Join(home, harness.StateDir, "proposals", "2026-09-17-retro.md"), "# retro\n")
	out, _ := statusOut(t, repo, home)
	if want := "retro      none yet — run /fyr-retro"; !strings.Contains(out, want) {
		t.Errorf("the old files counted; output lacks %q:\n%s", want, out)
	}

	old := time.Now().Add(-30 * 24 * time.Hour)
	if _, err := brain.Add(brain.Options{Home: home, Dir: t.TempDir(), Global: true, Now: func() time.Time { return old }},
		brain.AddInput{Kind: "retro", Title: "old", Body: strings.NewReader("an old retro")}); err != nil {
		t.Fatal(err)
	}
	out, _ = statusOut(t, repo, home)
	want := "retro      last proposal " + old.UTC().Format("2006-01-02") + " (30 days ago) — past 14 days, run /fyr-retro at the end of the next session\n"
	if !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}

	if _, err := recordCmd(t, "a retro\n", "record", "add", "retro", "--global", "--title", "r", "--home", home); err != nil {
		t.Fatal(err)
	}
	out, _ = statusOut(t, repo, home)
	want = "retro      last proposal " + time.Now().UTC().Format("2006-01-02") + " (0 days ago)\n"
	if !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
}

// An installed binary stamped with another commit than the checkout's is
// named older than the repo.
func TestStatusBinaryOlderThanRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("builds fyr")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo, home := plainCopy(t), t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@example.invalid", "-c", "user.name=t", "-c", "commit.gpgsign=false", "add", "-A"},
		{"-c", "user.email=t@example.invalid", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "fixture"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = gitCleanEnv()
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
	bin := filepath.Join(home, harness.BinPath)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/fyr")
	build.Dir = checkoutRoot
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	st := harness.State{Repo: repo, Commit: "x", Files: []string{harness.BinPath}}
	if err := harness.SaveState(home, st); err != nil {
		t.Fatal(err)
	}
	got, code := statusOut(t, repo, home)
	commit := harness.Inspect(repo).Commit
	if want := " — older than the repo (" + commit + "); run `fyr update`\n"; !strings.Contains(got, want) {
		t.Errorf("exit %d, output lacks %q:\n%s", code, want, got)
	}
}

// writeTestFile writes content at p, creating its directories.
func writeTestFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
