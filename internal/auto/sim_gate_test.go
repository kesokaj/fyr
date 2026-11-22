package auto

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/metrics"
)

func TestSIMAutoEvalGateEdgeCases(t *testing.T) {
	t.Run("dominant-model-7d-fallback-and-operator-problem-id", testSIMEvalGateDominantModelAndOperatorID)
	t.Run("sim-record-failure-fails-open-preserving-applied-sha", testSIMEvalGateRecordFailOpen)
	t.Run("predicted-rows-respect-session-cap", testSIMEvalGatePredictedRowsCap)
	t.Run("session-cap-stop-verdicts", testSIMEvalGateSessionCapVerdicts)
}

func seedDominantModelMetrics(t *testing.T, home, root, model string, now time.Time) {
	t.Helper()
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ts := now.Add(-48 * time.Hour).UnixMilli()
	if _, err := db.Exec(`INSERT INTO sessions(id, vendor, project_root, model, started_at, ended_at) VALUES ('dom-1', 'agy', ?, ?, ?, ?)`, root, model, ts, ts+500); err != nil {
		t.Fatal(err)
	}
}

func testSIMEvalGateDominantModelAndOperatorID(t *testing.T) {
	home, realHome := t.TempDir(), t.TempDir()
	root, _, _ := initHarnessEvalGateFixture(t, home, realHome, "")
	seedDominantModelMetrics(t, home, root, "gemini-fallback-model", runAt)
	if err := ensureDir(home); err != nil {
		t.Fatal(err)
	}
	if err := saveState(home, State{Date: runAt.Format("2006-01-02"), Projects: map[string]Project{root: {LastClass: ClassRetroLite}}}); err != nil {
		t.Fatal(err)
	}
	opRes, err := brain.RecordSIMAttempt(home, brain.SIMRecordInput{
		RootCause: "operator authored root cause", What: "operator baseline",
		SourceKind: "retro_harness", EvalVerdict: "none", FieldStatus: "n/a",
		Provenance: "operator", Now: runAt.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"kind":"sim_manifest","problem_id":"` + opRes.ProblemID + `","root_cause":"worker rephrased cause","rationale":"tighten alpha wording"}`
	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Work = makeManifestBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n", manifest)
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		return EvalOutcome{Row: req.Row, Passed: 3, Graded: req.Runs}, nil
	}
	if err := Run(o, false); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	det, err := brain.GetSIMProblem(home, opRes.ProblemID)
	if err != nil || len(det.Attempts) != 2 {
		t.Fatalf("GetSIMProblem = %+v (err=%v), want 2 attempts", det, err)
	}
	if det.Problem.RootCause != "operator authored root cause" || det.Problem.Provenance != "operator" {
		t.Errorf("operator problem mutated: %+v", det.Problem)
	}
	if det.Attempts[0].PolicyModel != "gemini-fallback-model" || det.Attempts[0].EvalVerdict != "kept" {
		t.Errorf("latest attempt = %+v, want fallback PolicyModel + kept", det.Attempts[0])
	}
}

func testSIMEvalGateRecordFailOpen(t *testing.T) {
	home, realHome := t.TempDir(), t.TempDir()
	root, _, _ := initHarnessEvalGateFixture(t, home, realHome, "")
	parentSHA := git(t, root, "rev-parse", "HEAD")
	manifest := `{"kind":"sim_manifest","problem_id":"SIM-P999","rationale":"points at nonexistent problem"}`
	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Work = makeManifestBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n", manifest)
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		return EvalOutcome{Row: req.Row, Passed: 3, Graded: req.Runs, PolicyModel: "gemini-4-neon-xhigh"}, nil
	}
	if err := Run(o, false); err != nil {
		t.Fatalf("Run should fail open on SIM record error: %v\n%s", err, out.String())
	}
	if head := git(t, root, "rev-parse", "HEAD"); head == parentSHA {
		t.Errorf("applied commit was lost when SIM record failed")
	}
	reps := reports(t, home)
	if len(reps) == 0 || !strings.Contains(reps[0].Body, "sim/record-failed") {
		t.Errorf("report missing sim/record-failed finding: %+v", reps)
	}
}

func testSIMEvalGatePredictedRowsCap(t *testing.T) {
	extra := "| `extra-2` |  | use skills/beta/SKILL.md |  | `true` |  | named | 1 | capability | extra |  |\n"
	home, realHome := t.TempDir(), t.TempDir()
	root, holdoutPath, _ := initHarnessEvalGateFixture(t, home, realHome, extra)
	base, err := collectGateRows(home, root, holdoutPath, []string{"alpha"}, ClassHarnessEdit)
	if err != nil || len(base) != 3 {
		t.Fatalf("collectGateRows = %+v (err=%v), want 3", base, err)
	}
	m := &simManifest{PredictedRows: []string{"alpha", "untouched", "extra-2", "nonexistent"}}
	got := appendPredictedGateRows(home, root, base, m)
	if len(got) != 4 || got[3].ID != "untouched" {
		t.Errorf("appendPredictedGateRows = %+v, want 4 rows capped at maxEvalGateSessions with untouched appended", got)
	}
}

func testSIMEvalGateSessionCapVerdicts(t *testing.T) {
	home, realHome := t.TempDir(), t.TempDir()
	root, holdoutPath, propID := initHarnessEvalGateFixture(t, home, realHome, "")
	parentSHA := git(t, root, "rev-parse", "HEAD")
	candSHA := commitPlumbing(t, root, parentSHA, "100644", "skills/alpha/SKILL.md", "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n", "cand")
	git(t, root, "update-ref", "refs/heads/work/auto-sim-cap", candSHA)
	manifest := &simManifest{RootCause: "cap hit with scores", MechanismClass: "prompt_guidance", Rationale: "hit 24 session cap", PredictedRows: []string{"alpha", "alpha-4"}}

	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		return EvalOutcome{Row: req.Row, Passed: 3, Graded: req.Runs}, nil
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
	task := Task{ID: propID, Class: ClassHarnessEdit, Outcome: OutcomeEvalGate}
	simCtx := newGateSIMCtx(root, task, parentSHA, candSHA, []string{"alpha"}, manifest, "")
	r.runEvalGateRows(context.Background(), root, task, Kickoff{Name: "auto-sim-cap", Branch: "work/auto-sim-cap"}, isolate.RunnerIsolate, isolate.RunnerIsolate, snap, nil, isolate.WorkerResult{Base: parentSHA, Head: candSHA, Verdict: "PASS"}, simCtx, overRows)

	att := mustSingleSIMDetail(t, home).Attempts[0]
	if att.EvalVerdict != "not_kept" || att.FieldStatus != "n/a" || att.EvalTotal != 4 {
		t.Errorf("session-cap with scores attempt = %+v, want not_kept with 4 evaluated rows", att)
	}
}
