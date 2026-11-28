package report

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

// Serie is one column in every chart, with a fixed colour class.
type Serie struct{ Key, Label, Class string }

// Chart holds one rendered inline SVG chart and its accessible data table.
type Chart struct {
	Title, Note string
	SVG         template.HTML
	Table       Table
}

// Table holds tabular report data for HTML and Markdown rendering.
type Table struct {
	Head []string
	Rows [][]string
}

// RenderFunc renders an HTML report page for sum and evals.
type RenderFunc func(w io.Writer, sum *Summary, evals *EvalView, now time.Time) error

func seriesOf(sum *Summary) []Serie {
	var out []Serie
	for _, v := range sum.ColumnsList() {
		if v == HistoricalVendor {
			out = append(out, Serie{v, "Claude Code (historical)", "s1"})
		} else {
			out = append(out, Serie{v, "Antigravity", "s2"})
		}
	}
	return out
}

func tableHead(first string, series []Serie) []string {
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

// DailyReportPath returns <home>/.config/fyr/reports/YYYY-MM-DD-daily-metrics.html.
func DailyReportPath(home string, now time.Time) string {
	return filepath.Join(home, harness.StateDir, "reports", now.Format("2006-01-02")+"-daily-metrics.html")
}

// WriteDailyReport writes the daily HTML report once per calendar day (0600,
// O_EXCL), treating os.ErrExist as a quiet nil no-op (ADR-528, D5, D14).
func WriteDailyReport(sum *Summary, evals *EvalView, home string, now time.Time) error {
	return WriteDailyReportWith(RenderReport, sum, evals, home, now)
}

// WriteDailyReportWith writes the daily HTML report using render.
func WriteDailyReportWith(render RenderFunc, sum *Summary, evals *EvalView, home string, now time.Time) error {
	file := DailyReportPath(home, now)
	if _, err := os.Lstat(file); err == nil {
		return nil
	}
	var page bytes.Buffer
	if err := render(&page, sum, evals, now); err != nil {
		return err
	}
	repDir := filepath.Dir(file)
	if err := os.MkdirAll(repDir, 0o700); err != nil {
		return err
	}
	if fi, err := os.Lstat(repDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("reports path %s is not a directory", repDir)
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
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

// WriteReport renders sum and evals (nil for none) to file (mode 0600); an
// existing file is refused.
func WriteReport(sum *Summary, evals *EvalView, file string, now time.Time) error {
	return WriteReportWith(RenderReport, sum, evals, file, now)
}

// WriteReportWith renders using render and writes file exclusively (0600).
func WriteReportWith(render RenderFunc, sum *Summary, evals *EvalView, file string, now time.Time) error {
	var page bytes.Buffer
	if err := render(&page, sum, evals, now); err != nil {
		return err
	}
	return writeExclusiveFile(file, page.Bytes())
}

func writeExclusiveFile(file string, data []byte) error {
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
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(file)
		return err
	}
	return f.Close()
}

// RenderReport writes the report's HTML for sum and evals (nil for none).
func RenderReport(w io.Writer, sum *Summary, evals *EvalView, now time.Time) error {
	series := seriesOf(sum)
	charts := buildReportCharts(sum, series)
	return reportTmpl.Execute(w, map[string]any{
		"Sum": sum, "Charts": charts, "Generated": now.UTC().Format("2006-01-02 15:04 UTC"),
		"Outcome": outcomeTable(sum, series), "Sections": reportSections(sum, series, evals),
		"Series": series, "Evals": evalsSectionOf(evals),
		"DailyBanner": buildDailyBannerView(sum.DailyDigest),
	})
}

func buildReportCharts(sum *Summary, series []Serie) []Chart {
	var charts []Chart
	day := func(title, note string, get func(Day, string) (float64, bool), format string) {
		charts = append(charts, LineChart(title, note, series, sum.Daily, get, format))
	}
	always := func(f func(Day, string) float64) func(Day, string) (float64, bool) {
		return func(d Day, v string) (float64, bool) { return f(d, v), true }
	}
	day("Tool calls per day", "", always(func(d Day, v string) float64 { return float64(d.ToolCalls[v]) }), "%.0f")
	day("Tokens per day", "input, output and cache", always(func(d Day, v string) float64 { return float64(d.Tokens[v]) }), "%.0f")
	if hasAnyCost(sum, series) {
		day("Cost per day (USD)", costNote(sum), func(d Day, v string) (float64, bool) {
			return d.CostUSD[v], sum.B.CostUSD[v] != nil
		}, "%.2f")
	}
	day("fyr search : grep per day", "searches of the index divided by grep, rg and Grep calls; a day with no grep call has no point", func(d Day, v string) (float64, bool) {
		if d.GrepCalls[v] == 0 {
			return 0, false
		}
		return float64(d.SQLiteVerbs[v]) / float64(d.GrepCalls[v]), true
	}, "%.3f")
	day("Tool errors per day", "", always(func(d Day, v string) float64 { return float64(d.Errors[v]) }), "%.0f")
	charts = append(charts, barChart("Tool calls by tool", "the ten most used", series, TopKeys(sum.B.ToolCallsByTool, 10), sum.B.ToolCallsByTool))
	charts = append(charts, barChart("Subagent tool calls by type", "", series, TopKeys(sum.D.SubagentToolCalls, 12), sum.D.SubagentToolCalls))
	if len(sum.A.PassK) > 0 {
		charts = append(charts, PassKChart(sum.A.PassK))
	}
	return charts
}

func costNote(sum *Summary) string {
	if sum.B.CostUSD["agy"] == nil {
		return "Antigravity exposes no cost"
	}
	return ""
}

func hasAnyCost(sum *Summary, series []Serie) bool {
	for _, s := range series {
		if sum.B.CostUSD[s.Key] != nil {
			return true
		}
	}
	return false
}

func outcomeTable(s *Summary, series []Serie) Table {
	t := Table{Head: tableHead("metric", series)}
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
		FmtFloatPtr(s.A.MeanOutcomeScore, "%.2f"), FmtFloatPtr(s.A.MeanTokenEfficiency, "%.2f"))))
	row("tool calls", func(v string) string { return fmt.Sprint(s.B.ToolCalls[v]) })
	row("cost USD", optFloatCol(s.B.CostUSD, "%.2f"))
	row("peak context", func(v string) string { return fmt.Sprint(s.B.PeakContext[v]) })
	row(fmt.Sprintf("sessions over context target (%d)", s.B.ContextTarget), s.OverTarget)
	row("fyr search : grep", optFloatCol(s.C.SearchGrepRatio, "%.3f"))
	row("bash read bypasses", agyCol(fmt.Sprint(s.C.BashReadSkips["agy"])))
	row("PreInvocation timeouts / p50 / p95", agyCol(fmtPreInvocationScorecardKPI(s)))
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
	row("sessions >= 60% knee", agyCol(fmt.Sprintf("%d of %d", s.Health.SessionsOverKnee, s.Health.MainSessions)))
	row("search.db compactions", agyCol(fmt.Sprint(s.Health.SearchDBCompactions)))
	row("hook denials (excl. from errors)", agyCol(fmt.Sprint(s.Health.HookDenials)))
	row("guarded subagent downgrades", agyCol(fmt.Sprint(s.Health.GuardedDowngrades)))
}
