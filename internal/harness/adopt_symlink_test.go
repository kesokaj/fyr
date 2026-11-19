package harness

import (
	"os"
	"path/filepath"
	"strings"
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

func TestAdoptDanglingSymlinkDest(t *testing.T) {
	home := t.TempDir()
	repo := newRepo(t)
	src := filepath.Join(t.TempDir(), "ext-skill")
	writeFile(t, filepath.Join(src, "SKILL.md"), "---\nname: ext-skill\ndescription: ext\n---\nbody\n")
	dest := filepath.Join(repo, "skills", "ext-skill")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing-target"), dest); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := Adopt(repo, home, src, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("Adopt without --force on dangling symlink err = %v, want --force refusal", err)
	}
	if _, err := Adopt(repo, home, src, true); err != nil {
		t.Fatalf("Adopt with --force on dangling symlink: %v", err)
	}
	fi, err := os.Lstat(dest)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("dest after --force = %v, %v; want real directory", fi, err)
	}
	left, _ := filepath.Glob(filepath.Join(repo, ".agents", "scratch", "adopt-*"))
	if len(left) != 0 {
		t.Errorf("stranded staging dirs: %v", left)
	}
}
