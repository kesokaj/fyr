package check

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitCheckGolden(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	git(t, filepath.Dir(remote), "init", "--bare", "-q", remote)

	dir := repo(t, map[string]string{"README.md": "hello\n"})
	git(t, dir, "remote", "add", "origin", remote)
	git(t, dir, "push", "-q", "-u", "origin", "HEAD")

	writeFile := func(rel, body string) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Clean checkout pushed to remote with no tags.
	cleanGot, err := GitCheck(dir)
	if err != nil {
		t.Fatalf("GitCheck clean: %v", err)
	}
	cleanWant := strings.Join([]string{
		"## git status --short --untracked-files=all",
		"(none)",
		"",
		"## git log --branches --not --remotes --oneline",
		"(none)",
		"",
		"## git tag",
		"(none)",
		"",
	}, "\n")
	if cleanGot != cleanWant {
		t.Fatalf("clean GitCheck mismatch:\ngot:\n%s\nwant:\n%s", cleanGot, cleanWant)
	}

	// Add an unpushed commit, a tag, a modified tracked file, and a nested untracked file.
	writeFile("tracked.txt", "v1\n")
	git(t, dir, "add", "tracked.txt")
	git(t, dir, "commit", "-q", "-m", "unpushed change")
	sha := git(t, dir, "rev-parse", "--short", "HEAD")
	git(t, dir, "tag", "v0.1.0")
	writeFile("tracked.txt", "v2\n")
	writeFile("nested/untracked.txt", "new\n")

	dirtyGot, err := GitCheck(dir)
	if err != nil {
		t.Fatalf("GitCheck dirty: %v", err)
	}
	dirtyWant := strings.Join([]string{
		"## git status --short --untracked-files=all",
		" M tracked.txt",
		"?? nested/untracked.txt",
		"",
		"## git log --branches --not --remotes --oneline",
		sha + " unpushed change",
		"",
		"## git tag",
		"v0.1.0",
		"",
	}, "\n")
	if dirtyGot != dirtyWant {
		t.Fatalf("dirty GitCheck mismatch:\ngot:\n%s\nwant:\n%s", dirtyGot, dirtyWant)
	}
}

func TestGitCheckRefusesNonCheckout(t *testing.T) {
	plain := t.TempDir()
	if err := os.WriteFile(filepath.Join(plain, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := GitCheck(plain)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
}
