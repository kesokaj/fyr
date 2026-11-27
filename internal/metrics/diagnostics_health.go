package metrics

import "fmt"

const (
	ruleDirectShellRead     = "bash_read_bypass"
	scopeAutoSkill          = "auto_skill"
	scopeProposeOnly        = "propose_only"
	minDirectShellReads     = 3
	directShellReadMedRate  = 0.25
	directShellReadHighRate = 1.00
	minTimeoutRatePct       = 5.0
	highTimeoutRatePct      = 15.0
	largeFileSpikeRatio     = 0.25
	minLargeFileSpikeCnt    = 3
)

func appendHealthRegressionSignals(out []DiagnosticSignal, sum Summary, pfx string) []DiagnosticSignal {
	out = appendPreInvocationTimeoutSignal(out, sum, pfx)
	out = appendDirectShellReadSignal(out, sum, pfx)
	return appendStructuralRegressionSignal(out, sum, pfx)
}

func appendPreInvocationTimeoutSignal(out []DiagnosticSignal, sum Summary, pfx string) []DiagnosticSignal {
	if sum.E.TurnVets < minDiagnosticRuns || sum.E.TimeoutRatePct == nil || *sum.E.TimeoutRatePct < minTimeoutRatePct {
		return out
	}
	cur := round4(*sum.E.TimeoutRatePct)
	prev := priorMetricVal(sum, "E.timeout_rate_pct")
	sev := pickSev(cur >= highTimeoutRatePct, "high", "medium")
	msg := fmt.Sprintf("PreInvocation timed out on %d of %d turns (%.1f%%)", sum.E.TimedOutTurns, sum.E.TurnVets, cur)
	return append(out, makeSignal(pfx, "pre_invocation_timeouts", sev, "E.timeout_rate_pct", cur, prev, sum.E.TurnVets, msg))
}

func appendDirectShellReadSignal(out []DiagnosticSignal, sum Summary, pfx string) []DiagnosticSignal {
	sessions := max(sum.Sessions["agy"], sum.Health.MainSessions)
	if sessions < minDiagnosticSessions {
		return out
	}
	skips := sum.C.BashReadSkips["agy"]
	if skips < minDirectShellReads {
		return out
	}
	rate := float64(skips) / float64(sessions)
	if rate < directShellReadMedRate {
		return out
	}
	prev := priorMetricVal(sum, "C.bash_read_skips.agy")
	sev := pickSev(rate >= directShellReadHighRate, "high", "medium")
	msg := fmt.Sprintf("%d direct shell file reads skipped read_file ledger across %d sessions (%.2f/session)", skips, sessions, rate)
	return append(out, makeSignal(pfx, ruleDirectShellRead, sev, "C.bash_read_skips.agy", float64(skips), prev, sessions, msg))
}

func appendStructuralRegressionSignal(out []DiagnosticSignal, sum Summary, pfx string) []DiagnosticSignal {
	st := sum.Health.Structural
	cycleRisk := st.ProdPkgCycles >= minProdPkgCycles
	couplingRisk := st.HiddenCouplingEditPairs >= minHiddenCouplingPairs
	largeRisk := st.TotalCodeFiles >= minDiagnosticSessions && st.LargeFilesOver350 >= minLargeFileSpikeCnt &&
		float64(st.LargeFilesOver350)/float64(st.TotalCodeFiles) >= largeFileSpikeRatio
	activeRisks := countTrue(cycleRisk, couplingRisk, largeRisk)
	worsened := structuralDeltaCount(sum)
	if activeRisks < 2 && (worsened <= 0 || (!cycleRisk && !couplingRisk && !largeRisk)) {
		return out
	}
	score := max(activeRisks, worsened)
	sev := pickSev(cycleRisk || activeRisks >= 2, "high", "medium")
	msg := fmt.Sprintf("structural regression across %d risk(s) (cycles=%d, co_edit=%d, large_files=%d/%d)",
		score, st.ProdPkgCycles, st.HiddenCouplingEditPairs, st.LargeFilesOver350, st.TotalCodeFiles)
	return append(out, makeSignal(pfx, "structural_regression", sev, "H.structural.regression",
		float64(score), 0, max(st.TotalCodeFiles, st.TotalProdPkgs), msg))
}

func structuralDeltaCount(sum Summary) int {
	if sum.Deltas == nil || len(sum.Deltas.Metrics) == 0 {
		return 0
	}
	n := 0
	for _, mName := range []string{
		"H.structural.prod_pkg_cycles",
		"H.structural.hidden_coupling_edit_pairs",
		"H.structural.large_files_over_350",
	} {
		if md, ok := sum.Deltas.Metrics[mName]; ok && md.Delta > 0 {
			n++
		}
	}
	return n
}

