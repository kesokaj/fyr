package auto

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/metrics"
)

func TestSIMAutoManifest(t *testing.T) {
	t.Run("valid-and-kickoff-instructions", func(t *testing.T) {
		assertSIMManifestValidCases(t)
		assertSIMKickoffInstructions(t)
	})
	t.Run("rejects-invalid-manifest-cases", assertSIMManifestRejectCases)
	t.Run("duplicate-keeps-first-and-flags-second", func(t *testing.T) {
		home, root := t.TempDir(), initGitRepo(t)
		r := &run{o: Options{Home: home, Now: func() time.Time { return runAt }}}
		data := `{"kind":"sim_manifest","problem_id":"SIM-P005","rationale":"first valid rationale","mechanism_class":"structural"}` + "\n" +
			`{"kind":"sim_manifest","problem_id":"SIM-P006","rationale":"duplicate rationale"}` + "\n"
		fs, m := r.processDraftLines(root, ClassHarnessEdit, []byte(data))
		if m == nil || m.ProblemID != "SIM-P005" || m.MechanismClass != "structural" {
			t.Fatalf("manifest = %+v, want SIM-P005/structural", m)
		}
		if len(fs) != 1 || fs[0].Check != "draft" || fs[0].Rule != "invalid" {
			t.Fatalf("findings = %+v, want 1 invalid draft finding for duplicate", fs)
		}
	})
}

func assertSIMManifestValidCases(t *testing.T) {
	t.Helper()
	r := &run{o: Options{Home: t.TempDir(), Now: func() time.Time { return runAt }}}
	root := initGitRepo(t)

	line1 := `{"kind":"sim_manifest","problem_id":"SIM-P1005","root_cause":"premature bypass","mechanism_class":"threshold","rationale":"tighten read fallback","predicted_rows":"alpha, smoke","predicted_signals":"sig:global:bash_read_bypass"}` + "\n"
	fs, m := r.processDraftLines(root, ClassHarnessEdit, []byte(line1))
	if len(fs) != 0 || m == nil {
		t.Fatalf("valid harness-edit manifest: fs=%+v m=%+v", fs, m)
	}
	if m.ProblemID != "SIM-P1005" || m.RootCause != "premature bypass" || m.MechanismClass != "threshold" || m.Rationale != "tighten read fallback" {
		t.Errorf("parsed manifest = %+v", m)
	}
	if !slices.Equal(m.PredictedRows, []string{"alpha", "smoke"}) || !slices.Equal(m.PredictedSignals, []string{"sig:global:bash_read_bypass"}) {
		t.Errorf("parsed predictions = rows:%v sigs:%v", m.PredictedRows, m.PredictedSignals)
	}
	line2 := `{"kind":"sim_manifest","root_cause":"repeated verb sequence","rationale":"distill common build steps"}` + "\n"
	fs2, m2 := r.processDraftLines(root, ClassDistill, []byte(line2))
	if len(fs2) != 0 || m2 == nil || m2.MechanismClass != "prompt_guidance" || m2.RootCause != "repeated verb sequence" {
		t.Fatalf("valid distill default mechanism: fs=%+v m=%+v", fs2, m2)
	}
}

func assertSIMKickoffInstructions(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		class, id string
		files     []string
	}{
		{ClassHarnessEdit, "PROPOSAL-2026-11-22-01", []string{"skills/alpha/"}},
		{ClassDistill, "", []string{".agents/skills/auto-distilled-x/"}},
	} {
		task, err := PrepareTask(tc.class, tc.files, tc.id, 3)
		if err != nil {
			t.Fatalf("PrepareTask(%s): %v", tc.class, err)
		}
		k, err := BuildKickoff(runAt, task)
		if err != nil {
			t.Fatalf("BuildKickoff(%s): %v", tc.class, err)
		}
		for _, want := range []string{"sim_manifest", "problem_id", "root_cause", "mechanism_class", "rationale", "predicted_rows", "predicted_signals"} {
			if !strings.Contains(k.Text, want) {
				t.Errorf("Kickoff(%s) missing %q:\n%s", tc.class, want, k.Text)
			}
		}
	}
}

type manifestRejectCase struct{ name, class, line string }

