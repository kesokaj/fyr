package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitCleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "GIT_DIR="),
			strings.HasPrefix(kv, "GIT_WORK_TREE="),
			strings.HasPrefix(kv, "GIT_INDEX_FILE="),
			strings.HasPrefix(kv, "GIT_OBJECT_DIRECTORY="),
			strings.HasPrefix(kv, "GIT_COMMON_DIR="),
			strings.HasPrefix(kv, "GIT_CONFIG"):
			continue
		}
		env = append(env, kv)
	}
	return env
}

// gitFixture builds a real checkout with an upstream: a bare origin, a clone
// with a trivial cmd/fyr that records that it ran, and a first commit
// pushed. Behind() needs a real remote, and Reinstall() needs something to
// build, so the guard under test is exercised on the path it actually
// guards. Returns the work tree and the marker path the built binary writes.
func gitFixture(t *testing.T) (repo, marker string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	repo = filepath.Join(base, "work")
	marker = filepath.Join(base, "installed.txt")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(gitCleanEnv(), "GIT_CONFIG_GLOBAL="+filepath.Join(base, "gitconfig"), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run(base, "git", "init", "--quiet", "--bare", "-b", "main", origin)
	run(base, "git", "clone", "--quiet", origin, repo)
	run(repo, "git", "config", "user.email", "fixture@example.invalid")
	run(repo, "git", "config", "user.name", "fixture")
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module fixture\n\ngo 1.24\n")
	// Stands in for the real installer: records that an install ran at all.
	write("cmd/fyr/main.go", "package main\n\nimport \"os\"\n\nfunc main() {\n\tos.WriteFile(os.Getenv(\"FIXTURE_MARKER\"), []byte(\"installed\"), 0o644)\n}\n")
	write("context/AGENTS.md", "# AGENTS.md\n")
	run(repo, "git", "add", "-A")
	run(repo, "git", "commit", "--quiet", "-m", "fixture")
	run(repo, "git", "push", "--quiet", "-u", "origin", "HEAD:refs/heads/main")
	run(repo, "git", "branch", "--quiet", "--set-upstream-to=origin/main")
	t.Setenv("FIXTURE_MARKER", marker)
	return repo, marker
}

// advanceOrigin pushes one commit to the fixture's origin from another
// clone, so the fixture's work tree is one commit behind.
func advanceOrigin(t *testing.T, repo string) {
	t.Helper()
	other := t.TempDir()
	base := filepath.Dir(repo)
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(gitCleanEnv(), "GIT_CONFIG_GLOBAL="+filepath.Join(base, "gitconfig"), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run(other, "git", "clone", "--quiet", filepath.Join(base, "origin.git"), filepath.Join(other, "c"))
	c := filepath.Join(other, "c")
	run(c, "git", "config", "user.email", "fixture@example.invalid")
	run(c, "git", "config", "user.name", "fixture")
	if err := os.WriteFile(filepath.Join(c, "context", "AGENTS.md"), []byte("# upstream moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(c, "git", "commit", "--quiet", "-am", "upstream")
	run(c, "git", "push", "--quiet", "origin", "HEAD:refs/heads/main")
}
