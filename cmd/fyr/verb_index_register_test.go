package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/auto"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

func initRegisterCheckoutAt(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".agents/scratch/*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agents", "trail.jsonl"), []byte("{\"kind\":\"bug\",\"id\":\"BUG-001\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.email=t@example.invalid", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

func runIndexRegister(t *testing.T, root string) (home, out string) {
	t.Helper()
	home = t.TempDir()
	var buf bytes.Buffer
	if err := run([]string{"index", "--register", "-C", root, "--home", home}, &buf); err != nil {
		t.Fatalf("index --register %s: %v\n%s", root, err, buf.String())
	}
	return home, buf.String()
}

func TestIndexRegisterSkipsEphemeralPaths(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}

	ephemeralRelPaths := []string{
		"real-home-smoke-20261121",
		"fyr-smoke-999",
		filepath.Join(harness.StateDir, "dev", "smoke-42"),
		filepath.Join("sandbox", "smoke-77"),
		filepath.Join("sandbox", "fyr-eval", "trial-1"),
		filepath.Join("sandbox", "fyr-auto", "run-1"),
		filepath.Join("host-repo", ".agents", "scratch", "e2e-home"),
	}
	for _, rel := range ephemeralRelPaths {
		t.Run(rel, func(t *testing.T) {
			root := initRegisterCheckoutAt(t, filepath.Join(base, rel))
			if !auto.IsEphemeralPath(root) {
				t.Fatalf("auto.IsEphemeralPath(%q) = false, want true", root)
			}
			home, got := runIndexRegister(t, root)
			if !strings.Contains(got, "note: skipping registration for ephemeral path") ||
				strings.Contains(got, "\nregistered ") || strings.HasPrefix(got, "registered ") {
				t.Errorf("unexpected ephemeral register output:\n%s", got)
			}
			if trail.Registered(filepath.Join(home, harness.StateDir), root) {
				t.Errorf("ephemeral path %s received a registration marker", root)
			}
			if ps, err := search.ListProjects(home); err != nil || len(ps) != 0 {
				t.Errorf("ephemeral path polluted search registry: projects=%v err=%v", ps, err)
			}
		})
	}

	t.Run("linked worktree under worktrees dir skips without polluting main", func(t *testing.T) {
		mainRepo := initRegisterCheckoutAt(t, filepath.Join(base, "normal-main"))
		wtDir := filepath.Join(base, ".gemini", "antigravity-cli", "worktrees", "conv-123", "subagent-1")
		if err := os.MkdirAll(filepath.Dir(wtDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "-C", mainRepo, "worktree", "add", "-q", "-b", "wt-branch", wtDir).CombinedOutput(); err != nil {
			t.Fatalf("git worktree add: %v\n%s", err, out)
		}
		t.Cleanup(func() {
			_ = exec.Command("git", "-C", mainRepo, "worktree", "remove", "--force", wtDir).Run()
		})
		home, got := runIndexRegister(t, wtDir)
		if !strings.Contains(got, "note: skipping registration for ephemeral path") {
			t.Errorf("expected skip note for worktree, got:\n%s", got)
		}
		if trail.Registered(filepath.Join(home, harness.StateDir), mainRepo) {
			t.Errorf("worktree registration polluted main repo %s with marker", mainRepo)
		}
		if ps, err := search.ListProjects(home); err != nil || len(ps) != 0 {
			t.Errorf("worktree polluted search registry: projects=%v err=%v", ps, err)
		}
	})

	t.Run("non-ephemeral lookalike repos still register", func(t *testing.T) {
		for _, rel := range []string{filepath.Join("code", "smoke-detector"), filepath.Join("sandbox", "my-app")} {
			root := initRegisterCheckoutAt(t, filepath.Join(base, rel))
			if auto.IsEphemeralPath(root) {
				t.Fatalf("auto.IsEphemeralPath(%q) = true, want false", root)
			}
			home, got := runIndexRegister(t, root)
			if !strings.Contains(got, "registered "+root) {
				t.Errorf("non-ephemeral repo %s did not register:\n%s", rel, got)
			}
			if ps, err := search.ListProjects(home); err != nil || len(ps) != 1 || ps[0].Root != root {
				t.Errorf("non-ephemeral repo %s registry = %v (err=%v)", rel, ps, err)
			}
		}
	})
}
