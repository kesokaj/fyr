package harness

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// DreamWorkPaths holds the directory as written and its resolved form: from
// the directory when it exists, from its deepest existing ancestor when it
// does not (a symlinked home, a symlinked config directory), and nothing
// more when no symlink is in the way (visible dream plan D15). InDreamWork
// matches the directory and below it, never a sibling sharing its prefix.
func TestDreamWorkPaths(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	real, _ = filepath.EvalSymlinks(real)
	asWritten, resolved := DreamWork(link), DreamWork(real)
	if want := filepath.Join(link, StateDir, "dream-work"); asWritten != want {
		t.Fatalf("DreamWork = %s, want %s", asWritten, want)
	}
	if got := DreamWorkPaths(link); !slices.Equal(got, []string{asWritten, resolved}) {
		t.Errorf("no directory yet: %v, want the home resolved", got)
	}
	if err := os.MkdirAll(resolved, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := DreamWorkPaths(link); !slices.Equal(got, []string{asWritten, resolved}) {
		t.Errorf("with the directory: %v", got)
	}
	if got := DreamWorkPaths(real); !slices.Equal(got, []string{resolved}) {
		t.Errorf("no symlink: %v, want one entry", got)
	}

	// The config directory a link elsewhere, dream-work removed.
	plain, conf := t.TempDir(), t.TempDir()
	conf, _ = filepath.EvalSymlinks(conf)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(plain, StateDir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(conf, filepath.Join(plain, StateDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(DreamWork(plain)); !os.IsNotExist(err) {
		t.Fatalf("dream-work exists under the linked config directory: %v", err)
	}
	if got, want := DreamWorkPaths(plain), []string{DreamWork(plain), filepath.Join(conf, "dream-work")}; !slices.Equal(got, want) {
		t.Errorf("linked config directory: %v, want %v", got, want)
	}

	set := DreamWorkPaths(link)
	for p, want := range map[string]bool{
		resolved:                             true,
		asWritten + "/":                      true,
		filepath.Join(asWritten, "sub", "x"): true,
		asWritten + "2":                      false,
		filepath.Dir(asWritten):              false,
		filepath.Join(real, "project"):       false,
	} {
		if got := InDreamWork(p, set); got != want {
			t.Errorf("InDreamWork(%s) = %v, want %v", p, got, want)
		}
	}
}
