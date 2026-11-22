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
	"github.com/kesokaj/fyr/internal/search"
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

func TestLandJudgeRunsAfterVerifyWithFreshStamp(t *testing.T) {
	ctx := context.Background()

	t.Run("PASS sees fresh verify.stamp matching landCommit and fast-forwards main", func(t *testing.T) {
		repo := initTestGoRepo(t)
		home := t.TempDir()
		mainHead := runGit(t, repo, "rev-parse", "HEAD")
		if _, err := WriteStamp(repo, Stamp{
			TreeSHA256: "stale-main-digest",
			UpdatedAt:  "2026-11-19T00:00:00Z",
			Branch:     "main",
			Head:       mainHead,
			OK:         true,
		}); err != nil {
			t.Fatalf("WriteStamp stale main: %v", err)
		}

		wt := filepath.Join(t.TempDir(), "wt-pass")
		runGit(t, repo, "worktree", "add", "-b", "branch-fresh-pass", wt, "main")
		writeFile(t, filepath.Join(wt, "prod.go"), "package main\n\nfunc Prod(a, b int) int { return a * b }\n")
		writeFile(t, filepath.Join(wt, "prod_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestProd(t *testing.T) {\n\tif Prod(2, 3) != 6 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
		runGit(t, wt, "add", "prod.go", "prod_test.go")
		runGit(t, wt, "commit", "-m", "add Prod")
		branchHead := runGit(t, wt, "rev-parse", "HEAD")
		runGit(t, repo, "worktree", "remove", wt)

		var gotReq JudgeRequest
		var wtStamp Stamp
		var recorded []JudgeVerdictRecord
		lRep, err := Land(ctx, LandOptions{
			Dir:        repo,
			Branch:     "branch-fresh-pass",
			Home:       home,
			Lease:      []string{"prod*.go"},
			Judge:      "Prod returns a * b",
			JudgeModel: "explicit-judge",
			JudgeRunner: func(_ context.Context, req JudgeRequest) (JudgeResult, error) {
				gotReq = req
				var rErr error
				wtStamp, rErr = ReadStamp(req.RepoDir)
				if rErr != nil {
					return JudgeResult{}, rErr
				}
				return JudgeResult{Model: req.ExplicitModel, Verdict: "PASS", Reason: "fresh stamp matched"}, nil
			},
			RecordVerdict: func(r JudgeVerdictRecord) error {
				recorded = append(recorded, r)
				return nil
			},
		})
		if err != nil || !lRep.OK || lRep.LandCommit == "" {
			t.Fatalf("Land PASS failed: err=%v rep=%+v", err, lRep)
		}
		if gotReq.Head == "" || gotReq.Head == mainHead || gotReq.Head == branchHead || gotReq.Head != lRep.LandCommit {
			t.Fatalf("judge Head = %q (main=%q branch=%q land=%q)", gotReq.Head, mainHead, branchHead, lRep.LandCommit)
		}
		if gotReq.MergeBase != mainHead || gotReq.RepoDir == repo {
			t.Fatalf("expected scratch worktree RepoDir and MergeBase=%q, got RepoDir=%q MergeBase=%q", mainHead, gotReq.RepoDir, gotReq.MergeBase)
		}
		if !wtStamp.OK || wtStamp.Head != lRep.LandCommit {
			t.Fatalf("scratch worktree verify.stamp = %+v, want OK=true Head=%q", wtStamp, lRep.LandCommit)
		}
		if !strings.HasPrefix(gotReq.StampSummary, "ok=true ") || !strings.Contains(gotReq.StampSummary, "head="+lRep.LandCommit) || strings.Contains(gotReq.StampSummary, "missing") {
			t.Fatalf("StampSummary did not reflect fresh landCommit stamp: %q", gotReq.StampSummary)
		}
		if lRep.Verify == nil || !lRep.Verify.OK || lRep.Gate == nil || !lRep.Gate.OK || lRep.Gate.JudgeVerdict != "PASS" {
			t.Fatalf("unexpected report fields: %+v", lRep)
		}
		if runGit(t, repo, "rev-parse", "HEAD") != lRep.LandCommit {
			t.Fatalf("main HEAD did not fast-forward to %s", lRep.LandCommit)
		}
		if len(recorded) != 1 || recorded[0].Verdict != "PASS" {
			t.Fatalf("unexpected recorded verdicts: %+v", recorded)
		}
	})

	t.Run("FAIL after verify aborts land without moving main", func(t *testing.T) {
		repo := initTestGoRepo(t)
		home := t.TempDir()
		headBefore := runGit(t, repo, "rev-parse", "HEAD")

		wt := filepath.Join(t.TempDir(), "wt-fail")
		runGit(t, repo, "worktree", "add", "-b", "branch-fresh-fail", wt, "main")
		writeFile(t, filepath.Join(wt, "div.go"), "package main\n\nfunc Div(a, b int) int { return a / b }\n")
		writeFile(t, filepath.Join(wt, "div_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestDiv(t *testing.T) {\n\tif Div(6, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
		runGit(t, wt, "add", "div.go", "div_test.go")
		runGit(t, wt, "commit", "-m", "add Div")
		runGit(t, repo, "worktree", "remove", wt)

		var gotReq JudgeRequest
		var recorded []JudgeVerdictRecord
		lRep, err := Land(ctx, LandOptions{
			Dir:        repo,
			Branch:     "branch-fresh-fail",
			Home:       home,
			Lease:      []string{"div*.go"},
			Judge:      "Div guards zero divisor",
			JudgeModel: "explicit-judge",
			JudgeRunner: func(_ context.Context, req JudgeRequest) (JudgeResult, error) {
				gotReq = req
				return JudgeResult{Model: req.ExplicitModel, Verdict: "FAIL", Reason: "zero divisor unchecked"}, nil
			},
			RecordVerdict: func(r JudgeVerdictRecord) error {
				recorded = append(recorded, r)
				return nil
			},
		})
		if !errors.Is(err, ErrFailed) || lRep.OK || lRep.LandCommit != "" {
			t.Fatalf("expected ErrFailed without LandCommit, got err=%v rep=%+v", err, lRep)
		}
		if lRep.Verify == nil || !lRep.Verify.OK {
			t.Fatalf("expected Verify to succeed before judge ran, got %+v", lRep.Verify)
		}
		if !strings.HasPrefix(gotReq.StampSummary, "ok=true ") || !strings.Contains(gotReq.StampSummary, "head="+gotReq.Head) {
			t.Fatalf("judge did not receive fresh landCommit stamp: %q", gotReq.StampSummary)
		}
		if lRep.Gate == nil || lRep.Gate.OK || lRep.Gate.JudgeVerdict != "FAIL" || !hasFinding(*lRep.Gate, "judge", "zero divisor unchecked") {
			t.Fatalf("expected Gate failure with judge finding, got %+v", lRep.Gate)
		}
		if runGit(t, repo, "rev-parse", "HEAD") != headBefore {
			t.Fatalf("main HEAD moved on judge FAIL")
		}
		if err := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/branch-fresh-fail").Run(); err != nil {
			t.Fatalf("branch-fresh-fail should be preserved on judge FAIL")
		}
		if len(recorded) != 1 || recorded[0].Verdict != "FAIL" {
			t.Fatalf("unexpected recorded verdicts: %+v", recorded)
		}
	})
}

func writeFakeLandJudgeAgy(t *testing.T, binDir, wantTreeSHA, mainHead, branchHead, diffNeedle, verdictJSON string) {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	modelsText := "gemini-4-carbon-high\tGemini 4 Carbon (High)\n" +
		"claude-opus-5-5-high\tClaude Opus 5.5 (High)\n"
	healthyUsage := "Gemini Models\tWeekly Limit Remaining\t80%\tx\n" +
		"Gemini Models\tFive Hour Limit Remaining\t90%\tx\n" +
		"Claude and GPT models\tWeekly Limit Remaining\t50%\tx\n" +
		"Claude and GPT models\tFive Hour Limit Remaining\t70%\tx\n"
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"models\" ]; then\n" +
		"  printf '%s' " + shSingleQuote(modelsText) + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"-p\" ] && [ \"$2\" = \"/usage\" ]; then\n" +
		"  printf '%s' " + shSingleQuote(healthyUsage) + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"-p\" ]; then\n" +
		"  case \"$2\" in *\"BEGIN DATA verify-stamp\"*) ;; *) echo 'missing verify-stamp block' >&2; exit 42 ;; esac\n" +
		"  case \"$2\" in *\"ok=true treeSha256=" + wantTreeSHA + " \"*) ;; *) echo 'prompt missing fresh scratch verify.stamp digest' >&2; exit 43 ;; esac\n" +
		"  case \"$2\" in *\"missing for head\"*) echo 'prompt carried missing-for-head note' >&2; exit 44 ;; esac\n" +
		"  case \"$2\" in *\"stale-main-preland-digest\"*) echo 'prompt carried stale main stamp' >&2; exit 45 ;; esac\n" +
		"  case \"$2\" in *\"head=" + mainHead + "\"*) echo 'prompt carried main pre-land head' >&2; exit 46 ;; esac\n" +
		"  case \"$2\" in *\"head=" + branchHead + "\"*) echo 'prompt carried un-squashed feature branch head' >&2; exit 47 ;; esac\n" +
		"  case \"$2\" in *" + shSingleQuote(diffNeedle) + "*) ;; *) echo 'prompt missing branch diff' >&2; exit 48 ;; esac\n" +
		"  printf '%s\\n' " + shSingleQuote("{\"conversation_id\":\"c-bug560\",\"status\":\"SUCCESS\",\"structured_output\":"+verdictJSON+"}") + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestLandJudgeRunsAfterScratchVerifyStamp is the BUG-560 regression test
// exercising the real fake-agy isolated judge path: Land must run
// verifyInScratchWorktree before applyGateJudge so the judge prompt carries a
// fresh ok=true verify.stamp matching the scratch-merged tree (never "missing
// for head" or main's stale pre-land stamp), and FAIL must abort cleanly.
func TestLandJudgeRunsAfterScratchVerifyStamp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	cases := []struct {
		name, branch, file, src, testFile, testSrc, lease, judge, diffNeedle, verdictJSON string
		expectOK                                                                          bool
	}{
		{
			name: "PASS fake agy receives fresh scratch verify.stamp and lands cleanly", branch: "feat-bug560-ok",
			file: "sub.go", src: "package main\n\nfunc Sub(a, b int) int { return a - b }\n",
			testFile: "sub_test.go", testSrc: "package main\n\nimport \"testing\"\n\nfunc TestSub(t *testing.T) {\n\tif Sub(5, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n",
			lease: "sub*.go", judge: "Sub returns a - b", diffNeedle: "Sub(a, b int)",
			verdictJSON: `{"verdict":"PASS","reason":"fresh scratch verify.stamp verified"}`, expectOK: true,
		},
		{
			name: "FAIL fake agy aborts land, preserves branch, and leaves main untouched", branch: "feat-bug560-fail",
			file: "mod.go", src: "package main\n\nfunc Mod(a, b int) int { return a % b }\n",
			testFile: "mod_test.go", testSrc: "package main\n\nimport \"testing\"\n\nfunc TestMod(t *testing.T) {\n\tif Mod(7, 3) != 1 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n",
			lease: "mod*.go", judge: "Mod guards zero divisor", diffNeedle: "Mod(a, b int)",
			verdictJSON: `{"verdict":"FAIL","reason":"VERDICT: FAIL — zero divisor not guarded"}`, expectOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, home := initTestGoRepo(t), t.TempDir()
			mainHead := runGit(t, repo, "rev-parse", "HEAD")
			if _, err := WriteStamp(repo, Stamp{
				TreeSHA256: "stale-main-preland-digest", UpdatedAt: "2026-11-19T00:00:00Z",
				Branch: "main", Head: mainHead, OK: true,
			}); err != nil {
				t.Fatalf("WriteStamp stale main: %v", err)
			}
			wt := filepath.Join(t.TempDir(), "wt-"+tc.branch)
			runGit(t, repo, "worktree", "add", "-b", tc.branch, wt, "main")
			writeFile(t, filepath.Join(wt, tc.file), tc.src)
			writeFile(t, filepath.Join(wt, tc.testFile), tc.testSrc)
			runGit(t, wt, "add", tc.file, tc.testFile)
			runGit(t, wt, "commit", "-m", "add "+tc.branch)
			branchHead := runGit(t, wt, "rev-parse", "HEAD")
			treeState, err := search.ComputeGoTreeState(wt)
			if err != nil {
				t.Fatalf("ComputeGoTreeState: %v", err)
			}
			runGit(t, repo, "worktree", "remove", wt)

			binDir := filepath.Join(t.TempDir(), "bin")
			writeFakeLandJudgeAgy(t, binDir, treeState.TreeSHA256, mainHead, branchHead, tc.diffNeedle, tc.verdictJSON)
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			var recorded []JudgeVerdictRecord
			lRep, err := Land(ctx, LandOptions{
				Dir: repo, Branch: tc.branch, Home: home, Lease: []string{tc.lease}, Judge: tc.judge,
				RecordVerdict: func(r JudgeVerdictRecord) error { recorded = append(recorded, r); return nil },
			})
			if tc.expectOK {
				if err != nil || !lRep.OK || !lRep.BranchDel || lRep.LandCommit == "" {
					t.Fatalf("Land with fake agy PASS failed: err=%v rep=%+v", err, lRep)
				}
				if lRep.Verify == nil || !lRep.Verify.OK || lRep.Verify.TreeSHA256 != treeState.TreeSHA256 {
					t.Fatalf("unexpected Verify report: %+v (want digest %s)", lRep.Verify, treeState.TreeSHA256)
				}
				if lRep.Gate == nil || !lRep.Gate.OK || lRep.Gate.JudgeVerdict != "PASS" || lRep.Gate.JudgeModel != "claude-opus-5-5-high" {
					t.Fatalf("unexpected Gate report: %+v", lRep.Gate)
				}
				if runGit(t, repo, "rev-parse", "HEAD") != lRep.LandCommit {
					t.Fatalf("main HEAD did not fast-forward to %s", lRep.LandCommit)
				}
				if len(recorded) != 1 || recorded[0].Verdict != "PASS" {
					t.Fatalf("unexpected recorded verdicts: %+v", recorded)
				}
				return
			}
			if !errors.Is(err, ErrFailed) || lRep.OK || lRep.BranchDel || lRep.LandCommit != "" {
				t.Fatalf("expected ErrFailed without moving main, got err=%v rep=%+v", err, lRep)
			}
			if lRep.Verify == nil || !lRep.Verify.OK {
				t.Fatalf("expected scratch Verify to succeed before judge ran, got %+v", lRep.Verify)
			}
			if lRep.Gate == nil || lRep.Gate.OK || lRep.Gate.JudgeVerdict != "FAIL" || !hasFinding(*lRep.Gate, "judge", "VERDICT: FAIL") {
				t.Fatalf("expected Gate FAIL with judge finding, got %+v", lRep.Gate)
			}
			if runGit(t, repo, "rev-parse", "HEAD") != mainHead {
				t.Fatalf("main HEAD moved on judge FAIL")
			}
			if err := exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+tc.branch).Run(); err != nil {
				t.Fatalf("%s must be preserved on judge FAIL", tc.branch)
			}
			if len(recorded) != 1 || recorded[0].Verdict != "FAIL" {
				t.Fatalf("unexpected recorded verdicts: %+v", recorded)
			}
		})
	}
}
