package metrics

import (
	"fmt"
	"math"
	"strings"
)

func scorecardSection(s *Summary) reportSection {
	return reportSection{
		Title:    "0. Scorecard",
		Note:     "Headline KPIs, equal-prior window deltas, and top diagnostic signals.",
		Table:    table{Head: []string{"Category", "KPI", "Current", "Window Delta"}, Rows: buildScorecardRows(s)},
		SubTitle: "Top-5 Diagnostic Signals",
		SubTable: buildTopDiagnosticTable(s.Diagnostics),
	}
}

func buildScorecardRows(s *Summary) [][]string {
	d := s.Deltas
	targetLbl := fmtTargetShort(s.B.ContextTarget)
	return [][]string{
		{"1. Volume & Context", "Sessions", fmt.Sprint(s.Sessions["agy"]), fmtDeltaField(d, func(w *WindowDeltas) *MetricDelta { return w.Sessions }, "%.0f")},
		{"1. Volume & Context", "Turns", fmt.Sprint(s.B.Turns["agy"]), fmtDeltaField(d, func(w *WindowDeltas) *MetricDelta { return w.Turns }, "%.0f")},
		{"1. Volume & Context", "Tool Calls", fmt.Sprint(s.B.ToolCalls["agy"]), fmtDeltaField(d, func(w *WindowDeltas) *MetricDelta { return w.ToolCalls }, "%.0f")},
		{"1. Volume & Context", fmt.Sprintf("Peak Context (>= 60%% knee %% & >= %s target %%)", targetLbl), fmtPeakContextKPI(s), fmtContextRatesDelta(d)},
		{"2. Second-Brain", "Search:Grep Ratio", fmtScorecardSearchGrep(s), fmtDeltaField(d, func(w *WindowDeltas) *MetricDelta { return w.SearchGrepRatio }, "%.2f")},
		{"2. Second-Brain", "Repeat Reads %", fmtRepeatReadsKPI(s), fmtRepeatReadsDelta(d)},
		{"2. Second-Brain", "Blocked Ledger / Clamp Saves", fmtLedgerClampKPI(s), fmtSavesDelta(d)},
		{"2. Second-Brain", "Bash Read Bypasses", fmt.Sprint(s.C.BashReadSkips["agy"]), "n/a"},
		{"2. Second-Brain", "PreInvocation Timeouts & Latency", fmtPreInvocationScorecardKPI(s), "n/a"},
		{"3. Reliability", "Tool Error Rate %", fmtToolErrorRateKPI(s), fmtDeltaField(d, func(w *WindowDeltas) *MetricDelta { return w.ToolErrorRatePct }, "%.2f%%")},
		{"3. Reliability", "Retry Loops", fmt.Sprint(s.D.RetryLoops["agy"]), fmtDeltaField(d, func(w *WindowDeltas) *MetricDelta { return w.RetryLoops }, "%.0f")},
		{"3. Reliability", "Guarded Subagent Downgrades", fmt.Sprint(s.Health.GuardedDowngrades), fmtMapMetricDelta(d, "H.guarded_downgrades", "%.0f")},
		{"4. Window Deltas", "Prior Window", fmtPriorWindowKPI(d), fmtOutcomeScoreDelta(d)},
	}
}

func buildTopDiagnosticTable(rep *DiagnosticReport) table {
	t := table{Head: []string{"Severity", "SignalID", "Metric", "Current -> Previous", "Delta", "Summary"}}
	if rep == nil || len(rep.Signals) == 0 {
		t.Rows = append(t.Rows, []string{"none", "-", "-", "-", "-", "no active diagnostic signals in window"})
		return t
	}
	limit := min(5, len(rep.Signals))
	for _, sig := range rep.Signals[:limit] {
		t.Rows = append(t.Rows, []string{
			printable(sig.Severity),
			printable(sig.SignalID),
			printable(sig.Metric),
			fmt.Sprintf("%s -> %s", fmtSignalNum(sig.Current), fmtSignalNum(sig.Previous)),
			fmtSignedNum(sig.Delta),
			printable(sig.Summary),
		})
	}
	return t
}

func fmtTargetShort(target int64) string {
	if target <= 0 {
		target = DefaultContextTarget
	}
	if target >= 1000 && target%1000 == 0 {
		return fmt.Sprintf("%dk", target/1000)
	}
	return fmt.Sprint(target)
}

func fmtPeakContextKPI(s *Summary) string {
	peak := s.B.PeakContext["agy"]
	kneeStr := "n/a"
	if pct, ok := kneePct(s); ok {
		kneeStr = fmt.Sprintf("%.1f%% (%d/%d)", pct, s.Health.SessionsOverKnee, s.Health.MainSessions)
	}
	targetStr := "n/a"
	if pct, ok := overTargetPct(s); ok {
		targetStr = fmt.Sprintf("%.1f%% (%d/%d)", pct, *s.B.SessionsOverContextTarget["agy"], len(s.B.SessionPeaks["agy"]))
	}
	return fmt.Sprintf("%d (knee %s, target %s)", peak, kneeStr, targetStr)
}

