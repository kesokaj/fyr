package harness

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLastRetro(t *testing.T) {
	home := t.TempDir()
	if _, ok := LastRetro(home); ok {
		t.Fatal("no proposals yet")
	}
	dir := filepath.Join(home, StateDir, "proposals")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-09-03-retro.md"), []byte("# retro\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, ok := LastRetro(home)
	if !ok {
		t.Fatal("proposal should be found")
	}
	// A project-level retro appends to the index without a proposal file;
	// that counts as a retro too, and its newer mtime wins.
	index := filepath.Join(home, StateDir, "retros.jsonl")
	if err := os.WriteFile(index, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := first.Add(48 * time.Hour)
	if err := os.Chtimes(index, later, later); err != nil {
		t.Fatal(err)
	}
	if got, ok := LastRetro(home); !ok || !got.Equal(later) {
		t.Fatalf("LastRetro = %v, %v; want %v", got, ok, later)
	}
	// A home path holding a glob metacharacter: the proposals are listed by
	// name, never through a pattern built from the home (BUG-109's class;
	// the golang skill's rule).
	odd := filepath.Join(t.TempDir(), "u[1]")
	oddDir := filepath.Join(odd, StateDir, "proposals")
	if err := os.MkdirAll(oddDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oddDir, "2026-09-30-retro.md"), []byte("# retro\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := LastRetro(odd); !ok {
		t.Fatal("a proposal under a home with [ in its path should be found")
	}
}

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
