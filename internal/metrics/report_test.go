package metrics

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// reportFixture is an Antigravity ingest, a labelled sandbox run of it and
// the historical rows an older ingest left from Claude Code: the report's
// two columns, agy first.
func reportFixture(t *testing.T) *Summary {
	t.Helper()
	ahome, root := agyFixture(t)
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	historicalRows(t, s, root)
	ingestT(t, s, Options{Home: ahome})
	ingestT(t, s, Options{Source: agyRunCopy(t), Label: "arm=lean", Outcome: "pass"})
	sum, err := s.Show(ShowOptions{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), AllLabels: true})
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

// The report is self-contained (no script, no host) and its numbers are
// show's: the summary rows and the per-day tables add up to the summary.
func TestReportSelfContainedAndConsistent(t *testing.T) {
	sum := reportFixture(t)
	var buf bytes.Buffer
	if err := RenderReport(&buf, sum, time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, bad := range []string{"<script", "http://", "https://", "@import", "url("} {
		if strings.Contains(strings.ToLower(html), bad) {
			t.Errorf("report contains %q", bad)
		}
	}
	for _, want := range []string{"Tool calls per day", "Tool calls by tool", "Subagent tool calls by type", "Sandbox runs", "<title>Antigravity, ", "<title>Claude Code (historical), ", "prefers-color-scheme:dark", "n/a"} {
		if !strings.Contains(html, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	// The summary table's tool-call row is show's B.tool_calls.
	row := regexp.MustCompile(`<td>tool calls</td><td>(\d+)</td><td>(\d+)</td>`).FindStringSubmatch(html)
	if row == nil || row[1] != strconv.Itoa(sum.B.ToolCalls["agy"]) || row[2] != strconv.Itoa(sum.B.ToolCalls["claude"]) {
		t.Errorf("summary tool-call row %v, want %d and %d", row, sum.B.ToolCalls["agy"], sum.B.ToolCalls["claude"])
	}
	// The per-day table sums to the same totals.
	for _, v := range []string{"agy", "claude"} {
		n := 0
		for _, d := range sum.Daily {
			n += d.ToolCalls[v]
		}
		if n != sum.B.ToolCalls[v] {
			t.Errorf("%s: daily tool calls sum to %d, summary says %d", v, n, sum.B.ToolCalls[v])
		}
	}
	if !strings.Contains(html, "1 of 1 · pass^1 = 1") {
		t.Error("the pass^k chart does not show the label's run")
	}
	// The chart reads c of n with the Wilson interval as a whisker and the
	// per-trial columns in its table (the eval-metrics plan, Phase 1).
	for _, want := range []string{`<line class="whisker"`, `Wilson 95 % [0.2065, 1.0000]`, `<th>wilson95</th>`, `<th>errors</th>`, `<td>[0.2065, 1.0000]</td>`} {
		if !strings.Contains(html, want) {
			t.Errorf("the pass^k chart lacks %q", want)
		}
	}
}

// passKChart's drawing: the bar is c of n, the whisker spans the interval
// over the track, a label with only error trials has an empty bar and no
// whisker, errors and flip are written in the value text.
func TestPassKChartDrawing(t *testing.T) {
	c := passKChart(map[string]PassK{
		"eval=flip": {K: 4, Passed: 2, N: 4, C: 2, Errors: 1, Wilson95: [2]float64{0.15, 0.85}, Flip: 1},
		"eval=err":  {Errors: 2},
	})
	svg := string(c.SVG)
	const left, iw = 150.0, 640.0 - 150 - 150
	// Rows sort by label: eval=err at y 16, eval=flip at y 40.
	for _, want := range []string{
		fmt.Sprintf(`<rect class="bar s1" x="150.0" y="40.0" width="%.1f"`, iw*0.5),
		fmt.Sprintf(`<line class="whisker" x1="%.1f" x2="%.1f" y1="47.0" y2="47.0"/>`, left+iw*0.15, left+iw*0.85),
		`2 of 4 · pass^4 = 0 · 1 errors · flip</text>`,
		`0 of 0 · pass^0 = 0 · 2 errors</text>`,
		`<rect class="bar s1" x="150.0" y="16.0" width="0.0"`,
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("chart lacks %q:\n%s", want, svg)
		}
	}
	if n := strings.Count(svg, `class="whisker"`); n != 3 {
		t.Errorf("%d whisker lines, want 3 (one label with graded trials)", n)
	}
	if want := []string{"eval=err", "0", "0", "0", "0", "0", "2", "[0.0000, 0.0000]", "0"}; fmt.Sprint(c.Table.Rows[0]) != fmt.Sprint(want) {
		t.Errorf("table row %q, want %q", c.Table.Rows[0], want)
	}
}

// A label or tool name is text, never markup.
func TestReportEscapesNames(t *testing.T) {
	sum := reportFixture(t)
	sum.B.ToolCallsByTool["agy"][`agy:<img src=x onerror=alert(1)>`] = 99
	var buf bytes.Buffer
	if err := RenderReport(&buf, sum, time.Now()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "<img") {
		t.Fatal("a tool name reached the page unescaped")
	}
}

func TestWriteReportRefusesOverwrite(t *testing.T) {
	sum := reportFixture(t)
	f := filepath.Join(t.TempDir(), "r", "m.html")
	if err := WriteReport(sum, f, time.Now()); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	if err := WriteReport(sum, f, time.Now()); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("second write: %v", err)
	}
}

// What a CLI cannot produce is not drawn as 0: Antigravity has no cost, so
// the cost chart has no Antigravity points; a day with no grep call has no
// search:grep point. Nothing the template sanitised away is left in the page.
func TestReportDrawsNoMissingValues(t *testing.T) {
	sum := reportFixture(t)
	var buf bytes.Buffer
	if err := RenderReport(&buf, sum, time.Now()); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if strings.Contains(html, "ZgotmplZ") {
		t.Error("the template sanitised a value away (ZgotmplZ)")
	}
	section := func(title string) string {
		for _, s := range strings.Split(html, "<section>") {
			if strings.Contains(s, "<h2>"+title) {
				return s
			}
		}
		t.Fatalf("no section %q", title)
		return ""
	}
	if strings.Contains(section("Cost per day"), "<title>Antigravity") {
		t.Error("the cost chart draws Antigravity, which exposes no cost")
	}
	if strings.Contains(section("fyr search : grep"), "<title>Antigravity") {
		t.Error("the ratio chart draws Antigravity days that have no grep call")
	}
}

// The summary table carries the over-target row as "k of n" per column, from the
// same Summary as show (plan 2026-10-23-session-sized-work, Phase 2).
func TestReportContextTargetRow(t *testing.T) {
	sum := reportFixture(t)
	var buf bytes.Buffer
	if err := RenderReport(&buf, sum, time.Now()); err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile(`<td>sessions over context target \(300000\)</td><td>([^<]*)</td><td>([^<]*)</td>`).FindStringSubmatch(buf.String())
	if row == nil {
		t.Fatalf("the summary table has no over-target row:\n%s", buf.String())
	}
	for i, v := range []string{"agy", "claude"} {
		want := fmt.Sprintf("%d of %d", *sum.B.SessionsOverContextTarget[v], len(sum.B.SessionPeaks[v]))
		if row[i+1] != want {
			t.Errorf("%s over-target cell %q, want %q", v, row[i+1], want)
		}
	}
	sum.B.SessionsOverContextTarget["agy"] = nil
	buf.Reset()
	RenderReport(&buf, sum, time.Now())
	if !regexp.MustCompile(`<td>sessions over context target \(300000\)</td><td>n/a</td><td>[^<]*</td>`).MatchString(buf.String()) {
		t.Error("a vendor with no context value is not drawn as n/a")
	}
}

// An agy-only store draws one series: no historical column, no Claude Code
// label, and the summary table has one value column.
func TestReportAgyOnly(t *testing.T) {
	ahome, _ := agyFixture(t)
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, s, Options{Home: ahome})
	sum, err := s.Show(ShowOptions{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := RenderReport(&buf, sum, time.Now()); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if strings.Contains(html, "Claude Code") {
		t.Error("an agy-only report names Claude Code")
	}
	if !regexp.MustCompile(`<td>tool calls</td><td>4</td></tr>`).MatchString(html) {
		t.Errorf("the summary's tool-call row is not one agy cell of 4:\n%s", html)
	}
}