func manifestRejectCases(rawSecret, escSecret string) []manifestRejectCase {
	manyRows := strings.Repeat("r1,", 16) + "r17"
	manySigs := strings.Repeat("sig:global:a,", 8) + "sig:global:b"
	return []manifestRejectCase{
		{"oversize-line", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":"` + strings.Repeat("a", 4100) + `"}`},
		{"oversize-rationale", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":"` + strings.Repeat("a", 401) + `"}`},
		{"oversize-root-cause", ClassHarnessEdit, `{"kind":"sim_manifest","root_cause":"` + strings.Repeat("r", 241) + `","rationale":"ok"}`},
		{"missing-rationale", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001"}`},
		{"missing-id-and-cause", ClassHarnessEdit, `{"kind":"sim_manifest","rationale":"only rationale"}`},
		{"bad-problem-id", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P12","rationale":"ok"}`},
		{"bad-mechanism", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","mechanism_class":"magic","rationale":"ok"}`},
		{"bad-predicted-row", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":"ok","predicted_rows":"Bad_Row"}`},
		{"too-many-rows", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":"ok","predicted_rows":"` + manyRows + `"}`},
		{"bad-predicted-sig", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":"ok","predicted_signals":"not-a-sig"}`},
		{"too-many-sigs", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":"ok","predicted_signals":"` + manySigs + `"}`},
		{"secret-raw", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":"tok ` + rawSecret + `"}`},
		{"secret-escaped", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":"tok ` + escSecret + `"}`},
		{"ctrl-newline", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":"line1\nline2"}`},
		{"ctrl-tab", ClassHarnessEdit, `{"kind":"sim_manifest","root_cause":"bad\ttab","rationale":"ok"}`},
		{"wrong-class", ClassRetroLite, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":"ok"}`},
		{"unknown-key", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":"ok","extra":"nope"}`},
		{"non-manifest-key", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":"ok","body":"nope"}`},
		{"non-manifest-empty-key", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":"ok","body":""}`},
		{"manifest-key-on-finding", ClassRetroLite, `{"kind":"finding","body":"ok","problem_id":"SIM-P001"}`},
		{"manifest-empty-key-on-finding", ClassRetroLite, `{"kind":"finding","body":"ok","problem_id":""}`},
		{"non-string-array", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":"ok","predicted_rows":["alpha"]}`},
		{"non-string-int", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":123,"rationale":"ok"}`},
		{"non-string-null", ClassHarnessEdit, `{"kind":"sim_manifest","problem_id":"SIM-P001","rationale":null}`},
	}
}

func assertSIMManifestRejectCases(t *testing.T) {
	t.Helper()
	r := &run{o: Options{Home: t.TempDir(), Now: func() time.Time { return runAt }}}
	root := initGitRepo(t)
	rawSecret := "gh" + "p_" + strings.Repeat("Z", 36)
	escSecret := `\u0067` + "hp_" + strings.Repeat("Y", 36)
	for _, tc := range manifestRejectCases(rawSecret, escSecret) {
		fs, m := r.processDraftLines(root, tc.class, []byte(tc.line+"\n"))
		if m != nil || len(fs) != 1 || fs[0].Check != "draft" || fs[0].Rule != "invalid" {
			t.Errorf("%s: got m=%+v fs=%+v, want nil manifest + 1 invalid finding", tc.name, m, fs)
		}
		if len(fs) > 0 && (strings.Contains(fs[0].Text, rawSecret) || strings.Contains(fs[0].Text, strings.Repeat("Y", 20))) {
			t.Errorf("%s: secret leaked into finding: %+v", tc.name, fs[0])
		}
	}
}

func TestSIMAutoEvalGate(t *testing.T) {
	t.Run("kept-with-policy-model-predicted-row-and-baseline", testSIMEvalGateKept)
	t.Run("not-kept-short-circuit-missed-rows", testSIMEvalGateNotKeptShortCircuit)
	t.Run("pre-eval-reject-writes-no-sim-row", testSIMEvalGatePreEvalNoRow)
	t.Run("eval-pair-error-writes-skipped", testSIMEvalGateEvalPairErrorSkipped)
	t.Run("apply-fail-writes-not-kept", testSIMEvalGateApplyFailNotKept)
	t.Run("missing-manifest-fallback", testSIMEvalGateMissingManifestFallback)
}

func makeManifestBranchWork(t *testing.T, root, newAlpha, manifestLine string) func(context.Context, isolate.WorkerOptions, ...string) ([]isolate.WorkerResult, error) {
	t.Helper()
	baseFn := makeCandidateBranchWork(t, root, newAlpha)
	return func(ctx context.Context, wo isolate.WorkerOptions, kickoffs ...string) ([]isolate.WorkerResult, error) {
		res, err := baseFn(ctx, wo, kickoffs...)
		if err != nil || len(res) == 0 || manifestLine == "" {
			return res, err
		}
		relDir := filepath.ToSlash(filepath.Join(".agents", "scratch", "work", "sim-test", res[0].Name))
		absDir := filepath.Join(root, filepath.FromSlash(relDir))
		if err := os.MkdirAll(absDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(absDir, "auto-drafts.jsonl"), []byte(manifestLine+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		res[0].Drafts, res[0].DraftsDir = 1, relDir
		return res, nil
	}
}

func mustSingleSIMDetail(t *testing.T, home string) brain.SIMProblemDetail {
	t.Helper()
	sums, err := brain.ListSIMProblems(home, brain.SIMListFilter{Status: "all", Limit: 10})
	if err != nil || len(sums) != 1 {
		t.Fatalf("ListSIMProblems = %+v (err=%v), want 1 problem", sums, err)
	}
	det, err := brain.GetSIMProblem(home, sums[0].Problem.ID)
	if err != nil || len(det.Attempts) != 1 {
		t.Fatalf("GetSIMProblem(%s) = %+v (err=%v), want 1 attempt", sums[0].Problem.ID, det, err)
	}
	return det
}

func seedKeptKneeFixture(t *testing.T) (home, realHome, root string) {
	t.Helper()
	home, realHome = t.TempDir(), t.TempDir()
	var defaultID string
	root, _, defaultID = initHarnessEvalGateFixture(t, home, realHome, "")
	rej := "rejected"
	_ = brain.Edit(brain.Options{Home: home, Global: true}, brain.EditInput{ID: defaultID, Status: &rej})
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
	if err := saveState(home, State{Date: runAt.Format("2006-01-02"), Projects: map[string]Project{root: {LastClass: ClassRetroLite}}}); err != nil {
		t.Fatal(err)
	}
	return home, realHome, root
}

func testSIMEvalGateKept(t *testing.T) {
	home, realHome, root := seedKeptKneeFixture(t)
	parentSHA := git(t, root, "rev-parse", "HEAD")
	manifest := `{"kind":"sim_manifest","root_cause":"context knee spike in alpha","mechanism_class":"threshold","rationale":"add early range read guidance","predicted_rows":"alpha,untouched","predicted_signals":"sig:proj-deadbeef:context_knee_rate"}`
	var evalRows []string
	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Work = makeManifestBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n", manifest)
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		evalRows = append(evalRows, req.Row)
		p := 3
		if req.Row == "alpha" && req.Commit == parentSHA {
			p = 2
		}
		return EvalOutcome{Row: req.Row, Passed: p, Graded: req.Runs, PolicyModel: "gemini-4-neon-xhigh", RecordID: "TEST-sim"}, nil
	}
	if err := Run(o, false); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	assertKeptSIMResult(t, home, root, parentSHA, evalRows)
}

func assertKeptSIMResult(t *testing.T, home, root, parentSHA string, evalRows []string) {
	t.Helper()
	if !slices.Contains(evalRows, "untouched") {
		t.Errorf("predicted row 'untouched' was not appended to evalRows: %v", evalRows)
	}
	appliedSHA := shortSHA(git(t, root, "rev-parse", "HEAD"))
	att := mustSingleSIMDetail(t, home).Attempts[0]
	if att.EvalVerdict != "kept" || att.CommitSHA != appliedSHA || att.ParentSHA != parentSHA || att.PolicyModel != "gemini-4-neon-xhigh" || att.HarnessVersion == "" {
		t.Errorf("attempt provenance/verdict = %+v", att)
	}
	if !slices.Equal(att.FixedRows, []string{"alpha"}) || !slices.Equal(att.MissedRows, []string{"untouched"}) || len(att.RegressedRows) != 0 || att.EvalPassed != 4 || att.EvalTotal != 4 {
		t.Errorf("attempt grading = fixed:%v missed:%v reg:%v %d/%d", att.FixedRows, att.MissedRows, att.RegressedRows, att.EvalPassed, att.EvalTotal)
	}
	if att.FieldStatus != "pending_data" || att.FieldMetric != "H.sessions_over_knee" || att.FieldBaseline == nil || *att.FieldBaseline != 1.0 {
		t.Errorf("attempt field baseline = status:%s metric:%s base:%v", att.FieldStatus, att.FieldMetric, att.FieldBaseline)
	}
	dig, err := metrics.ReadLatestDailyDigest(home)
	if err != nil || dig == nil || len(dig.Actions) == 0 || dig.Actions[0].PolicyModel != "gemini-4-neon-xhigh" {
		t.Errorf("DigestAppliedAction PolicyModel missing: %+v (err=%v)", dig, err)
	}
}

func testSIMEvalGateNotKeptShortCircuit(t *testing.T) {
	home, realHome := t.TempDir(), t.TempDir()
	root, _, _ := initHarnessEvalGateFixture(t, home, realHome, "")
	parentSHA := git(t, root, "rev-parse", "HEAD")
	manifest := `{"kind":"sim_manifest","root_cause":"alpha skill regression","rationale":"try shorter rule","predicted_rows":"alpha,untouched"}`
	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Work = makeManifestBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha bad\n", manifest)
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		p := 3
		if req.Row == "alpha" && req.Commit != parentSHA {
			p = 1
		}
		return EvalOutcome{Row: req.Row, Passed: p, Graded: req.Runs, PolicyModel: "gemini-4-neon-xhigh"}, nil
	}
	if err := Run(o, false); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	att := mustSingleSIMDetail(t, home).Attempts[0]
	if att.EvalVerdict != "not_kept" || att.FieldStatus != "n/a" || att.CommitSHA == "" || att.CommitSHA == parentSHA {
		t.Errorf("not_kept attempt = %+v", att)
	}
	if !slices.Equal(att.MissedRows, []string{"alpha"}) || !slices.Equal(att.RegressedRows, []string{"alpha"}) || att.EvalPassed != 0 || att.EvalTotal != 1 {
		t.Errorf("short-circuit grading = missed:%v reg:%v %d/%d", att.MissedRows, att.RegressedRows, att.EvalPassed, att.EvalTotal)
	}
}

func testSIMEvalGatePreEvalNoRow(t *testing.T) {
	home, realHome := t.TempDir(), t.TempDir()
	root, _, _ := initHarnessEvalGateFixture(t, home, realHome, "")
	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Work = func(_ context.Context, _ isolate.WorkerOptions, kickoffs ...string) ([]isolate.WorkerResult, error) {
		name := strings.TrimSuffix(filepath.Base(kickoffs[0]), ".md")
		base := git(t, root, "rev-parse", "HEAD")
		tip := commitPlumbing(t, root, base, "100644", "skills/fyr-auto/SKILL.md", "---\nname: fyr-auto\ndescription: auto\n---\n# denied\n", "denied")
		git(t, root, "update-ref", "refs/heads/work/"+name, tip)
		return []isolate.WorkerResult{{Name: name, Base: base, Head: tip, Commits: 1, Verdict: "PASS"}}, nil
	}
	if err := Run(o, false); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if sums, err := brain.ListSIMProblems(home, brain.SIMListFilter{Status: "all", Limit: 10}); err != nil || len(sums) != 0 {
		t.Errorf("pre-eval reject should write 0 SIM rows, got %+v (err=%v)", sums, err)
	}
}

func testSIMEvalGateEvalPairErrorSkipped(t *testing.T) {
	home, realHome := t.TempDir(), t.TempDir()
	root, _, _ := initHarnessEvalGateFixture(t, home, realHome, "")
	manifest := `{"kind":"sim_manifest","root_cause":"infra error path","rationale":"check skipped verdict","predicted_rows":"alpha"}`
	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Work = makeManifestBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n", manifest)
	o.Eval = func(_ context.Context, _ EvalRequest) (EvalOutcome, error) {
		return EvalOutcome{}, errors.New("sandbox timeout")
	}
	_ = Run(o, false)
	att := mustSingleSIMDetail(t, home).Attempts[0]
	if att.EvalVerdict != "skipped" || att.FieldStatus != "n/a" || att.EvalTotal != 0 || len(att.MissedRows) != 0 || att.CommitSHA == "" {
		t.Errorf("evalPair error attempt = %+v", att)
	}
}

func testSIMEvalGateApplyFailNotKept(t *testing.T) {
	home, realHome := t.TempDir(), t.TempDir()
	root, _, _ := initHarnessEvalGateFixture(t, home, realHome, "")
	writeTree(t, root, "AGENTS.md", "# AGENTS.md\n\n## Setup commands\n\n```bash\nfalse # auto: test\n```\n")
	git(t, root, "add", "AGENTS.md")
	git(t, root, "commit", "-q", "-m", "failing auto test")
	git(t, root, "push", "-q")
	backdate(t, root)
	manifest := `{"kind":"sim_manifest","root_cause":"apply verify failure","rationale":"tighten alpha instructions","predicted_rows":"alpha"}`
	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Work = makeManifestBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n", manifest)
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		return EvalOutcome{Row: req.Row, Passed: 3, Graded: req.Runs, PolicyModel: "gemini-4-neon-xhigh"}, nil
	}
	_ = Run(o, false)
	att := mustSingleSIMDetail(t, home).Attempts[0]
	if att.EvalVerdict != "not_kept" || att.FieldStatus != "n/a" || !strings.Contains(att.What, "; apply failed: ") || len(att.What) > 400 {
		t.Errorf("apply-fail attempt = %+v", att)
	}
}

func testSIMEvalGateMissingManifestFallback(t *testing.T) {
	home, realHome := t.TempDir(), t.TempDir()
	root, _, _ := initHarnessEvalGateFixture(t, home, realHome, "")
	var out bytes.Buffer
	o := gateOpts(home, realHome, root, &out)
	o.Work = makeCandidateBranchWork(t, root, "---\nname: alpha\ndescription: alpha\n---\n# alpha v2\n")
	o.Eval = func(_ context.Context, req EvalRequest) (EvalOutcome, error) {
		return EvalOutcome{Row: req.Row, Passed: 3, Graded: req.Runs}, nil
	}
	if err := Run(o, false); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	reps := reports(t, home)
	if len(reps) == 0 || !strings.Contains(reps[0].Body, "draft/missing-sim-manifest") {
		t.Errorf("report missing draft/missing-sim-manifest finding: %+v", reps)
	}
	det := mustSingleSIMDetail(t, home)
	if !strings.Contains(det.Problem.RootCause, "tighten skills/alpha/SKILL.md") || det.Attempts[0].EvalVerdict != "kept" {
		t.Errorf("fallback SIM problem/attempt = %+v", det)
	}
}

func testSIMEvalExecPolicyModel(t *testing.T) {
	home, root := t.TempDir(), idleProject(t)
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTrial(metrics.Trial{Run: "old/smoke/1", Label: "eval=smoke", Harness: "v9.9.0", Status: "pass", StartedAt: time.Now().UTC().Add(-10 * time.Minute), PolicyModel: "stale-model"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	fakeSelf := filepath.Join(t.TempDir(), "fake-fyr")
	writeExe(t, fakeSelf, "#!/usr/bin/env bash\necho 'eval: 3 of 3 PASS, 0 errors; record TEST-1'\n")
	if out, err := runFyrEval(context.Background(), fakeSelf, EvalRequest{Dir: root, Home: home, Runs: 3, Row: "smoke"}); err != nil || out.PolicyModel != "" {
		t.Fatalf("stale trial leaked into PolicyModel: %+v (err=%v)", out, err)
	}
	s, err = metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTrial(metrics.Trial{Run: "new/smoke/1", Label: "eval=smoke", Harness: "v9.9.0", Status: "pass", StartedAt: time.Now().UTC().Add(5 * time.Second), PolicyModel: "gemini-4-neon-xhigh"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if out2, err := runFyrEval(context.Background(), fakeSelf, EvalRequest{Dir: root, Home: home, Runs: 3, Row: "smoke"}); err != nil || out2.PolicyModel != "gemini-4-neon-xhigh" {
		t.Fatalf("runFyrEval PolicyModel = %q (err=%v), want gemini-4-neon-xhigh", out2.PolicyModel, err)
	}
}
