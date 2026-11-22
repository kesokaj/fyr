package relay

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/gate"
	"github.com/kesokaj/fyr/internal/models"
)

func initRelayRepo(t *testing.T) (repo, home string) {
	t.Helper()
	repo, home = t.TempDir(), t.TempDir()
	runGitT(t, repo, "init", "-b", "main")
	runGitT(t, repo, "config", "user.name", "Relay Test")
	runGitT(t, repo, "config", "user.email", "relay@example.com")
	writeFileT(t, filepath.Join(repo, ".gitignore"), ".agents/scratch/\n")
	writeFileT(t, filepath.Join(repo, "PRD.md"), "# PRD\n- R1: Keep invariants.\n")
	writeFileT(t, filepath.Join(repo, "go.mod"), "module example.com/relaytest\n\ngo 1.24\n")
	writeFileT(t, filepath.Join(repo, "main.go"), "package main\n\nfunc main() {}\n")
	runGitT(t, repo, "add", ".")
	runGitT(t, repo, "commit", "-m", "init")
	return repo, home
}

func runGitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, withoutGitRepoVars(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFileT(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func addPlanT(t *testing.T, home, repo, id, body string) string {
	t.Helper()
	got, err := brain.Add(brain.Options{Home: home, Dir: repo}, brain.AddInput{
		Kind: "plan", ID: id, Title: "Relay Test Plan", Body: strings.NewReader(body),
	})
	if err != nil {
		t.Fatalf("brain.Add plan: %v", err)
	}
	return got
}

func listHandoffsT(t *testing.T, home, repo string) []brain.Record {
	t.Helper()
	recs, err := brain.List(brain.Options{Home: home, Dir: repo}, brain.ListFilter{Kind: "handoff"})
	if err != nil {
		t.Fatalf("brain.List handoffs: %v", err)
	}
	return recs
}

const twoPhasePlanBody = "# Plan — Two Phases\n\n#### Phase 1 — Add alpha\n`Files:` `alpha.go`\n\n- [ ] Create alpha.go\n- **Verify:** `go test ./...`\n\n#### Phase 2 — Add beta\n`Files:` `beta.go`\n\n- [ ] Create beta.go\n- **Verify:** `go test ./...`\n"

func passVerify(_ context.Context, _ gate.VerifyOptions) (gate.VerifyReport, error) {
	return gate.VerifyReport{OK: true, TreeSHA256: "abc1234567890fedcba"}, nil
}

func passJudge(_ context.Context, _ gate.GateOptions) (gate.GateReport, error) {
	return gate.GateReport{OK: true, JudgeModel: "claude-opus-test", JudgeVerdict: "PASS"}, nil
}

func commitFileRunner(t *testing.T, rel, content, msg string) SessionRunner {
	t.Helper()
	return func(_ context.Context, req SessionRequest) (SessionResult, error) {
		writeFileT(t, filepath.Join(req.RepoDir, rel), content)
		runGitT(t, req.RepoDir, "add", rel)
		runGitT(t, req.RepoDir, "commit", "-m", msg)
		return SessionResult{}, nil
	}
}

func TestRelayMultiPhasePassChainAndComplete(t *testing.T) {
	repo, home := initRelayRepo(t)
	planID := addPlanT(t, home, repo, "PLAN-2026-11-22-chain", twoPhasePlanBody)
	var seen []string
	runner := func(_ context.Context, req SessionRequest) (SessionResult, error) {
		seen = append(seen, req.PhaseID)
		file := map[string]string{"Phase 1": "alpha.go", "Phase 2": "beta.go"}[req.PhaseID]
		writeFileT(t, filepath.Join(req.RepoDir, file), "package main\nfunc "+strings.TrimSuffix(file, ".go")+"() {}\n")
		runGitT(t, req.RepoDir, "add", file)
		runGitT(t, req.RepoDir, "commit", "-m", "add "+file)
		return SessionResult{}, nil
	}
	rep, err := Run(context.Background(), Options{
		Dir: repo, Home: home, PlanID: planID, MaxPhases: 2,
		JudgeModel: "claude-opus-test", SessionRunner: runner, VerifyFn: passVerify, JudgeFn: passJudge,
	})
	if err != nil || !rep.OK || rep.Status != StatusOK || !rep.PlanComplete || len(rep.Phases) != 2 || len(seen) != 2 {
		t.Fatalf("multi-phase chain: rep=%+v seen=%v err=%v", rep, seen, err)
	}
	if h := listHandoffsT(t, home, repo); len(h) != 2 || !strings.Contains(h[0].Body, "Relay-Outcome: PASS") || !strings.Contains(h[1].Body, "Plan: "+planID) {
		t.Fatalf("want 2 PASS handoffs, got %d: %+v", len(h), h)
	}
	calls := 0
	repDone, err := Run(context.Background(), Options{
		Dir: repo, Home: home, PlanID: planID,
		SessionRunner: func(context.Context, SessionRequest) (SessionResult, error) { calls++; return SessionResult{}, nil },
	})
	if err != nil || !repDone.OK || repDone.Status != StatusComplete || calls != 0 {
		t.Fatalf("complete plan rerun: rep=%+v calls=%d err=%v", repDone, calls, err)
	}
}

func TestRelayHaltScenariosTable(t *testing.T) {
	okAlpha := func(t *testing.T) SessionRunner {
		return commitFileRunner(t, "alpha.go", "package main\nfunc alpha() {}\n", "p1")
	}
	tests := []struct {
		name       string
		setup      func(t *testing.T, repo, home string)
		runner     func(t *testing.T) SessionRunner
		verify     VerifyFunc
		judge      GateFunc
		wantReason string
		wantCalls  int
	}{
		{
			name: "verify fail halts before phase 2", runner: okAlpha, judge: passJudge, wantReason: HaltFail, wantCalls: 1,
			verify: func(context.Context, gate.VerifyOptions) (gate.VerifyReport, error) {
				return gate.VerifyReport{Steps: []gate.StepResult{{Name: "go-test", Detail: "TestAlpha failed"}}}, errors.New("verify failed")
			},
		},
		{
			name: "judge fail halts before phase 2", runner: okAlpha, verify: passVerify, wantReason: HaltFail, wantCalls: 1,
			judge: func(context.Context, gate.GateOptions) (gate.GateReport, error) {
				return gate.GateReport{JudgeModel: "claude-opus-test", JudgeVerdict: "FAIL", Findings: []gate.GateFinding{{Check: "judge", Detail: "missing edge case"}}}, gate.ErrFailed
			},
		},
		{
			name: "zero progress exits without commit", verify: passVerify, judge: passJudge, wantReason: HaltZeroProgress, wantCalls: 1,
			runner: func(*testing.T) SessionRunner {
				return func(context.Context, SessionRequest) (SessionResult, error) { return SessionResult{}, nil }
			},
		},
		{
			name: "dirty tree after commit", verify: passVerify, judge: passJudge, wantReason: HaltDirtyTree, wantCalls: 1,
			runner: func(t *testing.T) SessionRunner {
				return func(_ context.Context, req SessionRequest) (SessionResult, error) {
					_, _ = okAlpha(t)(context.Background(), req)
					writeFileT(t, filepath.Join(req.RepoDir, "stray.txt"), "dirty\n")
					return SessionResult{}, nil
				}
			},
		},
		{
			name: "PRD.md modification immediate halt", verify: passVerify, judge: passJudge, wantReason: HaltPRDModified, wantCalls: 1,
			runner: func(t *testing.T) SessionRunner { return commitFileRunner(t, "PRD.md", "# PRD\n- tampered\n", "prd") },
		},
		{
			name: "out-of-lease edit triggers gate halt", verify: passVerify, judge: passJudge, wantReason: HaltLeaseOrGateFail, wantCalls: 1,
			runner: func(t *testing.T) SessionRunner {
				return commitFileRunner(t, "other.go", "package main\nfunc other() {}\n", "ool")
			},
		},
		{
			name: "new git tag triggers TAG_CREATED halt", verify: passVerify, judge: passJudge, wantReason: HaltTagCreated, wantCalls: 1,
			runner: func(t *testing.T) SessionRunner {
				return func(_ context.Context, req SessionRequest) (SessionResult, error) {
					_, _ = okAlpha(t)(context.Background(), req)
					runGitT(t, req.RepoDir, "tag", "v99.0.0")
					return SessionResult{}, nil
				}
			},
		},
		{
			name: "budget floor breach stops with zero agy calls", runner: okAlpha, verify: passVerify, judge: passJudge, wantReason: HaltBudgetFloor, wantCalls: 0,
			setup: func(t *testing.T, _, home string) {
				if err := models.SaveCatalog(home, models.Catalog{UpdatedAt: "2026-11-22T00:00:00Z", Pools: []models.Pool{{Name: "Gemini 3 Pro", Five: 10, Week: 50}}}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "session non-zero exit halts", verify: passVerify, judge: passJudge, wantReason: HaltSessionError, wantCalls: 1,
			runner: func(*testing.T) SessionRunner {
				return func(context.Context, SessionRequest) (SessionResult, error) {
					return SessionResult{ExitCode: 1, Stderr: "upstream failure"}, errors.New("exit status 1")
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo, home := initRelayRepo(t)
			if tc.setup != nil {
				tc.setup(t, repo, home)
			}
			planID := addPlanT(t, home, repo, "PLAN-2026-11-22-halt", twoPhasePlanBody)
			calls, orig := 0, tc.runner(t)
			rep, err := Run(context.Background(), Options{
				Dir: repo, Home: home, PlanID: planID, MaxPhases: 2, VerifyFn: tc.verify, JudgeFn: tc.judge,
				SessionRunner: func(ctx context.Context, req SessionRequest) (SessionResult, error) { calls++; return orig(ctx, req) },
			})
			if !errors.Is(err, ErrHalted) || rep.OK || rep.Status != StatusHalted || rep.HaltReason != tc.wantReason || calls != tc.wantCalls {
				t.Fatalf("got status=%q reason=%q calls=%d err=%v; want HALTED/%s calls=%d", rep.Status, rep.HaltReason, calls, err, tc.wantReason, tc.wantCalls)
			}
			if h := listHandoffsT(t, home, repo); len(h) != 1 || !strings.Contains(h[0].Body, "Relay-Halt-Reason: "+tc.wantReason) {
				t.Fatalf("expected 1 diagnostic handoff for %s, got %d", tc.wantReason, len(h))
			}
		})
	}
}

func TestRelayRepeatingErrorHashAndDryRunRefusals(t *testing.T) {
	repo, home := initRelayRepo(t)
	planID := addPlanT(t, home, repo, "PLAN-2026-11-22-rep", twoPhasePlanBody)
	calls := 0
	dry, err := Run(context.Background(), Options{
		Dir: repo, Home: home, PlanID: planID, MaxPhases: 2, DryRun: true,
		SessionRunner: func(context.Context, SessionRequest) (SessionResult, error) { calls++; return SessionResult{}, nil },
	})
	if err != nil || !dry.OK || dry.Status != StatusDryRun || calls != 0 || len(dry.PlannedPhases) != 2 || !strings.Contains(dry.DryRunKickoff, "NEVER modify PRD.md") || len(listHandoffsT(t, home, repo)) != 0 {
		t.Fatalf("dry-run failed: rep=%+v calls=%d err=%v", dry, calls, err)
	}
	attempt := 0
	runner := func(_ context.Context, req SessionRequest) (SessionResult, error) {
		attempt++
		writeFileT(t, filepath.Join(req.RepoDir, "alpha.go"), "package main\nfunc alpha() int { return "+string(rune('0'+attempt))+" }\n")
		runGitT(t, req.RepoDir, "add", "alpha.go")
		runGitT(t, req.RepoDir, "commit", "-m", "attempt")
		return SessionResult{}, nil
	}
	failV := func(detail string) VerifyFunc {
		return func(context.Context, gate.VerifyOptions) (gate.VerifyReport, error) {
			return gate.VerifyReport{Steps: []gate.StepResult{{Name: "go-test", Detail: detail}}}, errors.New("failed")
		}
	}
	if r1, e1 := Run(context.Background(), Options{Dir: repo, Home: home, PlanID: planID, SessionRunner: runner, VerifyFn: failV("alpha_test.go:12: want 9 got 1 (15ms)"), JudgeFn: passJudge}); !errors.Is(e1, ErrHalted) || r1.HaltReason != HaltFail {
		t.Fatalf("run 1: rep=%+v err=%v", r1, e1)
	}
	if r2, e2 := Run(context.Background(), Options{Dir: repo, Home: home, PlanID: planID, SessionRunner: runner, VerifyFn: failV("alpha_test.go:44: want 9 got 1 (98ms)"), JudgeFn: passJudge}); !errors.Is(e2, ErrHalted) || r2.HaltReason != HaltRepeatingErrorHash {
		t.Fatalf("run 2: rep=%+v err=%v; want REPEATING_ERROR_HASH", r2, e2)
	}
	if _, err := Run(context.Background(), Options{Dir: repo, Home: home, PlanID: "PLAN-MISSING"}); !errors.Is(err, ErrRefused) {
		t.Fatalf("missing plan: want ErrRefused, got %v", err)
	}
	writeFileT(t, filepath.Join(repo, "uncommitted.txt"), "dirty\n")
	if _, err := Run(context.Background(), Options{Dir: repo, Home: home, PlanID: planID}); !errors.Is(err, ErrRefused) {
		t.Fatalf("dirty tree: want ErrRefused, got %v", err)
	}
}
