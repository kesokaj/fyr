package metrics

import (
	"math"
	"testing"
	"time"
)

func TestModelEvidenceV910PolicyModelWeightsDecayAndRoles(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	s := openT(t, Path(t.TempDir()))

	// 1. PolicyModel takes precedence over session model, and works without a session row.
	if err := s.RecordTrialInput(TrialInput{
		Run: "eval/pm-override/1", Label: "eval=builder-task", Kind: "capability",
		Status: "pass", StartedAt: now.Add(-2 * time.Hour), Seconds: 12,
		Model: "session-fallback-model", PolicyModel: "gemini-4-neon-high",
	}); err != nil {
		t.Fatalf("RecordTrialInput pm-override: %v", err)
	}
	if err := s.RecordTrial(Trial{
		Run: "eval/pm-only/1", Label: "eval=reviewer-audit", Kind: "capability",
		Harness: "v9.10.0", Status: "pass", StartedAt: now.Add(-2 * time.Hour),
		Seconds: 8, PolicyModel: "claude-opus-5-5-high",
	}); err != nil {
		t.Fatalf("RecordTrial pm-only: %v", err)
	}
	// Fallback to session model when policy_model is empty.
	if err := s.RecordTrialInput(TrialInput{
		Run: "eval/session-fallback/1", Label: "eval=coding", Kind: "capability",
		Status: "fail", StartedAt: now.Add(-2 * time.Hour), Seconds: 10,
		Model: "gemini-4-carbon-high",
	}); err != nil {
		t.Fatalf("RecordTrialInput session-fallback: %v", err)
	}

	// 2. kind='bench' weighted at 0.20 vs real eval at 1.0.
	if err := s.RecordTrialInput(TrialInput{
		Run: "bench/20261122/1/gemini-5-nova-high", Label: "bench=sanity", Kind: "bench",
		Status: "pass", StartedAt: now, Seconds: 2,
		Model: "gemini-5-nova-high", PolicyModel: "gemini-5-nova-high",
	}); err != nil {
		t.Fatalf("RecordTrialInput bench: %v", err)
	}

	// 3. 56-day decay window: 45d-old trial included, 60d-old trial excluded.
	if err := s.RecordTrial(Trial{
		Run: "eval/old-45d/1", Label: "eval=builder-45d", Kind: "capability",
		Harness: "v9.10.0", Status: "pass", StartedAt: now.AddDate(0, 0, -45),
		Seconds: 5, PolicyModel: "gemini-4-window-high",
	}); err != nil {
		t.Fatalf("RecordTrial 45d: %v", err)
	}
	if err := s.RecordTrial(Trial{
		Run: "eval/old-60d/1", Label: "eval=builder-60d", Kind: "capability",
		Harness: "v9.10.0", Status: "pass", StartedAt: now.AddDate(0, 0, -60),
		Seconds: 5, PolicyModel: "gemini-4-expired-high",
	}); err != nil {
		t.Fatalf("RecordTrial 60d: %v", err)
	}

	// 4. Judge verdict (weight 0.9) and session rating (weight 0.75 + MeanTokenEfficiency + role partition).
	if _, err := s.db.Exec(
		`INSERT INTO judge_verdicts(ts, target, model, criterion_hash, verdict, worker_model) VALUES (?, 't1', 'claude-opus-5-5-high', 'h1', 'PASS', 'gemini-4-neon-high')`,
		now.UnixMilli(),
	); err != nil {
		t.Fatalf("insert judge_verdict: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO sessions(id, vendor, agent_type, model, started_at, ended_at) VALUES
			('s-rev', 'agy', 'reviewer', 'claude-opus-5-5-high', ?, ?),
			('s-bld', 'agy', 'builder', 'gemini-4-neon-high', ?, ?)`,
		now.Add(-time.Hour).UnixMilli(), now.UnixMilli(),
		now.Add(-time.Hour).UnixMilli(), now.UnixMilli(),
	); err != nil {
		t.Fatalf("insert sessions: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO session_ratings(session_id, evaluated_at, outcome_score, token_efficiency, plan_alignment, skill_action) VALUES
			('s-rev', ?, 5, 5, 'matched', 'none'),
			('s-bld', ?, 4, 3, 'matched', 'none')`,
		now.UnixMilli(), now.UnixMilli(),
	); err != nil {
		t.Fatalf("insert session_ratings: %v", err)
	}

	ev, err := s.ModelEvidence(time.Time{}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ModelEvidence: %v", err)
	}
	if _, leaked := ev["session-fallback-model"]; leaked {
		t.Errorf("policy_model did not override session model: %+v", ev["session-fallback-model"])
	}
	if got := ev["gemini-4-carbon-high"]; got.TrialRuns != 1 || got.TrialPass != 0 {
		t.Errorf("session fallback model mismatch: %+v", got)
	}
	nova := ev["gemini-5-nova-high"]
	if math.Abs(nova.DecayedRuns-0.20) > 0.01 || math.Abs(nova.DecayedSuccesses-0.20) > 0.01 {
		t.Errorf("bench trial weight = (%.4f, %.4f), want ~0.20", nova.DecayedRuns, nova.DecayedSuccesses)
	}
	if _, ok := ev["gemini-4-window-high"]; !ok {
		t.Errorf("45d-old trial excluded; expected 56d window")
	}
	if _, ok := ev["gemini-4-expired-high"]; ok {
		t.Errorf("60d-old trial included; expected 56d cutoff")
	}
	neon := ev["gemini-4-neon-high"]
	// 1.0 (eval) + 0.9 (judge verdict) + 0.75 (rating) = ~2.65 decayed runs, all in BuilderEvidence.
	if math.Abs(neon.DecayedRuns-2.65) > 0.05 || neon.BuilderEvidence.Runs != 3 || neon.JudgeEvidence.Runs != 0 || math.Abs(neon.MeanTokenEfficiency-3.0) > 1e-4 {
		t.Errorf("neon evidence = %+v", neon)
	}
	opus := ev["claude-opus-5-5-high"]
	// 1.0 (reviewer eval) + 0.75 (reviewer rating) = ~1.75 decayed runs, all in JudgeEvidence.
	if math.Abs(opus.DecayedRuns-1.75) > 0.05 || opus.JudgeEvidence.Runs != 2 || opus.BuilderEvidence.Runs != 0 || math.Abs(opus.MeanTokenEfficiency-5.0) > 1e-4 {
		t.Errorf("opus evidence = %+v", opus)
	}
}