func countTrue(flags ...bool) int {
	n := 0
	for _, f := range flags {
		if f {
			n++
		}
	}
	return n
}

func assignDiagnosticActions(signals []DiagnosticSignal) {
	for i := range signals {
		scope, action := classifyDiagnosticAction(signals[i].Rule)
		if signals[i].ActionScope == "" {
			signals[i].ActionScope = scope
		}
		if signals[i].RecommendedAction == "" {
			signals[i].RecommendedAction = action
		}
	}
}

func classifyDiagnosticAction(rule string) (string, string) {
	switch rule {
	case "search_grep_low":
		return scopeAutoSkill, "Update skills/ guidance to use fyr search --slice/--refs before grep/rg."
	case "redundant_reads_elevated", "redundant_reads_high":
		return scopeAutoSkill, "Update skills/ guidance to use ranged reads and honor read-ledger hints."
	case "eval_pass_rate_low":
		return scopeAutoSkill, "Update failing skill instructions under skills/ and re-run fyr eval."
	case "pre_invocation_timeouts":
		return scopeProposeOnly, "Propose Go hook fast-path optimization in internal/search/ (propose_only)."
	case ruleDirectShellRead:
		return scopeProposeOnly, "Propose PreToolUse bash guard tightening in internal/search/ (propose_only)."
	case "structural_regression", "prod_pkg_cycles", "hidden_coupling", "large_files_concentration", "god_node_churn":
		return scopeProposeOnly, "Propose modularization or dependency decoupling refactor (propose_only)."
	default:
		return scopeProposeOnly, "Open proposal or bug record for operator review (propose_only)."
	}
}

