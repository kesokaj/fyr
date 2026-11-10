package metrics

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// reportSection is one of the five airy sections rendered by both the
// Economist HTML report and the Markdown report (--md).
type reportSection struct {
	Title, Note string
	Table       table
}

// WriteMarkdownReport renders sum and evals as Markdown to file (mode 0600);
// an existing file is refused. Like WriteReport, it renders in memory first so
// a failure leaves no partial file on disk.
func WriteMarkdownReport(sum *Summary, evals *EvalRows, file string, now time.Time) error {
	var page bytes.Buffer
	if err := RenderMarkdownReport(&page, sum, evals, now); err != nil {
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

// RenderMarkdownReport writes the five-section Markdown usage report to w.
func RenderMarkdownReport(w io.Writer, sum *Summary, evals *EvalRows, now time.Time) error {
	series := seriesOf(sum)
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent metrics (%s .. %s)\n\n",
		mdCell(sum.Window["since"]), mdCell(sum.Window["until"]))
	fmt.Fprintf(&b, "Generated %s by `fyr report` from the local metrics store.\n",
		now.UTC().Format("2006-01-02 15:04 UTC"))
	for _, sec := range reportSections(sum, series, evals) {
		fmt.Fprintf(&b, "\n## %s\n\n", sec.Title)
		writeMDTable(&b, sec.Table)
	}
	if es := evalsSectionOf(evals); es != nil {
		b.WriteString("\n## Eval rows\n\n")
		writeMDTable(&b, es.Table)
		for _, line := range es.Lines {
			fmt.Fprintf(&b, "- %s\n", mdCell(line))
		}
	}
	if len(sum.Notes) > 0 {
		b.WriteString("\n## Notes\n\n")
		for _, n := range sum.Notes {
			fmt.Fprintf(&b, "- %s\n", mdCell(n))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeMDTable(b *strings.Builder, t table) {
	if len(t.Head) == 0 {
		return
	}
	writeMDRow(b, t.Head)
	sep := make([]string, len(t.Head))
	for i := range sep {
		if i == 0 {
			sep[i] = ":---"
		} else {
			sep[i] = "---:"
		}
	}
	writeMDRow(b, sep)
	for _, r := range t.Rows {
		writeMDRow(b, r)
	}
}

func writeMDRow(b *strings.Builder, cells []string) {
	b.WriteString("|")
	for _, c := range cells {
		fmt.Fprintf(b, " %s |", mdCell(c))
	}
	b.WriteString("\n")
}

func mdCell(s string) string {
	s = printable(s)
	s = strings.ReplaceAll(s, "|", `\|`)
	return s
}

func reportSections(sum *Summary, series []serie, evals *EvalRows) []reportSection {
	return []reportSection{
		executivePulseSection(sum, series),
		secondBrainSection(sum, series),
		qualityLoopSection(sum, series, evals),
		autonomySection(sum, series),
		projectBreakdownSection(sum, series),
	}
}

func executivePulseSection(s *Summary, series []serie) reportSection {
	t := table{Head: head("Metric", series)}
	addSeriesRow(&t, series, "Sessions", func(v string) string { return fmt.Sprint(s.Sessions[v]) })
	addSeriesRow(&t, series, "Subagents", func(v string) string {
		n := 0
		for _, c := range s.Subagents[v] {
			n += c
		}
		return fmt.Sprint(n)
	})
	addSeriesRow(&t, series, "Turns", func(v string) string { return fmt.Sprint(s.B.Turns[v]) })
	addSeriesRow(&t, series, "Tool calls", func(v string) string { return fmt.Sprint(s.B.ToolCalls[v]) })
	addSeriesRow(&t, series, "Active / wall min", func(v string) string {
		return fmt.Sprintf("%.1f / %.1f", s.B.ActiveMin[v], s.B.WallMin[v])
	})
	addSeriesRow(&t, series, "Model / tool / wait / idle min", func(v string) string {
		return fmt.Sprintf("%.1f / %.1f / %.1f / %.1f", s.B.ModelMin[v], s.B.ToolMin[v], s.B.WaitMin[v], s.B.IdleMin[v])
	})
	addSeriesRow(&t, series, "Tokens (in / out / cache read)", func(v string) string {
		if tk := s.B.Tokens[v]; tk != nil {
			return fmt.Sprintf("%d / %d / %d", tk.In, tk.Out, tk.CacheRead)
		}
		return "0 / 0 / 0"
	})
	addSeriesRow(&t, series, "Peak context", func(v string) string { return fmt.Sprint(s.B.PeakContext[v]) })
	addSeriesRow(&t, series, fmt.Sprintf("Over context target (%d)", s.B.ContextTarget), s.overTarget)
	addSeriesRow(&t, series, "Compactions", func(v string) string { return fmtIntPtr(s.B.Compactions[v]) })
	return reportSection{Title: "1. Executive Pulse", Table: t}
}

func secondBrainSection(s *Summary, series []serie) reportSection {
	t := table{Head: head("Metric", series)}
	addSeriesRow(&t, series, "Search : grep ratio (SQLite / grep)", func(v string) string {
		return fmt.Sprintf("%s (%d / %d)", fmtFloatPtr(s.C.SearchGrepRatio[v], "%.3f"), s.C.SQLiteVerbs[v], s.C.GrepCalls[v])
	})
	addSeriesRow(&t, series, "Ranged read %", func(v string) string { return fmtFloatPtr(s.C.RangedReadPct[v], "%.1f%%") })
	addSeriesRow(&t, series, "Redundant reads", func(v string) string { return fmt.Sprint(s.C.RedundantReads[v]) })
	addSeriesRow(&t, series, "Turn vets", agyCol(fmt.Sprint(s.E.TurnVets)))
	addSeriesRow(&t, series, "Mean vet score", agyCol(fmtFloatPtr(s.E.MeanVetScore, "%.1f")))
	addSeriesRow(&t, series, "VET_FIRST turns", agyCol(fmt.Sprint(s.E.VetFirstTurns)))
	addSeriesRow(&t, series, "Bypassed turns (!)", agyCol(fmt.Sprint(s.E.BypassedTurns)))
	addSeriesRow(&t, series, "Pivots pre-seeded", agyCol(fmt.Sprint(s.E.PivotsSeeded)))
	addSeriesRow(&t, series, "Read-ledger blocks saved", agyCol(fmt.Sprint(s.E.LedgerSaved)))
	return reportSection{Title: "2. Second-Brain Leverage", Table: t}
}

func qualityLoopSection(s *Summary, series []serie, evals *EvalRows) reportSection {
	t := table{Head: head("Metric", series)}
	addSeriesRow(&t, series, "DoD closed (ambiguous) / of", func(v string) string {
		c := s.A.DoDClosed[v]
		return fmt.Sprintf("%d (%d) / %d", c.Closed, c.Ambiguous, c.Of)
	})
	addSeriesRow(&t, series, "Verifier (PASS / FAIL)", agyCol(fmt.Sprintf("%d / %d", s.A.Verifier["PASS"], s.A.Verifier["FAIL"])))
	addSeriesRow(&t, series, "Review findings (high / medium / low)", agyCol(fmt.Sprintf("%d / %d / %d",
		s.A.ReviewFindings["high"], s.A.ReviewFindings["medium"], s.A.ReviewFindings["low"])))
	addSeriesRow(&t, series, "Escaped bugs", agyCol(fmt.Sprint(s.A.EscapedBugs)))
	addSeriesRow(&t, series, "Corrections (FEEDBACK-*)", agyCol(fmt.Sprint(s.A.Corrections)))
	addSeriesRow(&t, series, "Tool error rate / retry loops", func(v string) string {
		return fmt.Sprintf("%s / %d", fmtFloatPtr(s.D.ToolErrorRate[v], "%.3f"), s.D.RetryLoops[v])
	})
	addSeriesRow(&t, series, "Eval pass^k / suite", agyCol(fmtEvalSummary(s.A.PassK, evals)))
	return reportSection{Title: "3. Quality & Learning Loop", Table: t}
}

func autonomySection(s *Summary, series []serie) reportSection {
	t := table{Head: head("Metric", series)}
	addSeriesRow(&t, series, "Work waves / workers", agyCol(fmt.Sprintf("%d / %d", s.F.Waves, s.F.Workers)))
	addSeriesRow(&t, series, "Worker verdicts (PASS / FAIL)", agyCol(fmt.Sprintf("%d / %d", s.F.Passed, s.F.Failed)))
	addSeriesRow(&t, series, "Base-Check verdicts (PASS / FAIL)", agyCol(fmt.Sprintf("%d / %d", s.F.BaseCheckPassed, s.F.BaseCheckFailed)))
	addSeriesRow(&t, series, "Cross-family Judge (PASS / FAIL / fallback)", agyCol(fmt.Sprintf("%d / %d / %d",
		s.F.JudgePassed, s.F.JudgeFailed, s.F.JudgeFallback)))
	addSeriesRow(&t, series, "Subagent tool calls", func(v string) string { return fmtTopCounts(s.D.SubagentToolCalls[v], 5) })
	return reportSection{Title: "4. Autonomy & Model Routing", Table: t}
}

func projectBreakdownSection(s *Summary, series []serie) reportSection {
	if len(s.Projects) > 0 {
		t := table{Head: []string{"Project", "Sessions", "Active min", "Tokens", "Search : grep", "Mean vet score", "Corrections", "Escaped bugs", "Last active"}}
		for _, p := range s.Projects {
			t.Rows = append(t.Rows, []string{
				printable(p.Project),
				fmt.Sprint(p.Sessions),
				fmt.Sprintf("%.1f", p.ActiveMin),
				fmt.Sprint(p.Tokens),
				fmtFloatPtr(p.SearchGrepRatio, "%.3f"),
				fmtFloatPtr(p.MeanVetScore, "%.1f"),
				fmt.Sprint(p.Corrections),
				fmt.Sprint(p.EscapedBugs),
				printable(p.LastActive),
			})
		}
		return reportSection{Title: "5. Per-Project / Project Breakdown", Table: t}
	}
	t := table{Head: head("Skill / Subagent", series)}
	for _, k := range topKeys(s.C.Skills, 10) {
		addSeriesRow(&t, series, "skill: "+printable(k), func(v string) string { return fmt.Sprint(s.C.Skills[v][k]) })
	}
	for _, k := range topKeys(s.Subagents, 10) {
		addSeriesRow(&t, series, "subagent: "+printable(k), func(v string) string { return fmt.Sprint(s.Subagents[v][k]) })
	}
	if len(t.Rows) == 0 {
		addSeriesRow(&t, series, "none", func(string) string { return "0" })
	}
	return reportSection{Title: "5. Per-Project / Project Breakdown", Table: t}
}

func addSeriesRow(t *table, series []serie, name string, f func(v string) string) {
	r := []string{name}
	for _, sr := range series {
		r = append(r, f(sr.Key))
	}
	t.Rows = append(t.Rows, r)
}

func agyCol(val string) func(string) string {
	return func(v string) string {
		if v == "agy" {
			return val
		}
		return "n/a"
	}
}

func fmtFloatPtr(p *float64, format string) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf(format, *p)
}

func fmtIntPtr(p *int) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprint(*p)
}

func fmtEvalSummary(passK map[string]PassK, evals *EvalRows) string {
	if evals != nil && len(evals.Rows) > 0 {
		return fmt.Sprintf("%d rows (%d trials)", evals.Suite.Rows, evals.Suite.Trials)
	}
	if len(passK) > 0 {
		passed := 0
		for _, r := range passK {
			passed += r.PassK
		}
		return fmt.Sprintf("%d / %d labels pass^k", passed, len(passK))
	}
	return "n/a"
}

func fmtTopCounts(m map[string]int, limit int) string {
	if len(m) == 0 {
		return "0"
	}
	wrapped := map[string]map[string]int{"agy": safeMap(m)}
	keys := topKeys(wrapped, limit)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", printable(k), wrapped["agy"][k]))
	}
	return strings.Join(parts, ", ")
}
