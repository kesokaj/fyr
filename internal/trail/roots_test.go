package trail

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rootsTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".agents/memory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRootIsTheWorkTreeTop verifies Roots resolves main and local worktree
// tops across separate git dirs, submodules, linked worktrees, relative gitdir
// links, core.worktree escapes, forged commondir files, and bare repos.
func TestRootIsTheWorkTreeTop(t *testing.T) {
	same := func(a, b string) bool {
		ra, _ := filepath.EvalSymlinks(a)
		rb, _ := filepath.EvalSymlinks(b)
		return ra == rb
	}

	t.Run("separate git dir", func(t *testing.T) {
		work := t.TempDir()
		gitIn(t, work, "init", "-q", "--separate-git-dir", filepath.Join(t.TempDir(), "repo.git"))
		sub := filepath.Join(work, "pkg")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		main, top, err := Roots(sub)
		if err != nil || !same(main, work) || !same(top, work) {
			t.Errorf("Roots = (%q, %q), %v; want (%s, %s)", main, top, err, work, work)
		}
	})

	t.Run("submodule", func(t *testing.T) {
		inner := rootsTestRepo(t)
		gitIn(t, inner, "add", ".gitignore")
		gitIn(t, inner, "commit", "-q", "-m", "inner")
		super := rootsTestRepo(t)
		gitIn(t, super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", inner, "mod")
		mod := filepath.Join(super, "mod")
		if common := gitIn(t, mod, "rev-parse", "--path-format=absolute", "--git-common-dir"); !strings.Contains(common, filepath.Join(".git", "modules")) {
			t.Fatalf("the fixture's common dir is %s, not under .git/modules, so it proves nothing", common)
		}
		main, top, err := Roots(mod)
		if err != nil || !same(main, mod) || !same(top, mod) {
			t.Errorf("Roots = (%q, %q), %v; want (%s, %s)", main, top, err, mod, mod)
		}
	})

	t.Run("linked worktree keeps the main store", func(t *testing.T) {
		dir := rootsTestRepo(t)
		gitIn(t, dir, "add", ".gitignore")
		gitIn(t, dir, "commit", "-q", "-m", "init")
		wt := filepath.Join(t.TempDir(), "wt")
		gitIn(t, dir, "worktree", "add", "-q", wt)
		main, top, err := Roots(wt)
		if err != nil || !same(main, dir) || !same(top, wt) {
			t.Errorf("Roots = (%q, %q), %v; want (%s, %s)", main, top, err, dir, wt)
		}
	})

	t.Run("linked worktree registered by a relative path", func(t *testing.T) {
		dir := rootsTestRepo(t)
		gitIn(t, dir, "add", ".gitignore")
		gitIn(t, dir, "commit", "-q", "-m", "init")
		wt := filepath.Join(t.TempDir(), "wt")
		gitIn(t, dir, "worktree", "add", "-q", wt)
		gd := gitIn(t, wt, "rev-parse", "--path-format=absolute", "--git-dir")
		rel, err := filepath.Rel(gd, filepath.Join(wt, ".git"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gd, "gitdir"), []byte(rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		main, top, err := Roots(wt)
		if err != nil || !same(main, dir) || !same(top, wt) {
			t.Errorf("Roots = (%q, %q), %v; want (%s, %s)", main, top, err, dir, wt)
		}
	})

	t.Run("core.worktree outside is refused", func(t *testing.T) {
		dir := rootsTestRepo(t)
		elsewhere := t.TempDir()
		gitIn(t, dir, "config", "core.worktree", elsewhere)
		if got := gitIn(t, dir, "rev-parse", "--show-toplevel"); !same(got, elsewhere) {
			t.Fatalf("the fixture's toplevel is %s, not %s, so it proves nothing", got, elsewhere)
		}
		main, top, err := Roots(dir)
		if err == nil && (same(main, elsewhere) || same(top, elsewhere)) {
			t.Errorf("Roots followed core.worktree to (%s, %s)", main, top)
		}
	})

	t.Run("forged commondir is refused", func(t *testing.T) {
		victim := rootsTestRepo(t)
		gitIn(t, victim, "add", ".gitignore")
		gitIn(t, victim, "commit", "-q", "-m", "init")
		planted := t.TempDir()
		fake := filepath.Join(planted, "fakegit")
		if err := os.MkdirAll(fake, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(fake, "commondir"), []byte(filepath.Join(victim, ".git")+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(fake, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
		_ = os.WriteFile(filepath.Join(planted, ".git"), []byte("gitdir: "+fake+"\n"), 0o644)
		if common := gitIn(t, planted, "rev-parse", "--path-format=absolute", "--git-common-dir"); !same(common, filepath.Join(victim, ".git")) {
			t.Fatalf("the fixture's common dir is %s, not the victim's, so it proves nothing", common)
		}
		main, top, err := Roots(planted)
		if err == nil && (same(main, victim) || same(top, victim)) {
			t.Errorf("Roots followed a forged commondir to (%s, %s)", main, top)
		}
	})

	t.Run("bare repository is refused", func(t *testing.T) {
		bare := t.TempDir()
		gitIn(t, bare, "init", "-q", "--bare")
		main, top, err := Roots(bare)
		if !errors.Is(err, ErrNotCheckout) {
			t.Errorf("Roots = (%q, %q), %v; want ErrNotCheckout", main, top, err)
		}
	})
}
