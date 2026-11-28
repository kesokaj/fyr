package metrics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestReportJSONAndEmptyCost(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -7)
	ahome, _ := agyFixture(t)
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, s, Options{Home: ahome})
	sum, err := s.Show(ShowOptions{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}

	// Empty cost across all series suppresses "Cost per day (USD)" SVG chart.
	var htmlBuf bytes.Buffer
	if err := RenderReport(&htmlBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(htmlBuf.String(), "Cost per day (USD)") {
		t.Error("agy-only report with nil CostUSD drew Cost per day (USD) chart")
	}

	// RenderJSONReport & WriteJSONReport (0600, O_EXCL refusing overwrite).
	var jsonBuf bytes.Buffer
	if err := RenderJSONReport(&jsonBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	var parsed JSONReport
	if err := json.Unmarshal(jsonBuf.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal RenderJSONReport: %v\n%s", err, jsonBuf.String())
	}
	if parsed.GeneratedAt != "2026-11-20T12:00:00Z" || parsed.Scorecard.ToolCalls != sum.B.ToolCalls["agy"] {
		t.Errorf("unexpected JSONReport: %+v", parsed)
	}
	jsonOut := filepath.Join(t.TempDir(), "nested", "report.json")
	if err := WriteJSONReport(sum, nil, jsonOut, now); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(jsonOut)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat %s: err=%v mode=%v, want 0600", jsonOut, err, fi.Mode().Perm())
	}
	if err := WriteJSONReport(sum, nil, jsonOut, now); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("second WriteJSONReport = %v, want refusing to overwrite", err)
	}

	// DailyReportPath and WriteDailyReport quiet O_EXCL no-op on same day.
	home := t.TempDir()
	dailyPath := DailyReportPath(home, now)
	wantSuffix := filepath.Join(harness.StateDir, "reports", "2026-11-20-daily-metrics.html")
	if !strings.HasSuffix(dailyPath, wantSuffix) {
		t.Fatalf("DailyReportPath = %q, want suffix %q", dailyPath, wantSuffix)
	}
	if err := WriteDailyReport(sum, nil, home, since); err != nil {
		t.Fatalf("first WriteDailyReport: %v", err)
	}
	firstFile := DailyReportPath(home, since)
	info1, err := os.Stat(firstFile)
	if err != nil || info1.Mode().Perm() != 0o600 {
		t.Fatalf("daily report mode: err=%v mode=%v", err, info1.Mode().Perm())
	}
	sentinel := []byte("<!-- first daily write -->")
	if err := os.WriteFile(firstFile, sentinel, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteDailyReport(sum, nil, home, since); err != nil {
		t.Fatalf("second same-day WriteDailyReport = %v, want quiet nil", err)
	}
	gotBytes, err := os.ReadFile(firstFile)
	if err != nil || !bytes.Equal(gotBytes, sentinel) {
		t.Fatalf("second WriteDailyReport mutated existing file: %q", gotBytes)
	}
}

func TestReportScorecardAndAutonomy(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -7)
	prevTS := since.Add(-24 * time.Hour).UnixMilli()
	curTS := now.Add(-2 * time.Hour).UnixMilli()

	home := t.TempDir()
	proj := filepath.Join(t.TempDir(), "repo")
	seedGitBranchesForAutonomyT(t, proj, "work/auto-fix-1", "work/auto-fix-2", "feature/other")

	s := openT(t, Path(home))
	// Prior window session for deltas.
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, model, started_at, ended_at) VALUES ('P1', 'agy', ?, 'gemini-4-carbon', ?, ?)`,
		proj, prevTS, prevTS+30000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, context) VALUES ('P1', 'pu1', ?, 'user', '', 0), ('P1', 'pg1', ?, 'generation', '', 100000), ('P1', 'pt1', ?, 'tool', 'Bash', 0)`,
		prevTS+10, prevTS+20, prevTS+30); err != nil {
		t.Fatal(err)
	}

	// Current window: 6 sessions to exceed minDiagnosticSessions (5) and trigger > 5 signals.
	for i := 0; i < 6; i++ {
		sid := fmt.Sprintf("C%d", i)
		ts := curTS + int64(i)*1000
		if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, model, started_at, ended_at) VALUES (?, 'agy', ?, 'gemini-4-carbon', ?, ?)`,
			sid, proj, ts, ts+500); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, context) VALUES (?, ?, ?, 'user', 0), (?, ?, ?, 'generation', 320000)`,
			sid, sid+":u", ts+1, sid, sid+":g", ts+2); err != nil {
			t.Fatal(err)
		}
		// Grep calls (low search:grep), Bash errors + retry loops, redundant Read calls.
		for j := 0; j < 2; j++ {
			if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, is_error, cmd_hash) VALUES (?, ?, ?, 'tool', 'Bash', 1, 'samehash')`,
				sid, fmt.Sprintf("%s:b:%d", sid, j), ts+10+int64(j)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, cmd_hash) VALUES (?, ?, ?, 'tool', 'Read', 'filehash:1-20')`,
				sid, fmt.Sprintf("%s:r:%d", sid, j), ts+20+int64(j)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES (?, ?, ?, 'tool', 'Grep')`,
			sid, sid+":grep", ts+30); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO events(session_id, ts, kind, value) VALUES (?, ?, 'correction', '1'), (?, ?, 'bug', '1')`,
			sid, ts+40, sid, ts+41); err != nil {
			t.Fatal(err)
		}
	}
	// Guarded downgrade subagent.
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, parent_id, agent_type, model, started_at, ended_at) VALUES ('SUB_DOWN', 'agy', ?, 'C0', 'reviewer', 'gemini-3.8-flash', ?, ?)`,
		proj, curTS+50, curTS+100); err != nil {
		t.Fatal(err)
	}

	// Background runs + session ratings.
	for _, bg := range []BackgroundRunRow{
		{TS: curTS, Kind: "dream", Outcome: "pass", DurationMS: 1500, ProjectsChecked: 2, ProposalsWritten: 3, RatingsWritten: 2},
		{TS: curTS + 100, Kind: "dream", Outcome: "fail", DurationMS: 500, ProjectsChecked: 1},
		{TS: curTS + 200, Kind: "auto", Outcome: "pass", DurationMS: 2400, ProjectsChecked: 2, WorkerRan: 1, Applied: 1},
	} {
		if err := s.InsertBackgroundRun(bg); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertSessionRatings([]SessionRating{{
		SessionID: "C0", ProjectRoot: proj, EvaluatedAt: now.Add(-time.Hour).UnixMilli(),
		OutcomeScore: 4, TokenEfficiency: 5, PlanAlignment: "matched",
	}}); err != nil {
		t.Fatal(err)
	}

	// Seed brain.db with 2 queued and 1 closed proposal.
	bdb := filepath.Join(home, harness.StateDir, "brain.db")
	sqliteT(t, bdb,
		`CREATE TABLE records(rid INTEGER PRIMARY KEY, scope TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL, date TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, project TEXT NOT NULL DEFAULT '', updated TEXT NOT NULL, provenance TEXT NOT NULL DEFAULT '', UNIQUE(scope, id))`,
		fmt.Sprintf(`INSERT INTO records VALUES (1, '', 'PROPOSAL-001', 'proposal', 'queued', '2026-11-20', 'p1', 'b1', '%s', '2026-11-20T10:00:00Z', 'derived')`, proj),
		fmt.Sprintf(`INSERT INTO records VALUES (2, '', 'PROPOSAL-002', 'proposal', 'queued', '2026-11-20', 'p2', 'b2', '%s', '2026-11-20T10:01:00Z', 'derived')`, proj),
		fmt.Sprintf(`INSERT INTO records VALUES (3, '', 'PROPOSAL-003', 'proposal', 'accepted', '2026-11-20', 'p3', 'b3', '%s', '2026-11-20T10:02:00Z', 'derived')`, proj),
	)

	sum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if sum.Diagnostics == nil || len(sum.Diagnostics.Signals) < 6 {
		t.Fatalf("expected >= 6 diagnostic signals to test top-5 cap, got %+v", sum.Diagnostics)
	}
	if sum.Health.KeptAutoBranches != 2 || sum.Health.ProposalsQueued != 2 || sum.Health.ProposalsClosed != 1 {
		t.Fatalf("autonomy counts = branches:%d queued:%d closed:%d, want 2, 2, 1",
			sum.Health.KeptAutoBranches, sum.Health.ProposalsQueued, sum.Health.ProposalsClosed)
	}

	var htmlBuf, mdBuf bytes.Buffer
	if err := RenderReport(&htmlBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := RenderMarkdownReport(&mdBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	html, md := htmlBuf.String(), mdBuf.String()
	for _, want := range []string{
		"0. Scorecard",
		"Top-5 Diagnostic Signals",
		"Peak Context (>= 60% knee % & >= 300k target %)",
		"Search:Grep Ratio",
		"Repeat Reads %",
		"Blocked Ledger / Clamp Saves",
		"Tool Error Rate %",
		"Guarded Subagent Downgrades",
		"Prior Window",
		"sig:global:",
		"Background Autonomy",
		"Dream runs (pass / fail)",
		"2 (1 / 1)",
		"Auto runs (pass / fail)",
		"1 (1 / 0)",
		"Auto workers run / applied commits",
		"1 / 1",
		"Session ratings (outcome / token efficiency)",
		"1 (4.00 / 5.00)",
		"Kept work/auto-* branches",
		"PROPOSAL-* records (queued / closed)",
		"2 / 1",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
		if !strings.Contains(html, want) && !strings.Contains(html, esc(want)) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if n := strings.Count(md, "| sig:global:"); n != 5 {
		t.Errorf("markdown rendered %d diagnostic signal rows, want top 5", n)
	}

	// Clean degradation when git and brain.db are absent.
	emptyHome := t.TempDir()
	sEmpty := openT(t, Path(emptyHome))
	sumEmpty, err := sEmpty.Show(ShowOptions{Home: emptyHome, Project: filepath.Join(emptyHome, "no-git"), Since: since, Now: now})
	if err != nil {
		t.Fatalf("empty Show: %v", err)
	}
	if sumEmpty.Health.KeptAutoBranches != 0 || sumEmpty.Health.ProposalsQueued != 0 || sumEmpty.Health.ProposalsClosed != 0 {
		t.Fatalf("absent git/brain did not degrade to 0: %+v", sumEmpty.Health)
	}
}

func seedGitBranchesForAutonomyT(t *testing.T, dir string, branches ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "main.go")
	runGit("-c", "user.email=t@example.invalid", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")
	for _, b := range branches {
		runGit("branch", b)
	}
}

func TestScorecardSecondBrainLatencyAndBypasses(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	since := now.Add(-24 * time.Hour)
	ts := now.Add(-2 * time.Hour).UnixMilli()
	s := openT(t, Path(home))
	seedScorecardSecondBrainT(t, s, "/proj/sc", ts)
	sum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	var mdBuf, htmlBuf, jsonBuf bytes.Buffer
	if err := RenderMarkdownReport(&mdBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := RenderReport(&htmlBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := RenderJSONReport(&jsonBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Bash Read Bypasses", "PreInvocation Timeouts & Latency", "50.0% (1 / 2) (p50 15ms, p95 260ms)"} {
		if !strings.Contains(mdBuf.String(), want) || !strings.Contains(htmlBuf.String(), esc(want)) {
			t.Errorf("missing %q in md/html", want)
		}
	}
	var jr JSONReport
	if err := json.Unmarshal(jsonBuf.Bytes(), &jr); err != nil {
		t.Fatal(err)
	}
	if jr.Scorecard.BashReadSkips != 1 || jr.Scorecard.PreInvocationTimeoutPct == nil || *jr.Scorecard.PreInvocationTimeoutPct != 50.0 ||
		jr.Scorecard.PreInvocationP50MS == nil || *jr.Scorecard.PreInvocationP50MS != 15 || jr.Scorecard.PreInvocationP95MS == nil || *jr.Scorecard.PreInvocationP95MS != 260 {
		t.Fatalf("Scorecard JSON = %+v", jr.Scorecard)
	}
}

func seedScorecardSecondBrainT(t *testing.T, s *Store, root string, ts int64) {
	t.Helper()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES ('SC1', 'agy', ?, ?, ?)`, []any{root, ts, ts + 5000}},
		{`INSERT INTO steps(session_id, ext_id, ts, kind, tool, denied) VALUES ('SC1', 'c1', ?, 'tool', 'Bash', 0), ('SC1', 'c2', ?, 'tool', 'Bash', 1)`, []any{ts + 100, ts + 200}},
		{`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES ('SC1', 'c1', 0, 'cat'), ('SC1', 'c2', 0, 'cat')`, nil},
		{`INSERT INTO turn_vets(project_root, conversation_id, turn_num, intent, gate, score, duration_ms, timed_out, injected_bytes, pivots_used_count, skills_loaded_count, created_at) VALUES
			(?, 'SC1', 1, 'TASK', 'PROCEED', 90, 15, 0, 500, 1, 1, ?), (?, 'SC1', 2, 'TASK', 'PROCEED', 60, 260, 1, 100, 0, 0, ?)`, []any{root, ts, root, ts + 10}},
	} {
		if _, err := s.db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestV910ReportKPIs(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	since := now.Add(-24 * time.Hour)
	ts := now.Add(-2 * time.Hour).UnixMilli()
	root := "/proj/v910kpi"

	s := openT(t, Path(home))
	seedV910KPIFixtureT(t, s, root, ts)

	sum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	// F-023: sv.n > 0 pipe greps excluded from overall and per-project grep counts;
	// only standalone sv.n == 0 grep ('grep -rn') counts (1), against 2 fyr search verbs -> ratio 2.0.
	if sum.C.SQLiteVerbs["agy"] != 2 || sum.C.GrepCalls["agy"] != 1 {
		t.Fatalf("F-023 overall counts: sqlite=%d grep=%d, want 2, 1", sum.C.SQLiteVerbs["agy"], sum.C.GrepCalls["agy"])
	}
	if sum.C.SearchGrepRatio["agy"] == nil || *sum.C.SearchGrepRatio["agy"] != 2.0 {
		t.Fatalf("F-023 overall SearchGrepRatio = %v, want 2.0", sum.C.SearchGrepRatio["agy"])
	}
	if len(sum.Projects) != 1 || sum.Projects[0].SearchGrepRatio == nil || *sum.Projects[0].SearchGrepRatio != 2.0 {
		t.Fatalf("F-023 project SearchGrepRatio = %+v, want 2.0", sum.Projects)
	}

	// F-022: only unguarded bash reads count (unranged cat + repeated unguarded sed -n = 2);
	// allowed first-time ranged reads (head -n, tail -n, sed -n, bat -r) and denied cat are excluded.
	if got := sum.C.BashReadSkips["agy"]; got != 2 {
		t.Fatalf("F-022 BashReadSkips = %d, want 2", got)
	}

	// F-028: 1 ledger block present in BOTH turn_vets.ledger_saved=1 and hook_block('ledger')
	// must deduplicate to 1 (never 2).
	var jsonBuf, mdBuf, htmlBuf bytes.Buffer
	if err := RenderJSONReport(&jsonBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := RenderMarkdownReport(&mdBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := RenderReport(&htmlBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	var jr JSONReport
	if err := json.Unmarshal(jsonBuf.Bytes(), &jr); err != nil {
		t.Fatal(err)
	}
	if jr.Scorecard.BlockedLedgerSave != 1 {
		t.Fatalf("F-028 BlockedLedgerSave = %d, want 1 (must not double-count E.LedgerSaved + HookBlocksLedger)", jr.Scorecard.BlockedLedgerSave)
	}
	if !strings.Contains(mdBuf.String(), "1 (ledger:1, clamp:0)") || !strings.Contains(htmlBuf.String(), "1 (ledger:1, clamp:0)") {
		t.Fatalf("F-028 scorecard did not deduplicate ledger saves:\n%s", mdBuf.String())
	}
}

func seedV910KPIFixtureT(t *testing.T, s *Store, root string, ts int64) {
	t.Helper()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES ('K1', 'agy', ?, ?, ?)`, []any{root, ts, ts + 9000}},
		{`INSERT INTO steps(session_id, ext_id, ts, kind, tool, denied, cmd_hash) VALUES
			('K1', 's1', ?, 'tool', 'Bash', 0, 'h_search1'),
			('K1', 's2', ?, 'tool', 'Bash', 0, 'h_search2'),
			('K1', 's3', ?, 'tool', 'Bash', 0, 'h_pipe_grep'),
			('K1', 's4', ?, 'tool', 'Bash', 0, 'h_direct_grep'),
			('K1', 's5', ?, 'tool', 'Bash', 0, 'h_cat_raw'),
			('K1', 's6', ?, 'tool', 'Bash', 1, 'h_cat_denied'),
			('K1', 's7', ?, 'tool', 'Bash', 0, 'h_head'),
			('K1', 's8', ?, 'tool', 'Bash', 0, 'h_tail'),
			('K1', 's9', ?, 'tool', 'Bash', 0, 'h_bat_ranged'),
			('K1', 's10', ?, 'tool', 'Bash', 0, 'h_sed_same'),
			('K1', 's11', ?, 'tool', 'Bash', 0, 'h_sed_same'),
			('K1', 's12', ?, 'tool', 'Read', 1, 'r:abc:1-20')`,
			[]any{ts + 10, ts + 20, ts + 30, ts + 40, ts + 50, ts + 60, ts + 70, ts + 80, ts + 90, ts + 100, ts + 110, ts + 120}},
		{`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES
			('K1', 's1', 0, 'fyr search --slice'),
			('K1', 's2', 0, 'fyr search --refs'),
			('K1', 's3', 0, 'go test'), ('K1', 's3', 1, 'grep'), ('K1', 's3', 2, 'rg'),
			('K1', 's4', 0, 'grep -rn'),
			('K1', 's5', 0, 'cat'),
			('K1', 's6', 0, 'cat'),
			('K1', 's7', 0, 'head -n'),
			('K1', 's8', 0, 'tail -n'),
			('K1', 's9', 0, 'bat -r'),
			('K1', 's10', 0, 'sed -n'),
			('K1', 's11', 0, 'sed -n')`, nil},
		{`INSERT INTO events(session_id, ts, kind, ref, value) VALUES ('K1', ?, 'hook_block', 's12', 'ledger')`, []any{ts + 120}},
		{`INSERT INTO turn_vets(project_root, conversation_id, turn_num, intent, gate, score, ledger_saved, created_at) VALUES (?, 'K1', 1, 'TASK', 'PROCEED', 90, 1, ?)`, []any{root, ts}},
	} {
		if _, err := s.db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
}

func TestV910ReportKPIsSIMSkillsAndRecordRun(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	since := now.Add(-24 * time.Hour)
	ts := now.Add(-2 * time.Hour).UnixMilli()
	root := "/proj/v910full"

	// #28: Dynamic installed skill catalog (no hardcoded names).
	seedInstalledSkillsCatalogT(t, home)

	// F-051: brain.db SIM tables (active/resolved problems, workarounds, attempts).
	seedSIMBrainFixtureT(t, home)

	s := openT(t, Path(home))
	seedV910SkillsAndRatingsFixtureT(t, s, root, ts)

	// F-032: orchestration_runs via RecordOrchestrationRun.
	runRow := OrchestrationRunRow{
		ID: "orch-1", TS: now.Add(-time.Hour).UTC().Format(time.RFC3339),
		Kind: "swarm", ProjectRoot: root, Status: "ok",
		Units: 4, DefectRounds: 1, Failed: 1, DurationMS: 3600,
		WorkerModel: "gemini-4-carbon", JudgeModel: "claude-opus-5-5", Verdict: "PASS",
	}
	runRow.Passed += 3
	if err := s.InsertOrchestrationRun(runRow); err != nil {
		t.Fatalf("InsertOrchestrationRun: %v", err)
	}

	sum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	assertV910OrchestrationSIMAndSkillsT(t, sum)

	var jsonBuf, mdBuf, htmlBuf bytes.Buffer
	if err := RenderJSONReport(&jsonBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := RenderMarkdownReport(&mdBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := RenderReport(&htmlBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	var jr JSONReport
	if err := json.Unmarshal(jsonBuf.Bytes(), &jr); err != nil {
		t.Fatal(err)
	}
	if jr.Orchestration == nil || jr.Orchestration.Runs != 1 || jr.SIM == nil || !jr.SIM.Available || len(jr.SkillTelemetry) == 0 || len(jr.RuleTelemetry) == 0 {
		t.Fatalf("JSONReport missing v910 panels: orch=%+v sim=%+v skills=%+v rules=%+v", jr.Orchestration, jr.SIM, jr.SkillTelemetry, jr.RuleTelemetry)
	}
	for _, want := range []string{
		"Orchestration runs (units / ok / fail / rounds)",
		"SIM Summary",
		"Skill & Rule Telemetry",
		"Rule Telemetry",
		"Skill catalog coverage",
		"custom-swarm",
	} {
		if !strings.Contains(mdBuf.String(), want) {
			t.Errorf("markdown missing %q:\n%s", want, mdBuf.String())
		}
		if !strings.Contains(htmlBuf.String(), want) && !strings.Contains(htmlBuf.String(), esc(want)) {
			t.Errorf("HTML missing %q", want)
		}
	}
}

func assertV910OrchestrationSIMAndSkillsT(t *testing.T, sum *Summary) {
	t.Helper()
	if sum.Orchestration == nil || sum.Orchestration.Runs != 1 || sum.Orchestration.Units != 4 || sum.Orchestration.Succeeded != 3 || sum.Orchestration.Failed != 1 || sum.Orchestration.DefectRounds != 1 {
		t.Fatalf("F-032 Orchestration = %+v", sum.Orchestration)
	}
	if sum.SIM == nil || !sum.SIM.Available || sum.SIM.ActiveProblems != 2 || sum.SIM.ResolvedProblems != 1 || sum.SIM.Workarounds != 2 || sum.SIM.ActiveWorkarounds != 1 || sum.SIM.Attempts != 2 {
		t.Fatalf("F-051 SIM = %+v", sum.SIM)
	}
	tc := sum.TriggerCoverage
	if tc.InstalledSkillsCount != 3 || tc.TriggeredSkillsCount != 2 || tc.SwarmSkillLoads != 1 || tc.BuildTimeQuestions != 0 || len(tc.UntriggeredSkills) != 1 || tc.UntriggeredSkills[0] != "unused-skill" {
		t.Fatalf("#28 TriggerCoverage = %+v", tc)
	}
	bySkill := map[string]SkillTelemetry{}
	for _, row := range sum.SkillTelemetry {
		bySkill[row.Skill] = row
	}
	sw := bySkill["custom-swarm"]
	if sw.Suggested != 1 || sw.Loaded != 1 || sw.Converted != 1 || sw.ConversionRatePct == nil || *sw.ConversionRatePct != 100.0 ||
		sw.MeanOutcomeScore == nil || *sw.MeanOutcomeScore != 4.0 || sw.ToolErrors != 1 || sw.RewriteSkillCount != 1 {
		t.Fatalf("F-042 custom-swarm SkillTelemetry = %+v", sw)
	}
	if len(sum.RuleTelemetry) == 0 {
		t.Fatalf("F-042 RuleTelemetry empty")
	}
}

func seedInstalledSkillsCatalogT(t *testing.T, home string) {
	t.Helper()
	skills := map[string]string{
		"custom-interview": "---\nname: custom-interview\ndescription: pre-build interview via ask_question\n---\nRun pre-build interview.\n",
		"custom-swarm":     "---\nname: custom-swarm\ndescription: parallel orchestration via run_workflow\n---\nCoordinate parallel workers.\n",
		"unused-skill":     "---\nname: unused-skill\ndescription: rarely invoked helper\n---\nBody.\n",
	}
	for dir, body := range skills {
		p := filepath.Join(home, harness.AgyRoot, "skills", dir, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func seedSIMBrainFixtureT(t *testing.T, home string) {
	t.Helper()
	bdb := filepath.Join(home, harness.StateDir, "brain.db")
	if err := os.MkdirAll(filepath.Dir(bdb), 0o700); err != nil {
		t.Fatal(err)
	}
	sqliteT(t, bdb,
		`PRAGMA user_version = 5`,
		`CREATE TABLE sim_problems(pid INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, root_cause TEXT NOT NULL, norm_root_cause TEXT NOT NULL, mechanism_class TEXT NOT NULL DEFAULT '', signal_id TEXT NOT NULL DEFAULT '', target_paths TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'active', provenance TEXT NOT NULL DEFAULT 'derived', opened_at TEXT NOT NULL, updated_at TEXT NOT NULL, opened_harness TEXT NOT NULL DEFAULT '', opened_model TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE sim_attempts(id INTEGER PRIMARY KEY AUTOINCREMENT, problem_id TEXT NOT NULL, source_kind TEXT NOT NULL, eval_verdict TEXT NOT NULL DEFAULT 'none', field_status TEXT NOT NULL DEFAULT 'n/a', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`INSERT INTO sim_problems(pid, id, root_cause, norm_root_cause, mechanism_class, status, opened_at, updated_at) VALUES
			(1, 'SIM-P001', 'cause1', 'cause1', 'model_workaround', 'active', '2026-11-20T00:00:00Z', '2026-11-22T00:00:00Z'),
			(2, 'SIM-P002', 'cause2', 'cause2', 'code_guard', 'active', '2026-11-20T00:00:00Z', '2026-11-22T00:00:00Z'),
			(3, 'SIM-P003', 'cause3', 'cause3', 'model_workaround', 'resolved', '2026-11-19T00:00:00Z', '2026-11-21T00:00:00Z')`,
		`INSERT INTO sim_attempts(problem_id, source_kind, eval_verdict, field_status, created_at, updated_at) VALUES
			('SIM-P001', 'manual', 'kept', 'improved', '2026-11-21T00:00:00Z', '2026-11-21T00:00:00Z'),
			('SIM-P002', 'auto', 'reverted', 'pending_data', '2026-11-22T00:00:00Z', '2026-11-22T00:00:00Z')`,
	)
}

func seedV910SkillsAndRatingsFixtureT(t *testing.T, s *Store, root string, ts int64) {
	t.Helper()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES ('SK1', 'agy', ?, ?, ?)`, []any{root, ts, ts + 5000}},
		{`INSERT INTO steps(session_id, ext_id, ts, kind, tool, skill, is_error) VALUES
			('SK1', 'k1', ?, 'tool', 'Read', 'custom-interview', 0),
			('SK1', 'k2', ?, 'tool', 'agy:ask_question', '', 0),
			('SK1', 'k3', ?, 'tool', 'Read', 'custom-swarm', 0),
			('SK1', 'k4', ?, 'tool', 'Bash', '', 1)`,
			[]any{ts + 10, ts + 20, ts + 30, ts + 40}},
		{`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES ('SK1', 'k4', 0, 'cat')`, nil},
		{`INSERT INTO turn_vets(project_root, conversation_id, turn_num, intent, gate, score, suggested_skills, created_at) VALUES
			(?, 'SK1', 1, 'TASK', 'PROCEED', 90, 'custom-swarm,unused-skill', ?)`, []any{root, ts}},
	} {
		if _, err := s.db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	if err := s.UpsertSessionRatings([]SessionRating{{
		SessionID: "SK1", ProjectRoot: root, EvaluatedAt: ts + 6000,
		OutcomeScore: 4, TokenEfficiency: 4, PlanAlignment: "matched",
		SkillAction: "rewrite-skill", TargetSkill: "custom-swarm",
	}}); err != nil {
		t.Fatal(err)
	}
}
