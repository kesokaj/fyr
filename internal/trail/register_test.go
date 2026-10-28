package trail

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// committedRepo is a repository with one commit, so it can be cloned and
// grow a linked worktree.
func committedRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(root, "f"), []byte("f\n"), 0o644)
	gitIn(t, root, "add", "f")
	gitIn(t, root, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "one")
	return root
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

var day = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// registeredRepo is a committed repository registered under a fresh state
// directory.
func registeredRepo(t *testing.T) (root, state string) {
	t.Helper()
	root, state = committedRepo(t), filepath.Join(t.TempDir(), "fyr")
	if _, err := Register(state, root, day); err != nil {
		t.Fatal(err)
	}
	if !Registered(state, root) {
		t.Fatal("the checkout does not read as registered after Register")
	}
	return root, state
}

func TestRegisteredPlainCheckout(t *testing.T) {
	root, state := committedRepo(t), filepath.Join(t.TempDir(), "fyr")
	if Registered(state, root) {
		t.Fatal("a fresh checkout reads as registered")
	}
	path, err := Register(state, root, day)
	if err != nil {
		t.Fatal(err)
	}
	if !sameDir(filepath.Dir(path), filepath.Join(root, ".git")) || filepath.Base(path) != markerName {
		t.Errorf("marker at %s, want %s", path, filepath.Join(root, ".git", markerName))
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(b), "registered 2026-10-28\nseal ") {
		t.Errorf("marker holds %q (%v)", b, err)
	}
	for p, want := range map[string]os.FileMode{path: 0o600, filepath.Join(state, keyName): 0o600, state: 0o700} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v (%v), want %v", p, fi.Mode().Perm(), err, want)
		}
	}
	sub := filepath.Join(root, "sub")
	os.Mkdir(sub, 0o755)
	if !Registered(state, root) || !Registered(state, sub) {
		t.Error("the checkout, or a directory in it, does not read as registered")
	}
	// A rerun rewrites the date and keeps the key.
	key, _ := os.ReadFile(filepath.Join(state, keyName))
	if _, err := Register(state, root, day.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.HasPrefix(string(b), "registered 2026-10-29\n") {
		t.Errorf("a rerun left %q", b)
	}
	if again, _ := os.ReadFile(filepath.Join(state, keyName)); string(again) != string(key) {
		t.Error("a rerun replaced the key")
	}
	for _, dir := range []string{filepath.Join(root, ".git"), state} {
		if left, _ := filepath.Glob(filepath.Join(dir, ".*-*")); len(left) != 0 {
			t.Errorf("temporary files left: %v", left)
		}
	}
}

func TestRegisteredLinkedWorktreeSharesMarker(t *testing.T) {
	root, state := committedRepo(t), filepath.Join(t.TempDir(), "fyr")
	wt := filepath.Join(t.TempDir(), "wt")
	gitIn(t, root, "worktree", "add", "-q", "-b", "side", wt)
	if Registered(state, wt) {
		t.Fatal("a worktree of an unregistered checkout reads as registered")
	}
	if _, err := Register(state, wt, day); err != nil {
		t.Fatal(err)
	}
	if !Registered(state, root) || !Registered(state, wt) {
		t.Errorf("registering the worktree: main %v, worktree %v; want both", Registered(state, root), Registered(state, wt))
	}
	if _, err := os.Lstat(filepath.Join(root, ".git", markerName)); err != nil {
		t.Errorf("the marker is not in the common dir: %v", err)
	}
}

func TestRegisteredCloneIsNot(t *testing.T) {
	root, state := registeredRepo(t)
	clone := filepath.Join(t.TempDir(), "clone")
	gitIn(t, root, "clone", "-q", root, clone)
	if Registered(state, clone) {
		t.Error("a clone of a registered checkout reads as registered")
	}
}

// A copy that carries .git/ (cp -a, rsync, an archive of the directory)
// carries the marker, but its seal names the original's root (security
// review of Phase 2, finding 2).
func TestRegisteredCopyIsNot(t *testing.T) {
	root, state := registeredRepo(t)
	cp := filepath.Join(t.TempDir(), "copy")
	if out, err := exec.Command("cp", "-a", root, cp).CombinedOutput(); err != nil {
		t.Fatalf("cp -a: %v\n%s", err, out)
	}
	if _, err := os.Lstat(filepath.Join(cp, ".git", markerName)); err != nil {
		t.Fatalf("the copy does not carry the marker, so it proves nothing: %v", err)
	}
	if Registered(state, cp) {
		t.Error("a copy of a registered checkout reads as registered")
	}
}

