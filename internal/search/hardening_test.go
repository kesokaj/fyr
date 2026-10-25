package search

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/trail"
)

// BUG-165: search reconciled through `git status`, which runs a clean filter
// the repository's .gitattributes and config define. The view must come from
// commands that read objects and files only, and still tell each state apart.
func TestGitViewRunsNoCleanFilter(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "filter-ran")
	prog := filepath.Join(t.TempDir(), "fake-clean")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\ntouch '"+marker+"'\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) { os.WriteFile(filepath.Join(root, name), []byte(body), 0o644) }
	write("kept.md", "kept\n")
	write("edited.md", "before\n")
	write("gone.md", "gone\n")
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "files")
	write(".gitattributes", "*.md filter=x\n")
	git("config", "filter.x.clean", prog)
	write("edited.md", "after, longer\n")
	write("new.md", "new\n")
	os.Remove(filepath.Join(root, "gone.md"))
	later := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(root, "kept.md"), later, later) // forces status to hash through the filter
	if out, err := exec.Command("git", "-C", root, "status", "--porcelain").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the fixture does not run the filter on a plain status, so it proves nothing: %v", err)
	}
	os.Remove(marker)

	entries, _, err := gitView(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("search ran the repository's clean filter")
	}
	state := map[string]string{}
	for _, e := range entries {
		state[e.path] = e.state
	}
	want := map[string]string{"kept.md": "tracked", "edited.md": "modified", "new.md": "untracked", ".gitattributes": "untracked"}
	for p, s := range want {
		if state[p] != s {
			t.Errorf("%s: state %q, want %q", p, state[p], s)
		}
	}
	if _, ok := state["gone.md"]; ok {
		t.Error("a deleted file is still in the view")
	}
}

// The stat fast path never hides an edit git would see: a same-size edit
// whose mtime is put back, written in the second the index was, is racily
// clean and is hashed, so it reads as modified.
func TestGitViewRacilyCleanEditIsHashed(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	f := filepath.Join(root, "a.md")
	os.WriteFile(f, []byte("aaaa\n"), 0o644)
	run("init", "-q")
	run("add", "a.md")
	run("commit", "-q", "-m", "a")
	fi, _ := os.Stat(f)
	mt := fi.ModTime()
	os.WriteFile(f, []byte("bbbb\n"), 0o644) // same size
	os.Chtimes(f, mt, mt)                    // the index's recorded mtime
	os.Chtimes(filepath.Join(root, ".git", "index"), mt, mt)
	entries, _, err := gitView(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.path == "a.md" && e.state != "modified" {
			t.Fatalf("a racily clean edit read as %q", e.state)
		}
	}
}

// A tracked marker deleted from the work tree stops counting, whatever the
// prefix: a root marker gone from disk no longer leaves the checkout out of
// a prefix-limited view (the global index reads only .agents/docs).
func TestGitViewDeletedMarkerOutsidePrefix(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.MkdirAll(filepath.Join(root, ".agents", "docs"), 0o755)
	os.WriteFile(filepath.Join(root, ".agents", "docs", "h.md"), []byte("h\n"), 0o644)
	os.WriteFile(filepath.Join(root, trail.NoIndexMarkers[0]), nil, 0o644)
	run("init", "-q")
	run("add", ".")
	run("commit", "-q", "-m", "marked")
	os.Remove(filepath.Join(root, trail.NoIndexMarkers[0]))
	entries, _, err := gitView(root, ".agents/docs/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("view under the prefix has %d entries, want 1: a deleted root marker still counts", len(entries))
	}
}

// indexStats reads each entry's own stat lines: a tracked name holding a
// newline and a forged header cannot pin a size or mtime on another file
// (security review of BUG-165's fix, finding 2).
func TestIndexStatsIgnoresForgedNames(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(root, "a.md"), []byte("aaaa\n"), 0o644)
	forged := "evil\n  size: 999\tflags: 0\n100644 e69de29bb2d1d6434b8b29ae775ad8c2e48c5391 0\ta.md\n  mtime: 1:1\n  size: 999\tflags: 0"
	if err := os.WriteFile(filepath.Join(root, forged), []byte("x\n"), 0o644); err != nil {
		t.Skip("this file system refuses newlines in names")
	}
	run("init", "-q")
	run("add", ".")
	run("commit", "-q", "-m", "files")
	stats, _ := indexStats(root)
	if st := stats["a.md"]; st.size != 5 {
		t.Fatalf("a.md's indexed size reads %d, want its own 5: a forged name set it", st.size)
	}
}

// No search git call starts the checkout's core.fsmonitor program.
func TestGitViewRunsNoFsmonitor(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "fired")
	hook := filepath.Join(t.TempDir(), "hook.sh")
	os.WriteFile(hook, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o755)
	for _, args := range [][]string{{"init", "-q"}, {"config", "core.fsmonitor", hook}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	os.WriteFile(filepath.Join(root, "a.md"), []byte("a\n"), 0o644)
	exec.Command("git", "-C", root, "status", "--porcelain").Run()
	if _, err := os.Stat(marker); err != nil {
		t.Skip("control: this git did not run the core.fsmonitor hook")
	}
	os.Remove(marker)
	if _, _, err := gitView(root, ""); err != nil {
		t.Fatal(err)
	}
	if err := guardProject(root); err != nil && err != errNotIgnored {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("search ran the checkout's fsmonitor hook")
	}
}

// Search refuses a partial clone only when this git is too old (before 2.44)
// to honour GIT_NO_LAZY_FETCH: such a git could fetch through the clone's own
// core.sshCommand. A newer git searches it as any checkout.
func TestGitViewPartialCloneNeedsNewGit(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "extensions.partialClone", "origin"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	os.WriteFile(filepath.Join(root, "a.md"), []byte("a\n"), 0o644)
	saved := lazyFetchSafe
	t.Cleanup(func() { lazyFetchSafe = saved })

	lazyFetchSafe = func() bool { return false }
	if _, _, err := gitView(root, ""); err == nil || !strings.Contains(err.Error(), "partial clone") {
		t.Fatalf("an old git searched a partial clone: %v", err)
	}
	lazyFetchSafe = func() bool { return true }
	if _, _, err := gitView(root, ""); err != nil {
		t.Fatalf("a new git refused a partial clone: %v", err)
	}
}
