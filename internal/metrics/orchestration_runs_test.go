package metrics

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOrchestrationRunsCRUD(t *testing.T) {
	home := t.TempDir()
	base := time.Date(2026, 11, 22, 16, 0, 0, 0, time.UTC)

	r1 := OrchestrationRunRow{
		ID: "relay-2026-11-22-001", TS: base.Add(-2 * time.Hour).Format(time.RFC3339),
		Kind: "relay", ProjectRoot: "/work/alpha", RepoID: "git:github.com/org/alpha",
		Status: "HALTED", HaltCode: "BUDGET_FLOOR", Units: 3, DefectRounds: 1,
		Passed: 2, Failed: 1, DurationMS: 45200,
		WorkerModel: "gemini-4-neon-high", JudgeModel: "claude-opus-5-5-high", Verdict: "FAIL",
	}
	r2 := OrchestrationRunRow{
		ID: "swarm-2026-11-22-002", TS: "2026-11-22T17:00:00+02:00", // 15:00:00Z
		Kind: "SWARM", ProjectRoot: "/work/beta", RepoID: "git:github.com/org/beta",
		Status: "COMPLETE", Units: 4, DefectRounds: 0,
		Passed: 4, Failed: 0, DurationMS: 98100,
		WorkerModel: "gemini-4-neon-medium", JudgeModel: "claude-sonnet-4-6", Verdict: "PASS",
	}
	r3 := OrchestrationRunRow{
		ID: "phase-loop-003", TS: "", // defaults to current UTC RFC3339
		Kind: "phase_loop", Status: "OK", Units: 1, Passed: 1, DurationMS: 1200,
	}
	for _, r := range []OrchestrationRunRow{r1, r2, r3} {
		if err := RecordOrchestrationRun(home, r); err != nil {
			t.Fatalf("RecordOrchestrationRun(%s): %v", r.ID, err)
		}
	}

	s := openT(t, Path(home))
	assertOrchestrationRunValidation(t, s)
	assertOrchestrationRunQueriesAndUpsert(t, s, base)
}

func assertOrchestrationRunValidation(t *testing.T, s *Store) {
	t.Helper()
	secretTok := strings.Join([]string{"gh", "p_", "ORCHSECRET1234567890ABCDEFGH"}, "")
	if err := RecordOrchestrationRun("", OrchestrationRunRow{ID: "x", Kind: "relay", Status: "OK"}); err == nil {
		t.Fatal("RecordOrchestrationRun with empty home succeeded, want error")
	}
	badRows := []OrchestrationRunRow{
		{ID: "", Kind: "relay", Status: "OK"},
		{ID: "has space", Kind: "relay", Status: "OK"},
		{ID: "id\x1b[2J", Kind: "relay", Status: "OK"},
		{ID: "id-" + secretTok, Kind: "relay", Status: "OK"},
		{ID: "ok-1", TS: "not-a-timestamp", Kind: "relay", Status: "OK"},
		{ID: "ok-2", Kind: "", Status: "OK"},
		{ID: "ok-3", Kind: "bad/kind", Status: "OK"},
		{ID: "ok-4", Kind: "relay", Status: ""},
		{ID: "ok-5", Kind: "relay", Status: "bad status"},
		{ID: "ok-6", Kind: "relay", Status: "HALTED", HaltCode: "bad/halt/code"},
		{ID: "ok-7", Kind: "relay", Status: "HALTED", HaltCode: secretTok},
		{ID: "ok-8", Kind: "relay", Status: "OK", Units: -1},
		{ID: "ok-9", Kind: "relay", Status: "OK", DefectRounds: -1},
		{ID: "ok-10", Kind: "relay", Status: "OK", Passed: -1},
		{ID: "ok-11", Kind: "relay", Status: "OK", Failed: -1},
		{ID: "ok-12", Kind: "relay", Status: "OK", DurationMS: -1},
	}
	for _, bad := range badRows {
		if err := s.InsertOrchestrationRun(bad); err == nil {
			t.Errorf("InsertOrchestrationRun(%+v) succeeded, want error", bad)
		}
	}
}

func assertOrchestrationRunQueriesAndUpsert(t *testing.T, s *Store, base time.Time) {
	t.Helper()
	// Query within [base-3h, base): should find r2 (15:00Z) then r1 (14:00Z), newest first.
	got, err := s.QueryOrchestrationRuns("", base.Add(-3*time.Hour), base, 10)
	if err != nil {
		t.Fatalf("QueryOrchestrationRuns: %v", err)
	}
	if len(got) != 2 || got[0].ID != "swarm-2026-11-22-002" || got[1].ID != "relay-2026-11-22-001" {
		t.Fatalf("window query = %+v, want [swarm-002, relay-001]", got)
	}
	if got[0].TS != "2026-11-22T15:00:00Z" || got[0].Kind != "swarm" {
		t.Errorf("normalized r2 = (%q, %q), want (2026-11-22T15:00:00Z, swarm)", got[0].TS, got[0].Kind)
	}
	if got[1].HaltCode != "BUDGET_FLOOR" || got[1].DefectRounds != 1 || got[1].WorkerModel != "gemini-4-neon-high" || got[1].JudgeModel != "claude-opus-5-5-high" || got[1].Verdict != "FAIL" {
		t.Errorf("r1 fields mismatch: %+v", got[1])
	}

	// Kind filter + limit=1 bound.
	byKind, err := s.QueryOrchestrationRuns("relay", time.Time{}, time.Time{}, 1)
	if err != nil || len(byKind) != 1 || byKind[0].ID != "relay-2026-11-22-001" {
		t.Fatalf("kind query = %+v, err=%v", byKind, err)
	}
	limited, err := s.QueryOrchestrationRuns("", time.Time{}, time.Time{}, 1)
	if err != nil || len(limited) != 1 {
		t.Fatalf("limit=1 query = %+v, err=%v", limited, err)
	}

	// Upsert same ID updates cleanly; default timestamp row is valid RFC3339.
	updated := got[1]
	updated.Status = "COMPLETE"
	updated.HaltCode = ""
	updated.Verdict = "PASS"
	if err := s.InsertOrchestrationRun(updated); err != nil {
		t.Fatalf("upsert InsertOrchestrationRun: %v", err)
	}
	after, err := s.QueryOrchestrationRuns("relay", time.Time{}, time.Time{}, 10)
	if err != nil || len(after) != 1 || after[0].Status != "COMPLETE" || after[0].HaltCode != "" || after[0].Verdict != "PASS" {
		t.Fatalf("after upsert = %+v, err=%v", after, err)
	}

	blob, err := json.Marshal(after[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"id":"relay-2026-11-22-001"`, `"defect_rounds":1`, `"duration_ms":45200`, `"worker_model":"gemini-4-neon-high"`, `"judge_model":"claude-opus-5-5-high"`} {
		if !strings.Contains(string(blob), key) {
			t.Errorf("JSON missing %s: %s", key, blob)
		}
	}
	if strings.Contains(string(blob), `"halt_code"`) {
		t.Errorf("empty halt_code not omitted: %s", blob)
	}
}
