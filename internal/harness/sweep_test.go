package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSweepStaleDirsStaysInParent: the leftovers of an interrupted update are
// found by listing the parent and matching a prefix, never by a glob built
// from the home path — a home holding `[`, `*` or `?` would otherwise reach a
// sibling directory and remove its entries (project review 2026-09-30,
// security finding 1). A file, a symlink and a directory younger than the
// age are left where they are.
func TestSweepStaleDirsStaysInParent(t *testing.T) {
	root := t.TempDir()
	// `u[1]` as a glob matches `u1` and not itself: the old code swept the
	// sibling and skipped its own home.
	base := filepath.Join(root, "u[1]", StateDir)
	sibling := filepath.Join(root, "u1", StateDir, "release-keep")
	for _, d := range []string{filepath.Join(base, "release-old"), filepath.Join(base, "release-new"), sibling} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(base, "release-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "release-link")
	if err := os.Symlink(sibling, link); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{filepath.Join(base, "release-old"), file, sibling} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	SweepStaleDirs(base, "release-", time.Hour, &out)

	if _, err := os.Lstat(filepath.Join(base, "release-old")); !os.IsNotExist(err) {
		t.Errorf("the old directory should be removed, got %v", err)
	}
	for _, keep := range []string{filepath.Join(base, "release-new"), file, link, sibling} {
		if _, err := os.Lstat(keep); err != nil {
			t.Errorf("%s should survive the sweep: %v", keep, err)
		}
	}
	if got := out.String(); !bytes.Contains([]byte(got), []byte("release-old")) {
		t.Errorf("the removal should be named, got %q", got)
	}
}
