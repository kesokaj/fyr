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

	c, err := collect(f.home, project(f), since, maxPrompt)
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
