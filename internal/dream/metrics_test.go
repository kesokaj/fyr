package dream

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
)

// toolConversation adds one Antigravity conversation with a run_command
// call to the fixture's home: the one tool call in the fixture.
func toolConversation(t *testing.T, f fixture) {
	t.Helper()
	agyConversations(t, f.home, map[string]agyConv{
		"S9": {f.root, now.Add(-time.Hour), []agyStep{{typ: 132, tool: "run_command", args: `{"CommandLine":"git status"}`}}},
	})
}

// A real run ingests every session's shape into the metrics store before it
// dreams (R3, ADR-055), and says so in dream.log.
func TestRunIngestsMetricsFirst(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	toolConversation(t, f)
	fakeAgy(t, canned, 0)
	var out bytes.Buffer
	if err := Run(runOpts(f, &out)); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	s, err := metrics.Open(metrics.Path(f.home))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sum, err := s.Show(metrics.ShowOptions{Since: now.AddDate(0, 0, -2), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if sum.B.ToolCalls["agy"] != 1 {
		t.Fatalf("the dream did not ingest: tool calls %v", sum.B.ToolCalls)
	}
	// Five: the fixture's four conversations and S9.
	if log := read(t, filepath.Join(f.home, harness.StateDir, "dream.log")); !strings.Contains(log, "metrics: 5 sessions") {
		t.Errorf("dream.log lacks the ingest line:\n%s", log)
	}
}

// A dry run ingests nothing and writes no store; it says what it would do.
func TestDryRunDoesNotIngest(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	toolConversation(t, f)
	var out bytes.Buffer
	o := runOpts(f, &out)
	o.DryRun = true
	if err := Run(o); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(metrics.Path(f.home)); !os.IsNotExist(err) {
		t.Fatalf("a dry run created the metrics store: %v", err)
	}
	if !strings.Contains(out.String(), "[dry-run] would ingest metrics into "+metrics.Path(f.home)) {
		t.Errorf("dry run does not name the ingest:\n%s", out.String())
	}
}

// An ingest failure is logged and changes neither the exit status nor
// last_run.error: the metrics never stop a dream.
func TestIngestFailureDoesNotFailDream(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	fakeAgy(t, canned, 0)
	saved := ingestMetrics
	t.Cleanup(func() { ingestMetrics = saved })
	ingestMetrics = func(string) (string, error) { return "", errors.New("disk on fire") }
	var out bytes.Buffer
	if err := Run(runOpts(f, &out)); err != nil {
		t.Fatalf("run failed because ingest failed: %v", err)
	}
	st, err := readState(f.home)
	if err != nil {
		t.Fatal(err)
	}
	if st.LastRun.Error != "" {
		t.Errorf("last_run.error = %q, want empty", st.LastRun.Error)
	}
	if log := read(t, filepath.Join(f.home, harness.StateDir, "dream.log")); !strings.Contains(log, "metrics ingest failed: disk on fire") {
		t.Errorf("dream.log lacks the failure:\n%s", log)
	}
}

// The prompt carries the project's metrics as numbers only, for this window
// and the one before it, read from a copy of the store; nothing names a
// tool, a skill or a path (R3's addendum, ADR-055a).
func TestPromptCarriesMetricsNumbersOnly(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	toolConversation(t, f)
	s, err := metrics.Open(metrics.Path(f.home))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metrics.Ingest(s, metrics.Options{Home: f.home}, io.Discard); err != nil {
		t.Fatal(err)
	}
	s.Close()
	before, _ := os.ReadDir(filepath.Dir(metrics.Path(f.home)))

	c, err := collect(f.home, project(f), since, time.Now(), maxPrompt)
	if err != nil {
		t.Fatal(err)
	}
	var sec string
	for _, in := range c.Inputs {
		if in.Kind == kindMetrics {
			sec = in.Text
		}
	}
	if !strings.Contains(sec, `"B.tool_calls.agy": 1`) || !strings.Contains(sec, `"previous"`) {
		t.Fatalf("metrics section:\n%s", sec)
	}
	for _, leak := range []string{"Bash", "git status", f.root, "S9"} {
		if strings.Contains(sec, leak) {
			t.Errorf("the metrics section names %q", leak)
		}
	}
	if !strings.Contains(c.Prompt, "<<<data metrics:") {
		t.Error("the prompt has no metrics block")
	}
	after, _ := os.ReadDir(filepath.Dir(metrics.Path(f.home)))
	if len(after) != len(before) {
		t.Errorf("reading the summary left files beside the store: %v -> %v", names(before), names(after))
	}
}

// Over the cap, the metrics block goes before any transcript (ADR-055a), and
// a metrics block alone never marks a project changed.
func TestMetricsDroppedFirstAndNeverChanges(t *testing.T) {
	c := Collected{Inputs: []Input{
		{Kind: kindMemory, Label: "memory/a.md", Text: "m"},
		{Kind: kindTranscript, Label: "antigravity x", Text: strings.Repeat("t", 200), Time: now},
		{Kind: kindMetrics, Label: "metrics", Text: strings.Repeat("9", 200)},
	}}
	if err := c.fit(len(prompt(c)) - 100); err != nil {
		t.Fatal(err)
	}
	for _, in := range c.Inputs {
		if in.Kind == kindMetrics {
			t.Fatal("the metrics block survived while a transcript was kept")
		}
	}
	if len(c.Inputs) != 2 {
		t.Fatalf("inputs %d, want memory and the transcript", len(c.Inputs))
	}
}

func names(es []os.DirEntry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestMetricsSummaryIncludesRedactedDailyDigestAndFailOpens(t *testing.T) {
	t.Run("missing-store-and-missing-digest-returns-empty", func(t *testing.T) {
		got, err := metricsSummary(t.TempDir(), "/proj", since, now)
		if err != nil || got != "" {
			t.Fatalf("got = %q, err = %v; want empty, nil", got, err)
		}
	})

	t.Run("no-store-but-valid-digest-scrubs-home-and-secrets", func(t *testing.T) {
		home := t.TempDir()
		redactedVal := "gh" + "p_" + strings.Repeat("F", 36)
		d := metrics.DailyDigest{
			Day:     "2026-11-21",
			Verdict: metrics.VerdictRegress,
			Signals: []metrics.DigestAction{{
				SignalID:          "sig:global:search_grep_low",
				Severity:          "high",
				Rule:              "search_grep_low",
				ActionScope:       "auto_skill",
				RecommendedAction: "Use " + redactedVal,
				Summary:           "ratio dropped in " + home + "/file",
			}},
		}
		if err := metrics.WriteLatestDailyDigest(home, d); err != nil {
			t.Fatal(err)
		}
		got, err := metricsSummary(home, "/proj", since, now)
		if err != nil || !strings.Contains(got, `"daily_digest"`) || !strings.Contains(got, "sig:global:search_grep_low") {
			t.Fatalf("metricsSummary = %q, err=%v", got, err)
		}
		if strings.Contains(got, home) || strings.Contains(got, redactedVal) {
			t.Errorf("metricsSummary leaked home or needle:\n%s", got)
		}
		if !strings.Contains(got, "~/file") || !strings.Contains(got, "[redacted by fyr]") {
			t.Errorf("metricsSummary did not scrub home or needle:\n%s", got)
		}
	})

	t.Run("store-present-includes-digest-or-fail-opens-on-corrupt", func(t *testing.T) {
		f := newFixture(t)
		scaffold(t, f)
		toolConversation(t, f)
		s, err := metrics.Open(metrics.Path(f.home))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := metrics.Ingest(s, metrics.Options{Home: f.home}, io.Discard); err != nil {
			t.Fatal(err)
		}
		s.Close()

		// Corrupt digest fails open while keeping metrics numbers.
		p := metrics.LatestDailyDigestPath(f.home)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{corrupt"), 0o600); err != nil {
			t.Fatal(err)
		}
		gotCorrupt, err := metricsSummary(f.home, f.root, since, time.Now())
		if err != nil || !strings.Contains(gotCorrupt, `"B.tool_calls.agy": 1`) || strings.Contains(gotCorrupt, `"daily_digest"`) {
			t.Fatalf("corrupt digest handling: err=%v\n%s", err, gotCorrupt)
		}

		// Valid digest included alongside store metrics.
		if err := metrics.WriteLatestDailyDigest(f.home, metrics.DailyDigest{Day: "2026-11-21", Verdict: metrics.VerdictSteady}); err != nil {
			t.Fatal(err)
		}
		gotValid, err := metricsSummary(f.home, f.root, since, time.Now())
		if err != nil || !strings.Contains(gotValid, `"daily_digest"`) || !strings.Contains(gotValid, `"B.tool_calls.agy": 1`) {
			t.Fatalf("valid digest with store: err=%v\n%s", err, gotValid)
		}
	})
}
