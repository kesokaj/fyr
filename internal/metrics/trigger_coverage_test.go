package metrics

import (
	"bytes"
	"database/sql"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

type agyFixtureConv struct {
	id       string
	modified string
	root     string
	model    string
	steps    [][]any
}

func seedAgyHomeT(t *testing.T, home string, convs []agyFixtureConv) {
	t.Helper()
	agyDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(agyDir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		"CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')",
	}
	for _, c := range convs {
		stmts = append(stmts, `INSERT INTO conversation_summaries VALUES ('`+c.id+`', '`+c.modified+`', '["file://`+c.root+`"]', '', '')`)
	}
	sqliteT(t, filepath.Join(agyDir, "conversation_summaries.db"), stmts...)

	for _, c := range convs {
		convDB := filepath.Join(agyDir, "conversations", c.id+".db")
		sqliteT(t, convDB,
			"CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)",
			"CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)",
		)
		db, err := sql.Open("sqlite", convDB)
		if err != nil {
			t.Fatal(err)
		}
		for i, st := range c.steps {
			if _, err := db.Exec(`INSERT INTO steps VALUES (?, ?, ?, ?, ?, ?)`, i, st[0], st[1], st[2], st[3], []byte("payload")); err != nil {
				db.Close()
				t.Fatal(err)
			}
		}
		if c.model != "" {
			gm := pbB(1, cat(pbB(4, pbV(2, 100)), pbB(19, []byte(c.model))))
			if _, err := db.Exec(`INSERT INTO gen_metadata VALUES (0, ?, ?)`, gm, len(gm)); err != nil {
				db.Close()
				t.Fatal(err)
			}
		}
		db.Close()
	}
}

func writeWaveStatusT(t *testing.T, home, stamp, body string) {
	t.Helper()
	dir := filepath.Join(home, harness.StateDir, "work", stamp)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertFloatPtrNear(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %.4f", name, want)
	}
	if math.Abs(*got-want) > 0.01 {
		t.Errorf("%s = %.4f, want %.4f", name, *got, want)
	}
}

func TestReportTriggerCoverageKPIs(t *testing.T) {
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })

	mainHome := t.TempDir()
	rootA := filepath.Join(t.TempDir(), "proj-kpi")
	if err := os.MkdirAll(filepath.Join(rootA, ".agents", "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}

	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	at := func(h, m int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }

	// Initialize git repo and commit inside S_LEAD (10:10), S_HAND_CLOSED (11:10), and S_HAND_OPEN (12:10).
	gitT(t, rootA, at(9, 0), "init", "-q", "-b", "main")
	gitT(t, rootA, at(9, 0), "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(rootA, ".gitignore"), []byte(".agents/scratch/*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		file string
		when time.Time
		msg  string
	}{
		{"lead.go", at(10, 10), "lead wave merge"},
		{"hand1.go", at(11, 10), "hand closed work"},
		{"hand2.go", at(12, 10), "hand unclosed work"},
	} {
		if err := os.WriteFile(filepath.Join(rootA, c.file), []byte("package kpi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitT(t, rootA, c.when, "add", c.file)
		gitT(t, rootA, c.when, "commit", "-q", "-m", c.msg)
	}

	// Register rootA and record handoffs for S_LEAD (10:15) and S_HAND_CLOSED (11:15); S_HAND_OPEN has none.
	markRegistered(t, mainHome, rootA)
	bo := brain.Options{Home: mainHome, Dir: rootA, Now: func() time.Time { return at(15, 0) }}
	addRecord(t, bo, brain.AddInput{Kind: "handoff", ID: "HANDOFF-2026-10-08-1015-lead", Title: "Handoff — lead"}, "lead handoff\n")
	addRecord(t, bo, brain.AddInput{Kind: "handoff", ID: "HANDOFF-2026-10-08-1115-hand", Title: "Handoff — hand"}, "hand handoff\n")

	// Index rootA and seed 4 read-ledger blocks saved in search.db.
	if err := search.Index(search.Options{Dir: rootA, Home: mainHome}, io.Discard); err != nil {
		t.Fatalf("search.Index: %v", err)
	}
	sqliteT(t, filepath.Join(rootA, ".agents", "scratch", "search.db"),
		`INSERT INTO vet_history VALUES ('S_LEAD', 1, 'FEATURE', 'PROCEED', 95, 'p', 'ip', 'target', '', 'fyr-feature', 'lead.go', 1791453600)`,
	)
	for i := 0; i < 5; i++ {
		if _, err := search.CheckAndRecordRead(rootA, "S_LEAD", "lead.go", 1, 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	// Note: consume-on-hit allows 1st, denies 2nd, allows 3rd, denies 4th, allows 5th -> 2 blocks; add 2 more on hand1.go -> 4 total blocks.
	for i := 0; i < 4; i++ {
		if _, err := search.CheckAndRecordRead(rootA, "S_LEAD", "hand1.go", 1, 1, 1); err != nil {
			t.Fatal(err)
		}
	}

	tLead := uint64(at(10, 0).Unix())
	tHand1 := uint64(at(11, 0).Unix())
	tHand2 := uint64(at(12, 0).Unix())
	tQA := uint64(at(13, 0).Unix())
	grillingSkill := filepath.Join(mainHome, ".gemini", "config", "skills", "grilling", "SKILL.md")

	seedAgyHomeT(t, mainHome, []agyFixtureConv{
		{
			id:       "S_LEAD",
			modified: "2026-10-08 10:20:00+00:00",
			root:     rootA,
			model:    "gemini-4-argon",
			steps: [][]any{
				{14, 3, agyStepMeta(tLead, "", nil), nil},
				// Pre-build interview: loads grilling skill, asks 1 pre-build question BEFORE fyr work -> exempt!
				{132, 3, agyStepMeta(tLead+30, "read_file", map[string]any{"Path": grillingSkill}), nil},
				{132, 3, agyStepMeta(tLead+60, "ask_question", map[string]any{}), nil},
				{132, 3, agyStepMeta(tLead+120, "run_command", map[string]any{"CommandLine": "fyr search --slice Alpha"}), nil},
				{132, 3, agyStepMeta(tLead+150, "run_command", map[string]any{"CommandLine": "fyr search --slice Beta"}), nil},
				{132, 3, agyStepMeta(tLead+180, "run_command", map[string]any{"CommandLine": "fyr search --slice Gamma"}), nil},
				{132, 3, agyStepMeta(tLead+210, "grep_search", map[string]any{"Query": "Alpha"}), nil},
				{132, 3, agyStepMeta(tLead+240, "grep_search", map[string]any{"Query": "Beta"}), nil},
				{132, 3, agyStepMeta(tLead+300, "run_command", map[string]any{"CommandLine": "fyr work --home /tmp/dev kickoff.md"}), nil},
				{14, 3, agyStepMeta(tLead+1100, "", nil), nil},
			},
		},
		{
			id:       "S_HAND_CLOSED",
			modified: "2026-10-08 11:20:00+00:00",
			root:     rootA,
			model:    "gemini-4-argon",
			steps: [][]any{
				{14, 3, agyStepMeta(tHand1, "", nil), nil},
				// Build-time question without pre-build interview -> counted (1)!
				{132, 3, agyStepMeta(tHand1+60, "ask_question", map[string]any{}), nil},
				{132, 3, agyStepMeta(tHand1+120, "run_command", map[string]any{"CommandLine": "fyr search Delta"}), nil},
				{14, 3, agyStepMeta(tHand1+1100, "", nil), nil},
			},
		},
		{
			id:       "S_HAND_OPEN",
			modified: "2026-10-08 12:20:00+00:00",
			root:     rootA,
			model:    "gemini-3.8-flash",
			steps: [][]any{
				{14, 3, agyStepMeta(tHand2, "", nil), nil},
				{132, 3, agyStepMeta(tHand2+60, "edit_file", map[string]any{"Path": filepath.Join(rootA, "hand2.go")}), nil},
				{14, 3, agyStepMeta(tHand2+1100, "", nil), nil},
			},
		},
		{
			id:       "S_QA",
			modified: "2026-10-08 13:10:00+00:00",
			root:     rootA,
			model:    "gemini-3.8-flash",
			steps: [][]any{
				{14, 3, agyStepMeta(tQA, "", nil), nil},
				{14, 3, agyStepMeta(tQA+300, "", nil), nil},
			},
		},
	})

	// Seed real-home waves: 4 worker phases (3 configured with model+judge+base-check, 1 unconfigured) + 1 auto-* worker.
	writeWaveStatusT(t, mainHome, "2026-10-08-100500-1111", `{
		"stamp": "2026-10-08-100500-1111",
		"project_root": "`+rootA+`",
		"updated_at": "2026-10-08T10:05:00Z",
		"workers": [
			{"name": "p1", "state": "PASS", "model": "gemini-4-argon", "step_count": 6, "commits": 1, "base_check": "PASS", "judge_model": "claude-opus-5-5", "judge_verdict": "PASS"},
			{"name": "p2", "state": "PASS", "model": "gemini-4-argon", "step_count": 5, "commits": 1, "base_check": "PASS", "judge_model": "claude-opus-5-5", "judge_verdict": "PASS"},
			{"name": "p3", "state": "PASS", "model": "gemini-3.8-flash", "step_count": 4, "commits": 1, "base_check": "PASS", "judge_model": "claude-sonnet-4-6", "judge_verdict": "PASS"},
			{"name": "p4", "state": "FAIL", "model": "gemini-3.8-flash", "step_count": 2, "commits": 0, "base_check": "", "judge_model": "", "judge_verdict": ""}
		]
	}`)
	writeWaveStatusT(t, mainHome, "2026-10-09-020000-2222", `{
		"stamp": "2026-10-09-020000-2222",
		"project_root": "`+rootA+`",
		"updated_at": "2026-10-09T02:00:00Z",
		"workers": [
			{"name": "auto-drift-1", "state": "PASS", "model": "gemini-3.8-flash", "step_count": 3, "commits": 1, "base_check": "PASS", "judge_model": "", "judge_verdict": ""}
		]
	}`)

	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	var logBuf bytes.Buffer
	if _, err := Ingest(s, Options{Home: mainHome}, &logBuf); err != nil {
		t.Fatalf("Ingest mainHome: %v", err)
	}

	// 14-day (2.0-week) window.
	win := ShowOptions{
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Now:   time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC),
	}
	baseSum, err := s.Show(win)
	if err != nil {
		t.Fatalf("Show base: %v", err)
	}
	tc := baseSum.TriggerCoverage
	if tc.WorkerPhases != 4 || tc.QualifyingPhases != 6 {
		t.Fatalf("Qualifying phases = %d / %d, want 4 / 6", tc.WorkerPhases, tc.QualifyingPhases)
	}
	assertFloatPtrNear(t, "QualifyingWorkerPhasePct", tc.QualifyingWorkerPhasePct, 66.6667)
	if tc.ConfiguredWorkerPhases != 3 {
		t.Fatalf("ConfiguredWorkerPhases = %d, want 3", tc.ConfiguredWorkerPhases)
	}
	assertFloatPtrNear(t, "ConfiguredWorkerPhasePct", tc.ConfiguredWorkerPhasePct, 75.0)
	if tc.BuildTimeQuestions != 1 {
		t.Errorf("BuildTimeQuestions = %d, want 1 (grilling pre-build question must be exempt)", tc.BuildTimeQuestions)
	}
	if tc.SearchCalls != 4 || tc.GrepCalls != 2 {
		t.Errorf("Search:Grep counts = %d:%d, want 4:2", tc.SearchCalls, tc.GrepCalls)
	}
	assertFloatPtrNear(t, "SearchGrepRatio", tc.SearchGrepRatio, 2.0)
	if tc.ReadLedgerBlocks != 4 {
		t.Errorf("ReadLedgerBlocks = %d, want 4", tc.ReadLedgerBlocks)
	}
	if tc.AutoModelRuns != 1 || math.Abs(tc.WindowWeeks-2.0) > 0.001 || math.Abs(tc.AutoModelRunsPerWeek-0.5) > 0.001 {
		t.Errorf("AutoModelRuns = %d / %.2f wk (%.2f/wk), want 1 / 2.0 wk (0.50/wk)", tc.AutoModelRuns, tc.WindowWeeks, tc.AutoModelRunsPerWeek)
	}
	if tc.DoDClosedSessions != 2 || tc.BuildSessions != 3 {
		t.Errorf("DoDClosed = %d / %d, want 2 / 3", tc.DoDClosedSessions, tc.BuildSessions)
	}
	assertFloatPtrNear(t, "DoDClosedPct", tc.DoDClosedPct, 66.6667)

	// Seed devWorkHome (work wave + surviving worker sandbox) and devEvalHome (metrics.db with trials + sessions).
	devWorkHome := t.TempDir()
	writeWaveStatusT(t, devWorkHome, "2026-10-10-120000-3333", `{
		"stamp": "2026-10-10-120000-3333",
		"project_root": "`+rootA+`",
		"updated_at": "2026-10-10T12:00:00Z",
		"workers": [
			{"name": "dw1", "state": "PASS", "model": "gemini-4-argon", "step_count": 4, "commits": 1, "base_check": "PASS", "judge_model": "claude-opus-5-5", "judge_verdict": "PASS"},
			{"name": "dw2", "state": "FAIL", "model": "gemini-4-argon", "step_count": 2, "commits": 0, "base_check": "FAIL", "judge_model": "claude-opus-5-5", "judge_verdict": "FAIL"}
		]
	}`)
	dw1Home := filepath.Join(devWorkHome, harness.StateDir, "sandbox", "proj-kpi", "2026-10-10-120000-3333", "dw1", "home")
	tDW := uint64(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC).Unix())
	seedAgyHomeT(t, dw1Home, []agyFixtureConv{{
		id:       "C_DW1",
		modified: "2026-10-10 12:00:40+00:00",
		root:     rootA,
		model:    "gemini-4-argon",
		steps: [][]any{
			{14, 3, agyStepMeta(tDW, "", nil), nil},
			{132, 3, agyStepMeta(tDW+20, "run_command", map[string]any{"CommandLine": "go test ./..."}), nil},
			{14, 3, agyStepMeta(tDW+40, "", nil), nil},
		},
	}})

	devEvalHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(devEvalHome, harness.StateDir), 0o700); err != nil {
		t.Fatal(err)
	}
	devStore := openT(t, Path(devEvalHome))
	tEval := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	sec20, sec2, sec3 := int64(20), int64(2), int64(3)
	for _, tr := range []struct {
		run, label, status, cause, model, sid string
	}{
		{"2026-10-11/row-a/1", "eval=row-a", "pass", "", "gemini-4-argon", "E_SESS_1"},
		{"2026-10-11/row-b/1", "eval=row-b", "fail", "check_failed", "claude-opus-5-5", "E_SESS_2"},
	} {
		if err := devStore.RecordTrial(Trial{
			Run: tr.run, Label: tr.label, RowVersion: 1, Kind: "capability", Cluster: "core",
			Expect: "positive", Harness: "v1", CLI: "agy-1", Status: tr.status, Cause: tr.cause,
			StartedAt: tEval, Seconds: 25, SessionSeconds: &sec20, SetupSeconds: &sec2, CheckSeconds: &sec3,
		}); err != nil {
			t.Fatalf("RecordTrial %s: %v", tr.run, err)
		}
		if _, err := devStore.db.Exec(
			`INSERT INTO sessions(id, vendor, project_root, model, label, outcome, run, started_at, ended_at, duration_ms) VALUES (?, 'agy', ?, ?, ?, ?, ?, ?, ?, 20000)`,
			tr.sid, rootA, tr.model, tr.label, tr.status, tr.run, tEval.UnixMilli(), tEval.Add(20*time.Second).UnixMilli(),
		); err != nil {
			t.Fatal(err)
		}
	}
	devStore.Close()

	// Run labelled ingest twice for each dev home to verify idempotency and zero leakage into unlabelled totals.
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := Ingest(s, Options{Home: mainHome, Source: devWorkHome, Label: "dev=work"}, &logBuf); err != nil {
			t.Fatalf("Ingest devWorkHome attempt %d: %v", attempt+1, err)
		}
		if _, err := Ingest(s, Options{Home: mainHome, Source: devEvalHome, Label: "dev=eval"}, &logBuf); err != nil {
			t.Fatalf("Ingest devEvalHome attempt %d: %v", attempt+1, err)
		}
	}

	// Unlabelled totals MUST remain completely unchanged.
	afterUnlabelled, err := s.Show(win)
	if err != nil {
		t.Fatalf("Show after labelled ingest: %v", err)
	}
	if afterUnlabelled.Sessions["agy"] != 4 || afterUnlabelled.F.Waves != 2 || afterUnlabelled.F.Workers != 5 || len(afterUnlabelled.A.PassK) != 0 {
		t.Fatalf("labelled ingest leaked into unlabelled Show: sessions=%v F=%+v PassK=%+v",
			afterUnlabelled.Sessions, afterUnlabelled.F, afterUnlabelled.A.PassK)
	}
	if afterUnlabelled.TriggerCoverage.WorkerPhases != 4 || afterUnlabelled.TriggerCoverage.QualifyingPhases != 6 {
		t.Fatalf("labelled ingest leaked into unlabelled TriggerCoverage: %+v", afterUnlabelled.TriggerCoverage)
	}
	unlabelledEvals, err := s.Evals(EvalOptions{Since: win.Since, Now: win.Now})
	if err != nil {
		t.Fatalf("Evals: %v", err)
	}
	if len(unlabelledEvals.Rows) != 0 {
		t.Fatalf("dev=eval trials leaked into unlabelled Evals: %+v", unlabelledEvals.Rows)
	}

	// Labelled views surface the exact dev=work wave/sessions and dev=eval trials/sessions.
	workSum, err := s.Show(ShowOptions{Label: "dev=work", Since: win.Since, Now: win.Now})
	if err != nil {
		t.Fatalf("Show dev=work: %v", err)
	}
	if workSum.Sessions["agy"] != 1 || workSum.F.Waves != 1 || workSum.F.Workers != 2 || workSum.F.Passed != 1 || workSum.F.Failed != 1 {
		t.Errorf("unexpected dev=work Show: sessions=%v F=%+v", workSum.Sessions, workSum.F)
	}
	if workSum.TriggerCoverage.WorkerPhases != 2 || workSum.TriggerCoverage.ConfiguredWorkerPhases != 2 {
		t.Errorf("unexpected dev=work TriggerCoverage: %+v", workSum.TriggerCoverage)
	}

	evalSum, err := s.Show(ShowOptions{Label: "dev=eval", Since: win.Since, Now: win.Now})
	if err != nil {
		t.Fatalf("Show dev=eval: %v", err)
	}
	pk, ok := evalSum.A.PassK["dev=eval"]
	if !ok || pk.N != 2 || pk.C != 1 || evalSum.Sessions["agy"] != 2 {
		t.Errorf("unexpected dev=eval Show: sessions=%v PassK=%+v", evalSum.Sessions, evalSum.A.PassK)
	}

	allSum, err := s.Show(ShowOptions{AllLabels: true, Since: win.Since, Now: win.Now})
	if err != nil {
		t.Fatalf("Show --all-labels: %v", err)
	}
	if allSum.Sessions["agy"] != 7 || allSum.F.Waves != 3 || allSum.F.Workers != 7 {
		t.Errorf("unexpected --all-labels Show: sessions=%v F=%+v", allSum.Sessions, allSum.F)
	}

	// Verify Markdown and HTML report rendering of Trigger coverage.
	var mdBuf, htmlBuf bytes.Buffer
	reportNow := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	if err := RenderMarkdownReport(&mdBuf, afterUnlabelled, nil, reportNow); err != nil {
		t.Fatalf("RenderMarkdownReport: %v", err)
	}
	if err := RenderReport(&htmlBuf, afterUnlabelled, nil, reportNow); err != nil {
		t.Fatalf("RenderReport: %v", err)
	}
	md := mdBuf.String()
	html := htmlBuf.String()
	for _, want := range []string{
		"## Trigger coverage",
		"Qualifying phases run as workers",
		"66.7% (4 / 6)",
		">= 80%",
		"Worker phases with model: + judge: + base-check:",
		"75.0% (3 / 4)",
		"= 100%",
		"Worker verdicts (PASS / total)",
		"3 / 4",
		"Base-Check verdicts (PASS / FAIL)",
		"3 / 0",
		"Judge verdicts (PASS / FAIL)",
		"Build-time questions",
		"= 0",
		"Search : grep",
		"2.00 (4 : 2)",
		">= 1.0",
		"Read-ledger blocks",
		"> 0",
		"fyr auto model runs",
		"0.50/wk (1 in 2.0 wk)",
		"> 0/wk",
		"DoD closed",
		"66.7% (2 / 3)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown report missing %q:\n%s", want, md)
		}
	}
	for _, want := range []string{
		"<h2>Trigger coverage</h2>",
		"<td>Qualifying phases run as workers</td>",
		"<td>66.7% (4 / 6)</td>",
		"<td>Worker phases with model: &#43; judge: &#43; base-check:</td>",
		"<td>75.0% (3 / 4)</td>",
		"<td>Base-Check verdicts (PASS / FAIL)</td>",
		"<td>Judge verdicts (PASS / FAIL)</td>",
		"<td>Build-time questions</td>",
		"<td>Search : grep</td>",
		"<td>Read-ledger blocks</td>",
		"<td>fyr auto model runs</td>",
		"<td>DoD closed</td>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML report missing %q", want)
		}
	}

	// Verify Store.ModelEvidence across unlabelled + labelled waves and trials.
	ev, err := s.ModelEvidence(win.Since, win.Now)
	if err != nil {
		t.Fatalf("ModelEvidence: %v", err)
	}
	argon := ev["gemini-4-argon"]
	if argon.WorkRuns != 4 || argon.WorkPass != 3 || math.Abs(argon.WorkMeanSeconds-40.0) > 0.1 || argon.EvalRuns != 1 || argon.EvalPass != 1 {
		t.Errorf("gemini-4-argon evidence = %+v, want WorkRuns=4 WorkPass=3 WorkMeanSeconds=40 EvalRuns=1 EvalPass=1", argon)
	}
	flash := ev["gemini-3.8-flash"]
	if flash.WorkRuns != 3 || flash.WorkPass != 2 || flash.EvalRuns != 0 || flash.EvalPass != 0 {
		t.Errorf("gemini-3.8-flash evidence = %+v, want WorkRuns=3 WorkPass=2 EvalRuns=0 EvalPass=0", flash)
	}
	opus := ev["claude-opus-5-5"]
	if opus.WorkRuns != 0 || opus.EvalRuns != 1 || opus.EvalPass != 0 {
		t.Errorf("claude-opus-5-5 evidence = %+v, want WorkRuns=0 EvalRuns=1 EvalPass=0", opus)
	}
}
