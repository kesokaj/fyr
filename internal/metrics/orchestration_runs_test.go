package metrics

import (
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestOrchestrationRunsCRUD(t *testing.T) {
	t.Parallel()
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
	if err := RecordOrchestrationRun("", OrchestrationRunRow{ID: "x", Kind: "relay", Status: "OK"}); err == nil || errors.Is(err, ErrInvalidOrchestrationRun) {
		t.Fatalf("RecordOrchestrationRun with empty home = %v, want non-validation error", err)
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
		if err := s.InsertOrchestrationRun(bad); !errors.Is(err, ErrInvalidOrchestrationRun) {
			t.Errorf("InsertOrchestrationRun(%+v) = %v, want ErrInvalidOrchestrationRun", bad, err)
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

func TestShowWavesFiltersAndOrchestrationRepoIDMatch(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	s := openT(t, Path(home))
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	since, ts := now.Add(-24*time.Hour), now.Add(-2*time.Hour).UnixMilli()

	// 1. collectSuggestedSkills must exclude labeled eval sessions and ephemeral sandbox roots.
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, label, started_at, ended_at) VALUES
		('c-real', 'agy', '/work/real-app', NULL, ?, ?),
		('c-eval', 'agy', '/work/real-app', 'eval=smoke', ?, ?)`,
		ts, ts+500, ts+1000, ts+1500); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO turn_vets(project_root, conversation_id, turn_num, intent, gate, score, suggested_skills, created_at) VALUES
		('/work/real-app', 'c-real', 1, 'TASK', 'PROCEED', 80, '["golang"]', ?),
		('/work/real-app', 'c-eval', 1, 'TASK', 'PROCEED', 80, '["eval-only-skill"]', ?),
		('/tmp/fyr-smoke-99', 'c-sb', 1, 'TASK', 'PROCEED', 80, '["sandbox-only-skill"]', ?)`,
		ts+10, ts+1010, ts+2010); err != nil {
		t.Fatal(err)
	}
	defSum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatalf("Show default: %v", err)
	}
	seenDef := map[string]int{}
	for _, st := range defSum.SkillTelemetry {
		seenDef[st.Skill] = st.Suggested
	}
	if seenDef["golang"] != 1 || seenDef["eval-only-skill"] != 0 || seenDef["sandbox-only-skill"] != 0 {
		t.Fatalf("default SkillTelemetry leaked labeled/sandbox suggestions: %+v", defSum.SkillTelemetry)
	}
	allSum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now, AllLabels: true, IncludeSandboxes: true})
	if err != nil {
		t.Fatalf("Show all: %v", err)
	}
	seenAll := map[string]int{}
	for _, st := range allSum.SkillTelemetry {
		seenAll[st.Skill] = st.Suggested
	}
	if seenAll["eval-only-skill"] != 1 || seenAll["sandbox-only-skill"] != 1 {
		t.Fatalf("AllLabels+IncludeSandboxes missed expected suggestions: %+v", allSum.SkillTelemetry)
	}

	// 2. queryOrchestrationRunsForShow must match repo_id even when project_root differs (e.g. imported state).
	repoDir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "https://github.com/org/imported-repo.git"}} {
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	rid := ResolveRepoID(repoDir)
	if rid == "" {
		t.Fatal("ResolveRepoID returned empty for git repo with origin")
	}
	if err := s.InsertOrchestrationRun(OrchestrationRunRow{
		ID: "imported-run-1", TS: now.Add(-time.Hour).Format(time.RFC3339),
		Kind: "relay", ProjectRoot: "", RepoID: rid, Status: "COMPLETE", Units: 2, Passed: 2, DurationMS: 1200,
	}); err != nil {
		t.Fatal(err)
	}
	projSum, err := s.Show(ShowOptions{Home: home, Project: repoDir, Since: since, Now: now})
	if err != nil {
		t.Fatalf("Show project: %v", err)
	}
	if projSum.Orchestration == nil || projSum.Orchestration.Runs != 1 {
		t.Fatalf("Show(Project=%s) missed orchestration run matched by repo_id %q: %+v", repoDir, rid, projSum.Orchestration)
	}
}
