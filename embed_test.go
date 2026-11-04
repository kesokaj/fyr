package fyr

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestFilesMatchTree: every regular file under the five harness directories
// and docker/ is embedded with the same bytes — `.gitkeep` files included,
// which `embed` skips without `all:` (plan D2).
func TestFilesMatchTree(t *testing.T) {
	disk := os.DirFS(".")
	n := 0
	for _, root := range []string{"context", "skills", "agents", "commands", "rules", "docker"} {
		err := fs.WalkDir(disk, root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			want, err := fs.ReadFile(disk, path)
			if err != nil {
				return err
			}
			got, err := fs.ReadFile(Files, path)
			if err != nil {
				t.Errorf("%s not embedded: %v", path, err)
				return nil
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s embedded with different bytes", path)
			}
			n++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if n == 0 {
		t.Fatal("no files walked; the test ran in the wrong directory")
	}
}

// TestEmbedHoldsOnlyTrackedFiles: `go:embed` reads the WORKING TREE, and
// `all:` takes dotfiles, so an untracked or gitignored file sitting in one of
// the five directories at build time is compiled into the released binary and
// installed on every machine — a file review never saw, and one no loader
// reads, so nothing else would notice it (security review 2026-09-17,
// finding 1). The embedded set must therefore equal what git tracks, in both
// directions.
func TestEmbedHoldsOnlyTrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := os.Stat(".git"); err != nil {
		t.Skip("not a git checkout: an unpacked copy has nothing to compare against")
	}
	roots := []string{"context", "skills", "agents", "commands", "rules", "docker"}
	out, err := exec.Command("git", append([]string{"ls-files", "-z", "--"}, roots...)...).Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	tracked := map[string]bool{}
	for _, p := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if p != "" {
			tracked[p] = true
		}
	}
	if len(tracked) == 0 {
		t.Fatal("git tracks no files under the five directories; the test ran in the wrong directory")
	}
	for _, root := range roots {
		err := fs.WalkDir(Files, root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if !tracked[path] {
				t.Errorf("%s is embedded but not tracked by git: a release binary would ship it", path)
			}
			delete(tracked, path)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for p := range tracked {
		t.Errorf("%s is tracked by git but not embedded", p)
	}
}
