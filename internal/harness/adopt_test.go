package harness

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdopt(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	src := filepath.Join(home, ".agents", "skills", "gamma")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: gamma\ndescription: g\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink the way `npx skills add -g` leaves one in a vendor directory.
	link := filepath.Join(home, AgyRoot, "skills", "gamma")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, link); err != nil {
		t.Fatal(err)
	}

	dest, err := Adopt(repo, home, "gamma", false)
	if err != nil {
		t.Fatal(err)
	}
	if dest != filepath.Join(repo, "skills", "gamma") {
		t.Errorf("dest = %s", dest)
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Error(err)
	}
	if _, err := Adopt(repo, home, "gamma", false); err == nil {
		t.Error("second adopt without --force should fail")
	}
	if _, err := Adopt(repo, home, "gamma", true); err != nil {
		t.Errorf("adopt --force: %v", err)
	}
	if _, err := Adopt(repo, home, "nope", false); err == nil {
		t.Error("unknown skill should fail")
	}
}

// A source that resolves to the repo, to skills/, or to the destination must
// never become the deletion target: ".", ".." and a relative "skills/<name>"
// reached Base unchanged and named the deletion root itself (BUG-037).
func TestAdoptRefusesDestructiveSources(t *testing.T) {
	home := t.TempDir()
	skillDir := func(t *testing.T) string {
		t.Helper()
		d := filepath.Join(t.TempDir(), "delta")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\nname: delta\ndescription: d\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return d
	}

	t.Run("dot adopts the directory itself, never skills/", func(t *testing.T) {
		repo := newRepo(t)
		t.Chdir(skillDir(t))
		dest, err := Adopt(repo, home, ".", true)
		if err != nil {
			t.Fatalf("adopt . --force: %v", err)
		}
		if dest != filepath.Join(repo, "skills", "delta") {
			t.Errorf("dest = %s", dest)
		}
		if _, err := os.Stat(filepath.Join(repo, "skills", "alpha", "SKILL.md")); err != nil {
			t.Errorf("skills/ was removed: %v", err)
		}
	})

	t.Run("dotdot adopts the parent, never the repo", func(t *testing.T) {
		repo := newRepo(t)
		sub := filepath.Join(skillDir(t), "sub")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(sub)
		dest, err := Adopt(repo, home, "..", true)
		if err != nil {
			t.Fatalf("adopt .. --force: %v", err)
		}
		if dest != filepath.Join(repo, "skills", "delta") {
			t.Errorf("dest = %s", dest)
		}
		if _, err := os.Stat(filepath.Join(repo, "context", "AGENTS.md")); err != nil {
			t.Errorf("the repo was removed: %v", err)
		}
	})

	t.Run("a skill adopted onto itself", func(t *testing.T) {
		repo := newRepo(t)
		src := filepath.Join(repo, "skills", "alpha")
		if _, err := Adopt(repo, home, src, true); err == nil {
			t.Error("adopting a skill onto itself should fail")
		}
		if _, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil {
			t.Errorf("the source was removed: %v", err)
		}
	})

	t.Run("a relative path into the repo's own skills/", func(t *testing.T) {
		repo := newRepo(t)
		t.Chdir(repo)
		if _, err := Adopt(repo, home, filepath.Join("skills", "alpha"), true); err == nil {
			t.Error("adopting skills/alpha from the repo root should fail")
		}
		if _, err := os.Stat(filepath.Join(repo, "skills", "alpha", "SKILL.md")); err != nil {
			t.Errorf("the source was removed: %v", err)
		}
	})
}

