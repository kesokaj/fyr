package harness

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlashNoteOnlyWhenSomeSkillIsSlashOnly(t *testing.T) {
	repo := newRepo(t)
	inv, err := LoadInventory(DirSource(repo))
	if err != nil {
		t.Fatal(err)
	}
	const marked = "Skills marked `/name` are typed by the user"
	if !strings.Contains(renderInventoryCompact(inv), marked) || !strings.Contains(RenderInventory(inv), marked) {
		t.Errorf("fixture has a slash-only skill; the note should be printed")
	}
	for i := range inv.Skills {
		inv.Skills[i].SlashOnly = false
	}
	for _, out := range []string{renderInventoryCompact(inv), RenderInventory(inv)} {
		if strings.Contains(out, marked) {
			t.Errorf("no slash-only skill; the note must not be printed:\n%s", out)
		}
		if !strings.Contains(out, "can also be typed as `/name`") {
			t.Errorf("expected the all-model-invocable sentence:\n%s", out)
		}
	}
}

// TestInspectCountsUntrackedFiles: `status.showUntrackedFiles=no` used to
// make a tree holding a whole new untracked skill read as clean, which
// `update` would then deploy everywhere (security review 2026-09-16).
func TestInspectCountsUntrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := newRepo(t)
	env := append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "gitconfig"), "GIT_CONFIG_NOSYSTEM=1")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "--quiet", "-b", "main")
	git("config", "user.email", "fixture@example.invalid")
	git("config", "user.name", "fixture")
	git("add", "-A")
	git("commit", "--quiet", "-m", "fixture")
	if Inspect(repo).Dirty {
		t.Fatal("a freshly committed tree is clean")
	}

	// The operator's own setting, which Inspect must not inherit.
	git("config", "status.showUntrackedFiles", "no")
	if err := os.MkdirAll(filepath.Join(repo, "skills", "smuggled"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skills", "smuggled", "SKILL.md"), []byte("---\nname: smuggled\ndescription: never committed\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !Inspect(repo).Dirty {
		t.Error("an untracked skill directory makes the checkout dirty, whatever status.showUntrackedFiles says")
	}
}

// TestInspectFailsClosedOnUnknownStatus: "git could not answer" must never
// read as "the tree is clean" to a caller deciding whether to deploy
// (release review of v1.0.0, finding 4).
func TestInspectFailsClosedOnUnknownStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	repo := newRepo(t)
	// A .git that is a file, not a directory: IsCheckout is satisfied, and
	// every git command then fails rather than reporting a clean tree.
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("not a git dir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info := Inspect(repo)
	if !info.StatusUnknown {
		t.Error("a git status that failed must be reported as unknown")
	}
	if err := Pull(repo, io.Discard); err == nil {
		t.Error("Pull must refuse a checkout whose status git cannot report")
	}
}
