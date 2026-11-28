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

func runEvalGateSubprocessCheckoutAndNoRepo(t *testing.T) {
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
}

func runEvalGateAgentsDeniedInChangedTargets(t *testing.T) {
	home := t.TempDir()
	realHome := t.TempDir()
	root, _, _ := initHarnessEvalGateFixture(t, home, realHome, "")
	base := git(t, root, "rev-parse", "HEAD")
	tip := commitPlumbing(t, root, base, "100644", "agents/reviewer/agent.md", "---\nname: reviewer\ndescription: reviewer\n---\n# reviewer v2\n", "touch denied agent")
	if _, err := changedHarnessTargets(root, base, tip); err == nil || !strings.Contains(err.Error(), "outside skills/<s>/") {
		t.Fatalf("changedHarnessTargets accepted agents/: %v", err)
	}
}

func runEvalGateDistillWithoutEvalRow(t *testing.T) {
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
}

func runEvalGateCandidateVerifyOrApplyFailure(t *testing.T) {
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
}

func runEvalGateHoldoutNotStarvedByRegressions(t *testing.T) {
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
	if len(rows) != maxGateRows {
		t.Fatalf("len(rows) = %d, want %d: %+v", len(rows), maxGateRows, rows)
	}
	if rows[0].ID != "alpha" || rows[1].ID != "secret-holdout" || !rows[1].IsHoldout {
		t.Errorf("expected target + holdout before regressions, got rows = %+v", rows)
	}
}

func runEvalGateEmptySelfFailsFast(t *testing.T) {
	if _, err := runFyrEval(context.Background(), "", EvalRequest{Dir: t.TempDir(), Home: t.TempDir(), Row: "smoke"}); err == nil {
		t.Fatalf("expected error on empty self executable")
	}
}

func runEvalGateOpenAsksDecrementAndReconcile(t *testing.T) {
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
}

func runEvalGateAutoSeedsMissingHoldout(t *testing.T) {
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
}

func runEvalGateProposalQueueExpiresStaleAndRanks(t *testing.T) {
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
}

func runEvalGateRecordsDigestCheckpoint(t *testing.T) {
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

	noDBHome := t.TempDir()
	rNoDB := &run{o: Options{Home: noDBHome, Now: func() time.Time { return runAt }}, appliedSHA: "abc1234"}
	rNoDB.recordAppliedDigestCheckpoint(root, Task{Class: ClassHarnessEdit, SignalID: "sig:global:x", ReportDate: "2026-10-31"}, "")
	if _, err := os.Stat(metrics.Path(noDBHome)); !os.IsNotExist(err) {
		t.Errorf("recordAppliedDigestCheckpoint created metrics.db when absent: err=%v", err)
	}
}
