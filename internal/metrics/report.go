package metrics

import (
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
	return filepath.Join(home, ".config", "sh-agent", "reports", now.Format("2006-01-02-1504")+"-metrics.html")
}

// WriteReport renders sum to file (mode 0600); an existing file is refused.
func WriteReport(sum *Summary, file string, now time.Time) error {
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
	if err := RenderReport(f, sum, now); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

type chart struct {
	Title, Note string
	SVG         template.HTML
	Table       table
}

type table struct {
	Head []string
	Rows [][]string
}

// RenderReport writes the report's HTML for sum.
func RenderReport(w io.Writer, sum *Summary, now time.Time) error {
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
	day("sh-agent search : grep per day", "searches of the index divided by grep, rg and Grep calls; a day with no grep call has no point", func(d Day, v string) (float64, bool) {
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
		"Outcome": outcomeTable(sum, series), "Series": series,
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
	var max float64
	for _, d := range days {
		row := []string{d.Date}
		for _, s := range series {
			v, ok := get(d, s.Key)
			if !ok {
				row = append(row, "n/a")
				continue
			}
			max = math.Max(max, v)
			row = append(row, fmt.Sprintf(format, v))
		}
		c.Table.Rows = append(c.Table.Rows, row)
	}
	top := niceMax(max)
	iw, ih := chartW-padL-padR, chartH-padT-padB
	x := func(i int) float64 {
		if len(days) < 2 {
			return padL + iw/2
		}
		return padL + iw*float64(i)/float64(len(days)-1)
	}
	y := func(v float64) float64 { return padT + ih - ih*v/top }
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, chartW, chartH, esc(title))
	for i := 0; i <= 4; i++ {
		v := top * float64(i) / 4
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, padL, chartW-padR, y(v), y(v))
		fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, padL-6, y(v)+4, esc(short(v)))
	}
	for i, d := range days {
		if len(days) <= 8 || i == 0 || i == len(days)-1 || i%(len(days)/4+1) == 0 {
			fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, x(i), chartH-8, esc(d.Date[5:]))
		}
	}
	// End labels: one per drawn line, nudged apart so two lines ending close
	// together keep readable names.
	type endLabel struct {
		y     float64
		x     float64
		label string
	}
	var ends []endLabel
	var dots strings.Builder // markers over every line
	for _, s := range series {
		var pts []string
		var lastX, lastY float64
		drawn := false
		for i, d := range days {
			v, ok := get(d, s.Key)
			if !ok {
				continue
			}
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", x(i), y(v)))
			fmt.Fprintf(&dots, `<circle class="dot %s" cx="%.1f" cy="%.1f" r="4"><title>%s, %s: %s</title></circle>`,
				s.Class, x(i), y(v), esc(s.Label), esc(d.Date), esc(fmt.Sprintf(format, v)))
			lastX, lastY, drawn = x(i), y(v), true
		}
		if len(pts) > 1 {
			fmt.Fprintf(&b, `<polyline class="line %s" points="%s"/>`, s.Class, strings.Join(pts, " "))
		}
		if drawn {
			ends = append(ends, endLabel{lastY + 4, lastX + 8, s.Label})
		}
	}
	b.WriteString(dots.String())
	sort.Slice(ends, func(i, j int) bool { return ends[i].y < ends[j].y })
	for i := 1; i < len(ends); i++ {
		if ends[i].y-ends[i-1].y < 13 {
			ends[i].y = ends[i-1].y + 13
		}
	}
	for _, e := range ends {
		fmt.Fprintf(&b, `<text class="direct" x="%.1f" y="%.1f">%s</text>`, e.x, e.y, esc(e.label))
	}
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

// passKChart: per sandbox label, runs passed of k, and whether all passed.
func passKChart(p map[string]PassK) chart {
	c := chart{Title: "Sandbox runs: passed of k, per label", Note: "pass^k = 1 only when every run of the label passed",
		Table: table{Head: []string{"label", "passed", "k", "pass^k"}}}
	labels := sortedKeys(p)
	const barH, rowGap, left = 14.0, 10.0, 150.0
	h := padT + float64(len(labels))*(barH+rowGap) + 8
	iw := chartW - left - 120
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, chartW, h, esc(c.Title))
	for i, l := range labels {
		r := p[l]
		c.Table.Rows = append(c.Table.Rows, []string{l, fmt.Sprint(r.Passed), fmt.Sprint(r.K), fmt.Sprint(r.PassK)})
		y := padT + float64(i)*(barH+rowGap)
		frac := 0.0
		if r.K > 0 {
			frac = float64(r.Passed) / float64(r.K)
		}
		fmt.Fprintf(&b, `<text class="cat" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, left-8, y+barH-3, esc(l))
		fmt.Fprintf(&b, `<rect class="track" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2"/>`, left, y, iw, barH)
		fmt.Fprintf(&b, `<rect class="bar s1" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2"><title>%s: %d of %d passed</title></rect>`,
			left, y, iw*frac, barH, esc(l), r.Passed, r.K)
		fmt.Fprintf(&b, `<text class="value" x="%.1f" y="%.1f">%d of %d · pass^%d = %d</text>`, left+iw+6, y+barH-3, r.Passed, r.K, r.K, r.PassK)
	}
	b.WriteString(`</svg>`)
	c.SVG = template.HTML(b.String())
	return c
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
	row("sh-agent search : grep", pf(s.C.SearchGrepRatio, "%.3f"))
	row("tool error rate", pf(s.D.ToolErrorRate, "%.3f"))
	row("denials", pi(s.D.Denials))
	row("interrupts", pi(s.D.Interrupts))
	row("compactions", pi(s.B.Compactions))
	return t
}

