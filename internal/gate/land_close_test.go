package gate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/brain"
)

func TestLandAndCloseE2E(t *testing.T) {
	ctx := context.Background()
	repo := initTestGoRepo(t)
	home := t.TempDir()
	stampFile := filepath.Join(repo, filepath.FromSlash(StampRelPath))

	changeID, err := brain.Add(
		brain.Options{Home: home, Dir: repo, Now: fixedNow},
		brain.AddInput{Kind: "change", Title: "prior change", Body: strings.NewReader("kept intact")},
	)
	if err != nil {
		t.Fatalf("seed change: %v", err)
	}
	testID, err := brain.Add(
		brain.Options{Home: home, Dir: repo, Now: fixedNow},
		brain.AddInput{Kind: "testing", Title: "prior test", Body: strings.NewReader("kept intact")},
	)
	if err != nil {
		t.Fatalf("seed testing: %v", err)
	}

	t.Run("failed land preserves branch", func(t *testing.T) {
		wt := filepath.Join(t.TempDir(), "wt-bad")
		runGit(t, repo, "worktree", "add", "-b", "bad-branch", wt, "main")
		writeFile(t, filepath.Join(wt, "broken_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestBroken(t *testing.T) { t.Fatal(\"fail\") }\n")
		runGit(t, wt, "add", "broken_test.go")
		runGit(t, wt, "commit", "-m", "broken test")
		runGit(t, repo, "worktree", "remove", wt)

		headBefore := runGit(t, repo, "rev-parse", "HEAD")
		_, err := Land(ctx, LandOptions{Dir: repo, Branch: "bad-branch", Home: home})
		if !errors.Is(err, ErrFailed) {
			t.Fatalf("expected ErrFailed when verify fails inside worktree, got %v", err)
		}
		if runGit(t, repo, "rev-parse", "HEAD") != headBefore {
			t.Fatalf("main HEAD moved on failed land")
		}
		if err := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/bad-branch").Run(); err != nil {
			t.Fatalf("bad-branch must be preserved after failed land")
		}
		runGit(t, repo, "branch", "-D", "bad-branch")
	})

	t.Run("refuses landing branch still checked out in worktree", func(t *testing.T) {
		wt := addWorktreeBranch(t, repo, "checked-out")
		writeFile(t, filepath.Join(wt, "ok.go"), "package main\n")
		runGit(t, wt, "add", "ok.go")
		runGit(t, wt, "commit", "-m", "commit in wt")

		_, err := Land(ctx, LandOptions{Dir: repo, Branch: "checked-out", Home: home})
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("expected ErrRefused for checked-out worktree branch, got %v", err)
		}
	})

	t.Run("happy land and close preserves prior records", func(t *testing.T) {
		wt := filepath.Join(t.TempDir(), "wt-good")
		runGit(t, repo, "worktree", "add", "-b", "good-branch", wt, "main")
		writeFile(t, filepath.Join(wt, "mul.go"), "package main\n\nfunc Mul(a, b int) int { return a * b }\n")
		writeFile(t, filepath.Join(wt, "mul_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestMul(t *testing.T) {\n\tif Mul(2, 3) != 6 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
		runGit(t, wt, "add", "mul.go", "mul_test.go")
		runGit(t, wt, "commit", "-m", "add Mul")
		runGit(t, repo, "worktree", "remove", wt)

		if _, err := Verify(ctx, VerifyOptions{Dir: repo, Home: home, Now: fixedNow}); err != nil {
			t.Fatalf("Verify before Land failed: %v", err)
		}

		lRep, err := Land(ctx, LandOptions{Dir: repo, Branch: "good-branch", Home: home, Lease: []string{"mul*.go"}})
		if err != nil || !lRep.OK || !lRep.BranchDel || lRep.LandCommit == "" {
			t.Fatalf("Land happy failed: err=%v rep=%+v", err, lRep)
		}
		if err := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/good-branch").Run(); err == nil {
			t.Fatalf("good-branch should have been deleted")
		}
		if _, err := os.Lstat(stampFile); err != nil {
			t.Fatalf("main verify.stamp missing after land: %v", err)
		}

		// Stale stamp refusal on Go edit.
		writeFile(t, filepath.Join(repo, "stale.go"), "package main\n\nfunc Extra() {}\n")
		_, err = Close(ctx, CloseOptions{Dir: repo, Home: home, Title: "should fail", Body: "body", Now: fixedNow})
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("expected ErrRefused for stale stamp, got %v", err)
		}
		_ = os.Remove(filepath.Join(repo, "stale.go"))

		// Missing stamp refusal.
		_ = os.Remove(stampFile)
		_, err = Close(ctx, CloseOptions{Dir: repo, Home: home, Title: "missing", Body: "body", Now: fixedNow})
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("expected ErrRefused for missing stamp, got %v", err)
		}

		// Fresh verify then close.
		if _, err := Verify(ctx, VerifyOptions{Dir: repo, Home: home, Now: fixedNow}); err != nil {
			t.Fatalf("Verify before Close failed: %v", err)
		}
		cRep, err := Close(ctx, CloseOptions{Dir: repo, Home: home, Title: "phase-done", Body: "Summary line.\n", Now: fixedNow})
		if err != nil || !cRep.OK || !strings.HasPrefix(cRep.ID, "HANDOFF-") {
			t.Fatalf("Close failed: err=%v rep=%+v", err, cRep)
		}

		recs, missing, err := brain.Get(brain.Options{Home: home, Dir: repo}, []string{cRep.ID, changeID, testID})
		if err != nil || len(missing) != 0 || len(recs) != 3 {
			t.Fatalf("brain.Get failed: recs=%d missing=%v err=%v", len(recs), missing, err)
		}
		if !strings.Contains(recs[0].Body, "## Verification Digest") || !strings.Contains(recs[0].Body, cRep.TreeSHA256) {
			t.Fatalf("handoff missing verification digest:\n%s", recs[0].Body)
		}

		// Explicit --id handoff creation.
		const explicitID = "HANDOFF-2026-11-15-1830-explicit-id"
		idRep, err := Close(ctx, CloseOptions{Dir: repo, Home: home, ID: explicitID, Title: "explicit-id", Body: "Explicit ID body.\n", Now: fixedNow})
		if err != nil || !idRep.OK || idRep.ID != explicitID {
			t.Fatalf("Close with explicit ID failed: err=%v rep=%+v", err, idRep)
		}
		expRecs, expMissing, err := brain.Get(brain.Options{Home: home, Dir: repo}, []string{explicitID})
		if err != nil || len(expMissing) != 0 || len(expRecs) != 1 || !strings.Contains(expRecs[0].Body, "## Verification Digest") {
			t.Fatalf("brain.Get explicit handoff failed: recs=%d missing=%v err=%v", len(expRecs), expMissing, err)
		}
	})

	t.Run("pins gated commit SHA against concurrent branch update (IN105)", func(t *testing.T) {
		wt := filepath.Join(t.TempDir(), "wt-toctou")
		runGit(t, repo, "worktree", "add", "-b", "toctou-branch", wt, "main")
		writeFile(t, filepath.Join(wt, "good.go"), "package main\n\nfunc Good() int { return 42 }\n")
		writeFile(t, filepath.Join(wt, "good_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestGood(t *testing.T) {\n\tif Good() != 42 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
		runGit(t, wt, "add", "good.go", "good_test.go")
		runGit(t, wt, "commit", "-m", "good commit")
		c1 := runGit(t, wt, "rev-parse", "HEAD")

		writeFile(t, filepath.Join(wt, "broken_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestBrokenTOCTOU(t *testing.T) { t.Fatal(\"ungated\") }\n")
		runGit(t, wt, "add", "broken_test.go")
		runGit(t, wt, "commit", "-m", "ungated second commit")
		c2 := runGit(t, wt, "rev-parse", "HEAD")
		runGit(t, wt, "reset", "--hard", c1)
		runGit(t, repo, "worktree", "remove", wt)

		headBefore := runGit(t, repo, "rev-parse", "HEAD")
		lRep, err := Land(ctx, LandOptions{
			Dir:        repo,
			Branch:     "toctou-branch",
			Home:       home,
			Lease:      []string{"good*.go"},
			Judge:      "Good returns 42",
			JudgeModel: "explicit-judge",
			JudgeRunner: func(_ context.Context, _ JudgeRequest) (JudgeResult, error) {
				runGit(t, repo, "update-ref", "refs/heads/toctou-branch", c2)
				return JudgeResult{Model: "explicit-judge", Verdict: "PASS", Reason: "ok"}, nil
			},
		})
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("expected ErrRefused on concurrent branch update, got err=%v rep=%+v", err, lRep)
		}
		if lRep.Verify == nil || !lRep.Verify.OK {
			t.Fatalf("expected scratch worktree Verify to verify pinned C1, got %+v", lRep.Verify)
		}
		if runGit(t, repo, "rev-parse", "HEAD") != headBefore {
			t.Fatalf("main HEAD moved after concurrent branch update")
		}
		if got := runGit(t, repo, "rev-parse", "refs/heads/toctou-branch"); got != c2 {
			t.Fatalf("toctou-branch = %s, want preserved %s", got, c2)
		}
		runGit(t, repo, "branch", "-D", "toctou-branch")
	})
}
