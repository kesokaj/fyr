package metrics

import "fmt"

const (
	ruleDirectShellRead  = "bash_read_bypass"
	scopeAutoSkill       = "auto_skill"
	scopeProposeOnly     = "propose_only"
	minDirectShellReads  = 3
	minTimeoutRatePct    = 5.0
	highTimeoutRatePct   = 15.0
	largeFileSpikeRatio  = 0.25
	minLargeFileSpikeCnt = 3
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
	sev := pickSev(skips >= sessions*2 || skips >= 10, "high", "medium")
	msg := fmt.Sprintf("%d direct shell file reads skipped read_file ledger across %d sessions", skips, sessions)
	return append(out, makeSignal(pfx, ruleDirectShellRead, sev, "C.bash_read_skips.agy", float64(skips), 0, sessions, msg))
}

func appendStructuralRegressionSignal(out []DiagnosticSignal, sum Summary, pfx string) []DiagnosticSignal {
	st := sum.Health.Structural
	cycleRisk := st.ProdPkgCycles >= minProdPkgCycles
	couplingRisk := st.HiddenCouplingEditPairs >= minHiddenCouplingPairs
	largeRisk := st.TotalCodeFiles >= minDiagnosticSessions && st.LargeFilesOver350 >= minLargeFileSpikeCnt &&
		float64(st.LargeFilesOver350)/float64(st.TotalCodeFiles) >= largeFileSpikeRatio
	activeRisks := countTrue(cycleRisk, couplingRisk, largeRisk)
	worsened := structuralDeltaCount(sum)
	if activeRisks < 2 && !(worsened > 0 && (cycleRisk || couplingRisk || largeRisk)) {
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
