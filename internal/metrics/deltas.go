package metrics

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/metrics/report"
	"github.com/kesokaj/fyr/internal/search"
)

type (
	// MetricDelta compares a shape-only KPI in the current window against the equal prior window.
	MetricDelta = report.MetricDelta
	// WindowDeltas holds shape-only window-over-window KPI deltas.
	WindowDeltas = report.WindowDeltas
)

func (s *Store) populateWindowDeltas(sum *Summary, o ShowOptions, since, now time.Time) error {
	span := now.Sub(since)
	if o.SkipDeltas || span <= 0 {
		return nil
	}
	prevSince := since.Add(-span)
	prevOpts := o
	prevOpts.Since = prevSince
	prevOpts.Now = since
	prevOpts.SkipDeltas = true
	prev, err := s.Show(prevOpts)
	if err != nil {
		return err
	}
	if strings.TrimSpace(o.Project) == "" && o.Label == "" {
		if prevAST, ok := s.priorStructuralSnapshot(since, now); ok {
			prev.Health.Structural = prevAST
		}
	}
	sum.Deltas = buildWindowDeltas(sum, prev, prevSince, since)
	return nil
}

func (s *Store) priorStructuralSnapshot(since, now time.Time) (search.StructuralSignals, bool) {
	if s == nil || s.db == nil {
		return search.StructuralSignals{}, false
	}
	sinceDay := since.UTC().Format("2006-01-02")
	nowDay := now.UTC().Format("2006-01-02")
	if ast, ok := s.queryDigestASTSnapshot(`SELECT ast_json FROM daily_digests WHERE day <= ? AND day < ? ORDER BY day DESC LIMIT 1`, sinceDay, nowDay); ok {
		return ast, true
	}
	return s.queryDigestASTSnapshot(`SELECT ast_json FROM daily_digests WHERE day < ? ORDER BY day DESC LIMIT 1`, nowDay)
}

func (s *Store) queryDigestASTSnapshot(q string, args ...any) (search.StructuralSignals, bool) {
	var raw string
	if err := s.db.QueryRow(q, args...).Scan(&raw); err != nil || strings.TrimSpace(raw) == "" {
		return search.StructuralSignals{}, false
	}
	var wrap digestASTCol
	if json.Unmarshal([]byte(raw), &wrap) != nil || !hasStructuralSignals(wrap.AST) {
		return search.StructuralSignals{}, false
	}
	return wrap.AST, true
}

func hasStructuralSignals(st search.StructuralSignals) bool {
	return st.TotalCodeFiles > 0 || st.TotalProdPkgs > 0 || st.HiddenCouplingPairs > 0 ||
		st.HiddenCouplingEditPairs > 0 || st.HiddenCouplingReadPairs > 0 ||
		st.GodNodeClampHits > 0 || st.GodNodeRepeatReads > 0 || st.ProdPkgCycles > 0 || st.LargeFilesOver350 > 0
}

func buildWindowDeltas(cur, prev *Summary, prevSince, prevUntil time.Time) *WindowDeltas {
	curNum := cur.Numbers()
	prevNum := prev.Numbers()
	metricsMap := make(map[string]MetricDelta, len(curNum)+2)
	for k, cv := range curNum {
		if pv, ok := prevNum[k]; ok {
			metricsMap[k] = newMetricDelta(cv, pv)
		}
	}
	metricsMap["C.bash_read_skips.agy"] = newMetricDelta(float64(cur.C.BashReadSkips["agy"]), float64(prev.C.BashReadSkips["agy"]))
	if cur.E.TimeoutRatePct != nil && prev.E.TimeoutRatePct != nil {
		metricsMap["E.timeout_rate_pct"] = newMetricDelta(*cur.E.TimeoutRatePct, *prev.E.TimeoutRatePct)
	}
	populateEvalPassRateDelta(metricsMap, cur, prev)
	d := &WindowDeltas{
		PreviousWindow: map[string]string{
			"since": prevSince.UTC().Format("2006-01-02"),
			"until": prevUntil.UTC().Format("2006-01-02"),
		},
		PreviousSessions: prev.Sessions["agy"],
		Sessions:         ptrMetricDelta(float64(cur.Sessions["agy"]), float64(prev.Sessions["agy"])),
		Turns:            ptrMetricDelta(float64(cur.B.Turns["agy"]), float64(prev.B.Turns["agy"])),
		ToolCalls:        ptrMetricDelta(float64(cur.B.ToolCalls["agy"]), float64(prev.B.ToolCalls["agy"])),
		RetryLoops:       ptrMetricDelta(float64(cur.D.RetryLoops["agy"]), float64(prev.D.RetryLoops["agy"])),
		Metrics:          metricsMap,
	}
	populateRateDeltas(d, cur, prev)
	return d
}

