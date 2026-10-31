package trail

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cleanRepo is a committed repository holding a regular file, an executable
// and a tracked symlink (this checkout tracks .agents/skills as one), with a
// .gitignore for ignored.txt; the tree is clean when it returns.
func cleanRepo(t *testing.T) (root string, git func(args ...string)) {
	t.Helper()
	root = t.TempDir()
	git = func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	os.MkdirAll(filepath.Join(root, "dir"), 0o755)
	os.WriteFile(filepath.Join(root, "dir", "a.md"), []byte("a\n"), 0o644)
	os.WriteFile(filepath.Join(root, "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.txt\n"), 0o644)
	if err := os.Symlink("dir", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "init")
	return root, git
}

func TestDirty(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(t *testing.T, root string, git func(...string))
		dirty bool
	}{
		{"clean, with a tracked symlink", func(*testing.T, string, func(...string)) {}, false},
		{"exec bit changed only", func(t *testing.T, root string, _ func(...string)) {
			os.Chmod(filepath.Join(root, "run.sh"), 0o644)
		}, false},
		{"ignored file added", func(t *testing.T, root string, _ func(...string)) {
			os.WriteFile(filepath.Join(root, "ignored.txt"), []byte("x"), 0o644)
		}, false},
		{"content edited", func(t *testing.T, root string, _ func(...string)) {
			os.WriteFile(filepath.Join(root, "dir", "a.md"), []byte("b\n"), 0o644)
		}, true},
		{"tracked file deleted", func(t *testing.T, root string, _ func(...string)) {
			os.Remove(filepath.Join(root, "run.sh"))
		}, true},
		{"symlink retargeted", func(t *testing.T, root string, _ func(...string)) {
			os.Remove(filepath.Join(root, "link"))
			os.Symlink("elsewhere", filepath.Join(root, "link"))
		}, true},
		// Hashed through the root, never outside it (security review of
		// v2.6.0, part 2, finding 3): the same bytes behind a link still differ.
		{"tracked directory replaced by a link out, same bytes", func(t *testing.T, root string, _ func(...string)) {
			outside := t.TempDir()
			os.WriteFile(filepath.Join(outside, "a.md"), []byte("a\n"), 0o644)
			os.Remove(filepath.Join(root, "link"))
			os.Symlink("dir", filepath.Join(root, "link"))
			os.RemoveAll(filepath.Join(root, "dir"))
			os.Symlink(outside, filepath.Join(root, "dir"))
			// Excluded, or `ls-files -o` would report the link and this row
			// would pass without the read through the root.
			os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte("dir\n"), 0o644)
		}, true},
		{"symlink replaced by a file", func(t *testing.T, root string, _ func(...string)) {
			os.Remove(filepath.Join(root, "link"))
			os.WriteFile(filepath.Join(root, "link"), []byte("dir"), 0o644)
		}, true},
		{"untracked file", func(t *testing.T, root string, _ func(...string)) {
			os.WriteFile(filepath.Join(root, "new.md"), []byte("n\n"), 0o644)
		}, true},
		{"staged new file", func(t *testing.T, root string, git func(...string)) {
			os.WriteFile(filepath.Join(root, "staged.md"), []byte("s\n"), 0o644)
			git("add", "staged.md")
		}, true},
		{"staged removal kept on disk", func(t *testing.T, root string, git func(...string)) {
			git("rm", "-q", "--cached", "dir/a.md")
		}, true},
		// The work tree matches HEAD again, the index does not (security
		// review of the fix, finding 3).
		{"staged change, work tree restored", func(t *testing.T, root string, git func(...string)) {
			os.WriteFile(filepath.Join(root, "dir", "a.md"), []byte("staged\n"), 0o644)
			git("add", "dir/a.md")
			os.WriteFile(filepath.Join(root, "dir", "a.md"), []byte("a\n"), 0o644)
		}, true},
		{"staged removal of an ignored tracked file", func(t *testing.T, root string, git func(...string)) {
			os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.txt\nrun.sh\n"), 0o644)
			git("add", ".gitignore")
			git("commit", "-q", "-m", "ignore run.sh")
			git("rm", "-q", "--cached", "run.sh")
		}, true},
		{"staged mode change, work tree restored", func(t *testing.T, root string, git func(...string)) {
			git("update-index", "--chmod=-x", "run.sh")
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, git := cleanRepo(t)
			c.edit(t, root, git)
			got, err := Dirty(root)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.dirty {
				t.Errorf("Dirty = %v, want %v", got, c.dirty)
			}
		})
	}
}