var reportTmpl = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>sh-agent metrics {{.Sum.Window.since}} – {{.Sum.Window.until}}</title>
<style>
:root{color-scheme:light;--surface:#fcfcfb;--card:#ffffff;--ink:#0b0b0b;--ink-2:#52514e;--ink-3:#8a8984;--rule:#e6e5e0;--series-1:#2a78d6;--series-2:#eb6834}
@media (prefers-color-scheme:dark){:root{color-scheme:dark;--surface:#1a1a19;--card:#232321;--ink:#ffffff;--ink-2:#c3c2b7;--ink-3:#8f8e86;--rule:#3a3a37;--series-1:#3987e5;--series-2:#d95926}}
*{box-sizing:border-box}
body{margin:0;padding:24px 16px 48px;background:var(--surface);color:var(--ink);font:14px/1.45 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:760px;margin:0 auto}
h1{font-size:22px;margin:0 0 4px}
h2{font-size:15px;margin:0 0 2px}
.sub,.note{color:var(--ink-2);margin:0 0 12px}
.legend{display:flex;gap:16px;flex-wrap:wrap;margin:0 0 16px;color:var(--ink-2)}
.sw{display:inline-block;width:10px;height:10px;border-radius:2px;margin-right:6px;vertical-align:-1px}
section{background:var(--card);border:1px solid var(--rule);border-radius:8px;padding:16px;margin:0 0 16px}
svg{width:100%;height:auto;display:block;overflow:visible}
.grid{stroke:var(--rule);stroke-width:1}
.tick,.value{fill:var(--ink-3);font-size:11px}
.cat,.direct{fill:var(--ink-2);font-size:12px}
.line{fill:none;stroke-width:2;stroke-linejoin:round}
.line.s1{stroke:var(--series-1)}.line.s2{stroke:var(--series-2)}
.dot.s1,.bar.s1,.sw.s1{fill:var(--series-1);background:var(--series-1)}.dot.s2,.bar.s2,.sw.s2{fill:var(--series-2);background:var(--series-2)}
.dot{stroke:var(--card);stroke-width:2}
.track{fill:var(--rule)}
table{border-collapse:collapse;width:100%;font-size:12px;font-variant-numeric:tabular-nums}
th,td{text-align:right;padding:3px 8px;border-bottom:1px solid var(--rule)}
th:first-child,td:first-child{text-align:left}
th{color:var(--ink-2);font-weight:600}
details{margin-top:8px}summary{color:var(--ink-2);cursor:pointer}
.tablewrap{overflow-x:auto}
ul.notes{color:var(--ink-2);padding-left:18px}
</style></head><body><main>
<h1>Agent metrics</h1>
<p class="sub">{{.Sum.Window.since}} to {{.Sum.Window.until}} · generated {{.Generated}} by <code>sh-agent metrics report</code> from the local metrics store</p>
<div class="legend">{{range .Series}}<span><span class="sw {{.Class}}"></span>{{.Label}}</span>{{end}}</div>
<section><h2>Summary</h2><p class="note">n/a is a metric the CLI does not record.</p>
<div class="tablewrap"><table><tr>{{range .Outcome.Head}}<th>{{.}}</th>{{end}}</tr>{{range .Outcome.Rows}}<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>{{end}}</table></div></section>
{{range .Charts}}<section><h2>{{.Title}}</h2>{{if .Note}}<p class="note">{{.Note}}</p>{{end}}{{.SVG}}
<details><summary>Table</summary><div class="tablewrap"><table><tr>{{range .Table.Head}}<th>{{.}}</th>{{end}}</tr>{{range .Table.Rows}}<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>{{end}}</table></div></details></section>
{{end}}
{{if .Sum.Notes}}<section><h2>Notes</h2><ul class="notes">{{range .Sum.Notes}}<li>{{.}}</li>{{end}}</ul></section>{{end}}
</main></body></html>
`))
