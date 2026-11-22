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
	"github.com/kesokaj/fyr/internal/metrics"
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

func TestEvalGate(t *testing.T) {
	t.Run("keep-rule-applies-and-reports-install-pending", func(t *testing.T) {
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
			return EvalOutcome{Row: req.Row, Passed: passed, Graded: req.Runs, Errors: 0, RecordID: "TEST-gate"}, nil
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
	})

	t.Run("target-row-score-falls-becomes-proposal-with-both-scores", func(t *testing.T) {
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
			return EvalOutcome{Row: req.Row, Passed: passed, Graded: req.Runs, Errors: 0, RecordID: "TEST-gate"}, nil
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
	})

	t.Run("regression-or-holdout-failure-becomes-proposal", func(t *testing.T) {
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
					return EvalOutcome{Row: req.Row, Passed: passed, Graded: req.Runs, Errors: 0, RecordID: "TEST-gate"}, nil
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
	})

	t.Run("stops-at-24-eval-sessions-and-becomes-proposal", func(t *testing.T) {
		extra := "| `alpha-2` |  | use skills/alpha/SKILL.md |  | `true` |  | named | 1 | capability | alpha |  |\n" +
			"| `alpha-3` |  | use skills/alpha/SKILL.md |  | `true` |  | named | 1 | capability | alpha |  |\n" +
			"| `alpha-4` |  | use skills/alpha/SKILL.md |  | `true` |  | named | 1 | capability | alpha |  |\n" +
			"| `alpha-5` |  | use skills/alpha/SKILL.md |  | `true` |  | named | 1 | capability | alpha |  |\n"
		home := t.TempDir()
		realHome := t.TempDir()
		root, _, propID := initHarnessEvalGateFixture(t, home, realHome, extra)
		parentSHA := git(t, root, "rev-parse", "HEAD")

		var evalCalls []EvalRequest
		var out bytes.Buffer
		o := gateOpts(home, realHome, root, &out)
		o.Work = makeCandidateBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n")
		o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
			evalCalls = append(evalCalls, req)
			return EvalOutcome{Row: req.Row, Passed: 3, Graded: 3, Errors: 0, RecordID: "TEST-gate"}, nil
		}

		if err := Run(o, false); err != nil {
			t.Fatalf("Run: %v\n%s", err, out.String())
		}
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
	})

	t.Run("stops-between-rows-when-g9-fails", func(t *testing.T) {
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
	})

	t.Run("subprocess-checkout-commit-and-no-repo-trigger", func(t *testing.T) {
		home := t.TempDir()
		realHome := t.TempDir()
		root, holdoutPath, _ := initHarnessEvalGateFixture(t, home, realHome, "")
		c1 := git(t, root, "rev-parse", "HEAD")
		c2 := commitPlumbing(t, root, c1, "100644", "skills/alpha/SKILL.md", "---\nname: alpha\ndescription: alpha\n---\n# alpha c2\n", "c2")

		fakeSelf := filepath.Join(t.TempDir(), "fake-fyr")
		script := "#!/usr/bin/env bash\n" +
			"test -z \"${FYR_REPO:-}\" || exit 2\n" +
			"grep -q '# alpha c2' skills/alpha/SKILL.md || exit 3\n" +
			"echo 'eval: 3 of 3 PASS, 0 errors; record TEST-2026-11-01-1200-eval'\n"
		writeExe(t, fakeSelf, script)
		t.Setenv("FYR_REPO", "/should/be/stripped")
		out, err := runFyrEval(context.Background(), fakeSelf, EvalRequest{
			Dir:       root,
			Home:      home,
			SuiteFile: holdoutPath,
			Commit:    c2,
			Runs:      3,
			Row:       "secret-holdout",
		})
		if err != nil || out.Passed != 3 {
			t.Fatalf("runFyrEval with Commit: %+v, %v", out, err)
		}

		rNoRepo := &run{o: Options{Home: home, Now: func() time.Time { return runAt }}, cfg: Config{}}
		if rNoRepo.triggerHarnessEdit(root) != nil {
			t.Fatalf("triggerHarnessEdit fired with empty Repo")
		}
	})

	t.Run("agents-denied-in-changed-targets", func(t *testing.T) {
		home := t.TempDir()
		realHome := t.TempDir()
		root, _, _ := initHarnessEvalGateFixture(t, home, realHome, "")
		base := git(t, root, "rev-parse", "HEAD")
		tip := commitPlumbing(t, root, base, "100644", "agents/reviewer/agent.md", "---\nname: reviewer\ndescription: reviewer\n---\n# reviewer v2\n", "touch denied agent")
		if _, err := changedHarnessTargets(root, base, tip); err == nil || !strings.Contains(err.Error(), "outside skills/<s>/") {
			t.Fatalf("changedHarnessTargets accepted agents/: %v", err)
		}
	})

	t.Run("distill-without-eval-row-uses-regression-and-holdout", func(t *testing.T) {
		home := t.TempDir()
		realHome := t.TempDir()
		root, holdoutPath, _ := initHarnessEvalGateFixture(t, home, realHome, "")
		rows, err := collectGateRows(home, root, holdoutPath, []string{"auto-distilled-newskill"}, ClassDistill)
		if err != nil {
			t.Fatalf("collectGateRows(ClassDistill): %v", err)
		}
		var sawSmoke, sawHoldout bool
		for _, gr := range rows {
			if gr.ID == "smoke" && gr.IsRegression {
				sawSmoke = true
			}
			if gr.ID == "secret-holdout" && gr.IsHoldout {
				sawHoldout = true
			}
		}
		if !sawSmoke || !sawHoldout {
			t.Errorf("collectGateRows(ClassDistill) rows = %+v; want smoke regression and secret-holdout", rows)
		}
		if _, err := collectGateRows(home, root, holdoutPath, []string{"auto-distilled-newskill"}, ClassHarnessEdit); err == nil {
			t.Fatalf("collectGateRows(ClassHarnessEdit) succeeded without matching GUIDE-eval row")
		}
	})

	t.Run("candidate-verify-or-apply-failure-annotates-proposal", func(t *testing.T) {
		home := t.TempDir()
		realHome := t.TempDir()
		root, _, propID := initHarnessEvalGateFixture(t, home, realHome, "")
		writeTree(t, root, "AGENTS.md", "# AGENTS.md\n\n## Setup commands\n\n```bash\nfalse # auto: test\n```\n")
		git(t, root, "add", "AGENTS.md")
		git(t, root, "commit", "-q", "-m", "failing auto test")
		git(t, root, "push", "-q")
		backdate(t, root)

		var out bytes.Buffer
		o := gateOpts(home, realHome, root, &out)
		o.Work = makeCandidateBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n")
		o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
			return EvalOutcome{Row: req.Row, Passed: 3, Graded: req.Runs, Errors: 0, RecordID: "TEST-gate"}, nil
		}

		_ = Run(o, false)
		prop := getGlobalProposal(t, home, propID)
		if !strings.Contains(prop.Body, "## Eval gate") || !strings.Contains(prop.Body, "3/3") {
			t.Fatalf("proposal missing ## Eval gate after applyBranch failure:\n%s", prop.Body)
		}
		rHarn := &run{o: o, cfg: Config{}}
		if got := rHarn.triggerHarnessEdit(root); got != nil {
			t.Fatalf("triggerHarnessEdit re-selected failed proposal: %+v", got)
		}

		home2 := t.TempDir()
		realHome2 := t.TempDir()
		root2, _, propID2 := initHarnessEvalGateFixture(t, home2, realHome2, "")
		var out2 bytes.Buffer
		o2 := gateOpts(home2, realHome2, root2, &out2)
		o2.Work = func(_ context.Context, _ isolate.WorkerOptions, kickoffs ...string) ([]isolate.WorkerResult, error) {
			name := strings.TrimSuffix(filepath.Base(kickoffs[0]), ".md")
			base := git(t, root2, "rev-parse", "HEAD")
			tip := commitPlumbing(t, root2, base, "100644", "skills/fyr-auto/SKILL.md", "---\nname: fyr-auto\ndescription: auto\n---\n# denied\n", "touch denied skill")
			git(t, root2, "update-ref", "refs/heads/work/"+name, tip)
			return []isolate.WorkerResult{{Name: name, Base: base, Head: tip, Commits: 1, Verdict: "PASS"}}, nil
		}
		if err := Run(o2, false); err != nil {
			t.Fatalf("Run on rubric-denied candidate: %v\n%s", err, out2.String())
		}
		prop2 := getGlobalProposal(t, home2, propID2)
		if !strings.Contains(prop2.Body, "## Eval gate") {
			t.Fatalf("proposal missing ## Eval gate after verifyBranchForApply failure:\n%s", prop2.Body)
		}
	})

	t.Run("holdout-not-starved-by-regression-rows", func(t *testing.T) {
		extra := "| `reg-2` |  | check reg 2 |  | `true` |  | named | 1 | regression | reg |  |\n" +
			"| `reg-3` |  | check reg 3 |  | `true` |  | named | 1 | regression | reg |  |\n" +
			"| `reg-4` |  | check reg 4 |  | `true` |  | named | 1 | regression | reg |  |\n"
		home := t.TempDir()
		realHome := t.TempDir()
		root, holdoutPath, _ := initHarnessEvalGateFixture(t, home, realHome, extra)
		rows, err := collectGateRows(home, root, holdoutPath, []string{"alpha"}, ClassHarnessEdit)
		if err != nil {
			t.Fatalf("collectGateRows: %v", err)
		}
		if len(rows) != 6 {
			t.Fatalf("len(rows) = %d, want 6: %+v", len(rows), rows)
		}
		if rows[0].ID != "alpha" || rows[1].ID != "secret-holdout" || !rows[1].IsHoldout {
			t.Errorf("expected target + holdout before regressions, got rows = %+v", rows)
		}
	})

	t.Run("runFyrEval-empty-self-fails-fast", func(t *testing.T) {
		if _, err := runFyrEval(context.Background(), "", EvalRequest{Dir: t.TempDir(), Home: t.TempDir(), Row: "smoke"}); err == nil {
			t.Fatalf("expected error on empty self executable")
		}
	})

	t.Run("open-asks-decrement-and-reconcile", func(t *testing.T) {
		home := t.TempDir()
		realHome := t.TempDir()
		root, _, propID := initHarnessEvalGateFixture(t, home, realHome, "")
		autoPropID, err := brain.Add(brain.Options{Home: home, Global: true, Now: func() time.Time { return runAt.Add(-8 * time.Hour) }}, brain.AddInput{
			Kind:    "proposal",
			Status:  "queued",
			Project: root,
			Title:   "Auto, harness — tighten skills/alpha/SKILL.md",
			Body:    strings.NewReader("Target: harness\nEvidence: BUG-001\n\nUpdate `skills/alpha/SKILL.md`.\n"),
		})
		if err != nil {
			t.Fatal(err)
		}
		closedStatus := "rejected"
		if err := brain.Edit(brain.Options{Home: home, Global: true}, brain.EditInput{ID: propID, Status: &closedStatus}); err != nil {
			t.Fatal(err)
		}
		if err := ensureDir(home); err != nil {
			t.Fatal(err)
		}
		if err := saveState(home, State{Date: "2026-11-01", OpenAsks: 1}); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		o := gateOpts(home, realHome, root, &out)
		o.Work = makeCandidateBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n")
		o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
			return EvalOutcome{Row: req.Row, Passed: 3, Graded: req.Runs, Errors: 0, RecordID: "TEST-gate"}, nil
		}
		if err := Run(o, false); err != nil {
			t.Fatalf("Run: %v\n%s", err, out.String())
		}
		st, _, err := loadState(home)
		if err != nil {
			t.Fatal(err)
		}
		if st.OpenAsks != 0 {
			t.Errorf("OpenAsks = %d after applying proposal, want 0", st.OpenAsks)
		}
		// Externally opened then closed auto ask reconciles in Status.
		askID, err := brain.Add(brain.Options{Home: home, Global: true}, brain.AddInput{
			Kind: "proposal", Status: "queued", Project: root, Title: "auto ask: external test", Body: strings.NewReader("test"),
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = autoPropID
		st.OpenAsks = 1
		if err := saveState(home, st); err != nil {
			t.Fatal(err)
		}
		closed := "rejected"
		if err := brain.Edit(brain.Options{Home: home, Global: true}, brain.EditInput{ID: askID, Status: &closed}); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		if err := Status(o); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "asks       0 open\n") {
			t.Errorf("Status did not reconcile externally closed ask:\n%s", out.String())
		}
	})

	t.Run("auto-seeds-missing-holdout-under-home", func(t *testing.T) {
		home := t.TempDir()
		realHome := t.TempDir()
		root, holdoutPath, _ := initHarnessEvalGateFixture(t, home, realHome, "")
		if err := os.Remove(holdoutPath); err != nil {
			t.Fatal(err)
		}
		writeTree(t, root, ".agents/eval/holdout-starter.md", testGateHoldoutSuite)
		git(t, root, "add", ".agents/eval/holdout-starter.md")
		git(t, root, "commit", "-q", "-m", "add holdout starter")
		git(t, root, "push", "-q")
		backdate(t, root)

		var out bytes.Buffer
		o := gateOpts(home, realHome, root, &out)
		o.Work = makeCandidateBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n")
		o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
			return EvalOutcome{Row: req.Row, Passed: 3, Graded: req.Runs, RecordID: "TEST-gate"}, nil
		}
		if err := Run(o, false); err != nil {
			t.Fatalf("Run: %v\n%s", err, out.String())
		}
		info, err := os.Stat(holdoutPath)
		if err != nil {
			t.Fatalf("missing seeded holdout under o.Home: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("seeded holdout perm = %o, want 0600", info.Mode().Perm())
		}
		if _, err := os.Stat(filepath.Join(realHome, "sandbox", "fyr-auto", "holdout.md")); !os.IsNotExist(err) {
			t.Errorf("holdout leaked into realHome: err=%v", err)
		}
		reps := reports(t, home)
		if len(reps) == 0 || !strings.Contains(reps[0].Body, "eval-gate/holdout-seeded") {
			t.Errorf("expected eval-gate/holdout-seeded finding in report, got %+v", reps)
		}
	})

	t.Run("proposal-queue-expires-stale-and-ranks-impact", func(t *testing.T) {
		home := t.TempDir()
		realHome := t.TempDir()
		root, _, defaultID := initHarnessEvalGateFixture(t, home, realHome, "")
		closed := "rejected"
		if err := brain.Edit(brain.Options{Home: home, Global: true}, brain.EditInput{ID: defaultID, Status: &closed}); err != nil {
			t.Fatal(err)
		}
		staleID, err := brain.Add(brain.Options{Home: home, Global: true, Now: func() time.Time { return runAt.Add(-time.Hour) }}, brain.AddInput{
			Kind: "proposal", Status: "queued", Project: root,
			Title: "Dream, harness — deleted skill cleanup",
			Body:  strings.NewReader("Target: harness\nEvidence: BUG-010\n\nUpdate `skills/deleted-skill/SKILL.md`."),
		})
		if err != nil {
			t.Fatal(err)
		}
		lowID, err := brain.Add(brain.Options{Home: home, Global: true, Now: func() time.Time { return runAt.Add(-2 * time.Hour) }}, brain.AddInput{
			Kind: "proposal", Status: "queued", Project: root,
			Title: "Dream, harness — low impact alpha tweak",
			Body:  strings.NewReader("Target: harness\nEvidence: BUG-011\n\nUpdate `skills/alpha/SKILL.md`."),
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = lowID
		highID, err := brain.Add(brain.Options{Home: home, Global: true, Now: func() time.Time { return runAt.Add(-6 * time.Hour) }}, brain.AddInput{
			Kind: "proposal", Status: "queued", Project: root,
			Title: "Dream, harness — high impact alpha fix",
			Body:  strings.NewReader("Target: harness\nSessions: 5\nEvidence: BUG-012, RETRO-2026-10-01\n\nUpdate `skills/alpha/SKILL.md`."),
		})
		if err != nil {
			t.Fatal(err)
		}
		r := &run{o: Options{Home: home, Repo: root, Now: func() time.Time { return runAt }}}
		picked := r.triggerHarnessEdit(root)
		if picked == nil || picked.ID != highID {
			t.Fatalf("picked = %+v, want high-impact proposal %s", picked, highID)
		}
		staleRec := getGlobalProposal(t, home, staleID)
		if staleRec.Status != "rejected" || !strings.Contains(staleRec.Body, "## Auto-rejected") {
			t.Fatalf("stale proposal = %+v, want auto-rejected status=rejected", staleRec)
		}
	})

	t.Run("records-digest-checkpoint-with-metrics-db-and-fail-opens-without", func(t *testing.T) {
		home := t.TempDir()
		realHome := t.TempDir()
		root, _, defaultID := initHarnessEvalGateFixture(t, home, realHome, "")
		rejected := "rejected"
		if err := brain.Edit(brain.Options{Home: home, Global: true}, brain.EditInput{ID: defaultID, Status: &rejected}); err != nil {
			t.Fatal(err)
		}
		if _, err := brain.Add(brain.Options{Home: home, Global: true, Now: func() time.Time { return runAt.Add(-2 * time.Hour) }}, brain.AddInput{
			Kind: "proposal", Status: "queued", Project: root,
			Title: "Dream, harness — fix knee via skills/alpha/SKILL.md",
			Body:  strings.NewReader("Target: harness\nSignal: sig:proj-deadbeef:context_knee_rate\nReportDate: 2026-10-31\nEvidence: BUG-020\n\nUpdate `skills/alpha/SKILL.md`."),
		}); err != nil {
			t.Fatal(err)
		}
		seedAutoKneeMetrics(t, home, root, runAt)
		if err := ensureDir(home); err != nil {
			t.Fatal(err)
		}
		if err := saveState(home, State{
			Date:     runAt.Format("2006-01-02"),
			Projects: map[string]Project{root: {LastClass: ClassRetroLite}},
		}); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer
		o := gateOpts(home, realHome, root, &out)
		o.Work = makeCandidateBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n")
		o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
			return EvalOutcome{Row: req.Row, Passed: 3, Graded: req.Runs, Errors: 0, RecordID: "TEST-gate"}, nil
		}
		if err := Run(o, false); err != nil {
			t.Fatalf("Run: %v\n%s", err, out.String())
		}
		appliedSHA := shortSHA(git(t, root, "rev-parse", "HEAD"))
		s, err := metrics.Open(metrics.Path(home))
		if err != nil {
			t.Fatal(err)
		}
		dig, derr := metrics.LatestDailyDigest(s)
		s.Close()
		if derr != nil || dig == nil || len(dig.Actions) == 0 {
			t.Fatalf("LatestDailyDigest = %+v, err=%v", dig, derr)
		}
		var found *metrics.DigestAppliedAction
		for i := range dig.Actions {
			if dig.Actions[i].SignalID == "sig:proj-deadbeef:context_knee_rate" {
				found = &dig.Actions[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("missing recorded DigestAppliedAction for signal: %+v", dig.Actions)
		}
		if found.Status != metrics.StatusPendingData || found.AppliedSHA != appliedSHA || !found.InstallPending || found.RevertCmd != "git revert "+appliedSHA || found.ReportDate != "2026-10-31" {
			t.Errorf("recorded DigestAppliedAction = %+v, want status=%s sha=%s install_pending=true revert='git revert %s' date=2026-10-31",
				*found, metrics.StatusPendingData, appliedSHA, appliedSHA)
		}
		latestFile, rerr := metrics.ReadLatestDailyDigest(home)
		if rerr != nil || latestFile == nil || len(latestFile.Actions) == 0 {
			t.Errorf("ReadLatestDailyDigest = %+v, err=%v", latestFile, rerr)
		}

		// Fail-open when metrics.db is absent (already verified in keep-rule-applies-and-reports-install-pending;
		// verify recordAppliedDigestCheckpoint directly does not create metrics.db).
		noDBHome := t.TempDir()
		rNoDB := &run{o: Options{Home: noDBHome, Now: func() time.Time { return runAt }}, appliedSHA: "abc1234"}
		rNoDB.recordAppliedDigestCheckpoint(root, Task{Class: ClassHarnessEdit, SignalID: "sig:global:x", ReportDate: "2026-10-31"}, "")
		if _, err := os.Stat(metrics.Path(noDBHome)); !os.IsNotExist(err) {
			t.Errorf("recordAppliedDigestCheckpoint created metrics.db when absent: err=%v", err)
		}
	})
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