// TestAdoptFailedCopyKeepsTheExistingSkill: adopt --force used to delete the
// destination and then copy, so a copy that failed part-way left the operator
// with neither the old skill nor the new one. The copy is staged and renamed
// into place, so a refusal mid-walk changes nothing.
func TestAdoptFailedCopyKeepsTheExistingSkill(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()

	dest := filepath.Join(repo, "skills", "widget")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	original := "---\nname: widget\ndescription: the version already here\n---\nold body\n"
	if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// A source whose copy fails part-way: copyTree refuses symlinks.
	src := filepath.Join(t.TempDir(), "widget")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: widget\ndescription: replacement\n---\nnew body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "linked.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := Adopt(repo, home, src, true); err == nil {
		t.Fatal("a source holding a symlink must be refused")
	}
	got, err := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if err != nil {
		t.Fatalf("the existing skill must survive a failed adopt: %v", err)
	}
	if string(got) != original {
		t.Errorf("the existing skill was modified: %q", got)
	}
	assertSkillsClean(t, repo, "after a failed adopt")

	// A rename that cannot complete must leave the ORIGINAL in place, never
	// delete both copies: dest is moved aside and put back (release review of
	// v1.0.0, finding 3). Forced here by making the destination's parent
	// read-only so the rename into it fails.
	if os.Geteuid() != 0 {
		if err := os.Remove(filepath.Join(src, "linked.md")); err != nil {
			t.Fatal(err)
		}
		skillsDir := filepath.Join(repo, "skills")
		if err := os.Chmod(skillsDir, 0o500); err != nil {
			t.Fatal(err)
		}
		_, err := Adopt(repo, home, src, true)
		if chmodErr := os.Chmod(skillsDir, 0o755); chmodErr != nil {
			t.Fatal(chmodErr)
		}
		if err == nil {
			t.Error("a rename into an unwritable skills/ should fail")
		}
		got, readErr := os.ReadFile(filepath.Join(dest, "SKILL.md"))
		if readErr != nil {
			t.Fatalf("the original skill must survive a failed rename: %v", readErr)
		}
		if string(got) != original {
			t.Errorf("the original skill changed: %q", got)
		}
		if err != nil && !strings.Contains(err.Error(), ".agents/scratch") {
			t.Errorf("the error should name where the prepared copy is, got %v", err)
		}
	}

	// The happy path still replaces the skill and leaves no staging behind.
	os.Remove(filepath.Join(src, "linked.md"))
	if _, err := Adopt(repo, home, src, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "SKILL.md")); !strings.Contains(string(got), "new body") {
		t.Errorf("the skill should have been replaced, got %q", got)
	}
	assertSkillsClean(t, repo, "after a successful adopt")

	// The staging directory lives in .agents/scratch and is removed either
	// way; if one survived there with a complete SKILL.md, loadSkills would
	// not see it (it walks skills/ only), which is the point of the move.
	leftovers, _ := filepath.Glob(filepath.Join(repo, ".agents", "scratch", "adopt-*"))
	if len(leftovers) != 0 {
		t.Errorf("staging directories left in scratch: %v", leftovers)
	}
}

// assertSkillsClean fails if skills/ holds anything but real skill
// directories: a staging or partial copy there would be loaded as a second
// skill of the same name and installed over the real one (code review
// 2026-09-16).
func assertSkillsClean(t *testing.T, repo, when string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repo, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.Contains(e.Name(), "staging") || strings.Contains(e.Name(), "adopt-") || strings.HasPrefix(e.Name(), ".") {
			t.Errorf("%s: skills/ holds %q, which is not a skill", when, e.Name())
		}
	}
	skills, err := loadSkills(DirSource(repo))
	if err != nil {
		t.Fatalf("%s: loadSkills: %v", when, err)
	}
	seen := map[string]bool{}
	for _, s := range skills {
		if seen[s.Name] {
			t.Errorf("%s: two skills named %q are loaded", when, s.Name)
		}
		seen[s.Name] = true
	}
}

// TestAdoptRefusesSecretShapedFiles guards the entrance: Plan refuses a
// credential on the way out, but adopt copies into skills/, where it would
// be committed and pushed before any consumer's install ever refused it.
func TestAdoptRefusesSecretShapedFiles(t *testing.T) {
	repo := newRepo(t)
	home := t.TempDir()
	src := filepath.Join(t.TempDir(), "widget")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: widget\ndescription: has a key beside it\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "id_ed25519"), []byte("PRIVATE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Adopt(repo, home, src, false)
	if err == nil {
		t.Fatal("adopt must refuse a skill carrying a private key")
	}
	if !strings.Contains(err.Error(), "id_ed25519") {
		t.Errorf("the error should name the file, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "skills", "widget")); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("nothing should have been copied into skills/")
	}
}
