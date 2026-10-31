package metrics

import (
	"bytes"
	"strings"
	"testing"
)

// TestReportEvalRows: the report draws the per-row view under its pass^k
// chart, escaped, with its suite lines; with no row, or none given, there
// is no such section; and the page still holds no script and no URL.
func TestReportEvalRows(t *testing.T) {
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
