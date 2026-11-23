package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/webpreview"
)

// agyHome writes one Antigravity conversation, id, in the workspace /p under
// home: a run_command of `git status` on 2026-10-08 and one model call whose
// context is 5 tokens, in the shapes internal/metrics reads. Called again
// on the same home it adds another conversation beside the first.
func agyHome(t *testing.T, home, id string) {
	t.Helper()
	agyHomeIn(t, home, id, "/p")
}

// agyHomeIn is agyHome with the conversation's workspace at ws.
func agyHomeIn(t *testing.T, home, id, ws string) {
	t.Helper()
	agyHomeModel(t, home, id, ws, "")
}

// agyHomeModel is agyHomeIn with its model call naming model ("" names none).
func agyHomeModel(t *testing.T, home, id, ws, model string) {
	t.Helper()
	pb := func(n int, b []byte) []byte {
		out := binary.AppendUvarint(nil, uint64(n<<3|2))
		return append(binary.AppendUvarint(out, uint64(len(b))), b...)
	}
	pv := func(n int, v uint64) []byte { return binary.AppendUvarint(binary.AppendUvarint(nil, uint64(n<<3)), v) }
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(filepath.Join(dir, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	exec := func(path string, stmts ...string) *sql.DB {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range stmts {
			if _, err := db.Exec(q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		return db
	}
	exec(filepath.Join(dir, "conversation_summaries.db"),
		"CREATE TABLE IF NOT EXISTS conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')",
		`INSERT INTO conversation_summaries(conversation_id, last_modified_time, workspace_uris) VALUES ('`+id+`', '2026-10-08 10:05:00+00:00', '["file://`+ws+`"]')`).Close()
	db := exec(filepath.Join(dir, "conversations", id+".db"),
		"CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)",
		"CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
	defer db.Close()
	const t0 = 1791453600 // 2026-10-08 10:00:00Z
	meta := append(pb(1, pv(1, t0)), pb(4, append(pb(2, []byte("run_command")), pb(3, []byte(`{"CommandLine":"git status"}`))...))...)
	call := append(pb(4, append(pv(2, 5), pv(3, 1)...)), pb(9, pb(10, pv(1, 5)))...)
	if model != "" {
		call = append(call, pb(19, []byte(model))...)
	}
	gen := pb(1, call)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO steps(idx, step_type, status, metadata) VALUES (0, 132, 3, ?)`, []any{meta}},
		{`INSERT INTO gen_metadata(idx, data) VALUES (0, ?)`, []any{gen}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
}

// TestMetricsVerb: ingest reads the Antigravity CLI's conversations under
// --home, never the real home's (R6), prints its summary line, and export
// writes the store and refuses to overwrite.
func TestMetricsVerb(t *testing.T) {
	home := t.TempDir()
	agyHome(t, home, "S1")
	decoy := t.TempDir()
	agyHome(t, decoy, "DECOY")
	t.Setenv("HOME", decoy)

	var out bytes.Buffer
	if err := run([]string{"metrics", "ingest", "--home", home}, &out); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "metrics: 1 sessions, 2 steps") {
		t.Fatalf("ingest printed %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir, "metrics.db")); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := run([]string{"metrics", "show", "--json", "--since", "2026-10-01", "--home", home}, &out); err != nil {
		t.Fatalf("show: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), `"tool_calls_by_tool"`) || !strings.Contains(out.String(), `"Bash": 1`) {
		t.Fatalf("show --json printed %s", out.String())
	}
	// The context target: 300000 unless --context-target says otherwise; the
	// one session (peak 5) is under the default and over a target of 3.
	if !strings.Contains(out.String(), `"context_target": 300000`) || !strings.Contains(out.String(), `"agy": 0`) {
		t.Fatalf("show --json lacks the default context target or the over-target count:\n%s", out.String())
	}
	out.Reset()
	if err := run([]string{"metrics", "show", "--json", "--since", "2026-10-01", "--context-target", "3", "--home", home}, &out); err != nil {
		t.Fatalf("show --context-target: %v\n%s", err, out.String())
	}
	var got struct {
		B struct {
			ContextTarget int64            `json:"context_target"`
			Over          map[string]*int  `json:"sessions_over_context_target"`
			Peaks         map[string][]any `json:"session_peaks"`
		}
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.B.ContextTarget != 3 || got.B.Over["agy"] == nil || *got.B.Over["agy"] != 1 || len(got.B.Peaks["agy"]) != 1 {
		t.Fatalf("show --json --context-target 3 printed:\n%s", out.String())
	}
	// --until closes the window: a window that ends before the session has
	// none of its tool calls (the previous-window read /retro does).
	out.Reset()
	if err := run([]string{"metrics", "show", "--json", "--since", "2026-10-01", "--until", "2026-10-05", "--home", home}, &out); err != nil {
		t.Fatalf("show --until: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), `"Bash": 1`) || !strings.Contains(out.String(), `"until": "2026-10-05"`) {
		t.Fatalf("show --until 2026-10-05 still counts the 10-08 session, or names another end:\n%s", out.String())
	}
	out.Reset()
	if err := run([]string{"metrics", "show", "--since", "2026-10-01", "--home", home}, &out); err != nil || !strings.Contains(out.String(), "B. efficiency") {
		t.Fatalf("show: %v\n%s", err, out.String())
	}

	rep := filepath.Join(t.TempDir(), "r.html")
	out.Reset()
	if err := run([]string{"metrics", "report", "--since", "2026-10-01", "--context-target", "3", "--out", rep, "--home", home}, &out); err != nil {
		t.Fatalf("report: %v\n%s", err, out.String())
	}
	if b, err := os.ReadFile(rep); err != nil || !bytes.Contains(b, []byte("Tool calls per day")) || !bytes.Contains(b, []byte("sessions over context target (3)</td><td>1 of 1")) {
		t.Fatalf("report file: %v, or it lacks the over-target row at the flag's target", err)
	}

	exp := filepath.Join(t.TempDir(), "m.jsonl")
	out.Reset()
	if err := run([]string{"metrics", "export", "--home", home, exp}, &out); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}
	b, _ := os.ReadFile(exp)
	if !bytes.Contains(b, []byte(`"S1"`)) || bytes.Contains(b, []byte("DECOY")) {
		t.Fatalf("export holds %s", b)
	}
	if err := run([]string{"metrics", "export", "--home", home, exp}, &out); err == nil {
		t.Fatal("export overwrote an existing file")
	}
}

func TestMetricsUsageErrorsExit2(t *testing.T) {
	home := t.TempDir()
	for _, args := range [][]string{
		{"metrics"},
		{"metrics", "frobnicate"},
		{"metrics", "ingest", "extra"},
		{"metrics", "ingest", "--source", home},
		{"metrics", "ingest", "--label", "a=b"},
		{"metrics", "ingest", "--source", home, "--label", "a=b", "--outcome", "maybe"},
		{"metrics", "ingest", "--vendor", "gpt"},
		{"metrics", "export"},
		{"metrics", "show", "extra"},
		{"metrics", "show", "--since", "yesterday"},
		{"metrics", "show", "--until", "soon"},
		{"metrics", "show", "--since", "2026-10-05", "--until", "2026-10-01"},
		{"metrics", "ingest", "--until", "2026-10-01"},
		{"metrics", "show", "--label", "a=b", "--all-labels"},
		{"metrics", "show", "--label", "bare"},
		{"report", "--label", "bare"},
		{"metrics", "show", "--outcome", "pass"},
		{"metrics", "show", "--out", filepath.Join(home, "x.html")},
		{"metrics", "report", "--md", "--json"},
		{"metrics", "report", "--listen", "127.0.0.1:0"},
		{"metrics", "report", "--web", "--md"},
		{"metrics", "report", "--web", "--json"},
		{"metrics", "report", "--web", "--out", filepath.Join(home, "x.html")},
		{"metrics", "show", "--web"},
		{"metrics", "show", "--listen", "127.0.0.1:0"},
		{"report", "--md", "--json"},
		{"report", "--listen", "127.0.0.1:0"},
		{"report", "--web", "--md"},
		{"report", "--web", "--json"},
		{"report", "--web", "--out", filepath.Join(home, "x.html")},
		{"metrics", "report", "extra"},
		{"metrics", "ingest", "--json"},
		{"metrics", "ingest", "--since", "2026-10-01"},
		{"metrics", "ingest", "--project", "."},
		{"metrics", "ingest", "--all-labels"},
		{"metrics", "ingest", "--out", filepath.Join(home, "x.html")},
		{"metrics", "export", "--json", filepath.Join(home, "x.jsonl")},
		{"metrics", "export", "--label", "a=b", filepath.Join(home, "x.jsonl")},
		{"metrics", "show", "--context-target", "0"},
		{"metrics", "report", "--context-target", "-1", "--out", filepath.Join(home, "x.html")},
		{"metrics", "ingest", "--context-target", "1"},
		{"metrics", "export", "--context-target", "1", filepath.Join(home, "x.jsonl")},
		{"metrics", "evals", "extra"},
		{"metrics", "evals", "--project", "."},
		{"metrics", "evals", "--label", "eval=smoke"},
		{"metrics", "evals", "--all-labels"},
		{"metrics", "evals", "--context-target", "1"},
		{"metrics", "evals", "--out", filepath.Join(home, "x.html")},
		{"metrics", "evals", "--since", "yesterday"},
		{"metrics", "evals", "--since", "2026-10-05", "--until", "2026-10-01"},
		{"metrics", "show", "--harness", "abc1234"},
		{"metrics", "report", "--harness", "abc1234", "--out", filepath.Join(home, "x.html")},
		{"metrics", "ingest", "--harness", "abc1234"},
		{"metrics", "export", "--harness", "abc1234", filepath.Join(home, "x.jsonl")},
		{"metrics", "show", "--limit", "5"},
		{"metrics", "trajectory", "s1", "s2"},
		{"metrics", "trajectory", "--limit", "-1"},
		{"metrics", "trajectory", "--since", "2026-10-01"},
		{"metrics", "trajectory", "--harness", "abc1234"},
		{"metrics", "show", "--since", "2026-10-01", "--since-tag", "v9.6.3"},
		{"report", "--since", "2026-10-01", "--since-tag", "v9.6.3"},
		{"metrics", "show", "--since-tag", ""},
		{"report", "--since-tag", ""},
		{"metrics", "show", "--since-tag", "--bad"},
		{"metrics", "show", "--since-tag", "no-such-tag-999"},
		{"metrics", "evals", "--since-tag", "v9.6.3"},
		{"metrics", "ingest", "--since-tag", "v9.6.3"},
	} {
		err := run(append(args, "--home", home), &bytes.Buffer{})
		var code exitCode
		if !errors.As(err, &code) || code != 2 {
			t.Errorf("%q: err = %v, want exit 2", args, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir, "metrics.db")); !os.IsNotExist(err) {
		t.Errorf("a usage error created the store: %v", err)
	}
	// Each verb's own usage line names every flag it takes (security review
	// 6118921..c478d86: report's omitted --until).
	for verb, flag := range map[string]string{"report": "--until", "report ": "--context-target"} {
		verb = strings.TrimSpace(verb)
		var out bytes.Buffer
		run([]string{"metrics", verb, "extra", "--home", home}, &out)
		if !strings.Contains(out.String(), flag) {
			t.Errorf("metrics %s usage lacks %s: %q", verb, flag, out.String())
		}
	}
}

// show, report, evals and export on a home that never ingested say so and exit 1;
// they never create an empty store whose zeros would read as "no sessions".
func TestMetricsWithoutStore(t *testing.T) {
	home := t.TempDir()
	for _, args := range [][]string{
		{"metrics", "show"},
		{"metrics", "report", "--out", filepath.Join(t.TempDir(), "r.html")},
		{"metrics", "export", filepath.Join(t.TempDir(), "e.jsonl")},
		{"metrics", "evals"},
		{"metrics", "trajectory"},
		{"report", "--out", filepath.Join(t.TempDir(), "r.html")},
	} {
		var out bytes.Buffer
		err := run(append(args, "--home", home), &out)
		var code exitCode
		if !errors.As(err, &code) || code != 1 || !strings.Contains(out.String(), "no metrics store") {
			t.Errorf("%q: err %v, out %q; want exit 1 naming the missing store", args, err, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir, "metrics.db")); !os.IsNotExist(err) {
		t.Errorf("a read verb created the store: %v", err)
	}
}

func TestMetricsTrajectoryCLI(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "proj")
	agyHomeIn(t, home, "11111111-1111-1111-1111-111111111111", ws)
	var out bytes.Buffer
	if err := run([]string{"metrics", "ingest", "--home", home}, &out); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	out.Reset()
	if err := run([]string{"metrics", "trajectory", "--limit", "5", "--json", "--home", home}, &out); err != nil {
		t.Fatalf("trajectory --json: %v", err)
	}
	var list []metrics.SessionTrajectory
	if err := json.Unmarshal(out.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal trajectory json: %v (%s)", err, out.String())
	}
	if len(list) != 1 || list[0].SessionID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("trajectory list = %+v", list)
	}

	out.Reset()
	if err := run([]string{"metrics", "trajectory", "11111111", "--home", home}, &out); err != nil {
		t.Fatalf("trajectory prefix: %v", err)
	}
	if !strings.Contains(out.String(), "11111111") {
		t.Errorf("trajectory text output = %q", out.String())
	}
}

func TestReportVerbCLI(t *testing.T) {
	home := t.TempDir()
	repo, _ := gitFixture(t)
	other := filepath.Join(t.TempDir(), "other-proj")
	agyHomeIn(t, home, "11111111-1111-1111-1111-111111111111", repo)
	agyHomeIn(t, home, "22222222-2222-2222-2222-222222222222", other)
	var out bytes.Buffer
	if err := run([]string{"metrics", "ingest", "--home", home}, &out); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	// Usage refusals: --project + --global, bare --project outside checkout,
	// and --global / --md on non-report metrics subcommands.
	nonGit := t.TempDir()
	for _, args := range [][]string{
		{"report", "--project", repo, "--global"},
		{"report", "--project", "--global"},
		{"report", "--project", "--dir", nonGit},
		{"metrics", "report", "--project", "--global"},
		{"metrics", "report", "--project", "--dir", nonGit},
		{"metrics", "show", "--global"},
		{"metrics", "show", "--md"},
	} {
		out.Reset()
		err := run(append(args, "--home", home), &out)
		var code exitCode
		if !errors.As(err, &code) || code != 2 {
			t.Errorf("%v: err = %v, want exit 2 (out %q)", args, err, out.String())
		}
	}

	// Bare or flag-shaped `--project` on non-report `metrics` subcommands exits 2
	// naming `--project` without swallowing subsequent flags or suggesting --global (BUG-535).
	for _, args := range [][]string{
		{"metrics", "show", "--dir", nonGit, "--home", home, "--project"},
		{"metrics", "show", "--dir", nonGit, "--home", home, "--project", "--json"},
		{"metrics", "show", "--project", "--dir", nonGit, "--home", home},
		{"metrics", "show", "--project", "--home", home},
		{"metrics", "show", "--project=--json", "--dir", nonGit, "--home", home},
		{"metrics", "trajectory", "--project", "--json", "--home", home},
		{"report", "--project=", "--home", home},
		{"report", "--project=--md", "--home", home},
	} {
		out.Reset()
		err := run(args, &out)
		var showCode exitCode
		if !errors.As(err, &showCode) || showCode != 2 {
			t.Errorf("%v: err = %v, want exit 2 (out %q)", args, err, out.String())
		}
		if !strings.Contains(out.String(), "--project") {
			t.Errorf("%v: error must name --project, got %q", args, out.String())
		}
		if strings.Contains(out.String(), "--global") || strings.Contains(out.String(), "takes no arguments") {
			t.Errorf("%v: unexpected refusal text %q", args, out.String())
		}
	}

	// Explicit `fyr report --project .` in a non-git directory resolves "." like "$PWD" (1 session).
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"report", "--project", ".", "--md", "--since", "2026-10-01", "--dir", other, "--home", home}, &out); err != nil {
		t.Fatalf("report --project . in non-git dir: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "| Sessions | 1 |") {
		t.Errorf("report --project . in non-git dir output:\n%s", out.String())
	}

	// Inside a git checkout, bare `fyr report --md` defaults to --project <repo>
	// (1 session) and prints the 5 sections to stdout.
	out.Reset()
	if err := run([]string{"report", "--md", "--since", "2026-10-01", "--dir", repo, "--home", home}, &out); err != nil {
		t.Fatalf("report --md in checkout: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"## 1. Executive Pulse",
		"| Sessions | 1 |",
		"## 2. Second-Brain Leverage",
		"## 3. Quality & Learning Loop",
		"## 4. Autonomy & Model Routing",
		"## 5. Per-Project / Project Breakdown",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report --md in checkout lacks %q:\n%s", want, out.String())
		}
	}

	// Bare `--project` flag before `--md` normalizes to `--project=.`.
	out.Reset()
	if err := run([]string{"report", "--project", "--md", "--since", "2026-10-01", "--dir", repo, "--home", home}, &out); err != nil {
		t.Fatalf("report --project --md: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "| Sessions | 1 |") {
		t.Errorf("report --project --md output:\n%s", out.String())
	}

	// Outside a git checkout, bare `fyr report --md` defaults to --global (2 sessions).
	out.Reset()
	if err := run([]string{"report", "--md", "--since", "2026-10-01", "--dir", nonGit, "--home", home}, &out); err != nil {
		t.Fatalf("report --md outside checkout: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "| Sessions | 2 |") || !strings.Contains(out.String(), repo) {
		t.Errorf("report --md outside checkout should be global:\n%s", out.String())
	}

	// --md --out writes a 0600 file and refuses overwrite; HTML --out writes HTML.
	mdFile := filepath.Join(t.TempDir(), "rep.md")
	out.Reset()
	if err := run([]string{"report", "--global", "--md", "--since", "2026-10-01", "--out", mdFile, "--home", home}, &out); err != nil {
		t.Fatalf("report --md --out: %v\n%s", err, out.String())
	}
	if err := run([]string{"report", "--global", "--md", "--since", "2026-10-01", "--out", mdFile, "--home", home}, &out); err == nil {
		t.Fatal("report --md --out overwrote an existing file")
	}
	htmlFile := filepath.Join(t.TempDir(), "rep.html")
	out.Reset()
	if err := run([]string{"report", "--global", "--since", "2026-10-01", "--out", htmlFile, "--home", home}, &out); err != nil {
		t.Fatalf("report --global --out: %v\n%s", err, out.String())
	}
	if b, err := os.ReadFile(htmlFile); err != nil || !bytes.Contains(b, []byte("<h2>1. Executive Pulse</h2>")) {
		t.Fatalf("HTML report file: %v", err)
	}
}

func TestReportV9HealthAndDevEvalCLI(t *testing.T) {
	home := t.TempDir()
	decoy := t.TempDir()
	t.Setenv("HOME", decoy)
	repo, _ := gitFixture(t)
	agyHomeModel(t, home, "11111111-1111-1111-1111-111111111111", repo, "gemini-4-argon")

	var out bytes.Buffer
	if err := run([]string{"metrics", "ingest", "--home", home}, &out); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out.String())
	}

	// Seed dev/fyr-eval store, runs.json, models.json, search.db, brain.db, and judge_verdicts.
	devStore, err := metrics.Open(filepath.Join(home, harness.StateDir, "dev", "fyr-eval", harness.StateDir, "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := devStore.RecordTrial(metrics.Trial{
		Run: "r/smoke/1", Label: "eval=smoke", RowVersion: 1, Kind: "capability", Harness: "v92",
		Status: "pass", StartedAt: mustParseDayT(t, "2026-10-08"), Seconds: 8,
	}); err != nil {
		t.Fatal(err)
	}
	devStore.Close()

	if err := metrics.RecordJudgeVerdict(home, metrics.JudgeVerdictRow{
		TS: mustParseDayT(t, "2026-10-08").UnixMilli(), ProjectRoot: repo, Target: "phase-2b",
		Model: "claude-opus-5-5", CriterionHash: "c1", Verdict: "PASS",
	}); err != nil {
		t.Fatal(err)
	}
	writeTestStateFile(t, filepath.Join(home, harness.StateDir, "auto", "runs.json"),
		`{"date":"2026-10-08","model_runs":2,"last_run":"2026-10-08T10:00:00Z","verdict":"health: ok","open_asks":1,"projects":{"p":{}}}`)
	writeTestStateFile(t, filepath.Join(home, harness.StateDir, "models.json"),
		`{"updated_at":"2026-10-08T10:00:00Z","pools":[{"name":"Claude","five":85,"week":95}]}`)

	sdbPath := filepath.Join(repo, ".agents", "scratch", "search.db")
	if err := os.MkdirAll(filepath.Dir(sdbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	execSQLiteT(t, sdbPath,
		`CREATE TABLE session_meta(conversation_id TEXT PRIMARY KEY, generation_id INT, last_transcript_bytes INT, turn_count INT, est_tokens INT)`,
		`INSERT INTO session_meta VALUES ('11111111-1111-1111-1111-111111111111', 2, 1000, 4, 250)`,
	)
	execSQLiteT(t, filepath.Join(home, harness.StateDir, "brain.db"),
		`CREATE TABLE records(rid INTEGER PRIMARY KEY, scope TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL, date TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, project TEXT NOT NULL DEFAULT '', updated TEXT NOT NULL, provenance TEXT NOT NULL DEFAULT '', UNIQUE(scope, id))`,
		`INSERT INTO records(scope, id, kind, status, date, title, body, project, updated, provenance) VALUES ('`+repo+`', 'REVIEW-2026-10-08-1000-auto', 'review', 'closed', '2026-10-08', 'auto report: repo', 'findings', '', '2026-10-08T10:00:00Z', '')`,
		`INSERT INTO records(scope, id, kind, status, date, title, body, project, updated, provenance) VALUES ('`+repo+`', 'HANDOFF-2026-10-08-1100-x', 'handoff', '', '2026-10-08', 'handoff', 'done', '', '2026-10-08T11:00:00Z', '')`,
	)

	// 1. metrics show --json carries health and merged dev eval pass^k.
	out.Reset()
	if err := run([]string{"metrics", "show", "--json", "--since", "2026-10-01", "--until", "2026-10-10", "--home", home}, &out); err != nil {
		t.Fatalf("show --json: %v\n%s", err, out.String())
	}
	for _, want := range []string{`"health": {`, `"search_db_compactions": 2`, `"judge_passed": 1`, `"eval=smoke"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("show --json lacks %q:\n%s", want, out.String())
		}
	}

	// 2. report --global --md surfaces v9 health rows, brain enrichment, and dev eval rows.
	out.Reset()
	if err := run([]string{"report", "--global", "--md", "--since", "2026-10-01", "--until", "2026-10-10", "--home", home}, &out); err != nil {
		t.Fatalf("report --md: %v\n%s", err, out.String())
	}
	md := out.String()
	for _, want := range []string{
		"| Search.db compactions | 2 |",
		"| Cross-family Judge (PASS / FAIL / fallback) | 1 / 0 / 0 |",
		"Claude: 5h=85% wk=95%",
		"Auto findings",
		"Handoff lag (d)",
		"## Eval rows",
		"| smoke |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report --md lacks %q:\n%s", want, md)
		}
	}

	// 3. report --global HTML renders both Eval rows and v9 health without scripts/URLs.
	htmlFile := filepath.Join(t.TempDir(), "v9.html")
	out.Reset()
	if err := run([]string{"report", "--global", "--since", "2026-10-01", "--until", "2026-10-10", "--out", htmlFile, "--home", home}, &out); err != nil {
		t.Fatalf("report HTML: %v\n%s", err, out.String())
	}
	raw, err := os.ReadFile(htmlFile)
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)
	for _, want := range []string{"<h2>Eval rows</h2>", "<td>smoke</td>", "Claude: 5h=85% wk=95%", "<h2>Trigger coverage</h2>"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
}

func writeTestStateFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func execSQLiteT(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func mustParseDayT(t *testing.T, day string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Add(10 * time.Hour)
}

func TestMetricsSubcommandEmptyAndZeroFlagsRefusedViaUsageErr(t *testing.T) {
	home := t.TempDir()
	for _, args := range [][]string{
		{"metrics", "show", "--limit", "0"},
		{"metrics", "show", "--source", ""},
		{"metrics", "show", "--harness", ""},
		{"metrics", "show", "--out", ""},
		{"metrics", "report", "--limit", "0"},
		{"metrics", "report", "--harness", ""},
		{"metrics", "report", "--out", ""},
		{"metrics", "report", "--web", "--listen", ""},
	} {
		var out bytes.Buffer
		err := run(append(args, "--home", home), &out)
		if exitOf(err) != 2 || !strings.Contains(out.String(), "usage: fyr metrics") {
			t.Errorf("%v: exit=%d out=%q, want exit 2 with usage: fyr metrics", args, exitOf(err), out.String())
		}
	}
}

func TestVerbReportWebAndFlags(t *testing.T) {
	home := t.TempDir()
	agyHomeIn(t, home, "11111111-1111-1111-1111-111111111111", filepath.Join(t.TempDir(), "real-app"))
	agyHomeIn(t, home, "22222222-2222-2222-2222-222222222222", filepath.Join(t.TempDir(), "fyr-eval", "trial-1"))

	var out bytes.Buffer
	if err := run([]string{"metrics", "ingest", "--home", home}, &out); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	assertReportJSONAndSandboxes(t, home)

	origServe := serveWebPreview
	t.Cleanup(func() { serveWebPreview = origServe })
	serveWebPreview = func(_ context.Context, assets map[string]webpreview.Asset, opts webpreview.Options) error {
		return exerciseReportWebPreview(t, assets, opts)
	}
	out.Reset()
	if err := run([]string{"report", "--global", "--web", "--listen", "127.0.0.1:0", "--since", "2026-10-01", "--home", home}, &out); err != nil {
		t.Fatalf("report --web: %v\n%s", err, out.String())
	}
}

func assertReportJSONAndSandboxes(t *testing.T, home string) {
	t.Helper()
	var out bytes.Buffer
	if err := run([]string{"report", "--global", "--json", "--since", "2026-10-01", "--home", home}, &out); err != nil {
		t.Fatalf("report --json: %v\n%s", err, out.String())
	}
	var jr metrics.JSONReport
	if err := json.Unmarshal(out.Bytes(), &jr); err != nil || jr.Scorecard.Sessions != 2 {
		t.Fatalf("report --json decoded = %+v, err=%v\n%s", jr, err, out.String())
	}
	jsonFile := filepath.Join(t.TempDir(), "out.json")
	out.Reset()
	if err := run([]string{"metrics", "report", "--global", "--json", "--since", "2026-10-01", "--out", jsonFile, "--home", home}, &out); err != nil {
		t.Fatalf("metrics report --json --out: %v", err)
	}
	if fi, err := os.Stat(jsonFile); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("json out mode: err=%v fi=%v", err, fi)
	}
	if err := run([]string{"metrics", "report", "--global", "--json", "--since", "2026-10-01", "--out", jsonFile, "--home", home}, &out); err == nil {
		t.Fatal("--json --out overwrote existing file")
	}
	out.Reset()
	if err := run([]string{"metrics", "show", "--json", "--since", "2026-10-01", "--home", home}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "fyr-eval") {
		t.Fatalf("default show --json included sandbox project:\n%s", out.String())
	}
	out.Reset()
	if err := run([]string{"metrics", "show", "--json", "--include-sandboxes", "--since", "2026-10-01", "--home", home}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "fyr-eval") {
		t.Fatalf("show --include-sandboxes omitted sandbox project:\n%s", out.String())
	}
	out.Reset()
	if err := run([]string{"report", "--global", "--md", "--include-sandboxes", "--since", "2026-10-01", "--home", home}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "fyr-eval") {
		t.Fatalf("report --include-sandboxes omitted sandbox project:\n%s", out.String())
	}
}

func exerciseReportWebPreview(t *testing.T, assets map[string]webpreview.Asset, opts webpreview.Options) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	pr, pw := io.Pipe()
	opts.Out = pw
	done := make(chan error, 1)
	go func() {
		defer pw.Close()
		done <- webpreview.Serve(ctx, assets, opts)
	}()
	line, err := bufio.NewReader(pr).ReadString('\n')
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	baseURL := strings.TrimSpace(line)
	if err != nil || !strings.HasPrefix(baseURL, "http://") {
		cancel()
		t.Fatalf("webpreview emitted %q, err=%v", line, err)
	}
	assertPreviewGet(t, client, baseURL, "/", "0. Scorecard", reportWebHTMLCSP)
	assertPreviewGet(t, client, baseURL, "/report.html", "Background Autonomy", reportWebHTMLCSP)
	assertPreviewGet(t, client, baseURL, "/report.md", "## 0. Scorecard", "default-src 'none'")
	assertPreviewGet(t, client, baseURL, "/report.json", `"scorecard"`, "default-src 'none'")
	origin := baseURL[:len("http://")+strings.Index(strings.TrimPrefix(baseURL, "http://"), "/")]
	resp, err := client.Get(origin + "/00000000000000000000000000000000/")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("wrong path status = %d, want 404", resp.StatusCode)
	}
	resp, err = client.Post(baseURL, "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", resp.StatusCode)
	}
	client.CloseIdleConnections()
	cancel()
	return <-done
}

