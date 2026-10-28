package memory

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC)

// repo makes a git checkout whose .gitignore ignores the store, as
// /init-project leaves it.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".agents/memory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Root was the git common dir's parent: in a submodule (.git/modules/x) or a
// --separate-git-dir checkout that is no work tree, so the store pointed into
// git's own directory (audit 2026-10-25, SD16). It is the work tree's top.
func TestRootIsTheWorkTreeTop(t *testing.T) {
	same := func(a, b string) bool {
		ra, _ := filepath.EvalSymlinks(a)
		rb, _ := filepath.EvalSymlinks(b)
		return ra == rb
	}

	t.Run("separate git dir", func(t *testing.T) {
		work := t.TempDir()
		git(t, work, "init", "-q", "--separate-git-dir", filepath.Join(t.TempDir(), "repo.git"))
		sub := filepath.Join(work, "pkg")
		os.MkdirAll(sub, 0o755)
		got, err := Root(sub)
		if err != nil || !same(got, work) {
			t.Errorf("Root = %q, %v; want the work tree %s", got, err, work)
		}
	})

	t.Run("submodule", func(t *testing.T) {
		inner := repo(t)
		git(t, inner, "add", ".gitignore")
		git(t, inner, "commit", "-q", "-m", "inner")
		super := repo(t)
		git(t, super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", inner, "mod")
		mod := filepath.Join(super, "mod")
		if common := git(t, mod, "rev-parse", "--path-format=absolute", "--git-common-dir"); !strings.Contains(common, filepath.Join(".git", "modules")) {
			t.Fatalf("the fixture's common dir is %s, not under .git/modules, so it proves nothing", common)
		}
		got, err := Root(mod)
		if err != nil || !same(got, mod) {
			t.Errorf("Root = %q, %v; want the submodule's work tree %s", got, err, mod)
		}
	})

	t.Run("linked worktree keeps the main store", func(t *testing.T) {
		dir := repo(t)
		git(t, dir, "add", ".gitignore")
		git(t, dir, "commit", "-q", "-m", "init")
		wt := filepath.Join(t.TempDir(), "wt")
		git(t, dir, "worktree", "add", "-q", wt)
		got, err := Root(wt)
		if err != nil || !same(got, dir) {
			t.Errorf("Root = %q, %v; want the main worktree %s", got, err, dir)
		}
	})

	// git 2.48+ with worktree.useRelativePaths writes the registration's
	// gitdir relative to the worktree's git dir: still the main store, not
	// a refusal (gate review of v3.0.0). Written by hand, as this git may
	// predate the option.
	t.Run("linked worktree registered by a relative path", func(t *testing.T) {
		dir := repo(t)
		git(t, dir, "add", ".gitignore")
		git(t, dir, "commit", "-q", "-m", "init")
		wt := filepath.Join(t.TempDir(), "wt")
		git(t, dir, "worktree", "add", "-q", wt)
		gd := git(t, wt, "rev-parse", "--path-format=absolute", "--git-dir")
		rel, err := filepath.Rel(gd, filepath.Join(wt, ".git"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gd, "gitdir"), []byte(rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := Root(wt)
		if err != nil || !same(got, dir) {
			t.Errorf("Root = %q, %v; want the main worktree %s", got, err, dir)
		}
	})

	// A repository's core.worktree can name any directory: a store there
	// would be another project's (trail.Toplevel's rule).
	t.Run("core.worktree outside is refused", func(t *testing.T) {
		dir := repo(t)
		elsewhere := t.TempDir()
		git(t, dir, "config", "core.worktree", elsewhere)
		if got := git(t, dir, "rev-parse", "--show-toplevel"); !same(got, elsewhere) {
			t.Fatalf("the fixture's toplevel is %s, not %s, so it proves nothing", got, elsewhere)
		}
		got, err := Root(dir)
		if err == nil && same(got, elsewhere) {
			t.Errorf("Root followed core.worktree to %s", got)
		}
	})

	// A planted checkout whose own git dir carries a commondir file naming
	// another project's .git resolved to that project's store (security
	// review 2026-10-25, finding 1): a linked worktree counts only when the
	// common dir registers it.
	t.Run("forged commondir is refused", func(t *testing.T) {
		victim := repo(t)
		git(t, victim, "add", ".gitignore")
		git(t, victim, "commit", "-q", "-m", "init")
		planted := t.TempDir()
		fake := filepath.Join(planted, "fakegit")
		if err := os.MkdirAll(fake, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(fake, "commondir"), []byte(filepath.Join(victim, ".git")+"\n"), 0o644)
		os.WriteFile(filepath.Join(fake, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
		os.WriteFile(filepath.Join(planted, ".git"), []byte("gitdir: "+fake+"\n"), 0o644)
		if common := git(t, planted, "rev-parse", "--path-format=absolute", "--git-common-dir"); !same(common, filepath.Join(victim, ".git")) {
			t.Fatalf("the fixture's common dir is %s, not the victim's, so it proves nothing", common)
		}
		got, err := Root(planted)
		if err == nil && same(got, victim) {
			t.Errorf("Root followed a forged commondir to %s", got)
		}
	})

	t.Run("bare repository is refused", func(t *testing.T) {
		bare := t.TempDir()
		git(t, bare, "init", "-q", "--bare")
		if got, err := Root(bare); !IsRefusal(err) {
			t.Errorf("Root = %q, %v; want a refusal", got, err)
		}
	})
}

func samePath(a, b string) bool {
	ra, _ := filepath.EvalSymlinks(filepath.Dir(a))
	rb, _ := filepath.EvalSymlinks(filepath.Dir(b))
	return ra == rb && filepath.Base(a) == filepath.Base(b)
}

// The store check runs in every registered project from the dream timer: it
// must hold no index lock and run no fsmonitor hook (v2.0.0 gate, security
// code slice).
func TestStoreTrackedArgs(t *testing.T) {
	got := strings.Join(storeTracked("/r").Args[1:], " ")
	want := "--no-optional-locks -c core.fsmonitor=false -c log.showSignature=false -C /r ls-files -- .agents/memory"
	if got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

// Store's git call runs no project hook: a core.fsmonitor hook that writes a
// marker fires for a plain `git status` (the control) and not for Store
// (v2.0.0 gate, security code slice; the argument test alone would pass an
// inline call that dropped the guard).
func TestStoreRunsNoProjectHook(t *testing.T) {
	root := repo(t)
	marker := filepath.Join(t.TempDir(), "fired")
	hook := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch \""+marker+"\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "config", "core.fsmonitor", hook)
	exec.Command("git", "-C", root, "status", "--porcelain").Run()
	if _, err := os.Stat(marker); err != nil {
		t.Skip("control: this git did not run the core.fsmonitor hook")
	}
	os.Remove(marker)
	if _, err := Store(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Store ran the project's fsmonitor hook")
	}
}

// trusted's refusals, in their own package (SD22 of the 2026-10-27 audit:
// they were covered only through dream and brain): a symlinked .agents, a
// symlinked store, a store git tracks, and git unable to answer.
func TestTrustedRefusals(t *testing.T) {
	elsewhere := t.TempDir()
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
		want  string
	}{
		{"symlinked .agents", func(t *testing.T, root string) {
			os.MkdirAll(filepath.Join(elsewhere, "memory"), 0o700)
			os.Symlink(elsewhere, filepath.Join(root, ".agents"))
		}, "is a symlink"},
		{"symlinked store", func(t *testing.T, root string) {
			os.MkdirAll(filepath.Join(root, ".agents"), 0o755)
			os.Symlink(elsewhere, filepath.Join(root, ".agents", "memory"))
		}, "is a symlink"},
		{"tracked store", func(t *testing.T, root string) {
			os.MkdirAll(filepath.Join(root, ".agents", "memory"), 0o755)
			os.WriteFile(filepath.Join(root, ".agents", "memory", "m.md"), []byte("m"), 0o644)
			git(t, root, "add", "-f", ".agents/memory/m.md")
		}, "git tracks"},
		{"git cannot answer", func(t *testing.T, root string) {
			os.RemoveAll(filepath.Join(root, ".git"))
		}, "cannot ask git"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := repo(t)
			c.setup(t, root)
			_, err := Store(root)
			if !IsRefusal(err) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Store: %v, want a refusal naming %q", err, c.want)
			}
		})
	}
	root := repo(t)
	if _, err := Store(root); err != nil {
		t.Errorf("the project's own store: %v", err)
	}
}
