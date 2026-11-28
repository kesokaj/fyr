package report

import (
	"fmt"

	"github.com/kesokaj/fyr/internal/search"
)

func triggerCoverageSection(s *Summary) reportSection {
	tc := s.TriggerCoverage
	h := s.Health
	t := Table{Head: []string{"KPI", "Value", "Target"}}
	t.Rows = append(t.Rows,
		[]string{"v9 orchestration (/fyr-swarm / gate / land)", fmt.Sprintf("swarm-skill:%d, gate:%d, land:%d (tools:%d)", tc.SwarmSkillLoads, tc.GateVerbs, tc.LandVerbs, h.OrchestrationCalls), "-"},
		[]string{"Deterministic gate coverage", FmtPctRatio(tc.DeterministicGatePct, tc.DeterministicGateSessions, tc.BuildSessions), ">= 80%"},
		[]string{"PreInvocation timeout rate", FmtPctRatio(s.E.TimeoutRatePct, s.E.TimedOutTurns, s.E.TurnVets), "= 0%"},
		[]string{"PreInvocation latency (p50 / p95)", FmtP50P95MS(s.E.P50DurationMS, s.E.P95DurationMS), "< 250ms"},
		[]string{"Judge verdicts (PASS / FAIL)", fmt.Sprintf("%d / %d", tc.JudgePassed, tc.JudgeFailed), "-"},
		[]string{"Build-time questions", fmt.Sprint(tc.BuildTimeQuestions), "= 0"},
		[]string{"Search : grep", fmtSearchGrepKPI(tc), ">= 1.0"},
		[]string{"Read-ledger blocks", fmt.Sprint(tc.ReadLedgerBlocks), "> 0"},
		[]string{"fyr auto model runs", fmt.Sprintf("%.2f/wk (%d in %.1f wk)", tc.AutoModelRunsPerWeek, tc.AutoModelRuns, tc.WindowWeeks), "> 0/wk"},
		[]string{"DoD closed", FmtPctRatio(tc.DoDClosedPct, tc.DoDClosedSessions, tc.BuildSessions), ">= 80%"},
	)
	if tc.InstalledSkillsCount > 0 {
		t.Rows = append(t.Rows, []string{"Skill catalog coverage", FmtPctRatio(tc.SkillCoveragePct, tc.TriggeredSkillsCount, tc.InstalledSkillsCount), "-"})
	}
	appendV9TriggerRows(&t, s)
	return reportSection{
		Title: "Trigger coverage",
		Note:  "KPIs computed from metrics.db against the autonomy and feature-use targets.",
		Table: t,
	}
}

func appendV9TriggerRows(t *Table, s *Summary) {
	h := s.Health
	t.Rows = append(t.Rows,
		[]string{"v9 orchestration", fmt.Sprintf("%d (agent:%d, workflow:%d, swarm:%d)", h.OrchestrationCalls, h.SubagentDispatches, h.WorkflowCalls, h.SwarmCalls), "-"},
		[]string{"Deterministic gates", fmt.Sprint(h.DeterministicGates), "-"},
		[]string{"Hook blocks (clamped / ledger / guards)", fmt.Sprintf("%d / %d / %d (total %d)", h.ClampedSaves, h.HookBlocksLedger, h.HookBlocksGuards, h.HookDenials), "-"},
		[]string{"Model-family share", formatModelFamilyShare(h), "-"},
		[]string{"Guarded subagent downgrades", fmt.Sprint(h.GuardedDowngrades), "= 0"},
		[]string{"Live quota pools", formatPoolQuotas(h.PoolQuotas), "-"},
		[]string{"Sessions >= 60% knee", fmt.Sprintf("%d / %d", h.SessionsOverKnee, h.MainSessions), "= 0"},
		[]string{"Subagents over budget", fmt.Sprint(h.SubagentsOverBudget), "= 0"},
	)
}

// FmtOptMS formats an optional millisecond duration or "n/a".
func FmtOptMS(p *float64) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.0fms", *p)
}

// FmtP50P95MS formats optional p50/p95 millisecond latencies.
func FmtP50P95MS(p50, p95 *float64) string {
	if p50 == nil && p95 == nil {
		return "n/a"
	}
	return fmt.Sprintf("%s / %s", FmtOptMS(p50), FmtOptMS(p95))
}

// FmtPctRatio formats an optional percentage alongside its num/den counts.
func FmtPctRatio(pct *float64, num, den int) string {
	if pct == nil {
		return fmt.Sprintf("n/a (%d / %d)", num, den)
	}
	return fmt.Sprintf("%.1f%% (%d / %d)", *pct, num, den)
}

func fmtSearchGrepKPI(tc TriggerCoverage) string {
	if tc.SearchGrepRatio == nil {
		return fmt.Sprintf("n/a (%d : %d)", tc.SearchCalls, tc.GrepCalls)
	}
	return fmt.Sprintf("%.2f (%d : %d)", *tc.SearchGrepRatio, tc.SearchCalls, tc.GrepCalls)
}