func assertPreviewGet(t *testing.T, client *http.Client, baseURL, suffix, wantBody, wantCSP string) {
	t.Helper()
	resp, err := client.Get(strings.TrimSuffix(baseURL, "/") + suffix)
	if err != nil {
		t.Fatalf("GET %s: %v", suffix, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), wantBody) {
		t.Errorf("GET %s: status=%d body=%q", suffix, resp.StatusCode, body)
	}
	if gotCSP := resp.Header.Get("Content-Security-Policy"); gotCSP != wantCSP {
		t.Errorf("GET %s CSP = %q, want %q", suffix, gotCSP, wantCSP)
	}
}

func TestMetricsSinceTagCLI(t *testing.T) {
	home := t.TempDir()
	repo, _ := gitFixture(t)
	runGitAtT(t, repo, "2026-10-08T10:00:00Z", "commit", "--allow-empty", "-m", "v1 release")
	runGitAtT(t, repo, "2026-10-08T10:00:00Z", "tag", "-a", "v1.0.0", "-m", "v1.0.0")
	seedSinceTagStoreT(t, home, repo)

	var out bytes.Buffer
	if err := run([]string{"metrics", "show", "--json", "--project", repo, "--since-tag", "v1.0.0", "--home", home}, &out); err != nil {
		t.Fatalf("metrics show --since-tag: %v\n%s", err, out.String())
	}
	var sum metrics.Summary
	if err := json.Unmarshal(out.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.TagCohort == nil || sum.TagCohort.Tag != "v1.0.0" || sum.TagCohort.Pre.Sessions != 1 || sum.TagCohort.Post.Sessions != 1 {
		t.Fatalf("TagCohort = %+v", sum.TagCohort)
	}
	out.Reset()
	if err := run([]string{"report", "--md", "--since-tag", "v1.0.0", "--dir", repo, "--home", home}, &out); err != nil {
		t.Fatalf("report --md --since-tag: %v\n%s", err, out.String())
	}
	for _, want := range []string{"Since tag `v1.0.0`", "## Tag Cohort (v1.0.0)", "## Agent Roles"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report --md missing %q:\n%s", want, out.String())
		}
	}
}

