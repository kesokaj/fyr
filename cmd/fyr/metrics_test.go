package main

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
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
		{"metrics", "show", "--outcome", "pass"},
		{"metrics", "show", "--out", filepath.Join(home, "x.html")},
		{"metrics", "report", "--json"},
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