func tagCohortSection(tc *TagCohort) reportSection {
	tag := search.Printable(tc.Tag)
	t := Table{
		Head: []string{"Metric", "Pre-tag (< " + tag + ")", "Post-tag (>= " + tag + ")"},
		Rows: [][]string{
			{"Sessions", fmt.Sprint(tc.Pre.Sessions), fmt.Sprint(tc.Post.Sessions)},
			{"Tool calls", fmt.Sprint(tc.Pre.ToolCalls), fmt.Sprint(tc.Post.ToolCalls)},
			{"Search : grep ratio", fmtCohortSearchGrep(tc.Pre), fmtCohortSearchGrep(tc.Post)},
			{"Bash read bypasses", fmt.Sprint(tc.Pre.BashReadSkips), fmt.Sprint(tc.Post.BashReadSkips)},
			{"PreInvocation timeout %", FmtPctRatio(tc.Pre.PreInvocationTimeoutPct, tc.Pre.PreInvocationTimedOut, tc.Pre.TurnVets), FmtPctRatio(tc.Post.PreInvocationTimeoutPct, tc.Post.PreInvocationTimedOut, tc.Post.TurnVets)},
			{"PreInvocation latency (p50 / p95)", FmtP50P95MS(tc.Pre.PreInvocationP50MS, tc.Pre.PreInvocationP95MS), FmtP50P95MS(tc.Post.PreInvocationP50MS, tc.Post.PreInvocationP95MS)},
		},
	}
	return reportSection{
		Title: "Tag Cohort (" + tag + ")",
		Note:  fmt.Sprintf("Pre-tag history (< %s) vs post-tag window (>= %s).", tc.Cutoff, tc.Cutoff),
		Table: t,
	}
}

func agentRolesSection(roles []AgentRoleMetrics) reportSection {
	t := Table{Head: []string{"Role", "Sessions", "Tool calls", "Tool error %", "Hook denials", "Search : grep", "PreInvocation timeouts"}}
	for _, r := range roles {
		t.Rows = append(t.Rows, []string{
			search.Printable(r.Role),
			fmt.Sprint(r.Sessions),
			fmt.Sprint(r.ToolCalls),
			fmtScaledFloatPtr(r.ToolErrorRate, 100.0, "%.2f%%"),
			fmt.Sprint(r.HookDenials),
			fmtRatioCounts(r.SearchGrepRatio, r.SQLiteVerbs, r.GrepCalls),
			FmtPctRatio(r.TimeoutRatePct, r.TimedOutTurns, r.TurnVets),
		})
	}
	return reportSection{
		Title: "Agent Roles",
		Note:  "Per-role activity across filtered sessions (empty agent_type normalized to main; hook denials excluded from tool errors).",
		Table: t,
	}
}

func fmtCohortSearchGrep(c CohortMetrics) string {
	return fmtRatioCounts(c.SearchGrepRatio, c.SQLiteVerbs, c.GrepCalls)
}

func fmtRatioCounts(ratio *float64, num, den int) string {
	if ratio == nil {
		return fmt.Sprintf("n/a (%d : %d)", num, den)
	}
	return fmt.Sprintf("%.2f (%d : %d)", *ratio, num, den)
}

func fmtScaledFloatPtr(p *float64, scale float64, format string) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf(format, *p*scale)
}

func simSummarySection(sim *SIMSummary) reportSection {
	t := Table{
		Head: []string{"Metric", "Value"},
		Rows: [][]string{
			{"Active / resolved problems", fmt.Sprintf("%d / %d", sim.ActiveProblems, sim.ResolvedProblems)},
			{"Workarounds (total / active)", fmt.Sprintf("%d / %d", sim.Workarounds, sim.ActiveWorkarounds)},
			{"Attempts", fmt.Sprint(sim.Attempts)},
			{"Attempts by verdict", fmtTopCounts(sim.AttemptsByVerdict, 6)},
			{"Field status counts", fmtTopCounts(sim.FieldStatusCounts, 6)},
		},
	}
	return reportSection{
		Title: "SIM Summary",
		Note:  "Self-Improvement Memory ledger counts read from brain.db.",
		Table: t,
	}
}

func skillRuleTelemetrySection(skills []SkillTelemetry, rules []RuleTelemetry) reportSection {
	t := Table{Head: []string{"Skill", "Suggested", "Loaded", "Conversion %", "Sessions", "Mean Outcome", "Tool Error %", "Rewrite-Skill"}}
	for _, sk := range skills {
		t.Rows = append(t.Rows, []string{
			search.Printable(sk.Skill),
			fmt.Sprint(sk.Suggested),
			fmt.Sprint(sk.Loaded),
			FmtFloatPtr(sk.ConversionRatePct, "%.1f%%"),
			fmt.Sprint(sk.Sessions),
			FmtFloatPtr(sk.MeanOutcomeScore, "%.2f"),
			FmtFloatPtr(sk.ToolErrorRatePct, "%.2f%%"),
			fmt.Sprint(sk.RewriteSkillCount),
		})
	}
	if len(t.Rows) == 0 {
		t.Rows = append(t.Rows, []string{"none", "0", "0", "n/a", "0", "n/a", "n/a", "0"})
	}
	sec := reportSection{
		Title: "Skill & Rule Telemetry",
		Note:  "Per-skill suggestion/load conversion, session outcomes, error rates, rewrite-skill actions, and rule telemetry.",
		Table: t,
	}
	if len(rules) > 0 {
		rt := Table{Head: []string{"Rule", "Kind", "Count", "Severity"}}
		for _, r := range rules {
			sev := search.Printable(r.Severity)
			if sev == "" {
				sev = "-"
			}
			rt.Rows = append(rt.Rows, []string{search.Printable(r.Rule), search.Printable(r.Kind), fmt.Sprint(r.Count), sev})
		}
		sec.SubTitle = "Rule Telemetry"
		sec.SubTable = rt
	}
	return sec
}