func fmtScorecardSearchGrep(s *Summary) string {
	return fmt.Sprintf("%s (%d : %d)", fmtFloatPtr(s.C.SearchGrepRatio["agy"], "%.2f"), s.C.SQLiteVerbs["agy"], s.C.GrepCalls["agy"])
}

func repeatReadsCounts(s *Summary) (pct float64, red, total int, ok bool) {
	red = s.C.RedundantReads["agy"]
	if s.readCalls != nil {
		total = s.readCalls["agy"]
	}
	if total == 0 && s.B.ToolCallsByTool != nil {
		total = s.B.ToolCallsByTool["agy"]["Read"]
	}
	if total <= 0 {
		return 0, red, 0, false
	}
	return round4(float64(red) * 100.0 / float64(total)), red, total, true
}

func fmtRepeatReadsKPI(s *Summary) string {
	pct, red, total, ok := repeatReadsCounts(s)
	if !ok {
		if red > 0 {
			return fmt.Sprintf("n/a (%d / 0)", red)
		}
		return "0.0% (0 / 0)"
	}
	return fmt.Sprintf("%.1f%% (%d / %d)", pct, red, total)
}

func fmtLedgerClampKPI(s *Summary) string {
	ledger := s.E.LedgerSaved + s.Health.HookBlocksLedger
	clamp := s.Health.ClampedSaves
	return fmt.Sprintf("%d (ledger:%d, clamp:%d)", ledger+clamp, ledger, clamp)
}

func fmtPreInvocationScorecardKPI(s *Summary) string {
	return fmt.Sprintf("%s (p50 %s, p95 %s)",
		fmtPctRatio(s.E.TimeoutRatePct, s.E.TimedOutTurns, s.E.TurnVets),
		fmtOptMS(s.E.P50DurationMS), fmtOptMS(s.E.P95DurationMS))
}

func fmtToolErrorRateKPI(s *Summary) string {
	r := s.D.ToolErrorRate["agy"]
	if r == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.2f%%", *r*100.0)
}

func fmtPriorWindowKPI(d *WindowDeltas) string {
	if d == nil {
		return "n/a"
	}
	return fmt.Sprintf("%s .. %s (%d sessions)", printable(d.PreviousWindow["since"]), printable(d.PreviousWindow["until"]), d.PreviousSessions)
}

func fmtOutcomeScoreDelta(d *WindowDeltas) string {
	if d == nil || d.MeanOutcomeScore == nil {
		return "n/a"
	}
	return "outcome " + fmtMetricDeltaVal(*d.MeanOutcomeScore, "%.2f")
}

func fmtRepeatReadsDelta(d *WindowDeltas) string {
	v := fmtMapMetricDelta(d, "C.redundant_reads.agy", "%.0f")
	if v == "n/a" {
		return v
	}
	return "reads " + v
}

func fmtDeltaField(d *WindowDeltas, pick func(*WindowDeltas) *MetricDelta, format string) string {
	if d == nil {
		return "n/a"
	}
	md := pick(d)
	if md == nil {
		return "n/a"
	}
	return fmtMetricDeltaVal(*md, format)
}

func fmtMapMetricDelta(d *WindowDeltas, key, format string) string {
	if d == nil || d.Metrics == nil {
		return "n/a"
	}
	md, ok := d.Metrics[key]
	if !ok {
		return "n/a"
	}
	return fmtMetricDeltaVal(md, format)
}

func fmtContextRatesDelta(d *WindowDeltas) string {
	if d == nil {
		return "n/a"
	}
	var parts []string
	if d.KneeRatePct != nil {
		parts = append(parts, "knee "+fmtSignedFmt(d.KneeRatePct.Delta, "%.1f%%"))
	}
	if d.OverTargetPct != nil {
		parts = append(parts, "target "+fmtSignedFmt(d.OverTargetPct.Delta, "%.1f%%"))
	}
	if len(parts) == 0 {
		return "n/a"
	}
	return strings.Join(parts, ", ")
}

func fmtSavesDelta(d *WindowDeltas) string {
	if d == nil || d.Metrics == nil {
		return "n/a"
	}
	l1, ok1 := d.Metrics["E.ledger_saved"]
	l2, ok2 := d.Metrics["H.hook_blocks_ledger"]
	cl, ok3 := d.Metrics["H.clamped_saves"]
	if !ok1 && !ok2 && !ok3 {
		return "n/a"
	}
	cur := l1.Current + l2.Current + cl.Current
	prev := l1.Previous + l2.Previous + cl.Previous
	return fmtMetricDeltaVal(newMetricDelta(cur, prev), "%.0f")
}

func fmtMetricDeltaVal(md MetricDelta, format string) string {
	return fmt.Sprintf("%s (was %s)", fmtSignedFmt(md.Delta, format), fmt.Sprintf(format, md.Previous))
}

func fmtSignedFmt(v float64, format string) string {
	s := fmt.Sprintf(format, v)
	if v > 0 && !strings.HasPrefix(s, "+") {
		return "+" + s
	}
	return s
}

func fmtSignalNum(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.4f", v), "0"), ".")
}

func fmtSignedNum(v float64) string {
	s := fmtSignalNum(v)
	if v > 0 && !strings.HasPrefix(s, "+") {
		return "+" + s
	}
	return s
}
