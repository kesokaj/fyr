package report

import (
	"fmt"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
)

type reportSection struct {
	Title, Note string
	Table       Table
	SubTitle    string
	SubTable    Table
}

func reportSections(sum *Summary, series []Serie, evals *EvalView) []reportSection {
	secs := []reportSection{
		scorecardSection(sum),
		executivePulseSection(sum, series),
		secondBrainSection(sum, series),
		qualityLoopSection(sum, series, evals),
		autonomySection(sum, series),
		backgroundAutonomySection(sum),
		triggerCoverageSection(sum),
		projectBreakdownSection(sum, series),
	}
	if len(sum.AgentRoles) > 0 {
		secs = append(secs, agentRolesSection(sum.AgentRoles))
	}
	if sum.SIM != nil && sum.SIM.Available {
		secs = append(secs, simSummarySection(sum.SIM))
	}
	if len(sum.SkillTelemetry) > 0 || len(sum.RuleTelemetry) > 0 {
		secs = append(secs, skillRuleTelemetrySection(sum.SkillTelemetry, sum.RuleTelemetry))
	}
	if sum.TagCohort != nil {
		secs = append(secs, tagCohortSection(sum.TagCohort))
	}
	return secs
}

func executivePulseSection(s *Summary, series []Serie) reportSection {
	t := Table{Head: tableHead("Metric", series)}
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
	addSeriesRow(&t, series, fmt.Sprintf("Over context target (%d)", s.B.ContextTarget), s.OverTarget)
	addSeriesRow(&t, series, "Sessions >= 60% knee", agyCol(fmt.Sprintf("%d / %d", s.Health.SessionsOverKnee, s.Health.MainSessions)))
	addSeriesRow(&t, series, "Compactions", agyFallbackCol(s.B.Compactions, fmt.Sprintf("%d (steps:%d)", s.Health.SearchDBCompactions, s.Health.StepCompactions)))
	addSeriesRow(&t, series, "Search.db compactions", agyCol(fmt.Sprint(s.Health.SearchDBCompactions)))
	return reportSection{Title: "1. Executive Pulse", Table: t}
}

func secondBrainSection(s *Summary, series []Serie) reportSection {
	t := Table{Head: tableHead("Metric", series)}
	addSeriesRow(&t, series, "Search : grep ratio (SQLite / grep)", func(v string) string {
		return fmt.Sprintf("%s (%d / %d)", FmtFloatPtr(s.C.SearchGrepRatio[v], "%.3f"), s.C.SQLiteVerbs[v], s.C.GrepCalls[v])
	})
	addSeriesRow(&t, series, "Ranged read %", func(v string) string { return FmtFloatPtr(s.C.RangedReadPct[v], "%.1f%%") })
	addSeriesRow(&t, series, "Redundant reads", func(v string) string { return fmt.Sprint(s.C.RedundantReads[v]) })
	addSeriesRow(&t, series, "Bash read bypasses", func(v string) string { return fmt.Sprint(s.C.BashReadSkips[v]) })
	addSeriesRow(&t, series, "Top Bash verbs", agyCol(fmtTopVerbCounts(s.C.TopBashVerbs, 5)))
	addSeriesRow(&t, series, "Turn vets", agyCol(fmt.Sprint(s.E.TurnVets)))
	addSeriesRow(&t, series, "PreInvocation timeouts", agyCol(FmtPctRatio(s.E.TimeoutRatePct, s.E.TimedOutTurns, s.E.TurnVets)))
	addSeriesRow(&t, series, "PreInvocation latency (p50 / p95)", agyCol(FmtP50P95MS(s.E.P50DurationMS, s.E.P95DurationMS)))
	addSeriesRow(&t, series, "Mean injected bytes", agyCol(FmtFloatPtr(s.E.MeanInjectedBytes, "%.0f")))
	addSeriesRow(&t, series, "Mean vet score", agyCol(FmtFloatPtr(s.E.MeanVetScore, "%.1f")))
	addSeriesRow(&t, series, "VET_FIRST turns", agyCol(fmt.Sprint(s.E.VetFirstTurns)))
	addSeriesRow(&t, series, "Bypassed turns (!)", agyCol(fmt.Sprint(s.E.BypassedTurns)))
	addSeriesRow(&t, series, "Pivots pre-seeded", agyCol(fmt.Sprint(s.E.PivotsSeeded)))
	addSeriesRow(&t, series, "Pivots used / skills loaded", agyCol(fmt.Sprintf("%d / %d", s.E.PivotsUsed, s.E.SkillsLoaded)))
	addSeriesRow(&t, series, "Read-ledger blocks saved", agyCol(fmt.Sprint(s.E.LedgerSaved)))
	addSeriesRow(&t, series, "Clamped read saves ([CLAMPED])", agyCol(fmt.Sprint(s.Health.ClampedSaves)))
	return reportSection{Title: "2. Second-Brain Leverage", Table: t}
}

