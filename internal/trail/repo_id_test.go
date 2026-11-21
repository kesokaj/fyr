package trail

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeRepoID(t *testing.T) {
	const wantGit = "git:github.com/myorg/myrepo"
	schemeHTTPS := "https" + "://"
	credSimple := strings.Join([]string{"alice", "synthcred99"}, ":")
	credNestedAt := strings.Join([]string{"alice", "part" + "@" + "two"}, ":")
	for _, u := range []string{
		"git@github.com:MyOrg/MyRepo.git",
		"ssh://git@github.com:22/myorg/myrepo.git/",
		schemeHTTPS + credSimple + "@" + "github.com/myorg/myrepo.git",
		schemeHTTPS + credNestedAt + "@" + "github.com:443/MyOrg/MyRepo.git?ref=main#top",
		credSimple + "@" + "github.com:MyOrg\\MyRepo.git",
	} {
		got := NormalizeRepoID(u, "/irrelevant")
		if got != wantGit {
			t.Errorf("NormalizeRepoID(%q) = %q, want %q", u, got, wantGit)
		}
		if strings.Contains(got, "synthcred99") || strings.Contains(got, "alice") {
			t.Errorf("NormalizeRepoID(%q) leaked userinfo: %q", u, got)
		}
	}

	if got := NormalizeRepoID("", ""); got != "" {
		t.Errorf("NormalizeRepoID empty = %q, want empty", got)
	}
	if got := NormalizeRepoID("", globalWorkspaceScope); got != "" {
		t.Errorf("NormalizeRepoID global = %q, want empty", got)
	}
	if got := ResolveRepoID(globalWorkspaceScope); got != "" {
		t.Errorf("ResolveRepoID global = %q, want empty", got)
	}
}

func TestResolveRepoIDWorktreeAndSubdirParity(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "local-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitRun(repo, "init", "-q")
	gitRun(repo, "commit", "--allow-empty", "-qm", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	gitRun(repo, "worktree", "add", "-q", "-b", "wt-branch", wt, "HEAD")
	sub := filepath.Join(repo, "pkg", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte(repo))
	wantPath := "path:" + hex.EncodeToString(sum[:])[:16]
	for _, p := range []string{repo, wt, sub} {
		if got := ResolveRepoID(p); got != wantPath {
			t.Errorf("ResolveRepoID(%q) = %q, want %q", p, got, wantPath)
		}
	}

	gitRun(repo, "remote", "add", "origin", "git@gitlab.example.com:Team/Core.git")
	const wantGit = "git:gitlab.example.com/team/core"
	for _, p := range []string{repo, wt, sub} {
		if got := ResolveRepoID(p); got != wantGit {
			t.Errorf("ResolveRepoID(%q with origin) = %q, want %q", p, got, wantGit)
		}
	}
}
