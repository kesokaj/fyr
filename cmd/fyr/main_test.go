package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// checkoutShaped is plainCopy plus the one thing IsCheckout looks for: a
// `.git` entry. It is what the dry-run test needs — "this is a checkout"
// without being THIS checkout — and it is not a git repository, which
// Inspect tolerates (empty fields) and nothing on the install path needs.
func checkoutShaped(t *testing.T) string {
	t.Helper()
	dir := plainCopy(t)
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// plainCopy copies this checkout's deployable trees into a directory with
// no .git: what an unpacked bundle is (R8).
// checkoutRoot is this checkout, resolved once at package load: tests that
// move the working directory (isolate) still find it.
var checkoutRoot, checkoutRootErr = filepath.Abs("../..")

func plainCopy(t *testing.T) string {
	t.Helper()
	src, err := checkoutRoot, checkoutRootErr
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	for _, top := range []string{"context", "skills", "agents", "commands", "rules"} {
		root := filepath.Join(src, top)
		err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			if d.IsDir() {
				return os.MkdirAll(filepath.Join(dst, rel), 0o755)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if harness.IsCheckout(dst) {
		t.Fatal("fixture: the plain copy must not carry .git")
	}
	return dst
}

// plainCopyOf is the unpacked-zip place: this checkout's five harness
// directories copied without .git.
func plainCopyOf(t *testing.T) string {
	t.Helper()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy")
	for _, dir := range []string{"context", "skills", "agents", "commands", "rules"} {
		src := filepath.Join(repo, dir)
		err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(repo, path)
			target := filepath.Join(dst, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			info, _ := d.Info()
			return os.WriteFile(target, data, info.Mode().Perm())
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

// scratchClone clones this checkout into a temp dir, for a test that must
// let `install` rebuild and run a child install without touching this tree.
func scratchClone(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "clone")
	cmd := exec.Command("git", "clone", "-q", repo, dst)
	cmd.Env = append(gitCleanEnv(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	return dst
}