func qualityLoopSection(s *Summary, series []Serie, evals *EvalView) reportSection {
	t := Table{Head: tableHead("Metric", series)}
	addSeriesRow(&t, series, "DoD closed (ambiguous) / of", func(v string) string {
		c := s.A.DoDClosed[v]
		return fmt.Sprintf("%d (%d) / %d", c.Closed, c.Ambiguous, c.Of)
	})
	addSeriesRow(&t, series, "Verifier (PASS / FAIL)", agyCol(fmt.Sprintf("%d / %d", s.A.Verifier["PASS"], s.A.Verifier["FAIL"])))
	addSeriesRow(&t, series, "Review findings (high / medium / low)", agyCol(fmt.Sprintf("%d / %d / %d",
		s.A.ReviewFindings["high"], s.A.ReviewFindings["medium"], s.A.ReviewFindings["low"])))
	addSeriesRow(&t, series, "Escaped bugs", agyCol(fmt.Sprint(s.A.EscapedBugs)))
	addSeriesRow(&t, series, "Corrections (FEEDBACK-*)", agyCol(fmt.Sprint(s.A.Corrections)))
	addSeriesRow(&t, series, "Rated sessions", agyCol(fmt.Sprint(s.A.RatingsCount)))
	addSeriesRow(&t, series, "Mean outcome / token efficiency", agyCol(fmt.Sprintf("%s / %s",
		FmtFloatPtr(s.A.MeanOutcomeScore, "%.2f"), FmtFloatPtr(s.A.MeanTokenEfficiency, "%.2f"))))
	addSeriesRow(&t, series, "Tool error rate / retry loops", func(v string) string {
		return fmt.Sprintf("%s / %d", FmtFloatPtr(s.D.ToolErrorRate[v], "%.3f"), s.D.RetryLoops[v])
	})
	addSeriesRow(&t, series, "Hook denials (excl. from error rate)", agyCol(fmt.Sprintf("%d (clamped:%d, ledger:%d, guards:%d)",
		s.Health.HookDenials, s.Health.ClampedSaves, s.Health.HookBlocksLedger, s.Health.HookBlocksGuards)))
	addSeriesRow(&t, series, "Eval pass^k / suite", agyCol(fmtEvalSummary(s.A.PassK, evals)))
	if s.SIM != nil && s.SIM.Available {
		addSeriesRow(&t, series, "SIM problems (active / resolved / workarounds)", agyCol(fmt.Sprintf("%d / %d / %d",
			s.SIM.ActiveProblems, s.SIM.ResolvedProblems, s.SIM.Workarounds)))
		addSeriesRow(&t, series, "SIM attempts", agyCol(fmt.Sprint(s.SIM.Attempts)))
	}
	return reportSection{Title: "3. Quality & Learning Loop", Table: t}
}

func autonomySection(s *Summary, series []Serie) reportSection {
	t := Table{Head: tableHead("Metric", series)}
	if s.F.Waves > 0 || s.F.Workers > 0 {
		addSeriesRow(&t, series, "Work waves / workers", agyCol(fmt.Sprintf("%d / %d", s.F.Waves, s.F.Workers)))
		addSeriesRow(&t, series, "Worker verdicts (PASS / FAIL)", agyCol(fmt.Sprintf("%d / %d", s.F.Passed, s.F.Failed)))
		addSeriesRow(&t, series, "Base-Check verdicts (PASS / FAIL)", agyCol(fmt.Sprintf("%d / %d", s.F.BaseCheckPassed, s.F.BaseCheckFailed)))
	}
	addSeriesRow(&t, series, "Cross-family Judge (PASS / FAIL / fallback)", agyCol(fmt.Sprintf("%d / %d / %d",
		s.F.JudgePassed, s.F.JudgeFailed, s.F.JudgeFallback)))
	appendAutonomyV9Rows(&t, series, s)
	addSeriesRow(&t, series, "Subagent tool calls", func(v string) string { return fmtTopCounts(s.D.SubagentToolCalls[v], 5) })
	return reportSection{Title: "4. Autonomy & Model Routing", Table: t}
}

func appendAutonomyV9Rows(t *Table, series []Serie, s *Summary) {
	h := s.Health
	addSeriesRow(t, series, "v9 orchestration", agyCol(fmt.Sprintf("%d (agent:%d, workflow:%d, swarm:%d)",
		h.OrchestrationCalls, h.SubagentDispatches, h.WorkflowCalls, h.SwarmCalls)))
	addSeriesRow(t, series, "Deterministic gates", agyCol(fmt.Sprint(h.DeterministicGates)))
	addSeriesRow(t, series, "Model-family share", agyCol(formatModelFamilyShare(h)))
	addSeriesRow(t, series, "Guarded subagent downgrades", agyCol(fmt.Sprint(h.GuardedDowngrades)))
	addSeriesRow(t, series, "Subagents over budget", agyCol(fmt.Sprint(h.SubagentsOverBudget)))
	addSeriesRow(t, series, "Live quota pools", agyCol(formatPoolQuotas(h.PoolQuotas)))
	addSeriesRow(t, series, "fyr auto open asks / verdict", agyCol(formatAutoHealth(h)))
	if o := s.Orchestration; o != nil && o.Runs > 0 {
		addSeriesRow(t, series, "Orchestration runs (units / ok / fail / rounds)", agyCol(fmt.Sprintf("%d (%d / %d / %d / %d; %s)",
			o.Runs, o.Units, o.Succeeded, o.Failed, o.DefectRounds, fmtTopCounts(o.ByKind, 5))))
		addSeriesRow(t, series, "Orchestration mean duration", agyCol(FmtDurationMS(o.Runs, o.MeanDurationMS)))
	}
}

