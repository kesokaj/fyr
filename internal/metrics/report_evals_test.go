package metrics

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// TestReportEvalRows: the report draws the per-row view under its pass^k
// chart, escaped, with its suite lines; with no row, or none given, there
// is no such section; and the page still holds no script and no URL.
func TestReportEvalRows(t *testing.T) {
	t.Parallel()
	sum := reportFixture(t)
	e, err := evalsFixture(t).Evals(EvalOptions{Now: evalsNow})
	if err != nil {
		t.Fatal(err)
	}
	e.Rows[0].Row = "<b>broke"
	var buf bytes.Buffer
	if err := RenderReport(&buf, sum, e, evalsNow); err != nil {
		t.Fatal(err)
	}
	page := buf.String()
	sec := strings.Index(page, "<h2>Eval rows</h2>")
	if sec < 0 || sec < strings.Index(page, "Sandbox runs: passed of n") {
		t.Fatalf("no eval rows section under the pass^k chart:\n%s", page)
	}
	for _, want := range []string{"<th>tokens in/out/cache</th>", "<td>&lt;b&gt;broke</td>", "<td>saturated</td>",
		"<li>suite: 4 rows graded, 13 graded trials, 1 error; pass rate 0.75 [0.30, 0.95] over rows; C_out 1.00</li>"} {
		if !strings.Contains(page[sec:], want) {
			t.Errorf("the section lacks %q", want)
		}
	}
	for _, bad := range []string{"<script", "http://", "https://", "<b>broke"} {
		if strings.Contains(page, bad) {
			t.Errorf("the page holds %q", bad)
		}
	}
	for _, none := range []*EvalRows{nil, {Rows: []EvalRow{}}} {
		buf.Reset()
		if err := RenderReport(&buf, sum, none, evalsNow); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), "Eval rows") {
			t.Errorf("a section for no rows (%v)", none)
		}
	}
}

func TestV910DevEvalOpenFallbackRO(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// Must-fail: absent dev store returns ok=false.
	if _, _, ok := OpenDevEvalFallbackRO(home); ok {
		t.Fatal("OpenDevEvalFallbackRO returned ok=true for missing dev store")
	}
	// Valid dev/fyr-eval store opens read-only and exposes trials.
	devPath := filepath.Join(home, harness.StateDir, "dev", "fyr-eval", harness.StateDir, "metrics.db")
	devStore := openT(t, devPath)
	now := time.Now().UTC()
	if err := devStore.RecordTrial(Trial{
		Run: "dev/row1/1", Label: "eval=row1", Harness: "v9.10.0",
		Status: "pass", StartedAt: now.Add(-time.Hour), Seconds: 5, PolicyModel: "gemini-4-neon-high",
	}); err != nil {
		t.Fatalf("RecordTrial: %v", err)
	}
	devStore.Close()

	ro, cleanup, ok := OpenDevEvalFallbackRO(home)
	if !ok {
		t.Fatal("OpenDevEvalFallbackRO returned ok=false for valid dev store")
	}
	defer cleanup()
	ev, err := ro.Evals(EvalOptions{Now: now.Add(time.Minute)})
	if err != nil || len(ev.Rows) != 1 || ev.Rows[0].Row != "row1" {
		t.Fatalf("dev fallback Evals = (%+v, %v), want 1 row1", ev, err)
	}

	// Must-fail: symlinked dev store is refused.
	symHome := t.TempDir()
	symDev := filepath.Join(symHome, harness.StateDir, "dev", "fyr-eval", harness.StateDir, "metrics.db")
	if err := os.MkdirAll(filepath.Dir(symDev), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(devPath, symDev); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := OpenDevEvalFallbackRO(symHome); ok {
		t.Fatal("OpenDevEvalFallbackRO accepted symlinked dev store")
	}
}
