package work

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReflogMoved is the guard's every answer over a real checkout: none
// when nothing moved, the count and the first new entry, more entries than
// it reads, a reflog emptied during the run, and one it cannot read.
func TestReflogMoved(t *testing.T) {
	dir := filepath.Join(outsideTemp(t), "lead")
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=L", "-c", "user.email=l@localhost"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "init", "-q", "-b", "main", dir).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	git("commit", "-q", "--allow-empty", "-m", "base")
	start, err := reflogStart(dir)
	if err != nil || len(start) != 2 || start[0].ref != "HEAD" || start[1].ref != "refs/heads/main" || !strings.HasSuffix(start[0].entry, " commit (initial): base") {
		t.Fatalf("start %q err %v", start, err)
	}
	if why := reflogMoved(dir, start); why != "" {
		t.Fatalf("nothing moved, got %q", why)
	}
	git("commit", "-q", "--allow-empty", "-m", "first\x01move")
	git("commit", "-q", "--allow-empty", "-m", "second")
	if why := reflogMoved(dir, start); !strings.Contains(why, "HEAD reflog moved during the run: 2 new entries, the first ") || !strings.HasSuffix(why, `"commit: first\x01move"`) {
		t.Fatalf("got %q", why)
	}
	for i := 0; i < reflogWindow; i++ {
		git("commit", "-q", "--allow-empty", "-m", "more")
	}
	if why := reflogMoved(dir, start); !strings.Contains(why, "over 49 new entries, the newest ") {
		t.Fatalf("got %q", why)
	}
	git("reflog", "expire", "--expire=all", "--all")
	if why := reflogMoved(dir, start); !strings.Contains(why, "lost every entry") {
		t.Fatalf("got %q", why)
	}
	if why := reflogMoved(filepath.Join(dir, "missing"), start); !strings.Contains(why, "could not be read") {
		t.Fatalf("got %q", why)
	}
}

// TestReflogBranchMovedFromWorktree: another worktree that moves the lead's
// branch away and back writes only the branch's reflog, so HEAD's alone
// would pass (the P4 security review, finding 3); a detached HEAD guards
// HEAD alone.
func TestReflogBranchMovedFromWorktree(t *testing.T) {
	tmp := outsideTemp(t)
	dir, wt := filepath.Join(tmp, "lead"), filepath.Join(tmp, "wt")
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-c", "user.name=L", "-c", "user.email=l@localhost"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main", dir)
	git("-C", dir, "commit", "-q", "--allow-empty", "-m", "base")
	git("-C", dir, "commit", "-q", "--allow-empty", "-m", "two")
	git("-C", dir, "worktree", "add", "-q", "--detach", wt)
	start, err := reflogStart(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := git("-C", dir, "rev-parse", "HEAD")
	git("-C", wt, "update-ref", "refs/heads/main", "HEAD~1")
	git("-C", wt, "update-ref", "refs/heads/main", base)
	if why := reflogMoved(dir, start); !strings.Contains(why, "refs/heads/main reflog moved during the run: 2 new entries") {
		t.Fatalf("got %q", why)
	}
	// HEAD's reflog alone is not enough: the branch's must hold an entry.
	if err := os.Remove(filepath.Join(dir, ".git", "logs", "refs", "heads", "main")); err != nil {
		t.Fatal(err)
	}
	if _, err := reflogStart(dir); err == nil || !strings.Contains(err.Error(), "keeps no refs/heads/main reflog") {
		t.Fatalf("err %v", err)
	}
	git("-C", dir, "checkout", "-q", "--detach")
	if start, err = reflogStart(dir); err != nil || len(start) != 1 || start[0].ref != "HEAD" {
		t.Fatalf("detached: %q %v", start, err)
	}
}
