package metrics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestSIMMetricsMigrationV13(t *testing.T) {
	p := filepath.Join(t.TempDir(), "metrics.db")
	saved := migrations
	t.Cleanup(func() { migrations = saved })
	if len(saved) < 13 {
		t.Fatalf("len(migrations) = %d, want >= 13", len(saved))
	}

	migrations = saved[:12]
	s12 := openT(t, p)
	if got := s12.meta(t, "schema"); got != "12" {
		t.Fatalf("v12 schema = %q, want 12", got)
	}
	if _, err := s12.db.Exec(
		`INSERT INTO trials(run, label, harness, status, cause, started_at, seconds) VALUES ('r12/smoke/1', 'eval=smoke', 'abc1234', 'pass', '', 1700000000000, 10)`,
	); err != nil {
		t.Fatalf("insert v12 trial: %v", err)
	}
	if _, err := s12.db.Exec(`SELECT policy_model FROM trials`); err == nil {
		t.Fatal("v12 schema already has policy_model column")
	}
	s12.Close()

	migrations = saved
	s13 := openT(t, p)
	if got := s13.meta(t, "schema"); got != "14" {
		t.Fatalf("v14 schema = %q, want 14", got)
	}
	var oldPM string
	if err := s13.db.QueryRow(`SELECT policy_model FROM trials WHERE run = 'r12/smoke/1'`).Scan(&oldPM); err != nil || oldPM != "" {
		t.Fatalf("upgraded v12 trial policy_model = (%q, %v), want ('', nil)", oldPM, err)
	}
	tr := trialT("r13/smoke/1", "pass", "")
	tr.PolicyModel = "gemini-4-neon-xhigh"
	if err := s13.RecordTrial(tr); err != nil {
		t.Fatalf("RecordTrial v13: %v", err)
	}
	var newPM string
	if err := s13.db.QueryRow(`SELECT policy_model FROM trials WHERE run = 'r13/smoke/1'`).Scan(&newPM); err != nil || newPM != "gemini-4-neon-xhigh" {
		t.Fatalf("v13 trial policy_model = (%q, %v), want gemini-4-neon-xhigh", newPM, err)
	}

	s13.db.Exec(`UPDATE meta SET value = '15' WHERE key = 'schema'`)
	s13.Close()
	if s15, err := Open(p); err == nil {
		s15.Close()
		t.Fatal("Open accepted schema 15")
	} else if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("schema 15 err = %v, want ErrNewerSchema", err)
	}
}

