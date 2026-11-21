package metrics

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordTrialInputLinksSessionWithoutLeakingContent(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	secretTok := strings.Join([]string{"gh", "p_", "SECRETTRAIL1234567890ABCDEF"}, "")

	if err := RecordTrial(home, TrialInput{
		Run:         "eval-gate/row1/1",
		Label:       "gate=row1",
		Status:      "fail",
		Cause:       "raw stderr prose with " + secretTok,
		StartedAt:   now,
		Seconds:     15,
		Model:       "gemini-4-neon-high",
		ProjectRoot: "/Users/alice/private/repo",
		RepoID:      "git:github.com/org/repo",
	}); err != nil {
		t.Fatalf("RecordTrial: %v", err)
	}

	// Reject secret in Run or invalid status/model.
	for _, bad := range []TrialInput{
		{Run: "run-" + secretTok, Status: "pass"},
		{Run: "r2", Status: "pass", Cause: "unexpected"},
		{Run: "r3", Status: "bogus"},
		{Run: "r4", Status: "pass", Model: "not a model identifier!"},
	} {
		if err := RecordTrial(home, bad); err == nil {
			t.Errorf("RecordTrial(%+v) succeeded, want error", bad)
		}
	}

	s := openT(t, Path(home))
	var cause string
	if err := s.db.QueryRow(`SELECT cause FROM trials WHERE run = 'eval-gate/row1/1'`).Scan(&cause); err != nil || cause != "failed" {
		t.Fatalf("trial cause = %q, err=%v; want bucketed 'failed' without prose/secret", cause, err)
	}
	exp, err := ExportSanitizedState(s, "git:github.com/org/repo", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ExportSanitizedState: %v", err)
	}
	rawJSON, _ := json.Marshal(exp)
	for _, forbidden := range []string{secretTok, "/Users/alice/private/repo"} {
		if strings.Contains(string(rawJSON), forbidden) {
			t.Errorf("sanitized export leaked %q:\n%s", forbidden, string(rawJSON))
		}
	}
	if len(exp.Trials) != 1 || len(exp.Sessions) != 1 || exp.Sessions[0].Model != "gemini-4-neon-high" {
		t.Fatalf("unexpected export: trials=%+v sessions=%+v", exp.Trials, exp.Sessions)
	}
}