// Dirty decides a tree whose stat data is stale without the repository's
// clean filter, which `git status` would run (SD38 of the 2026-10-27 audit,
// BUG-164's rule).
func TestDirtyRunsNoCleanFilter(t *testing.T) {
	root, git := cleanRepo(t)
	marker := filepath.Join(t.TempDir(), "filter-ran")
	prog := filepath.Join(t.TempDir(), "fake-clean")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\ntouch '"+marker+"'\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.md filter=x\n"), 0o644)
	git("add", ".gitattributes")
	git("commit", "-q", "-m", "attributes")
	git("config", "filter.x.clean", prog)
	later := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(root, "dir", "a.md"), later, later)
	// The fixture must trip the marker with a plain status (silence is not evidence).
	if out, err := exec.Command("git", "-C", root, "status", "--porcelain").CombinedOutput(); err != nil {
		t.Fatalf("plain git status: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the fixture does not run the clean filter on a plain git status, so it proves nothing: %v", err)
	}
	os.Remove(marker)
	os.Chtimes(filepath.Join(root, "dir", "a.md"), later.Add(time.Hour), later.Add(time.Hour))
	got, err := Dirty(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Dirty ran the repository's clean filter")
	}
	if got {
		t.Error("a tree whose only change is stat data reads dirty")
	}
}

// A tracked name holding a newline cannot go through hash-object's
// --stdin-paths: Dirty says it cannot answer instead of guessing (D7).
func TestDirtyCannotAnswer(t *testing.T) {
	root, git := cleanRepo(t)
	os.WriteFile(filepath.Join(root, "a\nb.md"), []byte("x\n"), 0o644)
	git("add", ".")
	git("commit", "-q", "-m", "newline")
	if _, err := Dirty(root); err == nil {
		t.Error("Dirty answered for a tree it cannot hash")
	}
	if _, err := Dirty(t.TempDir()); err == nil {
		t.Error("Dirty answered outside a repository")
	}
}

// A tracked name opening with '"' is C-unquoted by hash-object's
// --stdin-paths, which then hashes another path: `"a"` hashes a. With both
// committed holding the same bytes and a unchanged, a modified `"a"` read
// clean. Dirty says it cannot answer instead (the v3.1.0 gate's security
// review).
func TestDirtyCannotAnswerQuotedName(t *testing.T) {
	root, git := cleanRepo(t)
	os.WriteFile(filepath.Join(root, "a"), []byte("a\n"), 0o644)
	os.WriteFile(filepath.Join(root, `"a"`), []byte("a\n"), 0o644)
	git("add", ".")
	git("commit", "-q", "-m", "quoted")
	os.WriteFile(filepath.Join(root, `"a"`), []byte("changed, and longer\n"), 0o644)
	got, err := Dirty(root)
	if err == nil {
		t.Errorf("Dirty answered %v for a tree holding a tracked name opening with a quote", got)
	}
}

// A GIT_DIR (or another repository-locating variable) in fyr's own
// environment, as inside a git hook or `git rebase -x`, never redirects a
// read from the root it names (SD24 of the 2026-10-27 audit, D20).
func TestCommandIgnoresRepositoryVariables(t *testing.T) {
	root, _ := cleanRepo(t)
	other, _ := cleanRepo(t)
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))
	out, err := Command(root, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	want, _ := filepath.EvalSymlinks(filepath.Join(root, ".git"))
	if got != want {
		t.Errorf("git dir %s, want %s", got, want)
	}
}

// Dirty refuses a root that is not the checkout's top level, rather than
// reading every file as deleted and answering dirty for a clean tree (ledger
// item 57; cmd/fyr's eval passes a resolved source directory).
func TestDirtyRefusesSubdir(t *testing.T) {
	root, _ := cleanRepo(t)
	if dirty, err := Dirty(root); err != nil || dirty {
		t.Fatalf("the clean fixture at its top level: dirty %v, err %v", dirty, err)
	}
	sub := filepath.Join(root, "dir")
	dirty, err := Dirty(sub)
	if err == nil || !strings.Contains(err.Error(), "trail: Dirty: "+sub+" is not the checkout's top level") {
		t.Errorf("Dirty(%s): dirty %v, err %v; want the top-level refusal", sub, dirty, err)
	}
	// Through a link to the top level, the same checkout is accepted.
	link := filepath.Join(t.TempDir(), "via")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if dirty, err := Dirty(link); err != nil || dirty {
		t.Errorf("Dirty through a link to the top level: dirty %v, err %v", dirty, err)
	}
}
