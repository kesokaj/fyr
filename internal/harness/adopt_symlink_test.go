package harness

import (
	"os"
	"path/filepath"
	"testing"
)

// BUG-502: a relative source named from a checkout reached through a symlink
// (macOS's /var is /private/var; any linked workspace) stayed unresolved
// while the destination was resolved, so the overlap check missed that
// skills/alpha is its own destination and --force replaced it.
func TestAdoptRelativeSourceThroughLinkedRepo(t *testing.T) {
	home := t.TempDir()
	repo := newRepo(t)
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(link)
	// The condition holds: the working directory is spelled through the link.
	if wd, err := os.Getwd(); err != nil || wd != link {
		t.Fatalf("fixture: working directory %q, %v; want the link %q", wd, err, link)
	}
	if _, err := Adopt(link, home, filepath.Join("skills", "alpha"), true); err == nil {
		t.Error("adopting skills/alpha from a linked repo root should fail")
	}
	if _, err := os.Stat(filepath.Join(repo, "skills", "alpha", "SKILL.md")); err != nil {
		t.Errorf("the source was removed: %v", err)
	}
}