func TestExportAndImportSanitizedStateFilteringDryRunAndIdempotency(t *testing.T) {
	src := openT(t, filepath.Join(t.TempDir(), "src.db"))
	salt := src.meta(t, "salt")
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	oldTS := now.Add(-10 * 24 * time.Hour)
	curTS := now.Add(-2 * time.Hour)
	const repoA = "git:github.com/org/alpha"
	const repoB = "git:github.com/org/beta"
	secret := strings.Join([]string{"gh", "p_", "TOPSECRETEXPORT999999999999"}, "")

	// Seed repoA (1 old, 1 current) and repoB (1 current) sessions, steps with cmd_hash, trials, vets, ratings, verdicts.
	for _, q := range []string{
		`INSERT INTO sessions(id, vendor, project_root, repo_id, model, outcome, run, started_at, ended_at) VALUES ('sA-old', 'agy', '/Users/bob/alpha', '` + repoA + `', 'gemini-4-neon-high', 'pass', 'runA-old', ?, ?)`,
		`INSERT INTO sessions(id, vendor, project_root, repo_id, model, outcome, run, started_at, ended_at) VALUES ('sA-cur', 'agy', '/Users/bob/alpha', '` + repoA + `', 'gemini-4-neon-high', 'pass', 'runA-cur', ?, ?)`,
		`INSERT INTO sessions(id, vendor, project_root, repo_id, model, outcome, run, started_at, ended_at) VALUES ('sB-cur', 'agy', '/Users/bob/beta', '` + repoB + `', 'gemini-4-neon-low', 'fail', 'runB-cur', ?, ?)`,
	} {
		ts := curTS.UnixMilli()
		if strings.Contains(q, "sA-old") {
			ts = oldTS.UnixMilli()
		}
		if _, err := src.db.Exec(q, ts, ts+5000); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := src.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, skill, tok_in, cmd_hash) VALUES ('sA-cur', 'sA-cur:1', ?, 'tool', 'Bash', 'golang', 500, 'deadbeefcafebabe')`, curTS.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := src.db.Exec(`INSERT INTO events(session_id, ts, kind, ref, value) VALUES ('sA-cur', ?, 'commit', 'abc1234', '')`, curTS.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	for _, tr := range []Trial{
		{Run: "runA-old", Label: "eval=a", Harness: "h1", Status: "pass", StartedAt: oldTS, Seconds: 5},
		{Run: "runA-cur", Label: "eval=a", Harness: "h1", Status: "pass", StartedAt: curTS, Seconds: 6},
		{Run: "runB-cur", Label: "eval=b", Harness: "h1", Status: "fail", Cause: "check", StartedAt: curTS, Seconds: 7},
	} {
		if err := src.RecordTrial(tr); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := src.db.Exec(`INSERT INTO turn_vets(project_root, repo_id, conversation_id, turn_num, intent, gate, score, directive, subtask_count, duration_ms, created_at) VALUES ('/Users/bob/alpha', ?, 'sA-cur', 1, 'TASK', 'PROCEED', 91, 'PASS_THROUGH', 2, 33, ?)`, repoA, curTS.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	tokAssign := strings.Join([]string{"tok", "en=", secret}, "")
	pwAssign := strings.Join([]string{"pass", "word=", secret}, "")
	if err := src.UpsertSessionRatings([]SessionRating{
		{SessionID: "sA-cur", ProjectRoot: "/Users/bob/alpha", RepoID: repoA, EvaluatedAt: curTS.UnixMilli(), OutcomeScore: 5, TokenEfficiency: 4, PlanAlignment: "matched", SkillAction: "none", Summary: tokAssign},
		{SessionID: "sB-cur", ProjectRoot: "/Users/bob/beta", RepoID: repoB, EvaluatedAt: curTS.UnixMilli(), OutcomeScore: 2, TokenEfficiency: 2, PlanAlignment: "missed", SkillAction: "none", Summary: "beta"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := src.RecordJudgeVerdict(JudgeVerdictRow{
		TS: curTS.UnixMilli(), ProjectRoot: "/Users/bob/alpha", RepoID: repoA, Target: "work/p1",
		Model: "claude-opus-5-5-high", WorkerModel: "gemini-4-neon-high", WorkerSessionID: "sA-cur",
		FallbackReason: pwAssign, CriterionHash: "hash1", Verdict: "PASS",
	}); err != nil {
		t.Fatal(err)
	}

	since := now.Add(-24 * time.Hour)
	exp, err := ExportSanitizedState(src, repoA, since)
	if err != nil {
		t.Fatalf("ExportSanitizedState: %v", err)
	}
	blob, err := json.Marshal(exp)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{salt, "deadbeefcafebabe", "/Users/bob", secret, "sB-cur", "runB-cur", "sA-old", "runA-old"} {
		if strings.Contains(string(blob), leak) {
			t.Errorf("export leaked %q:\n%s", leak, string(blob))
		}
	}
	if len(exp.Sessions) != 1 || len(exp.Steps) != 1 || len(exp.Events) != 1 || len(exp.Trials) != 1 || len(exp.TurnVets) != 1 || len(exp.SessionRatings) != 1 || len(exp.JudgeVerdicts) != 1 {
		t.Fatalf("unexpected filtered counts: %+v", exp)
	}

	dest := openT(t, filepath.Join(t.TempDir(), "dest.db"))
	dryCounts, err := ImportSanitizedState(dest, exp, true)
	if err != nil {
		t.Fatalf("ImportSanitizedState dryRun: %v", err)
	}
	if dryCounts.Inserted != 7 || dryCounts.Sessions != 1 || dryCounts.Trials != 1 || dryCounts.JudgeVerdicts != 1 {
		t.Errorf("dryRun counts = %+v, want 7 inserted", dryCounts)
	}
	var destSessCnt int
	dest.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&destSessCnt)
	if destSessCnt != 0 {
		t.Fatalf("dryRun wrote %d sessions", destSessCnt)
	}

	firstCounts, err := ImportSanitizedState(dest, exp, false)
	if err != nil || firstCounts != dryCounts {
		t.Fatalf("first import counts = %+v, err=%v; want %+v", firstCounts, err, dryCounts)
	}
	secondCounts, err := ImportSanitizedState(dest, exp, false)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if secondCounts.Inserted != 0 || secondCounts.Updated != 0 || secondCounts.Skipped != 7 {
		t.Errorf("idempotent second import counts = %+v, want Inserted=0 Updated=0 Skipped=7", secondCounts)
	}

	ev, err := dest.ModelEvidence(since, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("dest ModelEvidence: %v", err)
	}
	neon := ev["gemini-4-neon-high"]
	if neon.TrialRuns != 1 || neon.JudgeRuns != 1 || neon.RatingRuns != 1 {
		t.Errorf("imported ModelEvidence = %+v, want 1 trial, 1 judge, 1 rating", neon)
	}
}

func TestScrubHostHomePathsEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"space after /home/", "/home/ dir /home/alice/work", "/home/ dir ~/work"},
		{"double slash", "/home//alice/secret", "~/secret"},
		{"newline delim", "/home/alice\nnext line", "~\nnext line"},
		{"markdown punct", "`/Users/bob` and | /Users/carol |", "`~` and | ~ |"},
	}
	for _, tc := range cases {
		if got := ScrubHostHomePaths(tc.in); got != tc.want {
			t.Errorf("%s: ScrubHostHomePaths(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}

	s := openT(t, filepath.Join(t.TempDir(), "ratings.db"))
	now := time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC)
	if _, err := s.db.Exec(
		`INSERT INTO session_ratings(session_id, project_root, repo_id, evaluated_at, outcome_score, token_efficiency, plan_alignment, skill_action, target_skill, summary)
		 VALUES ('sess-/home/alice/work', '', 'git:github.com/org/r', ?, 5, 4, 'matched', 'keep /home/alice/sk', 'golang', 'ok')`,
		now.UnixMilli(),
	); err != nil {
		t.Fatal(err)
	}
	exp, err := ExportSanitizedState(s, "", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ExportSanitizedState: %v", err)
	}
	if len(exp.SessionRatings) != 1 {
		t.Fatalf("SessionRatings = %+v, want 1 row", exp.SessionRatings)
	}
	r := exp.SessionRatings[0]
	if strings.Contains(r.SessionID, "/home/alice") || strings.Contains(r.SkillAction, "/home/alice") {
		t.Errorf("SessionRating leaked home path: %+v", r)
	}
	if r.SessionID != "sess-~/work" || r.SkillAction != "keep ~/sk" {
		t.Errorf("SessionRating = %+v, want scrubbed SessionID/SkillAction", r)
	}
}
