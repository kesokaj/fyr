package gate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func initTestGoRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.name", "Test Operator")
	runGit(t, dir, "config", "user.email", "test@example.com")

	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/testmod\n\ngo 1.24\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc Add(a, b int) int { return a + b }\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(dir, "main_test.go"), `package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdd(t *testing.T) {
	if os.Getenv("TEST_TOUCH_GO_FILE") == "1" {
		_ = os.WriteFile(filepath.Join(".", "touched_mid_verify.go"), []byte("package main\n"), 0o644)
	}
	if Add(1, 2) != 3 {
		t.Fatal("bad sum")
	}
}
`)
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial commit")
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 11, 15, 16, 0, 0, 0, time.UTC)
}

func TestVerifyHappyAndMustFail(t *testing.T) {
	ctx := context.Background()
	repo := initTestGoRepo(t)
	stampFile := filepath.Join(repo, filepath.FromSlash(StampRelPath))

	t.Run("happy", func(t *testing.T) {
		rep, err := Verify(ctx, VerifyOptions{Dir: repo, Now: fixedNow})
		if err != nil {
			t.Fatalf("Verify happy failed: %v", err)
		}
		if !rep.OK || rep.TreeSHA256 == "" || rep.StampPath != stampFile {
			t.Fatalf("unexpected report: %+v", rep)
		}
		info, err := os.Lstat(stampFile)
		if err != nil {
			t.Fatalf("stat stamp: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("stamp mode = %o, want 0600", info.Mode().Perm())
		}
		st, err := ReadStamp(repo)
		if err != nil || !st.OK || st.TreeSHA256 != rep.TreeSHA256 {
			t.Fatalf("ReadStamp mismatch: st=%+v err=%v", st, err)
		}
	})

	t.Run("must-fail gofmt clears stale stamp", func(t *testing.T) {
		writeFile(t, filepath.Join(repo, "badfmt.go"), "package main\nfunc   badFmt(   )  {}\n")
		t.Cleanup(func() { _ = os.Remove(filepath.Join(repo, "badfmt.go")) })

		_, err := Verify(ctx, VerifyOptions{Dir: repo, Now: fixedNow})
		if !errors.Is(err, ErrFailed) {
			t.Fatalf("expected ErrFailed for gofmt, got %v", err)
		}
		if _, statErr := os.Lstat(stampFile); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("stale stamp should be removed on failure, stat=%v", statErr)
		}
	})

	t.Run("must-fail go vet", func(t *testing.T) {
		writeFile(t, filepath.Join(repo, "badvet.go"), "package main\n\nimport \"fmt\"\n\nfunc badVet() string {\n\treturn fmt.Sprintf(\"%d\", \"str\")\n}\n")
		t.Cleanup(func() { _ = os.Remove(filepath.Join(repo, "badvet.go")) })

		_, err := Verify(ctx, VerifyOptions{Dir: repo, Now: fixedNow})
		if !errors.Is(err, ErrFailed) {
			t.Fatalf("expected ErrFailed for go vet, got %v", err)
		}
	})

	t.Run("must-fail go test", func(t *testing.T) {
		writeFile(t, filepath.Join(repo, "fail_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestBoom(t *testing.T) { t.Fatal(\"boom\") }\n")
		t.Cleanup(func() { _ = os.Remove(filepath.Join(repo, "fail_test.go")) })

		_, err := Verify(ctx, VerifyOptions{Dir: repo, Now: fixedNow})
		if !errors.Is(err, ErrFailed) {
			t.Fatalf("expected ErrFailed for go test, got %v", err)
		}
	})

	t.Run("must-fail fyr check", func(t *testing.T) {
		readme := filepath.Join(repo, "README.md")
		writeFile(t, readme, "See `main.go:99`\n")
		runGit(t, repo, "add", "README.md")
		t.Cleanup(func() {
			runGit(t, repo, "reset", "-q", "HEAD", "--", "README.md")
			_ = os.Remove(readme)
		})

		_, err := Verify(ctx, VerifyOptions{Dir: repo, Now: fixedNow})
		if !errors.Is(err, ErrFailed) {
			t.Fatalf("expected ErrFailed for fyr check, got %v", err)
		}
	})

	t.Run("must-fail mid-verify modification", func(t *testing.T) {
		t.Setenv("TEST_TOUCH_GO_FILE", "1")
		t.Cleanup(func() { _ = os.Remove(filepath.Join(repo, "touched_mid_verify.go")) })

		_, err := Verify(ctx, VerifyOptions{Dir: repo, Now: fixedNow})
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("expected ErrRefused for mid-verify tree mutation, got %v", err)
		}
		if _, statErr := os.Lstat(stampFile); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("stamp must not persist after mid-verify change, stat=%v", statErr)
		}
	})

	t.Run("must-fail symlink stamp", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "outside.txt")
		writeFile(t, outside, "keep")
		_ = os.Remove(stampFile)
		_ = os.MkdirAll(filepath.Dir(stampFile), 0o755)
		if err := os.Symlink(outside, stampFile); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(stampFile) })

		_, err := Verify(ctx, VerifyOptions{Dir: repo, Now: fixedNow})
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("expected ErrRefused for symlinked stamp, got %v", err)
		}
		got, _ := os.ReadFile(outside)
		if string(got) != "keep" {
			t.Fatalf("symlink target was modified: %q", string(got))
		}
	})
}

func TestGofmtBatchesLargeSlices(t *testing.T) {
	dir := t.TempDir()
	files := []string{"a.go", "b.go", "c.go", "d.go", "z.go"}
	for _, f := range files[:4] {
		writeFile(t, filepath.Join(dir, f), "package main\n")
	}
	writeFile(t, filepath.Join(dir, "z.go"), "package main\nfunc   bad(  ) {}\n")

	unfmt, _, err := runGofmtBatches(context.Background(), dir, files, 2)
	if err != nil {
		t.Fatalf("runGofmtBatches err=%v", err)
	}
	if len(unfmt) != 1 || unfmt[0] != "z.go" {
		t.Fatalf("got %v, want [z.go]", unfmt)
	}
}
