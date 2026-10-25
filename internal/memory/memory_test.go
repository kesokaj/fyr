package memory

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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

func add(dir, name, desc, body string) (string, error) {
	return Add(dir, Entry{Type: "feedback", Name: name, Description: desc}, strings.NewReader(body), now)
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAddWritesFileAndIndex(t *testing.T) {
	dir := repo(t)
	path, err := add(dir, "deploy-fridays", "we deploy on Fridays", "We deploy on Fridays.\n")
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(dir, ".agents", "memory")
	if path != filepath.Join(store, "deploy-fridays.md") {
		t.Errorf("path = %s", path)
	}
	want := "---\nname: deploy-fridays\ndescription: \"we deploy on Fridays\"\nmetadata:\n  node_type: memory\n  type: feedback\n  modified: 2026-09-30T18:00:00Z\n---\n\nWe deploy on Fridays.\n"
	if got := read(t, path); got != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
	if got := read(t, filepath.Join(store, "MEMORY.md")); got != "- [deploy-fridays](deploy-fridays.md) — we deploy on Fridays\n" {
		t.Errorf("index = %q", got)
	}
	for p, mode := range map[string]os.FileMode{store: 0o700, path: 0o600} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("%s mode %o, want %o", p, info.Mode().Perm(), mode)
		}
	}
}

