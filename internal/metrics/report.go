package metrics

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
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

// RenderReport writes the report's HTML for sum (including Trigger coverage and
// the numbered report sections), with the per-row view evals under the pass^k
// chart when it holds a row (nil for none).
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
	row("main sessions", func(v string) string { return fmt.Sprint(s.Sessions[v]) })
	row("DoD closed (ambiguous) of", func(v string) string {
		c := s.A.DoDClosed[v]
		return fmt.Sprintf("%d (%d) of %d", c.Closed, c.Ambiguous, c.Of)
	})
	row("rated sessions", agyCol(fmt.Sprint(s.A.RatingsCount)))
	row("mean outcome / token efficiency", agyCol(fmt.Sprintf("%s / %s",
		fmtFloatPtr(s.A.MeanOutcomeScore, "%.2f"), fmtFloatPtr(s.A.MeanTokenEfficiency, "%.2f"))))
	row("tool calls", func(v string) string { return fmt.Sprint(s.B.ToolCalls[v]) })
	row("cost USD", optFloatCol(s.B.CostUSD, "%.2f"))
	row("peak context", func(v string) string { return fmt.Sprint(s.B.PeakContext[v]) })
	row(fmt.Sprintf("sessions over context target (%d)", s.B.ContextTarget), s.overTarget)
	row("fyr search : grep", optFloatCol(s.C.SearchGrepRatio, "%.3f"))
	row("tool error rate", optFloatCol(s.D.ToolErrorRate, "%.3f"))
	row("denials", agyFallbackCol(s.D.Denials, fmt.Sprint(s.Health.HookDenials)))
	row("interrupts", optIntCol(s.D.Interrupts))
	row("compactions", agyFallbackCol(s.B.Compactions, fmt.Sprintf("%d (steps:%d)", s.Health.SearchDBCompactions, s.Health.StepCompactions)))
	appendOutcomeHealthRows(row, s)
	return t
}

func optFloatCol(m map[string]*float64, format string) func(string) string {
	return func(v string) string {
		if m[v] == nil {
			return "n/a"
		}
		return fmt.Sprintf(format, *m[v])
	}
}

func optIntCol(m map[string]*int) func(string) string {
	return func(v string) string {
		if m[v] == nil {
			return "n/a"
		}
		return fmt.Sprint(*m[v])
	}
}

func agyFallbackCol(m map[string]*int, agyVal string) func(string) string {
	return func(v string) string {
		if m[v] != nil {
			return fmt.Sprint(*m[v])
		}
		if v == "agy" {
			return agyVal
		}
		return "n/a"
	}
}

func appendOutcomeHealthRows(row func(string, func(string) string), s *Summary) {
	row("sessions >= 180K knee", agyCol(fmt.Sprintf("%d of %d", s.Health.SessionsOverKnee, s.Health.MainSessions)))
	row("search.db compactions", agyCol(fmt.Sprint(s.Health.SearchDBCompactions)))
	row("hook denials (excl. from errors)", agyCol(fmt.Sprint(s.Health.HookDenials)))
	row("guarded subagent downgrades", agyCol(fmt.Sprint(s.Health.GuardedDowngrades)))
}
