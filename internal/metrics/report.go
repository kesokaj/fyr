package metrics

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// The report is one self-contained HTML file: inline SVG drawn here, no
// script, no URL to any host, light and dark through prefers-color-scheme
// (D26, spec § Report). Hover tooltips are SVG <title> elements, which every
// browser shows without script; each chart has its numbers as a table.

// serie is one column in every chart, with a fixed colour: colour follows
// the CLI, never its rank (dataviz: categorical slots 1, 2), so Antigravity
// keeps the slot it had beside Claude Code.
type serie struct{ Key, Label, Class string }

// seriesOf are the summary's columns as chart series: Antigravity, then the
// historical Claude Code rows only when the window holds them (plan fyr D7).
func seriesOf(sum *Summary) []serie {
	var out []serie
	for _, v := range sum.columns() {
		if v == historical {
			out = append(out, serie{v, "Claude Code (historical)", "s1"})
		} else {
			out = append(out, serie{v, "Antigravity", "s2"})
		}
	}
	return out
}

// head is a table header: first, then a cell per series.
func head(first string, series []serie) []string {
	h := []string{first}
	for _, s := range series {
		h = append(h, s.Label)
	}
	return h
}

const (
	chartW, chartH         = 640.0, 220.0
	padL, padR, padT, padB = 56.0, 96.0, 16.0, 28.0
)

// ReportPath is the default report file under home, stamped to the minute.
func ReportPath(home string, now time.Time) string {
	return filepath.Join(home, harness.StateDir, "reports", now.Format("2006-01-02-1504")+"-metrics.html")
}

// WriteReport renders sum and the per-row view evals (nil for none) to
// file (mode 0600); an existing file is refused. The page is rendered in
// memory first, so a render that fails leaves no partial file for the rerun
// to refuse (SD57 of the 2026-10-27 audit).
func WriteReport(sum *Summary, evals *EvalRows, file string, now time.Time) error {
	var page bytes.Buffer
	if err := renderReport(&page, sum, evals, now); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("refusing to overwrite %s", file)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(page.Bytes()); err != nil {
		f.Close()
		os.Remove(file)
		return err
	}
	return f.Close()
}

// renderReport is RenderReport; a test replaces it with one that fails.
var renderReport = RenderReport

type chart struct {
	Title, Note string
	SVG         template.HTML
	Table       table
}

type table struct {
	Head []string
	Rows [][]string
}

// RenderReport writes the report's HTML for sum, with the per-row view
// evals under the pass^k chart when it holds a row (nil for none).
func RenderReport(w io.Writer, sum *Summary, evals *EvalRows, now time.Time) error {
	series := seriesOf(sum)
	var charts []chart
	day := func(title, note string, get func(Day, string) (float64, bool), format string) {
		charts = append(charts, lineChart(title, note, series, sum.Daily, get, format))
	}
	always := func(f func(Day, string) float64) func(Day, string) (float64, bool) {
		return func(d Day, v string) (float64, bool) { return f(d, v), true }
	}
	day("Tool calls per day", "", always(func(d Day, v string) float64 { return float64(d.ToolCalls[v]) }), "%.0f")
	day("Tokens per day", "input, output and cache", always(func(d Day, v string) float64 { return float64(d.Tokens[v]) }), "%.0f")
	day("Cost per day (USD)", costNote(sum), func(d Day, v string) (float64, bool) {
		// A CLI that exposes no cost has no line, never a line at 0.
		return d.CostUSD[v], sum.B.CostUSD[v] != nil
	}, "%.2f")
	day("fyr search : grep per day", "searches of the index divided by grep, rg and Grep calls; a day with no grep call has no point", func(d Day, v string) (float64, bool) {
		if d.GrepCalls[v] == 0 {
			return 0, false
		}
		return float64(d.SQLiteVerbs[v]) / float64(d.GrepCalls[v]), true
	}, "%.3f")
	day("Tool errors per day", "", always(func(d Day, v string) float64 { return float64(d.Errors[v]) }), "%.0f")
	charts = append(charts, barChart("Tool calls by tool", "the ten most used", series, topKeys(sum.B.ToolCallsByTool, 10), sum.B.ToolCallsByTool))
	charts = append(charts, barChart("Subagent tool calls by type", "", series, topKeys(sum.D.SubagentToolCalls, 12), sum.D.SubagentToolCalls))
	if len(sum.A.PassK) > 0 {
		charts = append(charts, passKChart(sum.A.PassK))
	}
	return reportTmpl.Execute(w, map[string]any{
		"Sum": sum, "Charts": charts, "Generated": now.UTC().Format("2006-01-02 15:04 UTC"),
		"Outcome": outcomeTable(sum, series), "Sections": reportSections(sum, series, evals),
		"Series": series, "Evals": evalsSectionOf(evals),
	})
}

