package metrics

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestReportAccuracyFixes(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -7)
	ts := now.Add(-2 * time.Hour).UnixMilli()

	t.Run("M6_excludes_historical_claude_from_trigger_coverage", func(t *testing.T) {
		home := t.TempDir()
		s := openT(t, Path(home))
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES ('C_OLD', 'claude', '/p/m6', ?, ?), ('A_LIVE', 'agy', '/p/m6', ?, ?)`,
				[]any{ts, ts + 60000, ts + 120000, ts + 180000}},
		} {
			if _, err := s.db.Exec(q.sql, q.args...); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 10; i++ {
			if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('C_OLD', ?, ?, 'tool', 'Grep')`,
				fmt.Sprintf("c:%d", i), ts+int64(i)*100); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 3; i++ {
			ext := fmt.Sprintf("a:s:%d", i)
			if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('A_LIVE', ?, ?, 'tool', 'Bash')`, ext, ts+121000+int64(i)*100); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES ('A_LIVE', ?, 0, 'fyr search --slice')`, ext); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('A_LIVE', 'a:g:0', ?, 'tool', 'Grep')`, ts+125000); err != nil {
			t.Fatal(err)
		}

		sum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
		if err != nil {
			t.Fatalf("Show: %v", err)
		}
		if sum.C.GrepCalls["claude"] != 10 {
			t.Errorf("historical GrepCalls[claude] = %d, want 10", sum.C.GrepCalls["claude"])
		}
		tc := sum.TriggerCoverage
		if tc.SearchCalls != 3 || tc.GrepCalls != 1 || tc.SearchGrepRatio == nil || *tc.SearchGrepRatio != 3.0 {
			t.Fatalf("TriggerCoverage Search:Grep = %d:%d (ratio=%v), want 3:1 (3.0)", tc.SearchCalls, tc.GrepCalls, tc.SearchGrepRatio)
		}
		var md bytes.Buffer
		if err := RenderMarkdownReport(&md, sum, nil, now); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(md.String(), "| Search : grep | 3.00 (3 : 1) |") {
			t.Errorf("markdown Trigger coverage lacks 3.00 (3 : 1):\n%s", md.String())
		}
	})

	t.Run("M7_dev_eval_fallback_and_isolation", func(t *testing.T) {
		decoy := t.TempDir()
		seedDevEvalStoreT(t, decoy, "eval=decoy", "pass", now.Add(-time.Hour))
		t.Setenv("HOME", decoy)

		// Unit store not ending in <StateDir>/metrics.db with empty Home must never touch host $HOME.
		isolated := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
		evIso, err := ReportEvalsWithDevFallback(isolated, "", ShowOptions{Since: since, Now: now})
		if err != nil || len(evIso.Rows) != 0 {
			t.Fatalf("isolated fallback = %+v, err=%v; want 0 rows", evIso, err)
		}
		sumIso, err := isolated.Show(ShowOptions{Since: since, Now: now})
		if err != nil || len(sumIso.A.PassK) != 0 {
			t.Fatalf("isolated Show PassK = %+v, err=%v; want empty", sumIso.A.PassK, err)
		}

		// Real synthetic home with dev/fyr-eval/<StateDir>/metrics.db populated.
		home := t.TempDir()
		mainStore := openT(t, Path(home))
		devPath := filepath.Join(home, harness.StateDir, "dev", "fyr-eval", harness.StateDir, "metrics.db")
		devStore := openT(t, devPath)
		for i, st := range []struct {
			run, label, status, cause string
		}{
			{"r/smoke/1", "eval=smoke", "pass", ""},
			{"r/smoke/2", "eval=smoke", "pass", ""},
			{"r/edge/1", "eval=edge", "fail", "check_failed"},
		} {
			if err := devStore.RecordTrial(Trial{
				Run: st.run, Label: st.label, RowVersion: 1, Kind: "capability", Harness: "v92",
				Status: st.status, Cause: st.cause, StartedAt: now.Add(-time.Duration(i+1) * time.Hour), Seconds: 12,
			}); err != nil {
				t.Fatal(err)
			}
		}
		devStore.Close()

		ev, err := ReportEvalsWithDevFallback(mainStore, home, ShowOptions{Since: since, Now: now})
		if err != nil {
			t.Fatalf("ReportEvalsWithDevFallback: %v", err)
		}
		if ev == nil || len(ev.Rows) != 2 || ev.Suite.Trials != 3 {
			t.Fatalf("dev eval rows = %+v, want 2 rows and 3 trials", ev)
		}
		if projEv, err := ReportEvalsWithDevFallback(mainStore, home, ShowOptions{Project: "/p/a", Since: since, Now: now}); err != nil || projEv != nil {
			t.Fatalf("project scoped ReportEvalsWithDevFallback = %+v, err=%v; want nil", projEv, err)
		}

		sum, err := mainStore.Show(ShowOptions{Home: home, Since: since, Now: now})
		if err != nil {
			t.Fatalf("Show with dev eval fallback: %v", err)
		}
		if len(sum.A.PassK) != 2 || sum.A.PassK["eval=smoke"].PassK != 1 || sum.A.PassK["eval=edge"].PassK != 0 {
			t.Fatalf("merged PassK = %+v, want eval=smoke(pass^k=1) and eval=edge(pass^k=0)", sum.A.PassK)
		}
		var htmlBuf, mdBuf bytes.Buffer
		if err := RenderReport(&htmlBuf, sum, ev, now); err != nil {
			t.Fatal(err)
		}
		if err := RenderMarkdownReport(&mdBuf, sum, ev, now); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(htmlBuf.String(), "<h2>Eval rows</h2>") || !strings.Contains(htmlBuf.String(), "<td>smoke</td>") {
			t.Errorf("HTML missing dev eval fallback rows:\n%s", htmlBuf.String())
		}
		if !strings.Contains(mdBuf.String(), "## Eval rows") || !strings.Contains(mdBuf.String(), "| Eval pass^k / suite | 2 rows (3 trials) |") {
			t.Errorf("MD missing dev eval fallback rows:\n%s", mdBuf.String())
		}

		// Symlinked dev/fyr-eval metrics.db MUST be ignored.
		symHome := t.TempDir()
		_ = openT(t, Path(symHome))
		symDev := filepath.Join(symHome, harness.StateDir, "dev", "fyr-eval", harness.StateDir, "metrics.db")
		if err := os.MkdirAll(filepath.Dir(symDev), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(devPath, symDev); err != nil {
			t.Fatal(err)
		}
		symEv, err := ReportEvalsWithDevFallback(mainStore, symHome, ShowOptions{Since: since, Now: now})
		if err != nil || len(symEv.Rows) != 0 {
			t.Fatalf("symlinked dev eval must be skipped, got %+v, err=%v", symEv, err)
		}
	})
}

func seedDevEvalStoreT(t *testing.T, home, label, status string, at time.Time) {
	t.Helper()
	p := filepath.Join(home, harness.StateDir, "dev", "fyr-eval", harness.StateDir, "metrics.db")
	s := openT(t, p)
	defer s.Close()
	if err := s.RecordTrial(Trial{Run: "decoy/1", Label: label, RowVersion: 1, Kind: "capability", Harness: "h", Status: status, StartedAt: at, Seconds: 5}); err != nil {
		t.Fatal(err)
	}
}

func TestMetricsReportV9Health(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -7)
	ts := now.Add(-3 * time.Hour).UnixMilli()

	home := t.TempDir()
	projA := filepath.Join(t.TempDir(), "alpha")
	projB := filepath.Join(t.TempDir(), "beta")
	s := openT(t, Path(home))

	// Sessions & steps in metrics.db (no work_waves rows).
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO sessions(id, vendor, project_root, model, started_at, ended_at) VALUES
			('M1', 'agy', ?, 'gemini-4-argon', ?, ?),
			('M2', 'agy', ?, 'claude-opus-5-5', ?, ?)`,
			[]any{projA, ts, ts + 60000, projB, ts + 70000, ts + 90000}},
		{`INSERT INTO sessions(id, vendor, project_root, parent_id, agent_type, model, started_at, ended_at) VALUES
			('S_REV', 'agy', ?, 'M1', 'reviewer', 'gemini-3.8-flash-medium', ?, ?),
			('S_SELF', 'agy', ?, 'M1', 'self', 'gemini-4-hydrogen-high', ?, ?),
			('S_OK', 'agy', ?, 'M1', 'verifier', 'claude-opus-5-5', ?, ?)`,
			[]any{projA, ts + 1000, ts + 5000, projA, ts + 6000, ts + 10000, projA, ts + 11000, ts + 15000}},
		{`INSERT INTO steps(session_id, ext_id, ts, kind, context) VALUES
			('M1', 'g1', ?, 'generation', 310000),
			('M2', 'g2', ?, 'generation', 220000)`,
			[]any{ts + 500, ts + 70500}},
		{`INSERT INTO steps(session_id, ext_id, ts, kind) VALUES ('M1', 'c1', ?, 'compaction')`, []any{ts + 2000}},
		{`INSERT INTO steps(session_id, ext_id, ts, kind, tool, is_error, denied) VALUES
			('M1', 't1', ?, 'tool', 'Agent', 0, 0),
			('M1', 't2', ?, 'tool', 'agy:run_workflow', 0, 0),
			('M1', 't3', ?, 'tool', 'agy:swarm', 0, 0),
			('M1', 't4', ?, 'tool', 'Bash', 1, 0),
			('M1', 't5', ?, 'tool', 'Read', 0, 1),
			('M1', 't6', ?, 'tool', 'Read', 0, 1),
			('M1', 't7', ?, 'tool', 'Bash', 0, 1),
			('M2', 'b1', ?, 'tool', 'Read', 0, 0)`,
			[]any{ts + 100, ts + 200, ts + 300, ts + 400, ts + 500, ts + 600, ts + 700, ts + 71000}},
		{`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES
			('M1', 'vg1', ?, 'tool', 'Bash'),
			('M1', 'vg2', ?, 'tool', 'Bash'),
			('M1', 'vg3', ?, 'tool', 'Bash'),
			('M1', 'vg4', ?, 'tool', 'Bash')`,
			[]any{ts + 800, ts + 810, ts + 820, ts + 830}},
		{`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES
			('M1', 'vg1', 0, 'fyr verify'),
			('M1', 'vg2', 0, 'fyr gate'),
			('M1', 'vg3', 0, 'fyr land'),
			('M1', 'vg4', 0, 'fyr close --id X')`, nil},
		{`INSERT INTO events(session_id, ts, kind, ref, value) VALUES
			('M1', ?, 'hook_block', 't5', 'clamped'),
			('M1', ?, 'hook_block', 't6', 'ledger'),
			('M1', ?, 'hook_block', 't7', 'guard')`,
			[]any{ts + 500, ts + 600, ts + 700}},
		{`INSERT INTO turn_vets(project_root, conversation_id, turn_num, intent, gate, score, bypassed, skills_count, pivots_count, ledger_saved, created_at)
			VALUES (?, 'M1', 1, 'FEATURE', 'PROCEED', 88, 0, 1, 2, 2, ?)`, []any{projA, ts}},
	} {
		if _, err := s.db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	for i := 0; i < 45; i++ {
		if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('S_REV', ?, ?, 'tool', 'Read')`,
			fmt.Sprintf("sr:%d", i), ts+1100+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 125; i++ {
		if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('S_SELF', ?, ?, 'tool', 'Read')`,
			fmt.Sprintf("ss:%d", i), ts+6100+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('S_OK', ?, ?, 'tool', 'Read')`,
			fmt.Sprintf("so:%d", i), ts+11100+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordJudgeVerdict(JudgeVerdictRow{
		TS: ts + 900, ProjectRoot: projA, Target: "phase-2b", Model: "claude-opus-5-5", CriterionHash: "h1", Verdict: "PASS",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordJudgeVerdict(JudgeVerdictRow{
		TS: ts + 950, ProjectRoot: projA, Target: "phase-2c", Model: "gemini-4-carbon", FallbackReason: "anthropic quota low", CriterionHash: "h2", Verdict: "FAIL",
	}); err != nil {
		t.Fatal(err)
	}

	// Seed auto/runs.json & models.json.
	writeStateJSONT(t, filepath.Join(home, harness.StateDir, "auto", "runs.json"),
		`{"date":"2026-11-18","model_runs":3,"last_run":"2026-11-18T10:00:00Z","verdict":"health: 2 findings","open_asks":2,"projects":{"a":{},"b":{}}}`)
	writeStateJSONT(t, filepath.Join(home, harness.StateDir, "models.json"),
		`{"updated_at":"2026-11-18T10:00:00Z","quota_updated_at":"2026-11-18T10:00:00Z","pools":[{"name":"Claude","five":80,"week":90},{"name":"Gemini Pro","five":95,"week":100}]}`)

	// Seed projA/.agents/scratch/search.db (WAL mode).
	sdb := filepath.Join(projA, ".agents", "scratch", "search.db")
	if err := os.MkdirAll(filepath.Dir(sdb), 0o755); err != nil {
		t.Fatal(err)
	}
	sqliteT(t, sdb,
		`CREATE TABLE session_meta(conversation_id TEXT PRIMARY KEY, generation_id INT, last_transcript_bytes INT, turn_count INT, est_tokens INT, tool_calls INT, agent_name TEXT, parent_conversation_id TEXT)`,
		`INSERT INTO session_meta VALUES ('M1', 2, 1000, 10, 5000, 90, '', '')`,
		`INSERT INTO session_meta VALUES ('S_REV', 1, 500, 5, 1000, 45, 'reviewer', 'M1')`,
		`INSERT INTO session_meta VALUES ('sub-extra', 0, 500, 5, 1000, 42, 'explorer', 'M1')`,
		`CREATE TABLE read_ledger(conversation_id TEXT, generation_id INT, resolved_path TEXT, start_line INT, end_line INT, sha256 TEXT, turn_num INT, hit_count INT, updated_at INT, PRIMARY KEY(conversation_id, generation_id, resolved_path, start_line, end_line))`,
		fmt.Sprintf(`INSERT INTO read_ledger VALUES ('M1', 0, 'a.go', -5, -5, 'h1', 1, 1, %d)`, now.Add(-time.Hour).Unix()),
		fmt.Sprintf(`INSERT INTO read_ledger VALUES ('M1', 0, 'old.go', -5, -5, 'h2', 1, 1, %d)`, since.Add(-48*time.Hour).Unix()),
		fmt.Sprintf(`INSERT INTO read_ledger VALUES ('M1', 0, 'b.go', 1, 20, 'h3', 1, 1, %d)`, now.Add(-time.Hour).Unix()),
	)

	// Seed brain.db (WAL mode).
	bdb := filepath.Join(home, harness.StateDir, "brain.db")
	sqliteT(t, bdb,
		`CREATE TABLE records(rid INTEGER PRIMARY KEY, scope TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL, date TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, project TEXT NOT NULL DEFAULT '', updated TEXT NOT NULL, provenance TEXT NOT NULL DEFAULT '', UNIQUE(scope, id))`,
		fmt.Sprintf(`INSERT INTO records(scope, id, kind, status, date, title, body, project, updated, provenance) VALUES ('', 'REVIEW-2026-11-18-1000-auto-a', 'review', 'closed', '2026-11-18', 'auto report: alpha', '# findings', '%s', '2026-11-18T10:00:00Z', '')`, projA),
		fmt.Sprintf(`INSERT INTO records(scope, id, kind, status, date, title, body, project, updated, provenance) VALUES ('%s', 'BUG-001', 'bug', 'open', '2026-11-18', 'derived bug', 'body', '', '2026-11-18T10:00:00Z', 'derived')`, projA),
		fmt.Sprintf(`INSERT INTO records(scope, id, kind, status, date, title, body, project, updated, provenance) VALUES ('%s', 'REVIEW-2026-11-18-0900-auto-done', 'review', 'closed', '2026-11-18', 'auto report: done', '## Outcome\nresolved', '', '2026-11-18T09:00:00Z', '')`, projA),
		fmt.Sprintf(`INSERT INTO records(scope, id, kind, status, date, title, body, project, updated, provenance) VALUES ('%s', 'HANDOFF-2026-11-16-1200-a', 'handoff', '', '2026-11-16', 'handoff', 'done', '', '2026-11-16T12:00:00Z', '')`, projA),
	)

	sum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if sum.B.Compactions["agy"] != nil || sum.D.Denials["agy"] != nil {
		t.Fatalf("Compactions[agy]=%v Denials[agy]=%v, want nil for backward compatibility", sum.B.Compactions["agy"], sum.D.Denials["agy"])
	}
	h := sum.Health
	if h.OrchestrationCalls != 3 || h.SubagentDispatches != 1 || h.WorkflowCalls != 1 || h.SwarmCalls != 1 || h.DeterministicGates != 4 {
		t.Errorf("orchestration/gates = %+v", h)
	}
	if h.JudgePassed != 1 || h.JudgeFailed != 1 || h.JudgeFallback != 1 || sum.F.JudgePassed != 1 || sum.TriggerCoverage.JudgeFailed != 1 {
		t.Errorf("judge counts = health:%+v F:%+v TC:%+v", h, sum.F, sum.TriggerCoverage)
	}
	if !h.AutoRunsPresent || h.AutoModelRuns != 3 || h.AutoOpenAsks != 2 || h.AutoLastVerdict != "health: 2 findings" || h.AutoProjectCount != 2 || sum.TriggerCoverage.AutoModelRuns != 3 {
		t.Errorf("auto runs health = %+v TC.AutoModelRuns=%d", h, sum.TriggerCoverage.AutoModelRuns)
	}
	if h.SearchDBCompactions != 3 || h.StepCompactions != 1 || h.MainSessions != 2 || h.SessionsOverKnee != 1 || h.SubagentsOverBudget != 3 {
		t.Errorf("compactions/knee/budget = %+v", h)
	}
	if h.HookDenials != 3 || h.ClampedSaves != 1 || h.HookBlocksLedger != 1 || h.HookBlocksGuards != 1 || h.GuardedDowngrades != 1 {
		t.Errorf("denials/clamped/downgrades = %+v", h)
	}
	if h.ModelFamilyCounts["gemini"] != 3 || h.ModelFamilyCounts["anthropic"] != 2 || len(h.PoolQuotas) != 2 {
		t.Errorf("model family / pools = %+v", h)
	}
	// Real tool error rate: 1 real error (t4) out of 12 main+45+125+5 = 187 tool steps (denials excluded from errors!).
	if r := sum.D.ToolErrorRate["agy"]; r == nil || *r >= 0.01 {
		t.Errorf("ToolErrorRate[agy] = %v, want 1/187 (~0.0053) excluding hook denials", r)
	}
	byProj := map[string]ProjectRow{}
	for _, p := range sum.Projects {
		byProj[p.Project] = p
	}
	if pa := byProj[projA]; pa.AutoFindings != 2 || pa.HandoffLagDays == nil || *pa.HandoffLagDays != 2 {
		t.Errorf("projA brain enrichment = %+v, want AutoFindings=2 HandoffLagDays=2", pa)
	}
	if pb := byProj[projB]; pb.AutoFindings != 0 || pb.HandoffLagDays != nil {
		t.Errorf("projB brain enrichment = %+v, want AutoFindings=0 HandoffLagDays=nil", pb)
	}

	var mdBuf, htmlBuf bytes.Buffer
	if err := RenderMarkdownReport(&mdBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := RenderReport(&htmlBuf, sum, nil, now); err != nil {
		t.Fatal(err)
	}
	md, html := mdBuf.String(), htmlBuf.String()
	for _, hidden := range []string{"Work waves / workers", "Worker verdicts", "Base-Check verdicts", "Qualifying phases run as workers", "Worker phases with model:"} {
		if strings.Contains(md, hidden) || strings.Contains(html, hidden) {
			t.Errorf("legacy work_waves row %q rendered when Waves==0 && Workers==0", hidden)
		}
	}
	for _, want := range []string{
		"Search.db compactions",
		"Sessions >= 60% knee",
		"Clamped read saves ([CLAMPED])",
		"Hook denials (excl. from error rate)",
		"Cross-family Judge (PASS / FAIL / fallback)",
		"v9 orchestration",
		"Deterministic gates",
		"Hook blocks (clamped / ledger / guards)",
		"Model-family share",
		"Guarded subagent downgrades",
		"Live quota pools",
		"Subagents over budget",
		"Claude: 5h=80% wk=90%",
		"Auto findings",
		"Handoff lag (d)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
		if !strings.Contains(html, want) && !strings.Contains(html, esc(want)) {
			t.Errorf("HTML missing %q", want)
		}
	}
	for _, bad := range []string{"<script", "http://", "https://", "@import", "url("} {
		if strings.Contains(strings.ToLower(html), bad) {
			t.Errorf("HTML contains %q", bad)
		}
	}
	assertLiveV9ReportSections(t, md, html)
}

func assertLiveV9ReportSections(t *testing.T, md, html string) {
	t.Helper()
	for _, want := range []string{
		"v9 orchestration (/fyr-swarm / gate / land)",
		"Deterministic gate coverage",
		"PreInvocation timeout rate",
		"PreInvocation latency (p50 / p95)",
		"Bash read bypasses",
		"Top Bash verbs",
		"PreInvocation timeouts",
		"Mean injected bytes",
		"Pivots used / skills loaded",
		"Agent Roles",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing live v9 field %q:\n%s", want, md)
		}
		if !strings.Contains(html, want) && !strings.Contains(html, esc(want)) {
			t.Errorf("HTML missing live v9 field %q", want)
		}
	}
	for _, role := range []string{"main", "reviewer"} {
		if !strings.Contains(md, "| "+role+" |") {
			t.Errorf("markdown missing Agent Roles row %q:\n%s", role, md)
		}
		if !strings.Contains(html, "<td>"+role+"</td>") {
			t.Errorf("HTML missing Agent Roles cell %q", role)
		}
	}
}

func TestHealthAndReportsPerModelKnee(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	since := now.AddDate(0, 0, -7)
	ts := now.Add(-2 * time.Hour).UnixMilli()
	home := t.TempDir()
	proj := filepath.Join(t.TempDir(), "proj")
	writeStateJSONT(t, filepath.Join(home, harness.StateDir, "models.json"),
		`{"updated_at":"2026-11-20T00:00:00Z","entries":[{"id":"custom-wide","display_name":"Wide","status":"active","context_window":700000},{"id":"custom-tiny","display_name":"Tiny","status":"active","context_window":1}]}`)
	s := openT(t, Path(home))
	for i, row := range []struct {
		id, model string
		ctx       int64
	}{
		{"s1", "gemini-4-neon-high", 250000}, // under 300K
		{"s2", "gpt-oss-120b-medium", 77000}, // over 76.8K
		{"s3", "custom-wide", 380000},        // under 420K
		{"s4", "custom-wide", 425000},        // over 420K
		{"s5", "custom-tiny", 50000},         // knee=0 disabled
	} {
		t0 := ts + int64(i*1000)
		if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, model, started_at, ended_at) VALUES (?, 'agy', ?, ?, ?, ?)`,
			row.id, proj, row.model, t0, t0+500); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, context) VALUES (?, ?, ?, 'generation', ?)`,
			row.id, "g-"+row.id, t0+100, row.ctx); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if sum.Health.MainSessions != 5 || sum.Health.SessionsOverKnee != 2 {
		t.Fatalf("Health = %+v, want MainSessions=5 SessionsOverKnee=2", sum.Health)
	}
	digest, err := s.DreamTrajectoryDigest(proj, since)
	if err != nil {
		t.Fatalf("DreamTrajectoryDigest: %v", err)
	}
	if digest.Sessions != 5 || digest.KneeExceeded != 2 {
		t.Fatalf("DreamTrajectoryDigest = %+v, want Sessions=5 KneeExceeded=2", digest)
	}
}

func writeStateJSONT(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadBoundedRegularFileRejectsSymlinksAndOversized(t *testing.T) {
	base := t.TempDir()
	okBytes := []byte(`{"ok":true}`)
	writeStateJSONT(t, filepath.Join(base, "sub", "small.json"), string(okBytes))
	if got, ok := readBoundedRegularFile(base, "sub", "small.json"); !ok || !bytes.Equal(got, okBytes) {
		t.Fatalf("small regular file = (%q, %v), want (%q, true)", got, ok, okBytes)
	}

	atLimit := bytes.Repeat([]byte("a"), healthFileLimit)
	writeStateJSONT(t, filepath.Join(base, "exact.bin"), string(atLimit))
	if got, ok := readBoundedRegularFile(base, "exact.bin"); !ok || len(got) != healthFileLimit {
		t.Fatalf("exact limit file = (len=%d, %v), want (len=%d, true)", len(got), ok, healthFileLimit)
	}

	overLimit := bytes.Repeat([]byte("b"), healthFileLimit+1)
	writeStateJSONT(t, filepath.Join(base, "oversized.bin"), string(overLimit))
	if got, ok := readBoundedRegularFile(base, "oversized.bin"); ok || got != nil {
		t.Fatalf("oversized file accepted: len=%d ok=%v", len(got), ok)
	}

	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, okBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "link.json")); err != nil {
		t.Fatal(err)
	}
	if got, ok := readBoundedRegularFile(base, "link.json"); ok || got != nil {
		t.Fatalf("symlink file accepted: (%q, %v)", got, ok)
	}

	outsideDir := t.TempDir()
	writeStateJSONT(t, filepath.Join(outsideDir, "nested.json"), string(okBytes))
	if err := os.Symlink(outsideDir, filepath.Join(base, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if got, ok := readBoundedRegularFile(base, "linkdir", "nested.json"); ok || got != nil {
		t.Fatalf("symlinked parent dir accepted: (%q, %v)", got, ok)
	}
}