func TestAddReplacesInPlace(t *testing.T) {
	dir := repo(t)
	store := filepath.Join(dir, ".agents", "memory")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	// A native line (title, not slug) above ours must survive untouched.
	native := "- [Prefer tables](prefer-tables.md) — status as a table\n"
	if err := os.WriteFile(filepath.Join(store, "MEMORY.md"), []byte(native), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := add(dir, "deploy-day", "fridays", "a\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := add(dir, "deploy-day", "thursdays now", "b\n"); err != nil {
		t.Fatal(err)
	}
	want := native + "- [deploy-day](deploy-day.md) — thursdays now\n"
	if got := read(t, filepath.Join(store, "MEMORY.md")); got != want {
		t.Errorf("index:\n%s\nwant:\n%s", got, want)
	}
	if !strings.HasSuffix(read(t, filepath.Join(store, "deploy-day.md")), "\nb\n") {
		t.Error("body not replaced")
	}
}

func TestAddRefusals(t *testing.T) {
	cases := map[string]struct {
		setup func(t *testing.T, dir string)
		entry Entry
		body  string
	}{
		"not a checkout": {
			setup: func(t *testing.T, dir string) { os.RemoveAll(filepath.Join(dir, ".git")) },
		},
		"agents is a symlink": {
			setup: func(t *testing.T, dir string) {
				elsewhere := t.TempDir()
				if err := os.Symlink(elsewhere, filepath.Join(dir, ".agents")); err != nil {
					t.Fatal(err)
				}
			},
		},
		"store is a symlink": {
			setup: func(t *testing.T, dir string) {
				os.MkdirAll(filepath.Join(dir, ".agents"), 0o755)
				if err := os.Symlink(t.TempDir(), filepath.Join(dir, ".agents", "memory")); err != nil {
					t.Fatal(err)
				}
			},
		},
		"git tracks the store": {
			setup: func(t *testing.T, dir string) {
				store := filepath.Join(dir, ".agents", "memory")
				os.MkdirAll(store, 0o700)
				os.WriteFile(filepath.Join(store, "planted.md"), []byte("x"), 0o600)
				git(t, dir, "add", "-f", ".agents/memory/planted.md")
			},
		},
		"store not ignored": {
			setup: func(t *testing.T, dir string) { os.WriteFile(filepath.Join(dir, ".gitignore"), nil, 0o644) },
		},
		"index full": {
			setup: func(t *testing.T, dir string) {
				store := filepath.Join(dir, ".agents", "memory")
				os.MkdirAll(store, 0o700)
				var b strings.Builder
				for i := 0; i < 200; i++ {
					fmt.Fprintf(&b, "- [m%d](m%d.md) — x\n", i, i)
				}
				os.WriteFile(filepath.Join(store, "MEMORY.md"), []byte(b.String()), 0o600)
			},
		},
		"bad slug":            {entry: Entry{Type: "user", Name: "../escape", Description: "d"}},
		"bad type":            {entry: Entry{Type: "secret", Name: "ok", Description: "d"}},
		"newline description": {entry: Entry{Type: "user", Name: "ok", Description: "a\nb"}},
		"empty body":          {body: " \n"},
		"empty description":   {entry: Entry{Type: "user", Name: "ok"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := repo(t)
			if c.setup != nil {
				c.setup(t, dir)
			}
			e := c.entry
			if e == (Entry{}) {
				e = Entry{Type: "user", Name: "ok", Description: "d"}
			}
			body := c.body
			if body == "" {
				body = "fact\n"
			}
			before := snapshot(t, dir)
			_, err := Add(dir, e, strings.NewReader(body), now)
			if !IsRefusal(err) {
				t.Fatalf("err = %v, want a refusal", err)
			}
			if after := snapshot(t, dir); after != before {
				t.Errorf("a refusal wrote:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
}

// snapshot lists every path under the store with its size, so a refusal that
// writes anything shows up.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	filepath.Walk(filepath.Join(dir, ".agents"), func(p string, info os.FileInfo, err error) error {
		if err == nil {
			fmt.Fprintf(&b, "%s:%d ", p, info.Size())
		}
		return nil
	})
	return b.String()
}

func TestWorktreeSharesTheMainStore(t *testing.T) {
	dir := repo(t)
	git(t, dir, "add", ".gitignore")
	git(t, dir, "commit", "-q", "-m", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, dir, "worktree", "add", "-q", wt)
	path, err := add(wt, "shared", "d", "x\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, ".agents", "memory", "shared.md"); !samePath(path, want) {
		t.Errorf("worktree wrote %s, want the main store %s", path, want)
	}
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

func TestLeftoverLockFileIsNoLock(t *testing.T) {
	dir := repo(t)
	store := filepath.Join(dir, ".agents", "memory")
	os.MkdirAll(store, 0o700)
	// A lock file with no process holding it, as a crashed writer leaves it.
	os.WriteFile(filepath.Join(store, ".lock"), []byte("12345"), 0o600)
	if _, err := add(dir, "after-crash", "d", "x\n"); err != nil {
		t.Fatalf("a leftover lock file blocked the write: %v", err)
	}
}

func TestHeldLockRefusesAfterRetry(t *testing.T) {
	old := lockWait
	lockWait = 50 * time.Millisecond
	defer func() { lockWait = old }()
	dir := repo(t)
	store := filepath.Join(dir, ".agents", "memory")
	os.MkdirAll(store, 0o700)
	f, err := os.OpenFile(filepath.Join(store, ".lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if _, err := add(dir, "busy", "d", "x\n"); !IsRefusal(err) || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("err = %v, want a store-busy refusal", err)
	}
}

// TestIndexMatchIsTheLinkTarget: a description that mentions another file's
// name never makes its line the one replaced.
func TestIndexMatchIsTheLinkTarget(t *testing.T) {
	dir := repo(t)
	if _, err := add(dir, "new-name", "supersedes (old-name.md)", "a\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := add(dir, "old-name", "the old one", "b\n"); err != nil {
		t.Fatal(err)
	}
	want := "- [new-name](new-name.md) — supersedes (old-name.md)\n- [old-name](old-name.md) — the old one\n"
	if got := read(t, filepath.Join(dir, ".agents", "memory", "MEMORY.md")); got != want {
		t.Errorf("index:\n%s\nwant:\n%s", got, want)
	}
}

// TestShow: the index and nothing else — no hint line about another CLI's
// own memory follows it (plan fyr Phase 2).
func TestShow(t *testing.T) {
	dir := repo(t)
	got, err := Show(dir)
	if err != nil || got != "" {
		t.Fatalf("empty store: %q, %v", got, err)
	}
	if _, err := add(dir, "one", "first", "x\n"); err != nil {
		t.Fatal(err)
	}
	got, err = Show(dir)
	if err != nil || got != "- [one](one.md) — first\n" {
		t.Errorf("show = %q, %v", got, err)
	}
}

func TestShowCaps(t *testing.T) {
	dir := repo(t)
	store := filepath.Join(dir, ".agents", "memory")
	os.MkdirAll(store, 0o700)
	var b strings.Builder
	for i := 0; i < 230; i++ {
		fmt.Fprintf(&b, "- [m%d](m%d.md) — x\n", i, i)
	}
	os.WriteFile(filepath.Join(store, "MEMORY.md"), []byte(b.String()), 0o600)
	got, _ := Show(dir)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 201 || !strings.Contains(lines[200], "30 more lines") {
		t.Errorf("got %d lines, last %q", len(lines), lines[len(lines)-1])
	}
}

func TestShowRefusesTrackedStore(t *testing.T) {
	dir := repo(t)
	store := filepath.Join(dir, ".agents", "memory")
	os.MkdirAll(store, 0o700)
	os.WriteFile(filepath.Join(store, "MEMORY.md"), []byte("- [x](x.md) — planted\n"), 0o600)
	git(t, dir, "add", "-f", ".agents/memory/MEMORY.md")
	if _, err := Show(dir); !IsRefusal(err) {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestShowOutsideACheckoutRefuses(t *testing.T) {
	if _, err := Show(t.TempDir()); !IsRefusal(err) {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

// The store check runs in every indexed project at 03:00: it must hold no
// index lock and run no fsmonitor hook (v2.0.0 gate, security code slice).
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

// A symlinked MEMORY.md is never read or rewritten: the index is the store's
// own file (v2.0.0 gate, security code slice).
func TestSymlinkedIndexRefused(t *testing.T) {
	root := repo(t)
	store := filepath.Join(root, ".agents", "memory")
	os.MkdirAll(store, 0o700)
	target := filepath.Join(t.TempDir(), "elsewhere.md")
	os.WriteFile(target, []byte("- [x](x.md) — x\n"), 0o644)
	if err := os.Symlink(target, filepath.Join(store, "MEMORY.md")); err != nil {
		t.Skip("no symlinks here")
	}
	if _, err := Show(root); err == nil || !IsRefusal(err) {
		t.Errorf("Show followed a symlinked index: %v", err)
	}
	if _, err := add(root, "x", "x", "x"); err == nil || !IsRefusal(err) {
		t.Errorf("Add followed a symlinked index: %v", err)
	}
	// The refused Add left the link in place: the outside file was not copied
	// into the store as a regular index (what happened before the refusal).
	if info, err := os.Lstat(filepath.Join(store, "MEMORY.md")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Error("the index is no longer the link: Add wrote a regular MEMORY.md from the outside file")
	}
}

// Add's check-ignore runs no project hook either: `git check-ignore` reads
// the index and starts core.fsmonitor unless it is turned off (security
// review of BUG-165's fix, 2026-10-09).
func TestAddRunsNoProjectHook(t *testing.T) {
	root := repo(t)
	marker := filepath.Join(t.TempDir(), "fired")
	hook := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch \""+marker+"\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "config", "core.fsmonitor", hook)
	exec.Command("git", "-C", root, "check-ignore", "-q", ".agents/memory/x.md").Run()
	if _, err := os.Stat(marker); err != nil {
		t.Skip("control: this git did not run the core.fsmonitor hook for check-ignore")
	}
	os.Remove(marker)
	// Add must reach its check-ignore: a refusal before it would pass vacuously.
	if _, err := add(root, "x", "x", "x"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Add ran the project's fsmonitor hook")
	}
}
