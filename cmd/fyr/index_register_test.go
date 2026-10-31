package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// registerRepo is a checkout with a committed .agents/trail.jsonl and the
// .gitignore /init-project writes, from which the test runs.
func registerRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	os.MkdirAll(filepath.Join(root, ".agents"), 0o755)
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".agents/scratch/*\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".agents", "trail.jsonl"), nil, 0o644)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.email=t@example.invalid", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "trail"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Chdir(root)
	return root
}

func stubNow(t *testing.T, at time.Time) {
	t.Helper()
	saved := now
	t.Cleanup(func() { now = saved })
	now = func() time.Time { return at }
}

func TestIndexRegisterWritesMarkerThenIndexes(t *testing.T) {
	root := registerRepo(t)
	home := t.TempDir()
	marker := filepath.Join(root, ".git", "fyr-registered")
	var out bytes.Buffer
	// Before the marker, a search here leaves the checkout out of --global.
	if err := run([]string{"search", "bittern", "--home", home}, &out); err != nil {
		t.Fatalf("search: %v\n%s", err, out.String())
	}
	if ps, err := search.ListProjects(home); err != nil || len(ps) != 0 {
		t.Fatalf("unregistered: projects %v, %v", ps, err)
	}
	stubNow(t, time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC))
	out.Reset()
	if err := run([]string{"index", "--register", "--home", home}, &out); err != nil {
		t.Fatalf("index --register: %v\n%s", err, out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if !strings.HasPrefix(out.String(), "files    ") || lines[len(lines)-1] != "registered "+root {
		t.Errorf("index --register printed:\n%s\nwant the index counts, then %q", out.String(), "registered "+root)
	}
	if b, err := os.ReadFile(marker); err != nil || !strings.HasPrefix(string(b), "registered 2026-10-28\nseal ") {
		t.Errorf("marker %q (%v)", b, err)
	}
	if ps, err := search.ListProjects(home); err != nil || len(ps) != 1 || ps[0].Root != root {
		t.Errorf("registered: projects %v, %v", ps, err)
	}
	// A rerun rewrites the date, and --rebuild combines.
	stubNow(t, time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC))
	out.Reset()
	if err := run([]string{"index", "--register", "--rebuild", "--home", home}, &out); err != nil || !strings.HasSuffix(out.String(), "registered "+root+"\n") {
		t.Fatalf("rerun with --rebuild: %v\n%s", err, out.String())
	}
	if b, _ := os.ReadFile(marker); !strings.HasPrefix(string(b), "registered 2026-10-30\n") {
		t.Errorf("rerun left %q", b)
	}
}

func TestIndexRegisterUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		args       []string
		checkout   bool
	}{
		{"with --global", "fyr index: --register and --global do not go together", []string{"index", "--register", "--global"}, true},
		{"outside a checkout", "fyr index: --register needs a git checkout", []string{"index", "--register"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.checkout {
				dir = registerRepo(t)
			} else if out, err := exec.Command("git", "-C", dir, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
				// The condition holds first: a TMPDIR inside a work tree would
				// make this a write to that tree's .git.
				t.Fatalf("%s is inside a git repository (%s); this test needs a TMPDIR outside every work tree", dir, out)
			} else {
				t.Chdir(dir)
			}
			var out bytes.Buffer
			err := run(append(tc.args, "--home", t.TempDir()), &out)
			var code exitCode
			if !errors.As(err, &code) || code != 2 || strings.TrimSpace(out.String()) != tc.want {
				t.Errorf("err = %v, out = %q; want exit 2 and %q", err, out.String(), tc.want)
			}
			if _, err := os.Lstat(filepath.Join(dir, ".git", "fyr-registered")); err == nil {
				t.Error("a usage error wrote the marker")
			}
		})
	}
}

func TestIndexRegisterRefusedMarker(t *testing.T) {
	root := registerRepo(t)
	marker := filepath.Join(root, ".git", "fyr-registered")
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), marker); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := run([]string{"index", "--register", "--home", t.TempDir()}, &out)
	var code exitCode
	if err == nil || errors.As(err, &code) || !strings.Contains(err.Error(), marker) {
		t.Fatalf("a symlinked marker: err = %v, want an exit-1 error naming %s", err, marker)
	}
	if strings.Contains(out.String(), "registered") || strings.Contains(out.String(), "files ") {
		t.Errorf("a refused marker still indexed or reported:\n%s", out.String())
	}
}

// The marker comes first: an index that fails leaves a valid marker, and
// the error says the checkout is registered.
func TestIndexRegisterIndexFailsAfterMarker(t *testing.T) {
	root := registerRepo(t)
	os.Remove(filepath.Join(root, ".gitignore")) // the index refuses an unignored search.db
	home := t.TempDir()
	var out bytes.Buffer
	err := run([]string{"index", "--register", "--home", home}, &out)
	if err == nil || !strings.Contains(err.Error(), "registered "+root+", but the index failed") || !strings.Contains(err.Error(), "not gitignored") {
		t.Fatalf("err = %v, want the registered-but-failed error", err)
	}
	if !trail.Registered(filepath.Join(home, harness.StateDir), root) {
		t.Error("no valid marker after a failed index")
	}
}

// Every other failure to place the marker exits 1 and names what failed:
// a common directory that cannot be written, a checkout git places
// elsewhere; nothing is indexed and nothing reported as registered.
func TestIndexRegisterOtherFailures(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes a mode-0500 directory; the write cannot fail here")
	}
	for _, tc := range []struct {
		name, want string
		setup      func(t *testing.T, root string)
	}{
		{"common dir not writable", "permission denied", func(t *testing.T, root string) {
			git := filepath.Join(root, ".git")
			if err := os.Chmod(git, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(git, 0o755) })
			if f, err := os.CreateTemp(git, "probe-*"); err == nil {
				f.Close()
				os.Remove(f.Name())
				t.Fatal("the common dir is still writable, so this proves nothing")
			}
		}},
		{"work tree elsewhere", "which is not a parent of it", func(t *testing.T, root string) {
			if out, err := exec.Command("git", "-C", root, "config", "core.worktree", t.TempDir()).CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := registerRepo(t)
			tc.setup(t, root)
			var out bytes.Buffer
			err := run([]string{"index", "--register", "--home", t.TempDir()}, &out)
			var code exitCode
			if err == nil || errors.As(err, &code) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want an exit-1 error holding %q", err, tc.want)
			}
			if strings.Contains(out.String(), "registered") || strings.Contains(out.String(), "files ") {
				t.Errorf("a failed registration still indexed or reported:\n%s", out.String())
			}
		})
	}
}

// A marker on a checkout with no trail is consent nothing reads yet, and the
// run says so; with a trail it says nothing more.
func TestIndexRegisterNotesAMissingTrail(t *testing.T) {
	root := registerRepo(t)
	home := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"index", "--register", "--home", home}, &out); err != nil {
		t.Fatalf("index --register: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "note:") {
		t.Errorf("a note beside a trail:\n%s", out.String())
	}
	if err := os.Remove(filepath.Join(root, ".agents", "trail.jsonl")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"index", "--register", "--home", home}, &out); err != nil {
		t.Fatalf("index --register: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "note: "+root+" has no .agents/trail.jsonl") {
		t.Errorf("no note:\n%s", out.String())
	}
}