func TestSIMMetricsPolicyModel(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	s := openT(t, Path(home))

	// Must-fail Trial.PolicyModel validation: non-printable and > trialText.
	for name, pm := range map[string]string{
		"escape": "gemini\x1b[2J",
		"long":   strings.Repeat("m", trialText+1),
	} {
		bad := trialT("bad/"+name, "pass", "")
		bad.PolicyModel = pm
		if err := s.RecordTrial(bad); err == nil {
			t.Errorf("RecordTrial(%s policy_model) succeeded, want error", name)
		}
	}

	// Must-pass TrialInput persistence + secret stripping.
	secretTok := strings.Join([]string{"gh", "p_", "SIMPOLICYSECRET1234567890AB"}, "")
	if err := s.RecordTrialInput(TrialInput{
		Run: "ti/row1/1", Label: "eval=row1", Status: "pass", StartedAt: now, Seconds: 5,
		PolicyModel: "gemini-4-neon-xhigh", Model: "gemini-4-neon-xhigh",
	}); err != nil {
		t.Fatalf("RecordTrialInput valid: %v", err)
	}
	if err := s.RecordTrialInput(TrialInput{
		Run: "ti/row2/1", Label: "eval=row2", Status: "pass", StartedAt: now, Seconds: 5,
		PolicyModel: "token=" + secretTok,
	}); err != nil {
		t.Fatalf("RecordTrialInput secret policy_model: %v", err)
	}
	var pm1, pm2 string
	if err := s.db.QueryRow(`SELECT policy_model FROM trials WHERE run = 'ti/row1/1'`).Scan(&pm1); err != nil || pm1 != "gemini-4-neon-xhigh" {
		t.Fatalf("row1 policy_model = (%q, %v), want gemini-4-neon-xhigh", pm1, err)
	}
	if err := s.db.QueryRow(`SELECT policy_model FROM trials WHERE run = 'ti/row2/1'`).Scan(&pm2); err != nil || pm2 != "" {
		t.Fatalf("row2 redacted policy_model = (%q, %v), want empty", pm2, err)
	}

	// Sanitized export/import round-trip + omitempty + v12 backwards compatibility.
	exp, err := ExportSanitizedState(s, "", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ExportSanitizedState: %v", err)
	}
	if exp.Schema != 14 || len(exp.Trials) != 2 {
		t.Fatalf("export = schema %d trials %d, want 14 and 2", exp.Schema, len(exp.Trials))
	}
	blob, _ := json.Marshal(exp)
	if !strings.Contains(string(blob), `"policy_model":"gemini-4-neon-xhigh"`) {
		t.Fatalf("export JSON missing policy_model: %s", blob)
	}
	// row2 has empty PolicyModel and must omit the key.
	row2Blob, _ := json.Marshal(exp.Trials[1])
	if strings.Contains(string(row2Blob), `"policy_model"`) {
		t.Fatalf("empty PolicyModel was not omitted in JSON: %s", row2Blob)
	}

	dest := openT(t, filepath.Join(t.TempDir(), "dest.db"))
	if _, err := ImportSanitizedState(dest, exp, false); err != nil {
		t.Fatalf("ImportSanitizedState v13: %v", err)
	}
	var impPM string
	if err := dest.db.QueryRow(`SELECT policy_model FROM trials WHERE run = 'ti/row1/1'`).Scan(&impPM); err != nil || impPM != "gemini-4-neon-xhigh" {
		t.Fatalf("imported policy_model = (%q, %v), want gemini-4-neon-xhigh", impPM, err)
	}

	// v12 JSON payload lacking policy_model imports cleanly.
	v12JSON := fmt.Sprintf(`{"schema":12,"trials":[{"run":"v12/r/1","label":"eval=r","harness":"h1","status":"pass","cause":"","started_at":%d,"seconds":4}]}`, now.UnixMilli())
	var v12Exp SanitizedMetricsExport
	if err := json.Unmarshal([]byte(v12JSON), &v12Exp); err != nil {
		t.Fatalf("unmarshal v12 export: %v", err)
	}
	if cnt, err := ImportSanitizedState(dest, v12Exp, false); err != nil || cnt.Trials != 1 {
		t.Fatalf("import v12 payload = (%+v, %v), want 1 trial", cnt, err)
	}
	var v12PM string
	if err := dest.db.QueryRow(`SELECT policy_model FROM trials WHERE run = 'v12/r/1'`).Scan(&v12PM); err != nil || v12PM != "" {
		t.Fatalf("v12 imported policy_model = (%q, %v), want ''", v12PM, err)
	}

	// Exporting directly from a v12 store lacking trials.policy_model succeeds with empty PolicyModel.
	saved := migrations
	t.Cleanup(func() { migrations = saved })
	migrations = saved[:12]
	s12 := openT(t, filepath.Join(t.TempDir(), "v12.db"))
	if _, err := s12.db.Exec(`INSERT INTO trials(run, label, harness, status, cause, started_at, seconds) VALUES ('v12exp/r/1', 'eval=r', 'h1', 'pass', '', ?, 5)`, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	v12Out, err := ExportSanitizedState(s12, "", now.Add(-time.Hour))
	if err != nil || len(v12Out.Trials) != 1 || v12Out.Trials[0].PolicyModel != "" {
		t.Fatalf("ExportSanitizedState(v12 db) = (%+v, %v), want 1 trial with empty policy_model", v12Out.Trials, err)
	}
	s12.Close()
	migrations = saved

	// DigestAppliedAction.PolicyModel propagation through merge, normalize, save, load.
	norm := normalizeAppliedAction(DigestAppliedAction{
		SignalID: "sig:global:search_grep_low", PolicyModel: "  gemini-4-neon-xhigh  ",
	}, "2026-11-22", now.UnixMilli())
	if norm.PolicyModel != "gemini-4-neon-xhigh" {
		t.Fatalf("normalizeAppliedAction PolicyModel = %q, want gemini-4-neon-xhigh", norm.PolicyModel)
	}
	normEsc := normalizeAppliedAction(DigestAppliedAction{
		SignalID: "sig:global:search_grep_low", PolicyModel: "gemini\x1b[2J",
	}, "2026-11-22", now.UnixMilli())
	if strings.Contains(normEsc.PolicyModel, "\x1b") {
		t.Fatalf("normalizeAppliedAction left escape byte in PolicyModel: %q", normEsc.PolicyModel)
	}
	merged := mergeAppliedAction(DigestAppliedAction{SignalID: "sig:global:x"}, DigestAppliedAction{PolicyModel: "gemini-4-neon-xhigh"})
	if merged.PolicyModel != "gemini-4-neon-xhigh" {
		t.Fatalf("mergeAppliedAction PolicyModel = %q, want gemini-4-neon-xhigh", merged.PolicyModel)
	}
	kept := mergeAppliedAction(DigestAppliedAction{PolicyModel: "gemini-5-pro"}, DigestAppliedAction{PolicyModel: "gemini-4-neon-xhigh"})
	if kept.PolicyModel != "gemini-5-pro" {
		t.Fatalf("mergeAppliedAction overwrote base PolicyModel: got %q", kept.PolicyModel)
	}
	if err := RecordDigestAppliedAction(s, home, "", now, DigestAppliedAction{
		SignalID: "sig:global:search_grep_low", ReportDate: "2026-11-22",
		AppliedSHA: "abc1234", PolicyModel: "gemini-4-neon-xhigh",
	}); err != nil {
		t.Fatalf("RecordDigestAppliedAction: %v", err)
	}
	savedDigest, err := LatestDailyDigest(s)
	if err != nil || savedDigest == nil || len(savedDigest.Actions) != 1 || savedDigest.Actions[0].PolicyModel != "gemini-4-neon-xhigh" {
		t.Fatalf("saved digest PolicyModel = %+v, err=%v", savedDigest, err)
	}
	diskDigest, err := ReadLatestDailyDigest(home)
	if err != nil || diskDigest == nil || len(diskDigest.Actions) != 1 || diskDigest.Actions[0].PolicyModel != "gemini-4-neon-xhigh" {
		t.Fatalf("disk digest PolicyModel = %+v, err=%v", diskDigest, err)
	}
}

func TestSIMMetricsDominantAndStratified(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	since := now.Add(-7 * 24 * time.Hour)
	applyAt := now.Add(-24 * time.Hour)
	s := openT(t, Path(home))

	// Nil store guards.
	if _, err := DominantModelSince(nil, since); err == nil {
		t.Fatal("DominantModelSince(nil) succeeded")
	}
	if _, err := RecentModelsSince(nil, since.UnixMilli()); err == nil {
		t.Fatal("RecentModelsSince(nil) succeeded")
	}
	if _, err := StratifiedFieldRecovery(nil, "C.bash_read_skips.agy", nil, applyAt.UnixMilli(), now); err == nil {
		t.Fatal("StratifiedFieldRecovery(nil) succeeded")
	}
	if _, ok := LookupObservedMetric(nil, home, "C.bash_read_skips.agy", now); ok {
		t.Fatal("LookupObservedMetric(nil) returned ok")
	}

	// Empty store returns empty without error.
	if dom, err := DominantModelSince(s, since); err != nil || dom != "" {
		t.Fatalf("empty DominantModelSince = (%q, %v)", dom, err)
	}
	if rec, err := RecentModelsSince(s, since.UnixMilli()); err != nil || len(rec) != 0 {
		t.Fatalf("empty RecentModelsSince = (%v, %v)", rec, err)
	}

	// Seed pre-window noise, non-agy, eval-labeled, and subagent rows (all must be ignored).
	oldTS := now.Add(-10 * 24 * time.Hour).UnixMilli()
	postTS := applyAt.Add(time.Hour).UnixMilli()
	for _, q := range []string{
		fmt.Sprintf(`INSERT INTO sessions(id, vendor, model, started_at, ended_at) VALUES ('old-1', 'agy', 'ancient-model', %d, %d)`, oldTS, oldTS+1000),
		fmt.Sprintf(`INSERT INTO sessions(id, vendor, model, started_at, ended_at) VALUES ('claude-1', 'claude', 'claude-old', %d, %d)`, postTS, postTS+1000),
		fmt.Sprintf(`INSERT INTO sessions(id, vendor, model, label, started_at, ended_at) VALUES ('eval-1', 'agy', 'eval-only-model', 'eval=smoke', %d, %d)`, postTS, postTS+1000),
		fmt.Sprintf(`INSERT INTO sessions(id, vendor, parent_id, model, started_at, ended_at) VALUES ('sub-1', 'agy', 'm-b-0', 'subagent-flash', %d, %d)`, postTS, postTS+1000),
		fmt.Sprintf(`INSERT INTO sessions(id, vendor, parent_id, model, started_at, ended_at) VALUES ('sub-2', 'agy', 'm-b-0', 'subagent-flash', %d, %d)`, postTS, postTS+1000),
		fmt.Sprintf(`INSERT INTO sessions(id, vendor, parent_id, model, started_at, ended_at) VALUES ('sub-3', 'agy', 'm-b-0', 'subagent-flash', %d, %d)`, postTS, postTS+1000),
		fmt.Sprintf(`INSERT INTO sessions(id, vendor, parent_id, model, started_at, ended_at) VALUES ('sub-4', 'agy', 'm-b-0', 'subagent-flash', %d, %d)`, postTS, postTS+1000),
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	// Post-apply sessions:
	// - model-a: 1 session -> pending_data
	// - model-b: 3 sessions, 0 bash skips -> recovered (vs baseline 3.0)
	// - model-c: 2 sessions, 4 bash skips -> still_regressing (vs baseline 3.0)
	insertSess := func(id, model string, ts int64, bashCats int) {
		t.Helper()
		if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, model, started_at, ended_at) VALUES (?, 'agy', '/work/app', ?, ?, ?)`, id, model, ts, ts+5000); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < bashCats; i++ {
			ext := fmt.Sprintf("%s:cat:%d", id, i)
			if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES (?, ?, ?, 'tool', 'Bash')`, id, ext, ts+int64(i)+10); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES (?, ?, 0, 'cat')`, id, ext); err != nil {
				t.Fatal(err)
			}
		}
	}
	insertSess("m-a-0", "model-a", postTS+100, 0)
	insertSess("m-b-0", "model-b", postTS+200, 0)
	insertSess("m-b-1", "model-b", postTS+300, 0)
	insertSess("m-b-2", "model-b", postTS+400, 0)
	insertSess("m-c-0", "model-c", postTS+500, 2)
	insertSess("m-c-1", "model-c", postTS+600, 2)

	dom, err := DominantModelSince(s, since)
	if err != nil || dom != "model-b" {
		t.Fatalf("DominantModelSince = (%q, %v), want model-b", dom, err)
	}
	recent, err := RecentModelsSince(s, since.UnixMilli())
	wantRecent := []string{"model-a", "model-b", "model-c"}
	if err != nil || !reflect.DeepEqual(recent, wantRecent) {
		t.Fatalf("RecentModelsSince = (%v, %v), want %v", recent, err, wantRecent)
	}

	base := 3.0
	strat, err := StratifiedFieldRecovery(s, "C.bash_read_skips.agy", &base, applyAt.UnixMilli(), now)
	if err != nil {
		t.Fatalf("StratifiedFieldRecovery: %v", err)
	}
	wantStrat := []ModelFieldStatus{
		{Model: "model-a", Status: StatusPendingData, Sessions: 1},
		{Model: "model-b", Status: StatusRecovered, Sessions: 3},
		{Model: "model-c", Status: StatusStillRegressing, Sessions: 2},
	}
	if !reflect.DeepEqual(strat, wantStrat) {
		t.Fatalf("StratifiedFieldRecovery:\n got %+v\nwant %+v", strat, wantStrat)
	}

	obs, ok := LookupObservedMetric(s, home, "C.bash_read_skips.agy", now)
	if !ok || obs != 4.0 {
		t.Fatalf("LookupObservedMetric(C.bash_read_skips.agy) = (%v, %v), want (4, true)", obs, ok)
	}

	// Verify StratifiedFieldRecovery on turn_vets (E.timeout_rate_pct), ratings (A.mean_outcome_score), and nil-baseline outcome fallback.
	if _, err := s.db.Exec(`INSERT INTO turn_vets(project_root, conversation_id, turn_num, intent, gate, score, timed_out, created_at) VALUES
		('/work/app', 'm-b-0', 1, 'TASK', 'PROCEED', 90, 0, ?),
		('/work/app', 'm-b-1', 1, 'TASK', 'PROCEED', 90, 0, ?),
		('/work/app', 'm-c-0', 1, 'TASK', 'PROCEED', 50, 1, ?),
		('/work/app', 'm-c-1', 1, 'TASK', 'PROCEED', 50, 1, ?)`, postTS+200, postTS+300, postTS+500, postTS+600); err != nil {
		t.Fatal(err)
	}
	toBase := 20.0
	toStrat, err := StratifiedFieldRecovery(s, "E.timeout_rate_pct", &toBase, applyAt.UnixMilli(), now)
	if err != nil || len(toStrat) != 3 || toStrat[1].Status != StatusRecovered || toStrat[2].Status != StatusStillRegressing {
		t.Fatalf("timeout stratified = (%+v, %v)", toStrat, err)
	}

	// Empty metric / nil baseline fallback uses session ratings / outcomes.
	if err := s.UpsertSessionRatings([]SessionRating{
		{SessionID: "m-b-0", EvaluatedAt: postTS + 200, OutcomeScore: 5, TokenEfficiency: 4, PlanAlignment: "matched", SkillAction: "none"},
		{SessionID: "m-b-1", EvaluatedAt: postTS + 300, OutcomeScore: 4, TokenEfficiency: 4, PlanAlignment: "matched", SkillAction: "none"},
		{SessionID: "m-c-0", EvaluatedAt: postTS + 500, OutcomeScore: 2, TokenEfficiency: 2, PlanAlignment: "missed", SkillAction: "none"},
		{SessionID: "m-c-1", EvaluatedAt: postTS + 600, OutcomeScore: 2, TokenEfficiency: 2, PlanAlignment: "missed", SkillAction: "none"},
	}); err != nil {
		t.Fatal(err)
	}
	fbStrat, err := StratifiedFieldRecovery(s, "", nil, applyAt.UnixMilli(), now)
	if err != nil || len(fbStrat) != 3 || fbStrat[0].Status != StatusPendingData || fbStrat[1].Status != StatusRecovered || fbStrat[2].Status != StatusStillRegressing {
		t.Fatalf("fallback stratified = (%+v, %v)", fbStrat, err)
	}
}

func TestSIMMetricsLatestTrialModelCutoff(t *testing.T) {
	if got := LatestTrialModel("", "smoke", 0); got != "" {
		t.Fatalf("empty home LatestTrialModel = %q, want ''", got)
	}
	if got := LatestTrialModel(t.TempDir(), "smoke", 0); got != "" {
		t.Fatalf("missing db LatestTrialModel = %q, want ''", got)
	}

	evalHome := t.TempDir()
	s := openT(t, Path(evalHome))
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(2 * time.Hour)
	t2 := t0.Add(4 * time.Hour)

	// Old trial before cutoff.
	trOld := trialT("2026-11-20/smoke/1", "pass", "")
	trOld.StartedAt = t0
	trOld.PolicyModel = "old-model"
	if err := s.RecordTrial(trOld); err != nil {
		t.Fatal(err)
	}
	// Cutoff strictly after t0 must NOT see old-model.
	if got := LatestTrialModel(evalHome, "smoke", t1.UnixMilli()); got != "" {
		t.Fatalf("LatestTrialModel past cutoff = %q, want ''", got)
	}
	// Cutoff at t0 sees old-model.
	if got := LatestTrialModel(evalHome, "smoke", t0.UnixMilli()); got != "old-model" {
		t.Fatalf("LatestTrialModel at t0 = %q, want old-model", got)
	}

	// Trial at t1 with empty policy_model falls back to linked main session model (not subagent).
	trSess := trialT("2026-11-20/smoke/2", "pass", "")
	trSess.StartedAt = t1
	if err := s.RecordTrial(trSess); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, parent_id, model, label, run, started_at) VALUES ('s-sub', 'agy', 's-main', 'subagent-flash', 'eval=smoke', '2026-11-20/smoke/2', ?)`, t1.UnixMilli()+10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, model, label, run, started_at) VALUES ('s-main', 'agy', 'session-fallback-model', 'eval=smoke', '2026-11-20/smoke/2', ?)`, t1.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if got := LatestTrialModel(evalHome, "smoke", t1.UnixMilli()); got != "session-fallback-model" {
		t.Fatalf("LatestTrialModel session fallback = %q, want session-fallback-model", got)
	}

	// Newer trial at t2 with explicit policy_model wins over older session fallback.
	trExplicit := trialT("2026-11-20/smoke/3", "pass", "")
	trExplicit.StartedAt = t2
	trExplicit.PolicyModel = "explicit-policy-model"
	if err := s.RecordTrial(trExplicit); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, model, label, run, started_at) VALUES ('s-t2', 'agy', 'other-session-model', 'eval=smoke', '2026-11-20/smoke/3', ?)`, t2.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if got := LatestTrialModel(evalHome, "smoke", t1.UnixMilli()); got != "explicit-policy-model" {
		t.Fatalf("LatestTrialModel explicit policy_model = %q, want explicit-policy-model", got)
	}

	// Pre-v13 (v12) eval store without trials.policy_model still falls back to sessions.model safely.
	v12Home := t.TempDir()
	saved := migrations
	t.Cleanup(func() { migrations = saved })
	migrations = saved[:12]
	s12 := openT(t, Path(v12Home))
	if _, err := s12.db.Exec(`INSERT INTO trials(run, label, harness, status, cause, started_at, seconds) VALUES ('v12/smoke/1', 'eval=smoke', 'h1', 'pass', '', ?, 5)`, t1.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := s12.db.Exec(`INSERT INTO sessions(id, vendor, model, label, run, started_at) VALUES ('s12', 'agy', 'v12-session-model', 'eval=smoke', 'v12/smoke/1', ?)`, t1.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	s12.Close()
	migrations = saved
	if got := LatestTrialModel(v12Home, "smoke", t1.UnixMilli()); got != "v12-session-model" {
		t.Fatalf("LatestTrialModel v12 fallback = %q, want v12-session-model", got)
	}
}

func TestSIMMetricsModelShiftAudit(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	// Negative gates: <3 sessions, exact 50% split, and model already seen in prior 14d.
	for name, seed := range map[string]func(*Store){
		"under-min": func(s *Store) { addModelSessions(t, s, "low", "gemini-5-pro", 2, now.Add(-time.Hour)) },
		"half-split": func(s *Store) {
			addModelSessions(t, s, "new", "gemini-5-pro", 3, now.Add(-time.Hour))
			addModelSessions(t, s, "old", "gemini-4-neon", 3, now.Add(-2*time.Hour))
		},
		"seen-prior": func(s *Store) {
			addModelSessions(t, s, "cur", "gemini-5-pro", 4, now.Add(-time.Hour))
			addModelSessions(t, s, "pri", "gemini-5-pro", 1, now.Add(-10*24*time.Hour))
		},
	} {
		h := t.TempDir()
		s := openT(t, Path(h))
		seed(s)
		d, err := BuildDailyDigest(s, h, "", now)
		if err != nil {
			t.Fatalf("%s BuildDailyDigest: %v", name, err)
		}
		if findDigestSignal(d.Signals, "model_shift_audit") != nil {
			t.Fatalf("%s unexpectedly emitted model_shift_audit: %+v", name, d.Signals)
		}
	}

	// Pre-v5 brain.db: shift triggers info-only signal without mutating brain.db or flipping verdict.
	h3 := t.TempDir()
	s3 := openT(t, Path(h3))
	seedShiftSessions(t, s3, now)
	if err := SaveDailyDigest(s3, DailyDigest{Day: "2026-11-21", Verdict: VerdictSteady, CreatedAt: now.Add(-24 * time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	preV5Brain := filepath.Join(h3, harness.StateDir, "brain.db")
	sqliteT(t, preV5Brain,
		`PRAGMA user_version = 4`,
		`CREATE TABLE records(rid INTEGER PRIMARY KEY, scope TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL, date TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, project TEXT NOT NULL DEFAULT '', updated TEXT NOT NULL, provenance TEXT NOT NULL DEFAULT '', UNIQUE(scope, id))`,
	)
	beforeBytes, err := os.ReadFile(preV5Brain)
	if err != nil {
		t.Fatal(err)
	}
	d3, err := BuildDailyDigest(s3, h3, "", now)
	if err != nil {
		t.Fatalf("BuildDailyDigest pre-v5 brain: %v", err)
	}
	if afterBytes, rerr := os.ReadFile(preV5Brain); rerr != nil || !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatalf("pre-v5 brain.db mutated or unreadable: err=%v", rerr)
	}
	if d3.Verdict == VerdictRegress {
		t.Fatalf("info-only model_shift_audit flipped verdict to regressing: %+v", d3)
	}
	for _, nr := range d3.NewRegressions {
		if nr == "sig:global:model_shift_audit" {
			t.Fatalf("info signal appeared in NewRegressions: %v", d3.NewRegressions)
		}
	}
	sig3 := findDigestSignal(d3.Signals, "model_shift_audit")
	if sig3 == nil || sig3.Severity != "info" || sig3.SignalID != "sig:global:model_shift_audit" || sig3.ActionScope != "operator_rule" {
		t.Fatalf("pre-v5 model_shift_audit signal = %+v", sig3)
	}

	// v5 brain.db: lists up to 5 stale active model_workaround/prompt_guidance IDs, never flips verdict, renders cleanly.
	h4 := t.TempDir()
	s4 := openT(t, Path(h4))
	seedShiftSessions(t, s4, now)
	seedSIMBrainV5(t, filepath.Join(h4, harness.StateDir, "brain.db"))
	d4, err := BuildDailyDigest(s4, h4, "", now)
	if err != nil || d4.Verdict == VerdictRegress {
		t.Fatalf("v5 BuildDailyDigest = (verdict=%q, err=%v)", d4.Verdict, err)
	}
	sig4 := findDigestSignal(d4.Signals, "model_shift_audit")
	if sig4 == nil {
		t.Fatalf("v5 model_shift_audit missing: %+v", d4.Signals)
	}
	for _, excluded := range []string{"SIM-P007", "SIM-P008", "SIM-P009", "SIM-P010"} {
		if strings.Contains(sig4.Summary, excluded) || strings.Contains(sig4.RecommendedAction, excluded) {
			t.Errorf("model_shift_audit unexpectedly included %s: summary=%q", excluded, sig4.Summary)
		}
	}
	listed := 0
	for i := 1; i <= 6; i++ {
		if strings.Contains(sig4.Summary, fmt.Sprintf("SIM-P00%d", i)) {
			listed++
		}
	}
	if listed != 5 {
		t.Fatalf("listed %d stale SIM-P* IDs, want cap 5: summary=%q", listed, sig4.Summary)
	}

	txt := RenderDailyDigestText(d4)
	if !strings.Contains(txt, "[info/operator_rule] model_shift_audit (sig:global:model_shift_audit)") || !strings.Contains(txt, "SIM-P001") {
		t.Errorf("RenderDailyDigestText missing model_shift_audit info:\n%s", txt)
	}
	sum := reportFixture(t)
	sum.DailyDigest = &d4
	var mdBuf, htmlBuf bytes.Buffer
	if err := RenderMarkdownReport(&mdBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := RenderReport(&htmlBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mdBuf.String(), "model_shift_audit") || !strings.Contains(mdBuf.String(), "SIM-P001") {
		t.Errorf("Markdown banner missing model_shift_audit:\n%s", mdBuf.String())
	}
	if !strings.Contains(htmlBuf.String(), "model_shift_audit") || !strings.Contains(htmlBuf.String(), "SIM-P001") {
		t.Errorf("HTML banner missing model_shift_audit:\n%s", htmlBuf.String())
	}

	// Hardening: alias cluster coverage & strict SIM-P ID validation in model_shift_audit,
	// plus unmeasured tracked metric returning StatusPendingData (never false "recovered").
	h5 := t.TempDir()
	s5 := openT(t, Path(h5))
	seedShiftSessions(t, s5, now)
	sqliteT(t, filepath.Join(h5, harness.StateDir, "brain.db"),
		`PRAGMA user_version = 5`,
		`CREATE TABLE records(rid INTEGER PRIMARY KEY, scope TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL, date TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, project TEXT NOT NULL DEFAULT '', updated TEXT NOT NULL, provenance TEXT NOT NULL DEFAULT '', UNIQUE(scope, id))`,
		`CREATE TABLE sim_problems(pid INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, root_cause TEXT NOT NULL, norm_root_cause TEXT NOT NULL, mechanism_class TEXT NOT NULL DEFAULT '', signal_id TEXT NOT NULL DEFAULT '', target_paths TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'active', provenance TEXT NOT NULL DEFAULT 'derived', opened_at TEXT NOT NULL, updated_at TEXT NOT NULL, opened_harness TEXT NOT NULL DEFAULT '', opened_model TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE sim_aliases(old_id TEXT PRIMARY KEY, target_id TEXT NOT NULL, merged_at TEXT NOT NULL)`,
		`CREATE TABLE sim_attempts(id INTEGER PRIMARY KEY AUTOINCREMENT, problem_id TEXT NOT NULL, source_kind TEXT NOT NULL, proposal_id TEXT NOT NULL DEFAULT '', commit_sha TEXT NOT NULL DEFAULT '', parent_sha TEXT NOT NULL DEFAULT '', harness_version TEXT NOT NULL DEFAULT '', proposer_model TEXT NOT NULL DEFAULT '', policy_model TEXT NOT NULL DEFAULT '', project_scope TEXT NOT NULL DEFAULT '', files_json TEXT NOT NULL DEFAULT '[]', what TEXT NOT NULL, predicted_rows TEXT NOT NULL DEFAULT '[]', predicted_signals TEXT NOT NULL DEFAULT '[]', eval_verdict TEXT NOT NULL DEFAULT 'none', fixed_rows TEXT NOT NULL DEFAULT '[]', missed_rows TEXT NOT NULL DEFAULT '[]', regressed_rows TEXT NOT NULL DEFAULT '[]', eval_passed INTEGER NOT NULL DEFAULT 0, eval_total INTEGER NOT NULL DEFAULT 0, field_status TEXT NOT NULL DEFAULT 'n/a', field_metric TEXT NOT NULL DEFAULT '', field_baseline REAL, field_observed REAL, field_models_json TEXT NOT NULL DEFAULT '[]', provenance TEXT NOT NULL DEFAULT 'derived', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`INSERT INTO sim_problems(pid, id, root_cause, norm_root_cause, mechanism_class, status, opened_at, updated_at, opened_model) VALUES
			(1, 'SIM-P001', 'merged old', 'merged old', 'model_workaround', 'merged', '2026-11-10T00:00:00Z', '2026-11-15T00:00:00Z', ''),
			(2, 'SIM-P002', 'canon covered via alias', 'canon covered via alias', 'model_workaround', 'active', '2026-11-10T00:00:00Z', '2026-11-20T00:00:00Z', 'gemini-4-neon-xhigh'),
			(3, 'SIM-P003', 'merged old only', 'merged old only', 'model_workaround', 'merged', '2026-11-10T00:00:00Z', '2026-11-15T00:00:00Z', ''),
			(4, 'SIM-P004', 'canon stale via alias', 'canon stale via alias', 'model_workaround', 'active', '2026-11-10T00:00:00Z', '2026-11-20T00:00:00Z', ''),
			(5, 'SIM-Pbad-inject', 'bad', 'bad', 'model_workaround', 'active', '2026-11-10T00:00:00Z', '2026-11-20T00:00:00Z', 'gemini-4-neon-xhigh')`,
		`INSERT INTO sim_aliases(old_id, target_id, merged_at) VALUES
			('SIM-P001', 'SIM-P002', '2026-11-15T00:00:00Z'),
			('SIM-P003', 'SIM-P004', '2026-11-15T00:00:00Z')`,
		`INSERT INTO sim_attempts(problem_id, source_kind, policy_model, what, eval_verdict, field_models_json, created_at, updated_at) VALUES
			('SIM-P001', 'auto_harness_edit', 'gemini-5-pro', 'aliased new validation', 'kept', '[]', '2026-11-21T00:00:00Z', '2026-11-21T00:00:00Z'),
			('SIM-P003', 'auto_harness_edit', 'gemini-4-neon-xhigh', 'aliased old validation', 'kept', '[]', '2026-11-12T00:00:00Z', '2026-11-12T00:00:00Z')`,
	)
	ids5 := staleSIMWorkarounds(h5, "gemini-5-pro", 5)
	if !reflect.DeepEqual(ids5, []string{"SIM-P004"}) {
		t.Fatalf("alias-aware staleSIMWorkarounds = %v, want [SIM-P004] (SIM-P002 covered via SIM-P001, SIM-Pbad-inject excluded)", ids5)
	}
	unmeasBase := 10.0
	stratUnmeas, err := StratifiedFieldRecovery(s5, "E.timeout_rate_pct", &unmeasBase, now.Add(-24*time.Hour).UnixMilli(), now)
	if err != nil || len(stratUnmeas) != 1 || stratUnmeas[0].Status != StatusPendingData {
		t.Fatalf("unmeasured tracked metric StratifiedFieldRecovery = (%+v, %v), want StatusPendingData", stratUnmeas, err)
	}
}

func addModelSessions(t *testing.T, s *Store, prefix, model string, n int, base time.Time) {
	t.Helper()
	for i := 0; i < n; i++ {
		ts := base.Add(-time.Duration(i) * time.Minute).UnixMilli()
		if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, model, started_at, ended_at) VALUES (?, 'agy', ?, ?, ?)`,
			fmt.Sprintf("%s-%d", prefix, i), model, ts, ts+1000); err != nil {
			t.Fatal(err)
		}
	}
}

func seedShiftSessions(t *testing.T, s *Store, now time.Time) {
	t.Helper()
	addModelSessions(t, s, "new", "gemini-5-pro", 4, now.Add(-time.Hour))
	addModelSessions(t, s, "old-cur", "gemini-4-neon-xhigh", 1, now.Add(-2*24*time.Hour))
	addModelSessions(t, s, "old-pri", "gemini-4-neon-xhigh", 1, now.Add(-10*24*time.Hour))
}

func seedSIMBrainV5(t *testing.T, path string) {
	t.Helper()
	sqliteT(t, path,
		`PRAGMA user_version = 5`,
		`CREATE TABLE records(rid INTEGER PRIMARY KEY, scope TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL, date TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, project TEXT NOT NULL DEFAULT '', updated TEXT NOT NULL, provenance TEXT NOT NULL DEFAULT '', UNIQUE(scope, id))`,
		`CREATE TABLE sim_problems(pid INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, root_cause TEXT NOT NULL, norm_root_cause TEXT NOT NULL, mechanism_class TEXT NOT NULL DEFAULT '', signal_id TEXT NOT NULL DEFAULT '', target_paths TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'active', provenance TEXT NOT NULL DEFAULT 'derived', opened_at TEXT NOT NULL, updated_at TEXT NOT NULL, opened_harness TEXT NOT NULL DEFAULT '', opened_model TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE sim_attempts(id INTEGER PRIMARY KEY AUTOINCREMENT, problem_id TEXT NOT NULL, source_kind TEXT NOT NULL, proposal_id TEXT NOT NULL DEFAULT '', commit_sha TEXT NOT NULL DEFAULT '', parent_sha TEXT NOT NULL DEFAULT '', harness_version TEXT NOT NULL DEFAULT '', proposer_model TEXT NOT NULL DEFAULT '', policy_model TEXT NOT NULL DEFAULT '', project_scope TEXT NOT NULL DEFAULT '', files_json TEXT NOT NULL DEFAULT '[]', what TEXT NOT NULL, predicted_rows TEXT NOT NULL DEFAULT '[]', predicted_signals TEXT NOT NULL DEFAULT '[]', eval_verdict TEXT NOT NULL DEFAULT 'none', fixed_rows TEXT NOT NULL DEFAULT '[]', missed_rows TEXT NOT NULL DEFAULT '[]', regressed_rows TEXT NOT NULL DEFAULT '[]', eval_passed INTEGER NOT NULL DEFAULT 0, eval_total INTEGER NOT NULL DEFAULT 0, field_status TEXT NOT NULL DEFAULT 'n/a', field_metric TEXT NOT NULL DEFAULT '', field_baseline REAL, field_observed REAL, field_models_json TEXT NOT NULL DEFAULT '[]', provenance TEXT NOT NULL DEFAULT 'derived', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`INSERT INTO sim_problems(pid, id, root_cause, norm_root_cause, mechanism_class, status, opened_at, updated_at, opened_model) VALUES
			(1, 'SIM-P001', 'rc1', 'rc1', 'model_workaround', 'active', '2026-11-10T00:00:00Z', '2026-11-20T00:00:00Z', 'gemini-4-neon-xhigh'),
			(2, 'SIM-P002', 'rc2', 'rc2', 'model_workaround', 'active', '2026-11-10T00:00:00Z', '2026-11-19T00:00:00Z', 'gemini-4-neon-xhigh'),
			(3, 'SIM-P003', 'rc3', 'rc3', 'prompt_guidance',  'active', '2026-11-10T00:00:00Z', '2026-11-18T00:00:00Z', ''),
			(4, 'SIM-P004', 'rc4', 'rc4', 'prompt_guidance',  'active', '2026-11-10T00:00:00Z', '2026-11-17T00:00:00Z', ''),
			(5, 'SIM-P005', 'rc5', 'rc5', 'prompt_guidance',  'active', '2026-11-10T00:00:00Z', '2026-11-16T00:00:00Z', ''),
			(6, 'SIM-P006', 'rc6', 'rc6', 'prompt_guidance',  'active', '2026-11-10T00:00:00Z', '2026-11-15T00:00:00Z', ''),
			(7, 'SIM-P007', 'rc7', 'rc7', 'model_workaround', 'active', '2026-11-10T00:00:00Z', '2026-11-21T00:00:00Z', 'gemini-4-neon-xhigh'),
			(8, 'SIM-P008', 'rc8', 'rc8', 'structural',       'active', '2026-11-10T00:00:00Z', '2026-11-21T00:00:00Z', 'gemini-4-neon-xhigh'),
			(9, 'SIM-P009', 'rc9', 'rc9', 'model_workaround', 'resolved', '2026-11-10T00:00:00Z', '2026-11-21T00:00:00Z', 'gemini-4-neon-xhigh'),
			(10, 'SIM-P010', 'rc10', 'rc10', 'model_workaround', 'active', '2026-11-10T00:00:00Z', '2026-11-21T00:00:00Z', 'gemini-4-neon-xhigh')`,
		`INSERT INTO sim_attempts(problem_id, source_kind, policy_model, what, eval_verdict, field_models_json, created_at, updated_at) VALUES
			('SIM-P001', 'auto_harness_edit', 'gemini-4-neon-xhigh', 'w1', 'kept', '[]', '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z'),
			('SIM-P002', 'auto_harness_edit', 'gemini-4-neon-xhigh', 'w2', 'kept', '[]', '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z'),
			('SIM-P003', 'auto_harness_edit', 'gemini-4-neon-xhigh', 'w3', 'kept', '[]', '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z'),
			('SIM-P004', 'auto_harness_edit', 'gemini-4-neon-xhigh', 'w4', 'kept', '[]', '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z'),
			('SIM-P005', 'auto_harness_edit', 'gemini-4-neon-xhigh', 'w5', 'kept', '[]', '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z'),
			('SIM-P006', 'auto_harness_edit', 'gemini-4-neon-xhigh', 'w6', 'kept', '[]', '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z'),
			('SIM-P007', 'auto_harness_edit', 'gemini-5-pro',        'w7', 'kept', '[]', '2026-11-21T00:00:00Z', '2026-11-21T00:00:00Z'),
			('SIM-P008', 'auto_harness_edit', 'gemini-4-neon-xhigh', 'w8', 'kept', '[]', '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z'),
			('SIM-P009', 'auto_harness_edit', 'gemini-4-neon-xhigh', 'w9', 'kept', '[]', '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z'),
			('SIM-P010', 'auto_harness_edit', 'gemini-4-neon-xhigh', 'w10', 'kept', '[{"model":"gemini-5-pro","status":"recovered","sessions":3}]', '2026-11-10T00:00:00Z', '2026-11-10T00:00:00Z')`,
	)
}

func findDigestSignal(signals []DigestAction, rule string) *DigestAction {
	for i := range signals {
		if signals[i].Rule == rule {
			return &signals[i]
		}
	}
	return nil
}