func costNote(sum *Summary) string {
	if sum.B.CostUSD["agy"] == nil {
		return "Antigravity exposes no cost"
	}
	return ""
}

func niceMax(v float64) float64 {
	if v <= 0 {
		return 1
	}
	exp := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if v <= m*exp {
			return m * exp
		}
	}
	return 10 * exp
}

func esc(s string) string { return template.HTMLEscapeString(s) }

// lineChart draws one line per series over the window's days, a 2px line
// with a marker per day carrying its value as a hover title, the series'
// name at the line's end, and the same numbers as a table.
func lineChart(title, note string, series []serie, days []Day, get func(Day, string) (float64, bool), format string) chart {
	c := chart{Title: title, Note: note, Table: table{Head: head("day", series)}}
	rows, max := lineRows(series, days, get, format)
	c.Table.Rows = rows
	p := newPlot(len(days), niceMax(max))
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, chartW, chartH, esc(title))
	p.grid(&b, days)
	placeEndLabels(&b, p.lines(&b, series, days, get, format))
	b.WriteString(`</svg>`)
	c.SVG = template.HTML(b.String())
	return c
}

// barChart draws horizontal bars, one group per category with a bar per
// series, a 2px gap between the bars, the value at each bar's end.
func barChart(title, note string, series []serie, keys []string, data map[string]map[string]int) chart {
	c := chart{Title: title, Note: note, Table: table{Head: head("", series)}}
	var max float64
	for _, k := range keys {
		row := []string{k}
		for _, s := range series {
			v := data[s.Key][k]
			max = math.Max(max, float64(v))
			row = append(row, fmt.Sprint(v))
		}
		c.Table.Rows = append(c.Table.Rows, row)
	}
	const barH, gap, groupGap, left = 10.0, 2.0, 10.0, 150.0
	n := float64(len(series))
	h := padT + float64(len(keys))*(n*barH+(n-1)*gap+groupGap) + 8
	if len(keys) == 0 {
		h = 60
	}
	iw := chartW - left - 60
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, chartW, h, esc(title))
	if len(keys) == 0 {
		b.WriteString(`<text class="tick" x="16" y="34">no data in this window</text>`)
	}
	top := niceMax(max)
	for i, k := range keys {
		gy := padT + float64(i)*(n*barH+(n-1)*gap+groupGap)
		fmt.Fprintf(&b, `<text class="cat" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, left-8, gy+(n*barH+(n-1)*gap)/2+4, esc(k))
		for j, s := range series {
			v := float64(data[s.Key][k])
			by := gy + float64(j)*(barH+gap)
			bw := iw * v / top
			if v > 0 && bw < 2 {
				bw = 2
			}
			fmt.Fprintf(&b, `<rect class="bar %s" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2"><title>%s, %s: %.0f</title></rect>`,
				s.Class, left, by, bw, barH, esc(s.Label), esc(k), v)
			if v > 0 {
				fmt.Fprintf(&b, `<text class="value" x="%.1f" y="%.1f">%.0f</text>`, left+bw+4, by+barH-1, v)
			}
		}
	}
	b.WriteString(`</svg>`)
	c.SVG = template.HTML(b.String())
	return c
}

// passKChart: per sandbox label, graded trials passed of n with the Wilson
// 95 % interval as a whisker, the error trials, and whether all passed.
func passKChart(p map[string]PassK) chart {
	c := chart{Title: "Sandbox runs: passed of n, per label",
		Note:  "pass^k = 1 only when every graded trial of the label passed; the whisker is the Wilson 95 % interval of c of n; error trials are not graded; flip marks a label that both passed and failed",
		Table: table{Head: []string{"label", "passed", "k", "pass^k", "n", "c", "errors", "wilson95", "flip"}}}
	labels := sortedKeys(p)
	const barH, rowGap, left = 14.0, 10.0, 150.0
	h := padT + float64(len(labels))*(barH+rowGap) + 8
	iw := chartW - left - 150
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, chartW, h, esc(c.Title))
	for i, l := range labels {
		r := p[l]
		c.Table.Rows = append(c.Table.Rows, []string{l, fmt.Sprint(r.Passed), fmt.Sprint(r.K), fmt.Sprint(r.PassK),
			fmt.Sprint(r.N), fmt.Sprint(r.C), fmt.Sprint(r.Errors), wilsonText(r.Wilson95), fmt.Sprint(r.Flip)})
		passKRow(&b, l, r, padT+float64(i)*(barH+rowGap), left, iw, barH)
	}
	b.WriteString(`</svg>`)
	c.SVG = template.HTML(b.String())
	return c
}