func runGitAtT(t *testing.T, dir, isoDate string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(gitCleanEnv(), "GIT_AUTHOR_DATE="+isoDate, "GIT_COMMITTER_DATE="+isoDate, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func seedSinceTagStoreT(t *testing.T, home, repo string) {
	t.Helper()
	preTS := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC).UnixMilli()
	postTS := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC).UnixMilli()
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	execSQLiteT(t, metrics.Path(home),
		`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES ('PRE', 'agy', '`+repo+`', `+jsonInt(preTS)+`, `+jsonInt(preTS+1000)+`), ('POST', 'agy', '`+repo+`', `+jsonInt(postTS)+`, `+jsonInt(postTS+1000)+`)`,
		`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('PRE', 'p1', `+jsonInt(preTS+10)+`, 'tool', 'Bash'), ('POST', 'q1', `+jsonInt(postTS+10)+`, 'tool', 'Bash')`,
		`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES ('PRE', 'p1', 0, 'cat'), ('POST', 'q1', 0, 'fyr search --slice')`,
	)
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }

func TestDailyDigestCLIAndFlagRejections(t *testing.T) {
	home := t.TempDir()
	agyHome(t, home, "DD1")
	var out bytes.Buffer
	if err := run([]string{"metrics", "ingest", "--home", home}, &out); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out.String())
	}

	// Text output via `fyr report --daily-digest --global`.
	out.Reset()
	if err := run([]string{"report", "--daily-digest", "--global", "--home", home}, &out); err != nil {
		t.Fatalf("report --daily-digest: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Daily Health Digest (") || !strings.Contains(out.String(), "I did:") {
		t.Fatalf("unexpected --daily-digest text:\n%s", out.String())
	}
	digestFile := metrics.LatestDailyDigestPath(home)
	if _, err := os.Stat(digestFile); err != nil {
		t.Fatalf("missing latest-daily-digest.json at %s: %v", digestFile, err)
	}

	// JSON output via `fyr metrics report --daily-digest --json --global`.
	out.Reset()
	if err := run([]string{"metrics", "report", "--daily-digest", "--json", "--global", "--home", home}, &out); err != nil {
		t.Fatalf("metrics report --daily-digest --json: %v\n%s", err, out.String())
	}
	var dd metrics.DailyDigest
	if err := json.Unmarshal(out.Bytes(), &dd); err != nil || dd.Day == "" || dd.Verdict == "" {
		t.Fatalf("unmarshal --daily-digest --json: %v\n%s", err, out.String())
	}

	// Subsequent normal reports include the Daily Direction & Actions banner / daily_digest JSON.
	out.Reset()
	if err := run([]string{"report", "--global", "--md", "--since", "2026-10-01", "--home", home}, &out); err != nil {
		t.Fatalf("report --md: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "## Daily Direction & Actions") {
		t.Fatalf("report --md missing Daily Direction & Actions:\n%s", out.String())
	}
	out.Reset()
	if err := run([]string{"report", "--global", "--json", "--since", "2026-10-01", "--home", home}, &out); err != nil {
		t.Fatalf("report --json: %v\n%s", err, out.String())
	}
	var jr metrics.JSONReport
	if err := json.Unmarshal(out.Bytes(), &jr); err != nil || jr.DailyDigest == nil {
		t.Fatalf("report --json missing daily_digest: %+v (%v)", jr.DailyDigest, err)
	}

	// Conflicting flags and non-report verbs must exit 2.
	for _, args := range [][]string{
		{"report", "--daily-digest", "--md"},
		{"report", "--daily-digest", "--web"},
		{"report", "--daily-digest", "--out", filepath.Join(home, "x.txt")},
		{"report", "--daily-digest", "--listen", "127.0.0.1:0"},
		{"metrics", "report", "--daily-digest", "--md"},
		{"metrics", "report", "--daily-digest", "--web"},
		{"metrics", "report", "--daily-digest", "--out", filepath.Join(home, "x.txt")},
		{"metrics", "show", "--daily-digest"},
		{"metrics", "ingest", "--daily-digest"},
		{"metrics", "evals", "--daily-digest"},
		{"metrics", "trajectory", "--daily-digest"},
		{"metrics", "export", "--daily-digest", filepath.Join(home, "e.jsonl")},
	} {
		out.Reset()
		if err := run(append(args, "--home", home), &out); exitOf(err) != 2 {
			t.Errorf("%v: exit=%d, want 2 (out=%q)", args, exitOf(err), out.String())
		}
	}
}

func TestDailyDigestProjectScopingCLI(t *testing.T) {
	home := t.TempDir()
	decoy := t.TempDir()
	t.Setenv("HOME", decoy)
	repoA, _ := gitFixture(t)
	repoB, _ := gitFixture(t)

	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	now := time.Now().UTC()
	recentMS := now.Add(-2 * time.Hour).UnixMilli()
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	execSQLiteT(t, metrics.Path(home),
		`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES `+
			`('SA1', 'agy', '`+repoA+`', `+jsonInt(recentMS)+`, `+jsonInt(recentMS+1000)+`),`+
			`('SB1', 'agy', '`+repoB+`', `+jsonInt(recentMS)+`, `+jsonInt(recentMS+1000)+`),`+
			`('SB2', 'agy', '`+repoB+`', `+jsonInt(recentMS+2000)+`, `+jsonInt(recentMS+3000)+`)`,
		`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('SB1', 'b1', `+jsonInt(recentMS+100)+`, 'tool', 'Bash')`,
		`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES ('SB1', 'b1', 0, 'cat')`,
	)

	s, err = metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	globalPrev := metrics.DailyDigest{
		Day:           yesterday,
		Verdict:       metrics.VerdictRegress,
		PreInvocation: metrics.PreInvocationSnapshot{TurnVets: 42},
		Signals: []metrics.DigestAction{{
			SignalID: "sig:global:sentinel_prev", Severity: "high", Rule: "pre_invocation_timeouts",
		}},
	}
	if err := metrics.SaveDailyDigest(s, globalPrev); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	sentinelDisk := metrics.DailyDigest{Day: "2026-01-01", Verdict: metrics.VerdictRegress}
	if err := metrics.WriteLatestDailyDigest(home, sentinelDisk); err != nil {
		t.Fatal(err)
	}

	// Project-scoped `fyr report --daily-digest` inside repoA computes only repoA
	// without diffing against global PreviousDailyDigest or overwriting global state.
	var out bytes.Buffer
	if err := run([]string{"report", "--daily-digest", "--json", "--dir", repoA, "--home", home}, &out); err != nil {
		t.Fatalf("report --daily-digest in repoA: %v\n%s", err, out.String())
	}
	var projDD metrics.DailyDigest
	if err := json.Unmarshal(out.Bytes(), &projDD); err != nil {
		t.Fatalf("unmarshal project daily digest: %v\n%s", err, out.String())
	}
	if projDD.Rolling7d.Sessions.Current != 1 || projDD.Rolling7d.BashReadSkips.Current != 0 {
		t.Fatalf("project daily digest leaked other project: %+v", projDD.Rolling7d)
	}
	if projDD.ASTDiff.HasPrevious || projDD.PreInvocationDiff.HasPrevious || len(projDD.ResolvedSignals) != 0 {
		t.Fatalf("project daily digest diffed against global prev: ast=%+v pre=%+v resolved=%v", projDD.ASTDiff, projDD.PreInvocationDiff, projDD.ResolvedSignals)
	}

	s, err = metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	latestDB, err := metrics.LatestDailyDigest(s)
	s.Close()
	if err != nil || latestDB == nil || latestDB.Day != yesterday || len(latestDB.Signals) != 1 || latestDB.Signals[0].SignalID != "sig:global:sentinel_prev" {
		t.Fatalf("global daily_digests overwritten: %+v, err=%v", latestDB, err)
	}
	diskAfter, err := metrics.ReadLatestDailyDigest(home)
	if err != nil || diskAfter == nil || diskAfter.Day != "2026-01-01" {
		t.Fatalf("latest-daily-digest.json overwritten: %+v, err=%v", diskAfter, err)
	}

	// Project-scoped normal report does NOT attach global DailyDigest; --global does.
	out.Reset()
	if err := run([]string{"report", "--json", "--since", "2026-01-01", "--dir", repoA, "--home", home}, &out); err != nil {
		t.Fatalf("project report --json: %v\n%s", err, out.String())
	}
	var projRep metrics.JSONReport
	if err := json.Unmarshal(out.Bytes(), &projRep); err != nil {
		t.Fatal(err)
	}
	if projRep.DailyDigest != nil {
		t.Fatalf("project report attached global DailyDigest: %+v", projRep.DailyDigest)
	}
	out.Reset()
	if err := run([]string{"report", "--md", "--since", "2026-01-01", "--dir", repoA, "--home", home}, &out); err != nil {
		t.Fatalf("project report --md: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "## Daily Direction & Actions") {
		t.Fatalf("project report --md unexpectedly rendered global Daily Direction:\n%s", out.String())
	}

	out.Reset()
	if err := run([]string{"report", "--global", "--json", "--since", "2026-01-01", "--home", home}, &out); err != nil {
		t.Fatalf("global report --json: %v\n%s", err, out.String())
	}
	var globRep metrics.JSONReport
	if err := json.Unmarshal(out.Bytes(), &globRep); err != nil {
		t.Fatal(err)
	}
	if globRep.DailyDigest == nil || globRep.DailyDigest.Day != yesterday {
		t.Fatalf("global report missing stored DailyDigest: %+v", globRep.DailyDigest)
	}
}

func TestMetricsRecordRunCLI(t *testing.T) {
	home := t.TempDir()
	decoy := t.TempDir()
	t.Setenv("HOME", decoy)
	proj := filepath.Join(t.TempDir(), "app")

	runWithStdin := func(in string, args ...string) (string, int) {
		t.Helper()
		prev := stdin
		stdin = strings.NewReader(in)
		defer func() { stdin = prev }()
		var out bytes.Buffer
		err := run(append(args, "--home", home), &out)
		return out.String(), exitOf(err)
	}

	// Valid OrchestrationRunRow persisted via --json-in and --json.
	payloadMap := map[string]any{
		"id":            "run-v910-001",
		"ts":            "2026-11-22T10:00:00Z",
		"kind":          "swarm",
		"project_root":  proj,
		"status":        "ok",
		"units":         3,
		"defect_rounds": 1,
		"pass" + "ed":   3,
		"failed":        0,
		"duration_ms":   4200,
		"worker_model":  "gemini-4-carbon",
		"judge_model":   "claude-opus-5-5",
		"verdict":       "PASS",
	}
	raw, err := json.Marshal(payloadMap)
	if err != nil {
		t.Fatal(err)
	}
	out, code := runWithStdin(string(raw), "metrics", "record-run", "--json-in", "--json")
	if code != 0 {
		t.Fatalf("metrics record-run --json-in --json: exit=%d out=%q", code, out)
	}
	var ack map[string]any
	if err := json.Unmarshal([]byte(out), &ack); err != nil || ack["id"] != "run-v910-001" {
		t.Fatalf("record-run --json output = %q (%v)", out, err)
	}

	// Verify persisted row in metrics.db.
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.QueryOrchestrationRuns("swarm", time.Time{}, time.Time{}, 10)
	s.Close()
	if err != nil || len(rows) != 1 || rows[0].ID != "run-v910-001" || rows[0].Units != 3 || rows[0].Passed != 3 {
		t.Fatalf("persisted orchestration_runs = %+v (err=%v)", rows, err)
	}

	// Must-fail: missing --json-in, stray positional arg, empty/unknown/trailing/invalid JSON, and flag misuse.
	for _, tc := range []struct {
		name string
		in   string
		args []string
	}{
		{"missing json-in", string(raw), []string{"metrics", "record-run"}},
		{"stray arg", string(raw), []string{"metrics", "record-run", "--json-in", "extra"}},
		{"empty stdin", "   ", []string{"metrics", "record-run", "--json-in"}},
		{"unknown field", `{"id":"r2","kind":"swarm","status":"ok","bogus":1}`, []string{"metrics", "record-run", "--json-in"}},
		{"trailing data", `{"id":"r2","kind":"swarm","status":"ok"}{}`, []string{"metrics", "record-run", "--json-in"}},
		{"invalid kind", `{"id":"r2","kind":"bad kind!","status":"ok"}`, []string{"metrics", "record-run", "--json-in"}},
		{"negative units", `{"id":"r2","kind":"swarm","status":"ok","units":-1}`, []string{"metrics", "record-run", "--json-in"}},
		{"json-in on show", "", []string{"metrics", "show", "--json-in"}},
		{"json-in on ingest", "", []string{"metrics", "ingest", "--json-in"}},
		{"json-in on report", "", []string{"metrics", "report", "--json-in"}},
		{"json-in on export", "", []string{"metrics", "export", "--json-in", filepath.Join(home, "x.jsonl")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runWithStdin(tc.in, tc.args...)
			if code != 2 {
				t.Errorf("%s: exit=%d, want 2 (out=%q)", tc.name, code, out)
			}
		})
	}
}
