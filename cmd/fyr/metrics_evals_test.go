package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/metrics"
)

// TestMetricsEvals runs the verb over a store holding a saturated, a
// broken, an unstable and a history-only row and one error trial: the text
// and --json carry the same numbers, pass^3 is n/a at n = 2, --harness
// keeps one key exactly, and no value that is not a number reaches JSON.
// The view's means and suite are pinned in internal/metrics (TestEvalRows).
func TestMetricsEvals(t *testing.T) {
	home := t.TempDir()
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour)
	add := func(row string, i int, key, status string) {
		t.Helper()
		cause := map[string]string{"fail": "check", "error": "setup"}[status]
		if err := s.RecordTrial(metrics.Trial{Run: fmt.Sprintf("s/%s/%d", row, i), Label: "eval=" + row, Harness: key,
			Status: status, Cause: cause, StartedAt: at, Seconds: 10}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		add("sat", i, "aaa", "pass")
		add("sat", i+3, "bbb", "pass")
	}
	add("broke", 1, "aaa", "fail")
	add("broke", 2, "aaa", "fail")
	add("broke", 3, "aaa", "error")
	add("flaky", 1, "aaa", "pass")
	add("flaky", 2, "aaa", "fail")
	add("hist", 1, "unknown", "pass")
	s.Close()

	out, err := metricsCmd(t, "evals", "--home", home)
	if err != nil {
		t.Fatalf("evals: %v\n%s", err, out)
	}
	for _, want := range []string{
		"broke  -  2  0  1       0.00    n/a     [0.00, 0.66]  0     -",
		"flaky  -  2  1  0       0.50    n/a     [0.09, 0.91]  1     -",
		"hist   -  1  1  0       1.00    n/a     [0.21, 1.00]  0     -",
		"sat    -  6  6  0       1.00    1.00    [0.61, 1.00]  0     -",
		"suite: 4 rows graded, 11 graded trials, 1 error; pass rate 0.62 [0.22, 0.91] over rows; C_out 0.75",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the view lacks %q:\n%s", want, out)
		}
	}
	for row, flag := range map[string]string{"broke": "broken", "flaky": "unstable", "hist": "history", "sat": "saturated"} {
		if !strings.Contains(out, "  "+flag+"\n") || !strings.Contains(out, row+" ") {
			t.Errorf("%s is not flagged %s:\n%s", row, flag, out)
		}
	}

	js, err := metricsCmd(t, "evals", "--json", "--home", home)
	if err != nil {
		t.Fatalf("evals --json: %v\n%s", err, js)
	}
	if strings.Contains(js, "NaN") || strings.Contains(js, "Inf") {
		t.Errorf("a value that is not a number:\n%s", js)
	}
	var e metrics.EvalRows
	if err := json.Unmarshal([]byte(js), &e); err != nil {
		t.Fatalf("%v\n%s", err, js)
	}
	if len(e.Rows) != 4 || e.Rows[0].Row != "broke" || e.Rows[0].PassPow3 != nil || e.Rows[0].Errors != 1 || e.Rows[3].PassPow3 == nil {
		t.Errorf("--json rows: %+v", e.Rows)
	}

	only, err := metricsCmd(t, "evals", "--harness", "bbb", "--home", home)
	if err != nil {
		t.Fatalf("evals --harness: %v\n%s", err, only)
	}
	if !strings.Contains(only, ", harness bbb\n") || !strings.Contains(only, "sat  -  3  3  0") || strings.Contains(only, "broke") {
		t.Errorf("--harness bbb:\n%s", only)
	}
}

// ingest's refusal of a reading flag names every subcommand that reads it.
func TestMetricsIngestRefusalNamesEvals(t *testing.T) {
	out, err := metricsCmd(t, "ingest", "--since", "2026-10-01", "--home", t.TempDir())
	if err == nil || out != "fyr metrics ingest: --since belongs to show, report or evals\n" {
		t.Errorf("%v %q", err, out)
	}
}

// report draws the same rows under its pass^k chart, and none under
// --project: a trial belongs to no project (PLAN.md D16).
func TestMetricsReportEvalRows(t *testing.T) {
	home := t.TempDir()
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTrial(metrics.Trial{Run: "s/smoke/1", Label: "eval=smoke", Harness: "aaa", Status: "pass",
		StartedAt: time.Now().Add(-time.Hour), Seconds: 10}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	for _, c := range []struct {
		args []string
		want bool
	}{{nil, true}, {[]string{"--project", t.TempDir()}, false}} {
		file := filepath.Join(t.TempDir(), "r.html")
		if out, err := metricsCmd(t, append([]string{"report", "--home", home, "--out", file}, c.args...)...); err != nil {
			t.Fatalf("report %v: %v\n%s", c.args, err, out)
		}
		page, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(page), "<td>smoke</td>"); got != c.want {
			t.Errorf("report %v: the eval row shown %v, want %v", c.args, got, c.want)
		}
	}
}
