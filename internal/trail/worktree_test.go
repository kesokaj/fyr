package trail

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func initWorktreeTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := Command(dir, args...)
		cmd.Env = append(cmd.Env,
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-q", "-m", "initial")
	return dir
}

func commitInWorktreeTestRepo(t *testing.T, dir, name, body, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", name}, {"commit", "-q", "-m", msg}} {
		cmd := Command(dir, args...)
		cmd.Env = append(cmd.Env,
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestCheckoutSnapshotStatusAndUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := initWorktreeTestRepo(t)
	opts := SnapshotOptions{IncludeStatus: true}

	snap, err := CaptureCheckoutSnapshot(ctx, repo, opts)
	if err != nil {
		t.Fatalf("CaptureCheckoutSnapshot: %v", err)
	}
	if snap.Head == "" || snap.Branch != "main" || snap.StatusHash == "" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if err := CheckoutUnchanged(ctx, repo, snap, opts); err != nil {
		t.Fatalf("CheckoutUnchanged clean = %v, want nil", err)
	}

	// Must-fail: untracked file changes status hash.
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckoutUnchanged(ctx, repo, snap, opts); !errors.Is(err, ErrCheckoutChanged) {
		t.Fatalf("CheckoutUnchanged dirty = %v, want ErrCheckoutChanged", err)
	}
	if err := os.Remove(filepath.Join(repo, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
	if err := CheckoutUnchanged(ctx, repo, snap, opts); err != nil {
		t.Fatalf("CheckoutUnchanged restored = %v, want nil", err)
	}

	// Must-fail: new commit moves HEAD.
	commitInWorktreeTestRepo(t, repo, "a.txt", "a\n", "second")
	if err := CheckoutUnchanged(ctx, repo, snap, opts); !errors.Is(err, ErrCheckoutChanged) {
		t.Fatalf("CheckoutUnchanged after commit = %v, want ErrCheckoutChanged", err)
	}
}

func TestCheckoutSnapshotReflogAndDetachedGuards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := initWorktreeTestRepo(t)
	opts := SnapshotOptions{IncludeReflog: true, FullBranchRef: true, HardenConfig: true}

	snap, err := CaptureCheckoutSnapshot(ctx, repo, opts)
	if err != nil {
		t.Fatalf("CaptureCheckoutSnapshot reflog: %v", err)
	}
	if snap.Branch != "refs/heads/main" || snap.HeadLog == "" || snap.BranchLog == "" {
		t.Fatalf("unexpected reflog snapshot: %+v", snap)
	}
	if err := CheckoutUnchanged(ctx, repo, snap, opts); err != nil {
		t.Fatalf("CheckoutUnchanged reflog = %v, want nil", err)
	}

	// Must-fail: detached HEAD rejected when IncludeReflog is set, allowed when false.
	if out, err := Command(repo, "checkout", "-q", "--detach", "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("detach HEAD: %v\n%s", err, out)
	}
	if _, err := CaptureCheckoutSnapshot(ctx, repo, opts); err == nil {
		t.Fatal("CaptureCheckoutSnapshot on detached HEAD with IncludeReflog=true succeeded, want error")
	}
	detachedSnap, err := CaptureCheckoutSnapshot(ctx, repo, SnapshotOptions{IncludeStatus: true})
	if err != nil {
		t.Fatalf("CaptureCheckoutSnapshot detached without reflog: %v", err)
	}
	if detachedSnap.Branch != "" || detachedSnap.Head == "" {
		t.Fatalf("unexpected detached snapshot: %+v", detachedSnap)
	}

	// Must-fail: non-git directory fails closed.
	if _, err := CaptureCheckoutSnapshot(ctx, t.TempDir(), opts); err == nil {
		t.Fatal("CaptureCheckoutSnapshot on non-git dir succeeded, want error")
	}
}

func TestRemoveWorktreeAndGuards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := initWorktreeTestRepo(t)
	wtDir := filepath.Join(t.TempDir(), "linked-wt")

	if out, err := Command(repo, "worktree", "add", "--detach", "--", wtDir, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}
	if err := RemoveWorktree(ctx, repo, wtDir, true); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wtDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree dir still exists after RemoveWorktree: err=%v", err)
	}

	// Must-fail: removing mainRoot itself or empty path is refused without touching mainRoot.
	if err := RemoveWorktree(ctx, repo, repo, false); err == nil {
		t.Fatal("RemoveWorktree(repo, repo) succeeded, want refusal")
	}
	if err := RemoveWorktree(ctx, repo, "", false); err == nil {
		t.Fatal("RemoveWorktree(repo, empty) succeeded, want refusal")
	}
	if _, err := os.Stat(filepath.Join(repo, "README.md")); err != nil {
		t.Fatalf("mainRoot was damaged after refused RemoveWorktree: %v", err)
	}
}
