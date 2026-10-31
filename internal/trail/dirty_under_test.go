package trail

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDirtyUnder: a change counts only under the named top-level
// directories, by each of Dirty's reads (work tree, index, untracked), and a
// sibling whose name only starts with one (dirx/) is outside it (PLAN.md item
// 53: eval's harness key reads what `fyr install` installs).
func TestDirtyUnder(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(t *testing.T, root string, git func(...string))
		dirty bool
	}{
		{"clean", func(*testing.T, string, func(...string)) {}, false},
		{"content edited outside", func(t *testing.T, root string, _ func(...string)) {
			os.WriteFile(filepath.Join(root, "run.sh"), []byte("#!/bin/sh\necho\n"), 0o755)
		}, false},
		{"untracked file outside", func(t *testing.T, root string, _ func(...string)) {
			os.WriteFile(filepath.Join(root, "new.md"), []byte("n\n"), 0o644)
		}, false},
		{"untracked file in a sibling sharing the prefix", func(t *testing.T, root string, _ func(...string)) {
			os.MkdirAll(filepath.Join(root, "dirx"), 0o755)
			os.WriteFile(filepath.Join(root, "dirx", "f"), []byte("n\n"), 0o644)
		}, false},
		{"staged new file outside", func(t *testing.T, root string, git func(...string)) {
			os.WriteFile(filepath.Join(root, "staged.md"), []byte("s\n"), 0o644)
			git("add", "staged.md")
		}, false},
		{"staged removal outside", func(t *testing.T, root string, git func(...string)) {
			git("rm", "-q", "--cached", "run.sh")
		}, false},
		{"content edited inside", func(t *testing.T, root string, _ func(...string)) {
			os.WriteFile(filepath.Join(root, "dir", "a.md"), []byte("b\n"), 0o644)
		}, true},
		{"untracked file inside", func(t *testing.T, root string, _ func(...string)) {
			os.WriteFile(filepath.Join(root, "dir", "new.md"), []byte("n\n"), 0o644)
		}, true},
		{"staged removal inside", func(t *testing.T, root string, git func(...string)) {
			git("rm", "-q", "--cached", "dir/a.md")
		}, true},
		{"staged change inside, work tree restored", func(t *testing.T, root string, git func(...string)) {
			os.WriteFile(filepath.Join(root, "dir", "a.md"), []byte("staged\n"), 0o644)
			git("add", "dir/a.md")
			os.WriteFile(filepath.Join(root, "dir", "a.md"), []byte("a\n"), 0o644)
		}, true},
		{"the directory replaced by a file", func(t *testing.T, root string, _ func(...string)) {
			os.RemoveAll(filepath.Join(root, "dir"))
			os.WriteFile(filepath.Join(root, "dir"), []byte("x\n"), 0o644)
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, git := cleanRepo(t)
			c.edit(t, root, git)
			got, err := DirtyUnder(root, []string{"dir"})
			if err != nil {
				t.Fatal(err)
			}
			if got != c.dirty {
				t.Errorf("DirtyUnder = %v, want %v", got, c.dirty)
			}
		})
	}
}

// DirtyUnder keeps Dirty's top-level refusal: a subdirectory would read
// every file as deleted.
func TestDirtyUnderRefusesSubdir(t *testing.T) {
	root, _ := cleanRepo(t)
	if _, err := DirtyUnder(filepath.Join(root, "dir"), []string{"dir"}); err == nil {
		t.Error("DirtyUnder below the top level: no error")
	}
}
