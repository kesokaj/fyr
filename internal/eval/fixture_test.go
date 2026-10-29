package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// outsideTemp is a temporary directory outside every git checkout, or the
// test is skipped: guard b refuses anything else by design.
func outsideTemp(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	if r, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = r
	}
	if out, err := exec.Command("git", "-C", tmp, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
		t.Skipf("the test's temporary directory is inside a git checkout (%s): guard b refuses that by design", strings.TrimSpace(string(out)))
	}
	return tmp
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

func put(t *testing.T, p, s string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
}

// TestGuardPreflight: a root that is no checkout and a home inside a
// checkout (the BUG-106 message) are refused before anything is created.
func TestGuardPreflight(t *testing.T) {
	tmp := outsideTemp(t)
	project, real := filepath.Join(tmp, "project"), filepath.Join(tmp, "real")
	gitInit(t, project)
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	o := Options{Root: project, Home: filepath.Join(tmp, "home"), RealHome: real}
	if err := Preflight(o); err != nil {
		t.Errorf("a checkout and a home outside it: %v", err)
	}
	inside := o
	inside.Home = filepath.Join(project, ".agents", "scratch", "home")
	err := Preflight(inside)
	if err == nil || !strings.Contains(err.Error(), "is inside a git checkout (BUG-106); use a --home outside every checkout") {
		t.Errorf("a home inside a checkout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".agents")); !os.IsNotExist(err) {
		t.Error("the refused pre-flight created the home")
	}
	notRepo := o
	notRepo.Root = real
	if err := Preflight(notRepo); err == nil || !strings.Contains(err.Error(), "is not a git checkout") {
		t.Errorf("a root that is no checkout: %v", err)
	}
}