// A tree whose .git file names a registered repository's git dir resolves
// to that repository's marker, sealed for another root (security review of
// Phase 2, finding 1).
func TestRegisteredRedirectTreeIsNot(t *testing.T) {
	root, state := registeredRepo(t)
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, ".git"), []byte("gitdir: "+filepath.Join(root, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if common, _, _, err := roots(tree); err != nil || !sameDir(common, filepath.Join(root, ".git")) {
		t.Fatalf("the tree does not resolve to the registered git dir (%q, %v), so it proves nothing", common, err)
	}
	if Registered(state, tree) {
		t.Error("a tree borrowing a registered git dir reads as registered")
	}
}

// Only this state directory's key makes a valid seal: another home, a
// planted marker, a lost or linked key do not count.
func TestRegisteredNeedsThisHomesKey(t *testing.T) {
	root, state := registeredRepo(t)
	if Registered(filepath.Join(t.TempDir(), "fyr"), root) {
		t.Error("the checkout reads as registered under another home")
	}
	other := committedRepo(t)
	planted := "registered 2026-10-28\nseal " + strings.Repeat("ab", 32) + "\n"
	if err := os.WriteFile(filepath.Join(other, ".git", markerName), []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}
	if Registered(state, other) {
		t.Error("a planted marker reads as registered")
	}
	key := filepath.Join(state, keyName)
	moved := key + ".moved"
	if err := os.Rename(key, moved); err != nil {
		t.Fatal(err)
	}
	if Registered(state, root) {
		t.Error("the checkout reads as registered with the key gone")
	}
	if err := os.Symlink(moved, key); err != nil {
		t.Fatal(err)
	}
	if Registered(state, root) {
		t.Error("a linked key was followed")
	}
	if _, err := Register(state, root, day); err == nil {
		t.Error("Register used a linked key")
	}
}

func TestRegisteredRefusesOddMarker(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, path string)
	}{
		{"symlink", func(t *testing.T, path string) {
			target := filepath.Join(t.TempDir(), "elsewhere")
			if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory", func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, state := registeredRepo(t)
			path := filepath.Join(root, ".git", markerName)
			if tc.name == "symlink" {
				// The link's target holds this checkout's valid marker, so
				// only the refusal to follow it keeps it unregistered.
				valid, _ := os.ReadFile(path)
				os.Remove(path)
				tc.plant(t, path)
				target, _ := os.Readlink(path)
				os.WriteFile(target, valid, 0o600)
			} else {
				os.Remove(path)
				tc.plant(t, path)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("the planted marker is not there: %v", err)
			}
			if Registered(state, root) {
				t.Error("an odd marker reads as registered")
			}
			before, _ := os.Lstat(path)
			if _, err := Register(state, root, day); err == nil {
				t.Error("Register replaced an odd marker")
			}
			if after, err := os.Lstat(path); err != nil || after.Mode() != before.Mode() {
				t.Errorf("the odd marker changed: %v (%v)", after, err)
			}
		})
	}
}

func TestRegisteredOutsideACheckout(t *testing.T) {
	dir := t.TempDir()
	// The condition holds first: git finds no repository from dir, so a
	// TMPDIR inside a work tree cannot turn this into a write to its .git.
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
		t.Fatalf("%s is inside a git repository (%s); this test needs a TMPDIR outside every work tree", dir, out)
	}
	state := filepath.Join(t.TempDir(), "fyr")
	if Registered(state, dir) {
		t.Error("a plain directory reads as registered")
	}
	if _, err := Register(state, dir, day); !errors.Is(err, ErrNotCheckout) {
		t.Errorf("Register outside a checkout: %v, want ErrNotCheckout", err)
	}
}

// Two first registrations at once in a fresh home agree on one key, so
// neither leaves the other's marker unsealed.
func TestRegisterFirstKeyRace(t *testing.T) {
	state := filepath.Join(t.TempDir(), "fyr")
	repos := make([]string, 8)
	for i := range repos {
		repos[i] = committedRepo(t)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(repos))
	for i, r := range repos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = Register(state, r, day)
		}()
	}
	wg.Wait()
	for i, r := range repos {
		if errs[i] != nil || !Registered(state, r) {
			t.Errorf("registration %d: %v, registered %v", i, errs[i], Registered(state, r))
		}
	}
	if keys, _ := filepath.Glob(filepath.Join(state, "*"+keyName+"*")); len(keys) != 1 {
		t.Errorf("key files: %v, want one", keys)
	}
}

func TestRegisterRefusesRelativeStateDir(t *testing.T) {
	root, state := registeredRepo(t)
	t.Chdir(root)
	rel, err := filepath.Rel(root, state)
	if err != nil || filepath.IsAbs(rel) {
		t.Fatalf("no relative path to the state dir: %q, %v", rel, err)
	}
	if Registered(rel, root) {
		t.Error("a relative state directory was read")
	}
	if _, err := Register("fyr-state", root, day); err == nil || !strings.Contains(err.Error(), "not an absolute path") {
		t.Errorf("Register with a relative state dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "fyr-state")); err == nil {
		t.Error("a relative state dir was created inside the checkout")
	}
}
