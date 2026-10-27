package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A repo reached through a symlink still sees the overlap: the source is
// resolved, so the destination is compared resolved too, and a skill is never
// replaced by its own subdirectory (IN42 of the 2026-10-27 audit).
func TestAdoptSeesOverlapThroughALinkedRepo(t *testing.T) {
	real := t.TempDir()
	inner := filepath.Join(real, "skills", "alpha", "alpha")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(real, "skills", "alpha", "SKILL.md"), []byte("---\nname: alpha\ndescription: d\n---\n"), 0o644)
	os.WriteFile(filepath.Join(inner, "SKILL.md"), []byte("---\nname: alpha\ndescription: inner\n---\n"), 0o644)
	link := filepath.Join(t.TempDir(), "repo")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	_, err := Adopt(link, t.TempDir(), filepath.Join(link, "skills", "alpha", "alpha"), true)
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Errorf("Adopt through a linked repo: %v, want the overlap refusal", err)
	}
	if _, err := os.Stat(filepath.Join(real, "skills", "alpha", "SKILL.md")); err != nil {
		t.Errorf("the skill was replaced: %v", err)
	}
}