// passKRow draws one label of the pass^k chart: its name, the track, the
// bar c of n, the whisker from the interval's low to its high end over the
// bar, and the value text; a label with no graded trial has an empty bar
// and no whisker.
func passKRow(b *strings.Builder, l string, r PassK, y, left, iw, barH float64) {
	frac := 0.0
	if r.N > 0 {
		frac = float64(r.C) / float64(r.N)
	}
	fmt.Fprintf(b, `<text class="cat" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, left-8, y+barH-3, esc(l))
	fmt.Fprintf(b, `<rect class="track" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2"/>`, left, y, iw, barH)
	fmt.Fprintf(b, `<rect class="bar s1" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2"><title>%s: %d of %d passed, Wilson 95 %% %s</title></rect>`,
		left, y, iw*frac, barH, esc(l), r.C, r.N, wilsonText(r.Wilson95))
	if r.N > 0 {
		lo, hi, mid := left+iw*r.Wilson95[0], left+iw*r.Wilson95[1], y+barH/2
		fmt.Fprintf(b, `<line class="whisker" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, lo, hi, mid, mid)
		for _, x := range []float64{lo, hi} {
			fmt.Fprintf(b, `<line class="whisker" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, x, x, y, y+barH)
		}
	}
	text := fmt.Sprintf("%d of %d · pass^%d = %d", r.Passed, r.K, r.K, r.PassK)
	if r.Errors > 0 {
		text += fmt.Sprintf(" · %d errors", r.Errors)
	}
	if r.Flip == 1 {
		text += " · flip"
	}
	fmt.Fprintf(b, `<text class="value" x="%.1f" y="%.1f">%s</text>`, left+iw+6, y+barH-3, text)
}

func short(v float64) string {
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%.1fG", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%.1fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.0fk", v/1e3)
	case v == math.Trunc(v):
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// topKeys are the n categories with the most counts over every column.
func topKeys(m map[string]map[string]int, n int) []string {
	tot := map[string]int{}
	for _, byKey := range m {
		for k, v := range byKey {
			tot[k] += v
		}
	}
	keys := make([]string, 0, len(tot))
	for k := range tot {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if tot[keys[i]] != tot[keys[j]] {
			return tot[keys[i]] > tot[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > n {
		keys = keys[:n]
	}
	return keys
}

// outcomeTable is the summary's headline numbers, a row per metric and a
// column per series; n/a is a metric the CLI cannot produce.
func outcomeTable(s *Summary, series []serie) table {
	t := table{Head: head("metric", series)}
	row := func(name string, f func(v string) string) {
		r := []string{name}
		for _, sr := range series {
			r = append(r, f(sr.Key))
		}
		t.Rows = append(t.Rows, r)
	}
	pf := func(m map[string]*float64, format string) func(string) string {
		return func(v string) string {
			if m[v] == nil {
				return "n/a"
			}
			return fmt.Sprintf(format, *m[v])
		}
	}
	pi := func(m map[string]*int) func(string) string {
		return func(v string) string {
			if m[v] == nil {
				return "n/a"
			}
			return fmt.Sprint(*m[v])
		}
	}
	row("main sessions", func(v string) string { return fmt.Sprint(s.Sessions[v]) })
	row("DoD closed (ambiguous) of", func(v string) string {
		c := s.A.DoDClosed[v]
		return fmt.Sprintf("%d (%d) of %d", c.Closed, c.Ambiguous, c.Of)
	})
	row("tool calls", func(v string) string { return fmt.Sprint(s.B.ToolCalls[v]) })
	row("cost USD", pf(s.B.CostUSD, "%.2f"))
	row("peak context", func(v string) string { return fmt.Sprint(s.B.PeakContext[v]) })
	row(fmt.Sprintf("sessions over context target (%d)", s.B.ContextTarget), s.overTarget)
	row("fyr search : grep", pf(s.C.SearchGrepRatio, "%.3f"))
	row("tool error rate", pf(s.D.ToolErrorRate, "%.3f"))
	row("denials", pi(s.D.Denials))
	row("interrupts", pi(s.D.Interrupts))
	row("compactions", pi(s.B.Compactions))
	return t
}
