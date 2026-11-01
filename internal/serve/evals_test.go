package serve

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/metrics"
)

// TestEvalsTable: /evals renders `fyr metrics evals` from the run's home
// above the eval records both inside and outside a project; /metrics/report
// omits home-wide eval rows when scoped to a project and includes them
// outside a project (SPEC-fyr-serve).
func TestEvalsTable(t *testing.T) {
	f := newFix(t)
	s, err := metrics.Open(metrics.Path(f.home))
	if err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{"pass", "fail"} {
		cause := map[string]string{"fail": "check"}[status]
		if err := s.RecordTrial(metrics.Trial{Run: "s/quokka/" + string(rune('1'+i)), Label: "eval=quokka", Harness: "abc1234",
			Status: status, Cause: cause, StartedAt: time.Now().Add(-time.Hour), Seconds: 9}); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	r := f.run(t)
	body := get(t, r, "GET", "/evals").Body.String()
	table, recs := strings.Index(body, "fyr metrics evals, the last 30 days"), strings.Index(body, f.ids["eval"])
	if table < 0 || recs < 0 || table > recs {
		t.Errorf("the table is missing or below the records (%d, %d)\n%s", table, recs, body)
	}
	if !strings.Contains(preOf(body), "quokka  1  capability  2  1  0       0.50    n/a     [0.09, 0.91]  1") || !strings.Contains(body, "unstable") {
		t.Errorf("the row:\n%s", body)
	}
	if rep := get(t, r, "GET", "/metrics/report").Body.String(); strings.Contains(rep, "<h2>Eval rows</h2>") {
		t.Errorf("project-scoped /metrics/report holds home-wide eval rows:\n%s", rep)
	}

	o := f.options(io.Discard)
	o.Dir = t.TempDir()
	rOut, err := NewRun(o)
	if err != nil {
		t.Fatal(err)
	}
	outEvals := get(t, rOut, "GET", "/evals").Body.String()
	if !strings.Contains(outEvals, "fyr metrics evals, the last 30 days") || !strings.Contains(outEvals, f.ids["eval"]) || !strings.Contains(outEvals, f.repo) {
		t.Errorf("outside-project /evals:\n%s", outEvals)
	}
	repOut := get(t, rOut, "GET", "/metrics/report").Body.String()
	if !strings.Contains(repOut, "<h2>Eval rows</h2>") || !strings.Contains(repOut, "<td>quokka</td>") {
		t.Errorf("outside-project /metrics/report lacks the eval rows:\n%s", repOut)
	}
}
