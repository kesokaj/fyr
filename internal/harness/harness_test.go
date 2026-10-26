package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newRepo builds a minimal harness checkout: two skills, one agent, two
// context files. Tests never touch the real $HOME.
func newRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
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
	write("context/AGENTS.md", "# AGENTS.md\n\nBe honest.\n")
	write("context/dev-setup.md", "## Environment & Stack\n\n- GCP only.\n")
	write("skills/alpha/SKILL.md", "---\nname: alpha\ndescription: |\n  Alpha does things.\n  Use it for alpha work.\nmetadata:\n  version: \"1\"\n---\nbody\n")
	write("skills/alpha/references/notes.md", "notes\n")
	write("skills/beta/SKILL.md", "---\nname: beta\ndescription: \"Beta: slash only\"\ndisable-model-invocation: true\n---\nbody\n")
	write("skills/VENDORED.md", "not a skill\n")
	write("commands/deploy.md", "---\ndescription: Deploy the thing\n---\nSteps\n")
	write("rules/go-style.md", "# Go style\n\nUse slog.\n")
	write("agents/reviewer/agent.md", reviewerAgent)
	return repo
}

// gitInit makes a directory its own git checkout, which is what IsCheckout
// reads and what separates a tree update can move forward from a plain copy.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	cmd := exec.Command("git", "init", "--quiet", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

// gitIn runs git in dir and fails the test on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	outb, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, outb)
	}
	return strings.TrimSpace(string(outb))
}
