package report

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCandidateGitRootsMustPassAndMustFail(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	notRepo := filepath.Join(base, "plain")
	for _, d := range []string{repo, notRepo} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	missing := filepath.Join(base, "does-not-exist")
	got := CandidateGitRoots("", "scoped", false, []string{repo, repo, notRepo, missing}, nil)
	wantRepo, _ := filepath.EvalSymlinks(repo)
	if wantRepo == "" {
		wantRepo = repo
	}
	if len(got) != 1 || filepath.Clean(got[0]) != filepath.Clean(wantRepo) {
		t.Fatalf("CandidateGitRoots = %v, want [%s]", got, wantRepo)
	}
	if empty := CandidateGitRoots("", "scoped", false, []string{notRepo, missing}, nil); len(empty) != 0 {
		t.Fatalf("CandidateGitRoots non-git = %v, want empty", empty)
	}
}