func projectBreakdownSection(s *Summary, series []Serie) reportSection {
	if len(s.Projects) > 0 {
		t := Table{Head: []string{"Project", "Sessions", "Active min", "Tokens", "Search : grep", "Mean vet score", "Corrections", "Escaped bugs", "Last active", "Auto findings", "Handoff lag (d)"}}
		for _, p := range s.Projects {
			t.Rows = append(t.Rows, []string{
				search.Printable(RedactHomePath(p.Project)),
				fmt.Sprint(p.Sessions),
				fmt.Sprintf("%.1f", p.ActiveMin),
				fmt.Sprint(p.Tokens),
				FmtFloatPtr(p.SearchGrepRatio, "%.3f"),
				FmtFloatPtr(p.MeanVetScore, "%.1f"),
				fmt.Sprint(p.Corrections),
				fmt.Sprint(p.EscapedBugs),
				search.Printable(p.LastActive),
				fmt.Sprint(p.AutoFindings),
				FmtIntPtr(p.HandoffLagDays),
			})
		}
		return reportSection{Title: "5. Per-Project / Project Breakdown", Table: t}
	}
	t := Table{Head: tableHead("Skill / Subagent", series)}
	for _, k := range TopKeys(s.C.Skills, 10) {
		addSeriesRow(&t, series, "skill: "+search.Printable(k), func(v string) string { return fmt.Sprint(s.C.Skills[v][k]) })
	}
	for _, k := range TopKeys(s.Subagents, 10) {
		addSeriesRow(&t, series, "subagent: "+search.Printable(k), func(v string) string { return fmt.Sprint(s.Subagents[v][k]) })
	}
	if len(t.Rows) == 0 {
		addSeriesRow(&t, series, "none", func(string) string { return "0" })
	}
	return reportSection{Title: "5. Per-Project / Project Breakdown", Table: t}
}

func addSeriesRow(t *Table, series []Serie, name string, f func(v string) string) {
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

// FmtFloatPtr formats an optional float pointer or "n/a".
func FmtFloatPtr(p *float64, format string) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf(format, *p)
}

// FmtIntPtr formats an optional int pointer or "n/a".
func FmtIntPtr(p *int) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprint(*p)
}

func fmtEvalSummary(passK map[string]PassK, evals *EvalView) string {
	if evals != nil && len(evals.Rows) > 0 {
		return fmt.Sprintf("%d rows (%d trials)", evals.RowCount, evals.Trials)
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
	wrapped := map[string]map[string]int{"agy": SafeMap(m)}
	keys := TopKeys(wrapped, limit)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", search.Printable(k), wrapped["agy"][k]))
	}
	return strings.Join(parts, ", ")
}

func formatModelFamilyShare(h HealthSignals) string {
	if len(h.ModelFamilyCounts) == 0 {
		return "n/a"
	}
	var parts []string
	for _, fam := range []string{"gemini", "anthropic", "other"} {
		if cnt := h.ModelFamilyCounts[fam]; cnt > 0 {
			parts = append(parts, fmt.Sprintf("%s %.1f%% (%d)", fam, h.ModelFamilyShare[fam], cnt))
		}
	}
	if len(parts) == 0 {
		return "n/a"
	}
	return strings.Join(parts, ", ")
}

func formatPoolQuotas(pools []PoolQuota) string {
	if len(pools) == 0 {
		return "n/a"
	}
	parts := make([]string, 0, len(pools))
	for _, p := range pools {
		parts = append(parts, fmt.Sprintf("%s: 5h=%s wk=%s", search.Printable(p.Name), fmtPoolPct(p.Five), fmtPoolPct(p.Week)))
	}
	return strings.Join(parts, ", ")
}

func fmtPoolPct(v int) string {
	if v < 0 {
		return "disabled"
	}
	return fmt.Sprintf("%d%%", v)
}

func formatAutoHealth(h HealthSignals) string {
	if !h.AutoRunsPresent {
		return "n/a"
	}
	verdict := search.Printable(strings.TrimSpace(h.AutoLastVerdict))
	if verdict == "" {
		verdict = "n/a"
	}
	return fmt.Sprintf("%d / %s", h.AutoOpenAsks, verdict)
}

func fmtTopVerbCounts(verbs []VerbCount, limit int) string {
	if len(verbs) == 0 {
		return "none"
	}
	n := min(limit, len(verbs))
	parts := make([]string, 0, n)
	for _, vc := range verbs[:n] {
		parts = append(parts, fmt.Sprintf("%s:%d", search.Printable(vc.Verb), vc.Count))
	}
	return strings.Join(parts, ", ")
}
