package harness

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The trailer check (TR8, plan 2026-10-25-harness-audit-fix D4, D13): the
// handoff skill's Step 6 and the preflight skill's Step 6.4 run
// skills/fyr-handoff/scripts/check-trailers.sh before the push, and it must refuse
// an unpushed commit whose message carries a CLI trailer (BUG-177) while
// passing a clean range. The fixture is a real clone with an upstream, because
// the script reads `git log @{u}..HEAD`, and a commit already pushed is not
// its business.
func TestCheckTrailersScript(t *testing.T) {
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "skills", "fyr-handoff", "scripts", "check-trailers.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("the trailer check is missing: %v", err)
	}
	env := append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run := func(dir string) (stdout, stderr string, code int) {
		t.Helper()
		cmd := exec.Command("bash", script)
		cmd.Dir = dir
		cmd.Env = env
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return out.String(), errb.String(), code
	}
	// clone returns a working clone of a fresh bare repository whose one
	// commit is pushed, so @{u}..HEAD starts empty.
	clone := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		bare, work := filepath.Join(root, "origin.git"), filepath.Join(root, "work")
		git(root, "init", "-q", "--bare", "-b", "main", bare)
		git(root, "clone", "-q", bare, work)
		git(work, "checkout", "-q", "-b", "main")
		git(work, "commit", "-q", "--allow-empty", "-m", "init")
		git(work, "push", "-q", "-u", "origin", "main")
		return work
	}

	t.Run("nothing unpushed passes", func(t *testing.T) {
		work := clone(t)
		if out, errs, code := run(work); code != 0 {
			t.Fatalf("want exit 0, got %d\nstdout %q\nstderr %q", code, out, errs)
		}
	})
	t.Run("clean unpushed commit passes", func(t *testing.T) {
		work := clone(t)
		git(work, "commit", "-q", "--allow-empty", "-m", "Fix the parser\n\nA body that mentions a co-author in prose: Co-Authored-By is the trailer, not this.")
		if out, errs, code := run(work); code != 0 {
			t.Fatalf("want exit 0, got %d\nstdout %q\nstderr %q", code, out, errs)
		}
	})
	for _, trailer := range []string{
		"Claude-Session: https://claude.ai/code/session_x",
		"Co-Authored-By: Someone <s@example.invalid>",
		"co-authored-by: lower <l@example.invalid>",
	} {
		t.Run("refuses "+strings.SplitN(trailer, ":", 2)[0], func(t *testing.T) {
			work := clone(t)
			git(work, "commit", "-q", "--allow-empty", "-m", "Clean first")
			git(work, "commit", "-q", "--allow-empty", "-m", "Add a thing\n\n"+trailer)
			out, errs, code := run(work)
			if code != 1 {
				t.Fatalf("want exit 1 on %q, got %d\nstdout %q\nstderr %q", trailer, code, out, errs)
			}
			if !strings.Contains(errs, "Add a thing") || strings.Contains(errs, "Clean first") {
				t.Fatalf("stderr must name the offending commit and only it, got %q", errs)
			}
		})
	}
	t.Run("a pushed trailer commit is not this push's business", func(t *testing.T) {
		work := clone(t)
		git(work, "commit", "-q", "--allow-empty", "-m", "Old\n\nCo-Authored-By: x <x@example.invalid>")
		git(work, "push", "-q")
		if out, errs, code := run(work); code != 0 {
			t.Fatalf("want exit 0, got %d\nstdout %q\nstderr %q", code, out, errs)
		}
	})
	t.Run("no upstream reads every commit no remote has", func(t *testing.T) {
		work := clone(t)
		git(work, "checkout", "-q", "-b", "topic")
		git(work, "commit", "-q", "--allow-empty", "-m", "Topic\n\nClaude-Session: s")
		_, errs, code := run(work)
		if code != 1 || !strings.Contains(errs, "Topic") {
			t.Fatalf("want exit 1 naming Topic on a branch with no upstream, got %d, stderr %q", code, errs)
		}
	})
	t.Run("outside a repository refuses", func(t *testing.T) {
		_, _, code := run(t.TempDir())
		if code == 0 {
			t.Fatal("want a non-zero exit outside a git repository: a check that errors must not pass")
		}
	})
}