func populateRateDeltas(d *WindowDeltas, cur, prev *Summary) {
	if cr, ok1 := kneePct(cur); ok1 {
		if pr, ok2 := kneePct(prev); ok2 {
			d.KneeRatePct = ptrMetricDelta(cr, pr)
		}
	}
	if cr, ok1 := overTargetPct(cur); ok1 {
		if pr, ok2 := overTargetPct(prev); ok2 {
			d.OverTargetPct = ptrMetricDelta(cr, pr)
		}
	}
	d.SearchGrepRatio = ptrOptionalDelta(cur.C.SearchGrepRatio["agy"], prev.C.SearchGrepRatio["agy"], 1)
	d.RangedReadPct = ptrOptionalDelta(cur.C.RangedReadPct["agy"], prev.C.RangedReadPct["agy"], 1)
	d.ToolErrorRatePct = ptrOptionalDelta(cur.D.ToolErrorRate["agy"], prev.D.ToolErrorRate["agy"], 100)
	d.MeanOutcomeScore = ptrOptionalDelta(cur.A.MeanOutcomeScore, prev.A.MeanOutcomeScore, 1)
}

func kneePct(s *Summary) (float64, bool) {
	if s.Health.MainSessions <= 0 {
		return 0, false
	}
	return round4(float64(s.Health.SessionsOverKnee) * 100.0 / float64(s.Health.MainSessions)), true
}

func overTargetPct(s *Summary) (float64, bool) {
	over := s.B.SessionsOverContextTarget["agy"]
	peaks := len(s.B.SessionPeaks["agy"])
	if over == nil || peaks <= 0 {
		return 0, false
	}
	return round4(float64(*over) * 100.0 / float64(peaks)), true
}

func ptrOptionalDelta(cur, prev *float64, scale float64) *MetricDelta {
	if cur == nil || prev == nil {
		return nil
	}
	return ptrMetricDelta(*cur*scale, *prev*scale)
}

func ptrMetricDelta(cur, prev float64) *MetricDelta {
	md := newMetricDelta(cur, prev)
	return &md
}

func newMetricDelta(cur, prev float64) MetricDelta {
	c, p := round4(cur), round4(prev)
	return MetricDelta{Current: c, Previous: p, Delta: round4(c - p)}
}

func populateEvalPassRateDelta(metricsMap map[string]MetricDelta, cur, prev *Summary) {
	curRate, curRuns := evalPassRateFromSummary(cur)
	prevRate, prevRuns := evalPassRateFromSummary(prev)
	if curRuns > 0 || prevRuns > 0 {
		metricsMap["eval.graded_runs"] = newMetricDelta(float64(curRuns), float64(prevRuns))
	}
	if curRuns > 0 && prevRuns > 0 {
		metricsMap["eval.pass_rate"] = newMetricDelta(curRate, prevRate)
	}
}

func evalRowsFromSummary(sum *Summary) []EvalRow {
	if sum == nil {
		return nil
	}
	rows, _ := sum.EvalRows.([]EvalRow)
	return rows
}

func evalPassRateFromSummary(sum *Summary) (float64, int) {
	if sum == nil {
		return 0, 0
	}
	if evalRows := evalRowsFromSummary(sum); len(evalRows) > 0 {
		totalRuns, totalPassed := 0, 0
		for _, r := range evalRows {
			if strings.EqualFold(r.Kind, "indicator") || r.N <= 0 {
				continue
			}
			totalRuns += r.N
			totalPassed += r.C
		}
		if totalRuns > 0 {
			return round4(float64(totalPassed) / float64(totalRuns)), totalRuns
		}
		return 0, 0
	}
	totalRuns, totalPassed := 0, 0
	for k, pk := range sum.A.PassK {
		if strings.HasPrefix(k, evalPrefix) && pk.N > 0 {
			totalRuns += pk.N
			totalPassed += pk.C
		}
	}
	if totalRuns == 0 {
		return 0, 0
	}
	return round4(float64(totalPassed) / float64(totalRuns)), totalRuns
}
