package auto

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/isolate"
)

const testHarnessSuite = "# GUIDE-eval\n\n" +
	"| id | setup | prompt | prompt2 | check | agent | grants | version | kind | cluster | expect |\n" +
	"|---|---|---|---|---|---|---|---|---|---|---|\n" +
	"| `alpha` |  | use skills/alpha/SKILL.md |  | `true` |  | named | 1 | capability | alpha |  |\n" +
	"| `agent-reviewer` |  | dispatch reviewer |  | `true` | reviewer | named | 1 | adversarial | agent-reviewer |  |\n" +
	"| `smoke` |  | say hi |  | `true` |  | named | 1 | regression | smoke |  |\n" +
	"| `untouched` |  | use skills/beta/SKILL.md |  | `true` |  | named | 1 | capability | untouched |  |\n"

const testGateHoldoutSuite = "# Holdout suite\n\n" +
	"| id | setup | prompt | prompt2 | check | agent | grants | version | kind | cluster | expect |\n" +
	"|---|---|---|---|---|---|---|---|---|---|---|\n" +
	"| `secret-holdout` |  | check secret holdout |  | `true` |  | named | 1 | adversarial | holdout |  |\n"

func initHarnessEvalGateFixture(t *testing.T, home, realHome, extraRows string) (root, holdoutPath, propID string) {
	t.Helper()
	root = idleProject(t)
	writeTree(t, root, "AGENTS.md", "# AGENTS.md\n\n## Setup commands\n\n```bash\ntrue # auto: test\n```\n")
	writeTree(t, root, "skills/alpha/SKILL.md", "---\nname: alpha\ndescription: alpha\n---\n# alpha v1\n")
	writeTree(t, root, "skills/beta/SKILL.md", "---\nname: beta\ndescription: beta\n---\n# beta v1\n")
	writeTree(t, root, "agents/reviewer/agent.md", "---\nname: reviewer\ndescription: reviewer\n---\n# reviewer v1\n")

	nowFn := func() time.Time { return runAt.Add(-9 * time.Hour) }
	if _, err := brain.Add(brain.Options{Home: home, Dir: root, Now: nowFn}, brain.AddInput{
		ID:     "GUIDE-eval",
		Kind:   "guide",
		Status: "current",
		Title:  "GUIDE-eval",
		Body:   strings.NewReader(testHarnessSuite + extraRows),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := brain.Add(brain.Options{Home: home, Dir: root, Now: nowFn}, brain.AddInput{
		ID:     "DEPLOY-local",
		Kind:   "deploy",
		Status: "current",
		Title:  "DEPLOY-local",
		Body:   strings.NewReader("# DEPLOY-local\n\n## Operator steps\n\nStanding permission: install into the real home.\n"),
	}); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "harness eval gate fixture")
	git(t, root, "push", "-q")
	backdate(t, root)

	holdoutPath = filepath.Join(home, "sandbox", "fyr-auto", "holdout.md")
	if err := os.MkdirAll(filepath.Dir(holdoutPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(holdoutPath, []byte(testGateHoldoutSuite), 0o600); err != nil {
		t.Fatal(err)
	}

	var err error
	propID, err = brain.Add(brain.Options{Home: home, Global: true, Now: nowFn}, brain.AddInput{
		Kind:    "proposal",
		Status:  "queued",
		Project: root,
		Title:   "Dream, harness — tighten skills/alpha/SKILL.md",
		Body:    strings.NewReader("Target: harness\nScope: harness\nEvidence: BUG-001, RETRO-2026-10-01\n\nUpdate `skills/alpha/SKILL.md` to add step 2.\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := save(home, Config{Enabled: true, ConversationIdle: "1h"}); err != nil {
		t.Fatal(err)
	}
	return root, holdoutPath, propID
}

func makeCandidateBranchWork(t *testing.T, root, newAlpha string) func(context.Context, isolate.WorkerOptions, ...string) ([]isolate.WorkerResult, error) {
	t.Helper()
	return func(_ context.Context, _ isolate.WorkerOptions, kickoffs ...string) ([]isolate.WorkerResult, error) {
		data, err := os.ReadFile(kickoffs[0])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "Class: harness-edit") || !strings.Contains(string(data), "skills/alpha/") {
			t.Fatalf("unexpected kickoff:\n%s", string(data))
		}
		name := strings.TrimSuffix(filepath.Base(kickoffs[0]), ".md")
		base := git(t, root, "rev-parse", "HEAD")
		tip := commitPlumbing(t, root, base, "100644", "skills/alpha/SKILL.md", newAlpha, "candidate edit to alpha")
		git(t, root, "update-ref", "refs/heads/work/"+name, tip)
		return []isolate.WorkerResult{{
			Name:    name,
			Base:    base,
			Head:    tip,
			Commits: 1,
			Verdict: "PASS",
			Handoff: &isolate.WorkerRecord{ID: "HANDOFF-2026-11-01-1200-alpha", Kind: "handoff", Title: "tighten alpha skill"},
		}}, nil
	}
}

func getGlobalProposal(t *testing.T, home, id string) brain.Record {
	t.Helper()
	recs, _, err := brain.Get(brain.Options{Home: home, Global: true}, []string{id})
	if err != nil || len(recs) != 1 {
		t.Fatalf("Get(%s): %+v, %v", id, recs, err)
	}
	return recs[0]
}

// gateOpts stubs IsolateAvailable so eval-gate tests run deterministically
// without requiring live Landlock kernels.
func gateOpts(home, realHome, root string, out *bytes.Buffer) Options {
	o := runOpts(home, out)
	o.RealHome = realHome
	o.Repo = root
	o.Env = &fakeEnv{targets: targetsOf(root), usage: usageText("90%", "90%")}
	o.IsolateAvailable = func() error { return nil }
	return o
}

func gateEvalOutcome(row string, n, runs int) EvalOutcome {
	out := EvalOutcome{Row: row, Graded: runs, RecordID: "TEST-gate"}
	out.Passed = n
	return out
}

func TestEvalGate(t *testing.T) {
	t.Run("keep-rule-applies-and-reports-install-pending", runEvalGateKeepRuleApplies)
	t.Run("target-row-score-falls-becomes-proposal-with-both-scores", runEvalGateTargetRowScoreFalls)
	t.Run("regression-or-holdout-failure-becomes-proposal", runEvalGateRegressionOrHoldoutFailure)
	t.Run("stops-at-24-eval-sessions-and-becomes-proposal", runEvalGateStopsAt24EvalSessions)
	t.Run("stops-between-rows-when-g9-fails", runEvalGateStopsBetweenRowsOnG9)
	t.Run("subprocess-checkout-commit-and-no-repo-trigger", runEvalGateSubprocessCheckoutAndNoRepo)
	t.Run("agents-denied-in-changed-targets", runEvalGateAgentsDeniedInChangedTargets)
	t.Run("distill-without-eval-row-uses-regression-and-holdout", runEvalGateDistillWithoutEvalRow)
	t.Run("candidate-verify-or-apply-failure-annotates-proposal", runEvalGateCandidateVerifyOrApplyFailure)
	t.Run("holdout-not-starved-by-regression-rows", runEvalGateHoldoutNotStarvedByRegressions)
	t.Run("runFyrEval-empty-self-fails-fast", runEvalGateEmptySelfFailsFast)
	t.Run("open-asks-decrement-and-reconcile", runEvalGateOpenAsksDecrementAndReconcile)
	t.Run("auto-seeds-missing-holdout-under-home", runEvalGateAutoSeedsMissingHoldout)
	t.Run("proposal-queue-expires-stale-and-ranks-impact", runEvalGateProposalQueueExpiresStaleAndRanks)
	t.Run("records-digest-checkpoint-with-metrics-db-and-fail-opens-without", runEvalGateRecordsDigestCheckpoint)
}

func runEvalGateKeepRuleApplies(t *testing.T) {
	home := t.TempDir()
	realHome := t.TempDir()
	root, holdoutPath, propID := initHarnessEvalGateFixture(t, home, realHome, "")
	parentSHA := git(t, root, "rev-parse", "HEAD")

	var evalCalls []EvalRequest
	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Work = makeCandidateBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n")
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		evalCalls = append(evalCalls, req)
		passed := 3
		if req.Row == "alpha" && req.Commit == parentSHA {
			passed = 2
		}
		return gateEvalOutcome(req.Row, passed, req.Runs), nil
	}

	if err := Run(o, false); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if len(evalCalls) != 6 {
		t.Fatalf("evalCalls = %d (%+v), want 6 (alpha, smoke, secret-holdout on parent and candidate)", len(evalCalls), evalCalls)
	}
	for _, c := range evalCalls {
		if c.Runs != 3 {
			t.Errorf("call %+v: Runs = %d, want 3", c, c.Runs)
		}
		if c.Row == "untouched" {
			t.Errorf("unreferenced row untouched should not run in eval gate")
		}
		if c.Row == "secret-holdout" && c.SuiteFile != holdoutPath {
			t.Errorf("holdout call SuiteFile = %q, want %q", c.SuiteFile, holdoutPath)
		}
	}
	gotAlpha, err := os.ReadFile(filepath.Join(root, "skills/alpha/SKILL.md"))
	if err != nil || !strings.Contains(string(gotAlpha), "# alpha v2") {
		t.Fatalf("skills/alpha/SKILL.md not applied in checkout: %q, %v", string(gotAlpha), err)
	}
	if branches := git(t, root, "branch", "--list", "work/*"); branches != "" {
		t.Errorf("worker branch not deleted after apply: %s", branches)
	}
	if !strings.Contains(out.String(), "install pending") {
		t.Errorf("output missing 'install pending':\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(realHome, ".gemini")); err == nil {
		t.Errorf("auto installed into realHome despite D19 item 3")
	}
	prop := getGlobalProposal(t, home, propID)
	if !strings.HasPrefix(prop.Status, "applied:") {
		t.Errorf("proposal status = %q, want applied:<sha>", prop.Status)
	}
	st, _, err := loadState(home)
	if err != nil || st.ModelRuns != 1 {
		t.Errorf("ModelRuns = %d (%v), want 1", st.ModelRuns, err)
	}
}

func runEvalGateTargetRowScoreFalls(t *testing.T) {
	home := t.TempDir()
	realHome := t.TempDir()
	root, _, propID := initHarnessEvalGateFixture(t, home, realHome, "")
	parentSHA := git(t, root, "rev-parse", "HEAD")

	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Work = makeCandidateBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha broken\n")
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		passed := 3
		if req.Row == "alpha" && req.Commit != parentSHA {
			passed = 0
		}
		return gateEvalOutcome(req.Row, passed, req.Runs), nil
	}

	if err := Run(o, false); err != nil {
		t.Fatalf("Run should exit 0 when eval gate rejects candidate: %v\n%s", err, out.String())
	}
	if head := git(t, root, "rev-parse", "HEAD"); head != parentSHA {
		t.Errorf("checkout HEAD moved from %s to %s on rejected candidate", parentSHA, head)
	}
	if branches := git(t, root, "branch", "--list", "work/*"); branches == "" {
		t.Errorf("worker branch should be kept when eval gate rejects candidate")
	}
	prop := getGlobalProposal(t, home, propID)
	if prop.Status != "queued" {
		t.Errorf("proposal status = %q, want queued", prop.Status)
	}
	if !strings.Contains(prop.Body, "## Eval gate") || !strings.Contains(prop.Body, "3/3") || !strings.Contains(prop.Body, "0/3") {
		t.Errorf("proposal body missing both scores:\n%s", prop.Body)
	}
}

func runEvalGateRegressionOrHoldoutFailure(t *testing.T) {
	for _, failRow := range []string{"smoke", "secret-holdout"} {
		t.Run(failRow, func(t *testing.T) {
			home := t.TempDir()
			realHome := t.TempDir()
			root, _, propID := initHarnessEvalGateFixture(t, home, realHome, "")
			parentSHA := git(t, root, "rev-parse", "HEAD")

			var out bytes.Buffer
			o := gateOpts(home, realHome, root, &out)
			o.Work = makeCandidateBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n")
			o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
				passed := 3
				if req.Row == failRow && req.Commit != parentSHA {
					passed = 2
				}
				return gateEvalOutcome(req.Row, passed, req.Runs), nil
			}

			if err := Run(o, false); err != nil {
				t.Fatalf("Run: %v\n%s", err, out.String())
			}
			if head := git(t, root, "rev-parse", "HEAD"); head != parentSHA {
				t.Errorf("checkout HEAD moved when %s failed", failRow)
			}
			prop := getGlobalProposal(t, home, propID)
			if prop.Status != "queued" || !strings.Contains(prop.Body, "2/3") {
				t.Errorf("proposal = %+v; want queued with 2/3 score", prop)
			}
		})
	}
}

func runEvalGateStopsAt24EvalSessions(t *testing.T) {
	home := t.TempDir()
	realHome := t.TempDir()
	root, holdoutPath, propID := initHarnessEvalGateFixture(t, home, realHome, "")
	parentSHA := git(t, root, "rev-parse", "HEAD")
	candSHA := commitPlumbing(t, root, parentSHA, "100644", "skills/alpha/SKILL.md", "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n", "cand")
	git(t, root, "update-ref", "refs/heads/work/auto-cap-test", candSHA)

	var evalCalls []EvalRequest
	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		evalCalls = append(evalCalls, req)
		return EvalOutcome{Row: req.Row, Passed: 3, Graded: 3, Errors: 0, RecordID: "TEST-gate"}, nil
	}
	r := &run{ctx: context.Background(), o: o, env: o.env(), cfg: Config{Enabled: true, ConversationIdle: "1h"}, say: func(string) {}}
	snap, err := takeCheckoutSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	overRows := []gateRow{
		{ID: "alpha"}, {ID: "alpha-2"}, {ID: "alpha-3"}, {ID: "alpha-4"},
		{ID: "secret-holdout", SuiteFile: holdoutPath, IsHoldout: true},
	}
	simCtx := newGateSIMCtx(root, Task{ID: propID, Class: ClassHarnessEdit, Outcome: OutcomeEvalGate}, parentSHA, candSHA, []string{"alpha"}, nil, "")
	r.runEvalGateRows(context.Background(), root, simCtx.task, Kickoff{Name: "auto-cap-test", Branch: "work/auto-cap-test"}, isolate.RunnerIsolate, isolate.RunnerIsolate, snap, nil, isolate.WorkerResult{Base: parentSHA, Head: candSHA, Verdict: "PASS"}, simCtx, overRows)

	sessions := 0
	for _, c := range evalCalls {
		sessions += c.Runs
	}
	if sessions != 24 || len(evalCalls) != 8 {
		t.Fatalf("sessions = %d across %d calls, want 24 across 8 calls", sessions, len(evalCalls))
	}
	if head := git(t, root, "rev-parse", "HEAD"); head != parentSHA {
		t.Errorf("checkout HEAD moved despite 24-session stop")
	}
	prop := getGlobalProposal(t, home, propID)
	if prop.Status != "queued" || !strings.Contains(prop.Body, "24 eval sessions") {
		t.Errorf("proposal = %+v; want queued noting 24 eval sessions", prop)
	}
}

func runEvalGateStopsBetweenRowsOnG9(t *testing.T) {
	home := t.TempDir()
	realHome := t.TempDir()
	root, _, propID := initHarnessEvalGateFixture(t, home, realHome, "")
	parentSHA := git(t, root, "rev-parse", "HEAD")

	usageCalls := 0
	fe := &fakeEnv{targets: targetsOf(root), usage: usageText("90%", "90%")}
	var evalCalls []EvalRequest
	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Env = &countingUsageEnv{fakeEnv: fe, calls: &usageCalls, secondText: usageText("80%", "40%")}
	o.Work = makeCandidateBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n")
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		evalCalls = append(evalCalls, req)
		return EvalOutcome{Row: req.Row, Passed: 3, Graded: 3, Errors: 0, RecordID: "TEST-gate"}, nil
	}

	if err := Run(o, false); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if len(evalCalls) != 2 {
		t.Fatalf("evalCalls = %d, want 2 (1 row on parent and candidate before G9 stops)", len(evalCalls))
	}
	if head := git(t, root, "rev-parse", "HEAD"); head != parentSHA {
		t.Errorf("checkout HEAD moved when G9 stopped between rows")
	}
	prop := getGlobalProposal(t, home, propID)
	if prop.Status != "queued" || !strings.Contains(prop.Body, "## Eval gate") {
		t.Errorf("proposal = %+v; want queued with Eval gate scores", prop)
	}
}

