package harness

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSource(t *testing.T) {
	t.Setenv("FYR_REPO", "")
	cwd := t.TempDir()
	t.Chdir(cwd)
	// The walk starts at the working directory, so a temp directory INSIDE a
	// harness checkout (a TMPDIR under one) would be found and every case
	// below would read that tree instead (security review 2026-09-17).
	if _, err := ResolveSource("", t.TempDir(), ""); err == nil {
		t.Skip("the temp directory happens to be inside a harness repo")
	}
	repo := newRepo(t)
	home := t.TempDir()

	// A development build with no tree found keeps the error: only a release
	// binary may install the files it carries (D14).
	for _, v := range []string{"", "b64ffe886729", "b64ffe886729+dirty", "(devel)", "v1.2.3-rc.1", "v1.2.3-0.20260917090000-b64ffe886729", "1.2.3", "v1.2"} {
		_, err := ResolveSource("", home, v)
		if err == nil || !strings.Contains(err.Error(), "harness repo not found") {
			t.Errorf("version %q with no tree: err = %v, want harness repo not found", v, err)
			continue
		}
		// The message says WHY the embedded fallback was refused — this build
		// has no harness in it — and names the release-binary way out. Saying
		// only "run from inside the checkout" sent the operator looking for a
		// repository they had been told they would not need (2026-09-18).
		if !strings.Contains(err.Error(), "no harness built in") || !strings.Contains(err.Error(), "release binary") {
			t.Errorf("version %q: err = %v, want it to name the missing harness and the release-binary path", v, err)
		}
		if v != "" && v != "unknown" && !strings.Contains(err.Error(), v) {
			t.Errorf("version %q: err = %v, want it to quote the version", v, err)
		}
	}

	src, err := ResolveSource("", home, "v1.2.3")
	if err != nil {
		t.Fatalf("release binary with no tree: %v", err)
	}
	if src.Dir != "" || src.Version != "v1.2.3" || src.FS == nil {
		t.Errorf("release binary with no tree: %+v, want the embedded source", src)
	}
	if _, err := fs.Stat(src.FS, "context/AGENTS.md"); err != nil {
		t.Errorf("the embedded source has no context/AGENTS.md: %v", err)
	}

	// The working-directory walk, for a development build: standing inside a
	// harness tree, or anywhere under it, finds that tree. Nothing pinned this
	// before, so deleting the walk kept the suite green (code review
	// 2026-09-18).
	nested := filepath.Join(repo, "skills", "alpha", "references")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{repo, nested} {
		t.Chdir(dir)
		if src, err := ResolveSource("", t.TempDir(), ""); err != nil || src.Dir != repo {
			t.Errorf("development build in %s: %+v, %v — want the tree walked up to", dir, src, err)
		}
	}
	t.Chdir(cwd)

	// A development build takes the path it is given.
	if src, err := ResolveSource(repo, home, ""); err != nil || src.Dir != repo {
		t.Errorf("explicit repo, development build: %+v, %v", src, err)
	}
	if err := SaveState(home, State{Repo: repo}); err != nil {
		t.Fatal(err)
	}
	// The recorded tree comes before the embedded files for a DEVELOPMENT
	// build, which has nothing of its own to offer.
	if src, err := ResolveSource("", home, ""); err != nil || src.Dir != repo {
		t.Errorf("repo from state, development build: %+v, %v", src, err)
	}
	// A RELEASE binary takes NO tree on this machine: not the recorded one,
	// whether a plain copy or a real checkout, and not the working directory.
	// What it installs is the published repository at the tagged commit, which
	// it carries; `update` replaces the whole binary to change that (ADR-034 —
	// the operator, after a box installed months-old content from a recorded
	// plain copy while reporting no drift: "the binary should only use the
	// published git repo in github always and only").
	if src, err := ResolveSource("", home, "v1.2.3"); err != nil || src.Dir != "" || src.Version != "v1.2.3" {
		t.Errorf("recorded plain copy, release binary: %+v, %v — want the embedded files", src, err)
	}
	checkout := newRepo(t)
	gitInit(t, checkout)
	if err := SaveState(home, State{Repo: checkout}); err != nil {
		t.Fatal(err)
	}
	if src, err := ResolveSource("", home, "v1.2.3"); err != nil || src.Dir != "" {
		t.Errorf("recorded checkout, release binary: %+v, %v — want the embedded files", src, err)
	}
	// Standing inside a tree does not change it either.
	t.Chdir(checkout)
	if src, err := ResolveSource("", home, "v1.2.3"); err != nil || src.Dir != "" {
		t.Errorf("inside a checkout, release binary: %+v, %v — want the embedded files", src, err)
	}
	// A named path is REFUSED, not ignored: doing something other than what
	// was typed is what D15 exists to prevent.
	// The refusal names WHICH input carried the path: an exported
	// $FYR_REPO makes every command on the machine fail, and "unset it"
	// is unactionable without knowing which one (code review 2026-09-18).
	if _, err := ResolveSource(checkout, home, "v1.2.3"); err == nil ||
		!strings.Contains(err.Error(), "release binary") || !strings.Contains(err.Error(), "--repo="+checkout) {
		t.Errorf("--repo on a release binary: err = %v, want a refusal naming --repo and the path", err)
	}
	t.Setenv("FYR_REPO", checkout)
	if _, err := ResolveSource("", home, "v1.2.3"); err == nil ||
		!strings.Contains(err.Error(), "release binary") || !strings.Contains(err.Error(), "$FYR_REPO="+checkout) {
		t.Errorf("$FYR_REPO on a release binary: err = %v, want a refusal naming the variable", err)
	}
	t.Setenv("FYR_REPO", "")
	os.Unsetenv("FYR_REPO")
	// The old name is not read (plan fyr D15): set, it is neither used nor
	// refused; main names it on stderr instead.
	t.Setenv("SH_AGENT_REPO", checkout)
	if src, err := ResolveSource("", home, "v1.2.3"); err != nil || src.Dir != "" {
		t.Errorf("$SH_AGENT_REPO on a release binary: %+v, %v — want it ignored and the embedded files", src, err)
	}
	os.Unsetenv("SH_AGENT_REPO")
	t.Chdir(cwd)
	if err := SaveState(home, State{Repo: repo}); err != nil {
		t.Fatal(err)
	}

	// A manifest written by an embedded install records no path: its repo
	// value is never read as one, even when a directory of that name holding
	// a harness sits in the working directory.
	if err := os.CopyFS(filepath.Join(cwd, "embedded:v1.0.0"), os.DirFS(repo)); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(home, State{Repo: "embedded:v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if src, err := ResolveSource("", home, "v1.2.3"); err != nil || src.Dir != "" {
		t.Errorf("recorded embedded install: %+v, %v, want the embedded source", src, err)
	}
	if _, err := ResolveSource("", home, ""); err == nil {
		t.Error("a development build must not read a recorded embedded:<version> as a path")
	}
}

func TestBehindAndPull(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	origin := filepath.Join(base, "origin.git")
	run(base, "init", "--bare", "-q", "-b", "main", origin)

	a := filepath.Join(base, "a")
	run(base, "clone", "-q", origin, a)
	if err := os.WriteFile(filepath.Join(a, "f"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(a, "add", "f")
	run(a, "commit", "-q", "-m", "one")
	run(a, "push", "-q", "-u", "origin", "main")

	b := filepath.Join(base, "b")
	run(base, "clone", "-q", origin, b)

	if n, err := Behind(b); err != nil || n != 0 {
		t.Fatalf("fresh clone behind = %d, %v", n, err)
	}

	if err := os.WriteFile(filepath.Join(a, "f"), []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(a, "commit", "-q", "-am", "two")
	run(a, "push", "-q")

	if n, err := Behind(b); err != nil || n != 1 {
		t.Fatalf("behind = %d, %v; want 1", n, err)
	}

	if err := os.WriteFile(filepath.Join(b, "f"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Pull(b, &bytes.Buffer{}); err == nil {
		t.Fatal("pull over a dirty tree must be refused")
	}
	run(b, "checkout", "--", "f")
	if err := Pull(b, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if n, _ := Behind(b); n != 0 {
		t.Errorf("after pull behind = %d", n)
	}
}

// TestNestedCopyIsNotACheckout: a harness-shaped tree inside another git
// repository must not inherit that repository's branch, commit, or dirty
// state, and git-driven update must refuse it.
func TestNestedCopyIsNotACheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := newRepo(t)
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "fixture")

	nested := filepath.Join(root, "mirror")
	for _, rel := range []string{"context", "skills"} {
		if err := os.MkdirAll(filepath.Join(nested, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(nested, "context", "AGENTS.md"), []byte("# copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isRepo(nested) {
		t.Fatal("fixture: nested copy should look like a harness")
	}

	if got := Inspect(nested); got != (RepoInfo{}) {
		t.Errorf("nested copy Inspect = %+v, want empty", got)
	}
	if _, err := Behind(nested); !errors.Is(err, errPlainCopy) {
		t.Errorf("nested copy Behind err = %v, want %v", err, errPlainCopy)
	}
	if err := Pull(nested, &bytes.Buffer{}); !errors.Is(err, errPlainCopy) {
		t.Errorf("nested copy Pull err = %v, want %v", err, errPlainCopy)
	}

	// An inherited GIT_DIR/GIT_WORK_TREE must not redirect the check to the
	// enclosing repository: the check looks for .git itself and never asks git.
	t.Setenv("GIT_DIR", filepath.Join(root, ".git"))
	t.Setenv("GIT_WORK_TREE", nested)
	if got := Inspect(nested); got != (RepoInfo{}) {
		t.Errorf("nested copy with GIT_DIR/GIT_WORK_TREE set: Inspect = %+v, want empty", got)
	}
	if _, err := Behind(nested); !errors.Is(err, errPlainCopy) {
		t.Errorf("nested copy with GIT_DIR set: Behind err = %v, want %v", err, errPlainCopy)
	}
	t.Setenv("GIT_DIR", "")
	t.Setenv("GIT_WORK_TREE", "")
	os.Unsetenv("GIT_DIR")
	os.Unsetenv("GIT_WORK_TREE")

	got := Inspect(root)
	if got.Branch == "" || got.Commit == "" || !got.Dirty {
		t.Errorf("repo root Inspect = %+v, want branch, commit, and dirty (the untracked mirror)", got)
	}
	if _, err := Behind(root); errors.Is(err, errPlainCopy) {
		t.Errorf("repo root Behind must not be refused as a plain copy: %v", err)
	}
}

// An explicit --repo or $FYR_REPO is used or refused, never replaced by
// the embedded files, even in a release binary (D15).
func TestResolveSourceRefusesNamedNonRepo(t *testing.T) {
	home := t.TempDir()
	bogus := filepath.Join(t.TempDir(), "not-a-checkout")
	for _, v := range []string{"", "v1.2.3"} {
		t.Setenv("FYR_REPO", "")
		if _, err := ResolveSource(bogus, home, v); err == nil {
			t.Errorf("version %q: an explicit path that is not a checkout must be an error, not a fallback", v)
		}
		t.Setenv("FYR_REPO", bogus)
		if _, err := ResolveSource("", home, v); err == nil {
			t.Errorf("version %q: $FYR_REPO pointing at a non-checkout must be an error, not a fallback", v)
		}
	}
	t.Setenv("FYR_REPO", "")
	repo := newRepo(t)
	if got, err := ResolveSource(repo, home, ""); err != nil || got.Dir != repo {
		t.Errorf("explicit checkout: %+v, %v", got, err)
	}
}
