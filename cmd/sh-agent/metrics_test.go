package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMetricsVerb: ingest reads <home>/.claude/projects under --home even with
// CLAUDE_CONFIG_DIR pointing elsewhere (D9, R6), prints its summary line, and
// export writes the store and refuses to overwrite.
func TestMetricsVerb(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-p")
	os.MkdirAll(dir, 0o755)
	line := `{"type":"assistant","sessionId":"S1","cwd":"/p","timestamp":"2026-10-08T10:00:00Z","message":{"id":"m1","usage":{"input_tokens":5},"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"git status"}}]}}` + "\n"
	os.WriteFile(filepath.Join(dir, "S1.jsonl"), []byte(line), 0o600)

	decoy := filepath.Join(t.TempDir(), "decoy")
	os.MkdirAll(filepath.Join(decoy, "projects", "-q"), 0o755)
	os.WriteFile(filepath.Join(decoy, "projects", "-q", "D.jsonl"), []byte(strings.ReplaceAll(line, "S1", "DECOY")), 0o600)
	t.Setenv("CLAUDE_CONFIG_DIR", decoy)

	var out bytes.Buffer
	if err := run([]string{"metrics", "ingest", "--home", home, "--vendor", "claude"}, &out); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "metrics: 1 sessions, 2 steps") {
		t.Fatalf("ingest printed %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "sh-agent", "metrics.db")); err != nil {
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
	if !strings.Contains(out.String(), `"context_target": 300000`) || !strings.Contains(out.String(), `"claude": 0`) {
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
	if got.B.ContextTarget != 3 || got.B.Over["claude"] == nil || *got.B.Over["claude"] != 1 || got.B.Over["agy"] != nil || len(got.B.Peaks["claude"]) != 1 {
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
	} {
		err := run(append(args, "--home", home), &bytes.Buffer{})
		var code exitCode
		if !errors.As(err, &code) || code != 2 {
			t.Errorf("%q: err = %v, want exit 2", args, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "sh-agent", "metrics.db")); !os.IsNotExist(err) {
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

// show, report and export on a home that never ingested say so and exit 1;
// they never create an empty store whose zeros would read as "no sessions".
func TestMetricsWithoutStore(t *testing.T) {
	home := t.TempDir()
	for _, args := range [][]string{
		{"metrics", "show"},
		{"metrics", "report", "--out", filepath.Join(t.TempDir(), "r.html")},
		{"metrics", "export", filepath.Join(t.TempDir(), "e.jsonl")},
	} {
		var out bytes.Buffer
		err := run(append(args, "--home", home), &out)
		var code exitCode
		if !errors.As(err, &code) || code != 1 || !strings.Contains(out.String(), "no metrics store") {
			t.Errorf("%q: err %v, out %q; want exit 1 naming the missing store", args, err, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "sh-agent", "metrics.db")); !os.IsNotExist(err) {
		t.Errorf("a read verb created the store: %v", err)
	}
}