type countingUsageEnv struct {
	*fakeEnv
	calls      *int
	secondText string
}

func (c *countingUsageEnv) Usage(home, dir string) (string, error) {
	*c.calls++
	if *c.calls >= 2 && c.secondText != "" {
		return c.secondText, nil
	}
	return c.fakeEnv.Usage(home, dir)
}

func TestEnsureHoldoutSuiteRefusesSymlinks(t *testing.T) {
	t.Run("symlinked .agents/eval starter dir", func(t *testing.T) {
		root := t.TempDir()
		ext := t.TempDir()
		if err := os.WriteFile(filepath.Join(ext, "holdout-starter.md"), []byte("starter"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, ".agents"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(ext, filepath.Join(root, ".agents", "eval")); err != nil {
			t.Fatal(err)
		}
		outPath := filepath.Join(t.TempDir(), "eval", "holdout.md")
		_, err := ensureHoldoutSuite("", root, outPath)
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("ensureHoldoutSuite err = %v, want symlink refusal", err)
		}
	})

	t.Run("symlinked target parent outside home", func(t *testing.T) {
		root := t.TempDir()
		evalDir := filepath.Join(root, ".agents", "eval")
		if err := os.MkdirAll(evalDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(evalDir, "holdout-starter.md"), []byte("starter"), 0o600); err != nil {
			t.Fatal(err)
		}
		outer := t.TempDir()
		symParent := filepath.Join(outer, "linked-dir")
		if err := os.Symlink(t.TempDir(), symParent); err != nil {
			t.Fatal(err)
		}
		home := t.TempDir()
		_, err := ensureHoldoutSuite(home, root, filepath.Join(symParent, "sub", "holdout.md"))
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("ensureHoldoutSuite outside-home err = %v, want symlink refusal", err)
		}
	})
}

func TestV910AutoAndDreamUnblock(t *testing.T) {
	t.Run("per-pool-g9", runV910AutoPerPoolG9)
	t.Run("ephemeral-and-system-roots", runV910Ephemeral)
	t.Run("pre-dispatch-0-row-feasibility", runV910EvalPrefixAndPreDispatchFeasibility)
	t.Run("deterministic-row-planner-24-sessions", runV910DeterministicRowPlanner)
	t.Run("quota-adaptive-cooldown", runV910AdaptiveCooldown)
	t.Run("install-pending-banner", runV910InstallPendingBanner)
	t.Run("halt-reason-persistence", runV910HaltReasonPersistence)
}

func TestV910AutoPerPoolG9(t *testing.T) { runV910AutoPerPoolG9(t) }

func TestV910Ephemeral(t *testing.T) { runV910Ephemeral(t) }