func severityRank(sev string) int {
	switch sev {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func pickSev(cond bool, t, f string) string {
	if cond {
		return t
	}
	return f
}

func priorMetricVal(sum Summary, key string) float64 {
	if sum.Deltas != nil && sum.Deltas.PreviousSessions >= minDiagnosticSessions {
		if md, ok := sum.Deltas.Metrics[key]; ok {
			return md.Previous
		}
	}
	return 0
}

func priorKneeRate(sum Summary) float64 {
	if sum.Deltas != nil && sum.Deltas.PreviousSessions >= minDiagnosticSessions && sum.Deltas.KneeRatePct != nil {
		return round4(sum.Deltas.KneeRatePct.Previous / 100.0)
	}
	return 0
}

func applyPersistenceHysteresis(signals []DiagnosticSignal, sum Summary, prevDigest *DailyDigest) {
	for i := range signals {
		if signals[i].Severity != "high" || isStaticHighSignal(signals[i].Rule, sum) {
			continue
		}
		if isHighConfirmedByPriorDigest(signals[i].Rule, prevDigest) || isHighConfirmedByPriorWindow(signals[i].Rule, sum) {
			continue
		}
		signals[i].Severity = "medium"
		signals[i].Summary += " (1st window; awaiting 2-day persistence)"
	}
}

func isStaticHighSignal(rule string, sum Summary) bool {
	if rule == "prod_pkg_cycles" {
		return true
	}
	return rule == "structural_regression" && sum.Health.Structural.ProdPkgCycles >= minProdPkgCycles
}

func isHighConfirmedByPriorDigest(rule string, prevDigest *DailyDigest) bool {
	if prevDigest == nil {
		return false
	}
	for _, s := range prevDigest.Signals {
		if s.Rule == rule && (s.Severity == "high" || s.Severity == "medium") {
			return true
		}
	}
	return false
}

func isHighConfirmedByPriorWindow(rule string, sum Summary) bool {
	if sum.Deltas == nil {
		return false
	}
	pv := func(k string) float64 { return sum.Deltas.Metrics[k].Previous }
	prevSess := max(sum.Deltas.PreviousSessions, int(pv("H.main_sessions")))
	prevSubDenom := max(prevSess, int(pv("subagents.agy")))
	if ok, handled := priorWindowSessionRuleConfirmed(rule, sum.Deltas, pv, prevSess, prevSubDenom); handled {
		return ok
	}
	return priorWindowRunRuleConfirmed(rule, sum.Deltas, pv, prevSess)
}

func priorWindowSessionRuleConfirmed(rule string, d *WindowDeltas, pv func(string) float64, prevSess, prevSubDenom int) (bool, bool) {
	if prevSess < minDiagnosticSessions {
		switch rule {
		case "context_knee_rate", "tool_error_rate_high", "subagents_over_budget", "guarded_downgrades",
			"search_grep_low", "retry_loops_elevated", "redundant_reads_elevated", "corrections_elevated",
			"escaped_bugs_present", ruleDirectShellRead, "god_node_churn":
			return false, true
		}
	}
	switch rule {
	case "context_knee_rate":
		return d.KneeRatePct != nil && d.KneeRatePct.Previous >= 25.0, true
	case "tool_error_rate_high":
		errRate := pv("D.tool_error_rate.agy")
		if d.ToolErrorRatePct != nil {
			errRate = d.ToolErrorRatePct.Previous / 100.0
		}
		return pv("B.tool_calls.agy") >= 10 && errRate >= 0.08, true
	case "subagents_over_budget":
		c := pv("H.subagents_over_budget")
		return c >= 2 && c/float64(prevSubDenom) >= subagentOverBudgetMedRate, true
	case "guarded_downgrades":
		c := pv("H.guarded_downgrades")
		return c >= 1 && c/float64(prevSubDenom) >= guardedDowngradeMedRate, true
	case "search_grep_low":
		return pv("C.grep_calls.agy") >= 5 && d.SearchGrepRatio != nil && d.SearchGrepRatio.Previous < 1.0, true
	case "retry_loops_elevated":
		c := pv("D.retry_loops.agy")
		return c >= 5 && c/float64(prevSess) >= retryLoopsMedRate, true
	case "redundant_reads_elevated":
		c := pv("C.redundant_reads.agy")
		return c >= 5 && c/float64(prevSess) >= redundantReadsMedRate, true
	case "corrections_elevated":
		c := pv("A.corrections")
		return c >= 3 && c/float64(prevSess) >= correctionsMedRate, true
	case "escaped_bugs_present":
		c := pv("A.escaped_bugs")
		return c >= 2 && c/float64(prevSess) >= escapedBugsMedRate, true
	case ruleDirectShellRead:
		c := pv("C.bash_read_skips.agy")
		return c >= minDirectShellReads && c/float64(prevSess) >= directShellReadMedRate, true
	case "god_node_churn":
		hits := pv("H.structural.god_node_clamp_hits") + pv("H.structural.god_node_repeat_reads")
		return hits >= 5 && (pv("H.structural.god_node_churn_rate") >= godNodeChurnMedRate || hits/float64(prevSess) >= godNodeHitsPerSessMed), true
	}
	return false, false
}

func priorWindowRunRuleConfirmed(rule string, d *WindowDeltas, pv func(string) float64, _ int) bool {
	switch rule {
	case "low_session_outcome":
		return pv("A.ratings_count") >= minDiagnosticSessions && d.MeanOutcomeScore != nil && d.MeanOutcomeScore.Previous < 3.5
	case "pre_invocation_timeouts":
		return pv("E.turn_vets") >= minDiagnosticRuns && pv("E.timeout_rate_pct") >= minTimeoutRatePct
	case "hidden_coupling":
		return pv("H.structural.hidden_coupling_edit_pairs") >= minHiddenCouplingPairs
	case "structural_regression":
		return pv("H.structural.prod_pkg_cycles") >= minProdPkgCycles || pv("H.structural.hidden_coupling_edit_pairs") >= minHiddenCouplingPairs
	case "judge_fail_rate":
		tot := pv("F.judge_passed") + pv("F.judge_failed")
		return tot >= minDiagnosticRuns && pv("F.judge_failed")/tot >= 0.33
	case "work_waves_failing":
		return pv("F.workers") >= minDiagnosticRuns && pv("F.failed")/pv("F.workers") >= 0.33
	case "dream_runs_failing":
		return pv("H.background_runs.dream.runs") >= minDiagnosticRuns && pv("H.background_runs.dream.failed")/pv("H.background_runs.dream.runs") >= 0.33
	case "auto_runs_failing":
		return pv("H.background_runs.auto.runs") >= minDiagnosticRuns && pv("H.background_runs.auto.failed")/pv("H.background_runs.auto.runs") >= 0.33
	case "eval_pass_rate_low":
		md, ok := d.Metrics["eval.pass_rate"]
		if !ok || md.Previous >= 0.85 {
			return false
		}
		if runs, hasRuns := d.Metrics["eval.graded_runs"]; hasRuns && runs.Previous < minDiagnosticRuns {
			return false
		}
		return true
	}
	return false
}
