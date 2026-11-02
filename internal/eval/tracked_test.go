package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadTrackedSuite pins ADR-500's reads: no file is not found (the
// record fallback), a committed file is read, and with atHead a file that is
// not committed or differs from HEAD is refused; a link is refused always.
func TestReadTrackedSuite(t *testing.T) {
	root := outsideTemp(t)
	gitInit(t, root)
	if rows, found, err := ReadTrackedSuite(root, true); found || err != nil || rows != nil {
		t.Fatalf("no file: %v, %v, %v; want not found", rows, found, err)
	}
	path := filepath.Join(root, filepath.FromSlash(SuitePath))
	suite := header + "| `a` |  | p |  | true |  | named |\n"
	put(t, path, suite, 0o644)
	if _, found, err := ReadTrackedSuite(root, true); !found || err == nil || !strings.Contains(err.Error(), "is not committed") {
		t.Errorf("an untracked file at HEAD: found %v, %v", found, err)
	}
	if rows, found, err := ReadTrackedSuite(root, false); !found || err != nil || len(rows) != 1 {
		t.Errorf("an untracked file, working copy: %v, %v, %v", rows, found, err)
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@localhost"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("add", SuitePath)
	git("commit", "-qm", "suite")
	if rows, found, err := ReadTrackedSuite(root, true); !found || err != nil || len(rows) != 1 || rows[0].ID != "a" {
		t.Errorf("a committed file: %v, %v, %v", rows, found, err)
	}
	put(t, path, suite+"| `b` |  | p |  | true |  | named |\n", 0o644)
	if _, _, err := ReadTrackedSuite(root, true); err == nil || !strings.Contains(err.Error(), "has uncommitted changes") {
		t.Errorf("a changed file at HEAD: %v", err)
	}
	if rows, _, err := ReadTrackedSuite(root, false); err != nil || len(rows) != 2 {
		t.Errorf("a changed file, working copy: %v, %v", rows, err)
	}
	put(t, path, "no table\n", 0o644)
	if _, found, err := ReadTrackedSuite(root, false); !found || err == nil || !strings.Contains(err.Error(), SuitePath) {
		t.Errorf("a file that does not parse: found %v, %v", found, err)
	}
	other := filepath.Join(root, "other.md")
	put(t, other, suite, 0o644)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, path); err != nil {
		t.Fatal(err)
	}
	if _, found, err := ReadTrackedSuite(root, false); !found || err == nil {
		t.Errorf("a link: found %v, %v; want refused", found, err)
	}
}
