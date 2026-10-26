package harness

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithin(t *testing.T) {
	for _, c := range []struct {
		parent, child string
		want          bool
	}{
		{"/a/skills", "/a/skills", true},
		{"/a/skills", "/a/skills/", true},
		{"/a/skills", "/a/skills/delta", true},
		{"/a/skills/delta", "/a/skills", false},
		{"/a/skills", "/a/skills-old", false},
		{"/a/skills", "/a/skillsdelta", false},
		{"/a/skills", "/b/skills/delta", false},
		{"/a", "/a/skills/delta", true},
	} {
		if got := within(c.parent, c.child); got != c.want {
			t.Errorf("within(%q, %q) = %v, want %v", c.parent, c.child, got, c.want)
		}
	}
}

// TestWriteAtomicIsAtomicAndDurable covers the write every deployed file
// goes through: the bytes land whole under the asked-for mode, and no temp
// file is left beside them on either outcome. The Sync added on 2026-09-16
// is not directly observable from a test — a crash is what would show it —
// so what is asserted here is the contract it protects: after the call
// returns, the destination holds exactly the bytes and nothing else does.
func TestWriteAtomicIsAtomicAndDurable(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "nested", "context.md")
	data := []byte("# AGENTS.md\n\nwhole file\n")
	if err := WriteAtomic(dest, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("content = %q, want %q", got, data)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(dest))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".fyr-") {
			t.Errorf("temp file %s left beside the destination", e.Name())
		}
	}
	// Overwriting an existing file keeps the same guarantee.
	if err := WriteAtomic(dest, []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "second\n" {
		t.Errorf("after overwrite = %q", got)
	}
}

// TestRenderContextRefusesSymlinks is BUG-036's class, tested directly on
// RenderContext rather than only through Plan: a symlinked context directory
// or context file would let a tree outside the repo decide what every
// machine reads.
func TestRenderContextRefusesSymlinks(t *testing.T) {
	t.Run("context directory is a link", func(t *testing.T) {
		repo := newRepo(t)
		outside := t.TempDir()
		if err := os.WriteFile(filepath.Join(outside, "AGENTS.md"), []byte("# hostile\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(repo, "context")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(repo, "context")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		inv, _ := LoadInventory(DirSource(repo))
		if _, err := RenderContext(DirSource(repo), inv); err == nil {
			t.Fatal("a symlinked context directory must be refused")
		} else if !strings.Contains(err.Error(), "symlink") {
			t.Errorf("the error should name the symlink, got %v", err)
		}
	})

	t.Run("a file inside context is a link", func(t *testing.T) {
		repo := newRepo(t)
		outside := filepath.Join(t.TempDir(), "extra.md")
		if err := os.WriteFile(outside, []byte("# hostile\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(repo, "context", "extra.md")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		inv, _ := LoadInventory(DirSource(repo))
		if _, err := RenderContext(DirSource(repo), inv); err == nil {
			t.Fatal("a symlinked context file must be refused")
		} else if !strings.Contains(err.Error(), "symlink") {
			t.Errorf("the error should name the symlink, got %v", err)
		}
	})
}

// TestAbandonNamesATempFileItCouldNotRemove: a failed write removes its temp
// file, and when even that fails the returned error names the file, so
// nothing is left behind unseen (the settings writer's own diagnostic, kept
// when it moved onto WriteAtomic; code review 2026-09-30).
func TestAbandonNamesATempFileItCouldNotRemove(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permissions this test relies on")
	}
	dir := t.TempDir()
	tmp, err := os.CreateTemp(dir, ".fyr-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	got := abandon(tmp, errors.New("disk full"))
	if got == nil || !strings.Contains(got.Error(), "disk full") || !strings.Contains(got.Error(), "left behind") || !strings.Contains(got.Error(), tmp.Name()) {
		t.Errorf("the cause and the leftover temp file should both be named, got %v", got)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	tmp2, err := os.CreateTemp(dir, ".fyr-*")
	if err != nil {
		t.Fatal(err)
	}
	if got := abandon(tmp2, errors.New("disk full")); got == nil || got.Error() != "disk full" {
		t.Errorf("a removed temp file adds nothing to the cause, got %v", got)
	}
	if _, err := os.Lstat(tmp2.Name()); !os.IsNotExist(err) {
		t.Errorf("the temp file should be removed, got %v", err)
	}
}

// TestWriteAtomicClosesBeforeRename pins the order no behavioural test can
// observe: the temp file is closed before it is renamed into place, so an
// error from WriteAtomic always means dest is unchanged, and a binary
// renamed into place is never still open (ETXTBSY on Linux). Round 2 of the
// 2026-09-30 review swapped the two lines and every test stayed green.
func TestWriteAtomicClosesBeforeRename(t *testing.T) {
	src, err := os.ReadFile("apply.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)[strings.Index(string(src), "func WriteAtomic("):]
	body = body[:strings.Index(body, "\n}\n")]
	// Code lines only: a comment naming the close above the rename must not
	// satisfy the check (v1.13.1 gate review).
	var code []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "//") {
			code = append(code, line)
		}
	}
	body = strings.Join(code, "\n")
	closeAt, renameAt := strings.Index(body, "tmp.Close()"), strings.Index(body, "os.Rename(")
	if closeAt < 0 || renameAt < 0 || closeAt > renameAt {
		t.Errorf("WriteAtomic must close the temp file before renaming it: close at %d, rename at %d", closeAt, renameAt)
	}
}
