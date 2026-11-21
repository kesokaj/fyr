package metrics

import (
	"fmt"
	"strings"
	"time"
)

// TriggerCoverage aggregates the autonomy and feature-use KPIs computed from
// metrics.db for Summary and rendered in Markdown and HTML reports.
type TriggerCoverage struct {
	WorkerPhases              int      `json:"worker_phases"`
	QualifyingPhases          int      `json:"qualifying_phases"`
	QualifyingWorkerPhasePct  *float64 `json:"qualifying_worker_phase_pct"`
	ConfiguredWorkerPhases    int      `json:"configured_worker_phases"`
	ConfiguredWorkerPhasePct  *float64 `json:"configured_worker_phase_pct"`
	WorkerPassed              int      `json:"worker_passed"`
	BaseCheckPassed           int      `json:"base_check_passed"`
	BaseCheckFailed           int      `json:"base_check_failed"`
	JudgePassed               int      `json:"judge_passed"`
	JudgeFailed               int      `json:"judge_failed"`
	BuildTimeQuestions        int      `json:"build_time_questions"`
	SearchCalls               int      `json:"search_calls"`
	GrepCalls                 int      `json:"grep_calls"`
	SearchGrepRatio           *float64 `json:"search_grep_ratio"`
	ReadLedgerBlocks          int      `json:"read_ledger_blocks"`
	AutoModelRuns             int      `json:"auto_model_runs"`
	WindowWeeks               float64  `json:"window_weeks"`
	AutoModelRunsPerWeek      float64  `json:"auto_model_runs_per_week"`
	DoDClosedSessions         int      `json:"dod_closed_sessions"`
	BuildSessions             int      `json:"build_sessions"`
	DoDClosedPct              *float64 `json:"dod_closed_pct"`
	SessionsOverKnee          int      `json:"sessions_over_knee"`
	MainSessions              int      `json:"main_sessions"`
	OrchestrationCalls        int      `json:"orchestration_calls"`
	SwarmSkillLoads           int      `json:"swarm_skill_loads"`
	GateVerbs                 int      `json:"gate_verbs"`
	LandVerbs                 int      `json:"land_verbs"`
	DeterministicGates        int      `json:"deterministic_gates"`
	DeterministicGateSessions int      `json:"deterministic_gate_sessions"`
	DeterministicGatePct      *float64 `json:"deterministic_gate_pct"`
	PreInvocationTurnVets     int      `json:"pre_invocation_turn_vets"`
	PreInvocationTimedOut     int      `json:"pre_invocation_timed_out"`
	PreInvocationTimeoutPct   *float64 `json:"pre_invocation_timeout_pct"`
	PreInvocationP50MS        *float64 `json:"pre_invocation_p50_ms"`
	PreInvocationP95MS        *float64 `json:"pre_invocation_p95_ms"`
}

type sessionCommitSummary struct {
	hasNonAmbigCommit bool
	hasHandoff        bool
	firstCommitMS     int64
}

func populateTriggerCoverage(sum *Summary, o ShowOptions, all []sess, trials []trial, data *showData, since, now time.Time) {
	var tc TriggerCoverage
	var rawWeeks float64
	if d := now.Sub(since); d > 0 {
		rawWeeks = d.Hours() / (24 * 7)
		tc.WindowWeeks = round4(rawWeeks)
	}
	addWaveTriggerCoverageRows(&tc, data.waves)
	for _, tr := range trials {
		if strings.HasPrefix(tr.label, "auto=") {
			tc.AutoModelRuns++
		}
	}
	if rawWeeks > 0 && tc.AutoModelRuns > 0 {
		tc.AutoModelRunsPerWeek = round4(float64(tc.AutoModelRuns) / rawWeeks)
	}
	addSessionTriggerCoverage(&tc, all, data.steps, data.events)
	tc.SearchCalls = sum.C.SQLiteVerbs["agy"]
	tc.GrepCalls = sum.C.GrepCalls["agy"]
	if tc.GrepCalls > 0 {
		r := round4(float64(tc.SearchCalls) / float64(tc.GrepCalls))
		tc.SearchGrepRatio = &r
	}
	tc.ReadLedgerBlocks = sum.E.LedgerSaved
	tc.PreInvocationTurnVets = sum.E.TurnVets
	tc.PreInvocationTimedOut = sum.E.TimedOutTurns
	tc.PreInvocationTimeoutPct = sum.E.TimeoutRatePct
	tc.PreInvocationP50MS = sum.E.P50DurationMS
	tc.PreInvocationP95MS = sum.E.P95DurationMS
	finalizeTriggerCoverageRates(&tc)
	sum.TriggerCoverage = tc
}

func finalizeTriggerCoverageRates(tc *TriggerCoverage) {
	if tc.QualifyingPhases > 0 {
		pct := round4(float64(tc.WorkerPhases) * 100 / float64(tc.QualifyingPhases))
		tc.QualifyingWorkerPhasePct = &pct
	}
	if tc.WorkerPhases > 0 {
		pct := round4(float64(tc.ConfiguredWorkerPhases) * 100 / float64(tc.WorkerPhases))
		tc.ConfiguredWorkerPhasePct = &pct
	}
	if tc.BuildSessions > 0 {
		pct := round4(float64(tc.DoDClosedSessions) * 100 / float64(tc.BuildSessions))
		tc.DoDClosedPct = &pct
		gatePct := round4(float64(tc.DeterministicGateSessions) * 100 / float64(tc.BuildSessions))
		tc.DeterministicGatePct = &gatePct
	}
}

func addWaveTriggerCoverageRows(tc *TriggerCoverage, waves []waveRow) {
	for _, w := range waves {
		recordWaveCoverageRow(tc, w)
	}
}

func recordWaveCoverageRow(tc *TriggerCoverage, w waveRow) {
	if strings.HasPrefix(strings.TrimSpace(w.worker), "auto-") {
		tc.AutoModelRuns++
		return
	}
	tc.WorkerPhases++
	m := strings.TrimSpace(w.model)
	bcTrim := strings.TrimSpace(w.baseCheck)
	jmTrim := strings.TrimSpace(w.judgeModel)
	jvTrim := strings.TrimSpace(w.judgeVerdict)
	if m != "" && !strings.EqualFold(m, "default") && bcTrim != "" && (jmTrim != "" || jvTrim != "") {
		tc.ConfiguredWorkerPhases++
	}
	var ws WorkSummary
	addWaveWorkerCounts(&ws, w.state, w.model, w.baseCheck, w.judgeModel, w.judgeVerdict)
	tc.WorkerPassed += ws.Passed
	tc.BaseCheckPassed += ws.BaseCheckPassed
	tc.BaseCheckFailed += ws.BaseCheckFailed
	tc.JudgePassed += ws.JudgePassed
	tc.JudgeFailed += ws.JudgeFailed
}

func addSessionTriggerCoverage(tc *TriggerCoverage, all []sess, stepsByID map[string][]stp, eventsByID map[string][]sessEvent) {
	seenQ, seenSteps := map[string]bool{}, map[string]bool{}
	handBuildSessions := 0
	for _, x := range all {
		ev := sessionCommitSummaryFromEvents(eventsByID[x.id])
		hasWorkVerb := countSessionBuildQuestions(tc, x, stepsByID[x.id], ev.firstCommitMS, seenQ)
		hasGate := countSessionV9Coverage(tc, x, stepsByID[x.id], seenSteps)
		if x.parent == "" && ev.hasNonAmbigCommit {
			tc.BuildSessions++
			if ev.hasHandoff {
				tc.DoDClosedSessions++
			}
			if hasGate {
				tc.DeterministicGateSessions++
			}
			if !hasWorkVerb && x.label == "" {
				handBuildSessions++
			}
		}
	}
	tc.QualifyingPhases = tc.WorkerPhases + handBuildSessions
}

func countSessionV9Coverage(tc *TriggerCoverage, x sess, steps []stp, seen map[string]bool) bool {
	if x.vendor == historical {
		return false
	}
	hasGate := false
	for _, st := range steps {
		if st.kind == "tool" || st.kind == "assistant" || st.kind == "generation" {
			key := x.vendor + "\x00" + x.label + "\x00" + st.ext
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		if st.kind != "tool" {
			continue
		}
		if st.skill == "fyr-swarm" {
			tc.SwarmSkillLoads++
		}
		for _, v := range st.verbs {
			switch {
			case v == "fyr gate" || strings.HasPrefix(v, "fyr gate "):
				tc.GateVerbs++
				hasGate = true
			case v == "fyr land" || strings.HasPrefix(v, "fyr land "):
				tc.LandVerbs++
				hasGate = true
			case v == "fyr verify" || strings.HasPrefix(v, "fyr verify ") || v == "fyr close" || strings.HasPrefix(v, "fyr close "):
				hasGate = true
			}
		}
	}
	return hasGate
}

func countSessionBuildQuestions(tc *TriggerCoverage, x sess, steps []stp, firstCommitMS int64, seenQ map[string]bool) bool {
	interviewSkill := false
	workStarted := false
	hasWorkVerb := false
	for _, st := range steps {
		if st.skill == "grilling" || st.skill == "fyr-feature" {
			interviewSkill = true
		}
		for _, v := range st.verbs {
			if v == "fyr work" || strings.HasPrefix(v, "fyr work ") {
				hasWorkVerb = true
				workStarted = true
			}
		}
		if firstCommitMS > 0 && st.ts >= firstCommitMS {
			workStarted = true
		}
		if st.kind != "tool" || (st.tool != "agy:ask_question" && st.tool != "AskUserQuestion") {
			continue
		}
		if st.ext != "" {
			key := x.vendor + "\x00" + st.ext
			if seenQ[key] {
				continue
			}
			seenQ[key] = true
		}
		if x.parent == "" && interviewSkill && !workStarted {
			continue
		}
		tc.BuildTimeQuestions++
	}
	return hasWorkVerb
}

func sessionCommitSummaryFromEvents(events []sessEvent) sessionCommitSummary {
	var out sessionCommitSummary
	for _, ev := range events {
		if ev.kind == "handoff" {
			out.hasHandoff = true
		} else if ev.kind == "commit" && ev.value != "ambiguous" {
			out.hasNonAmbigCommit = true
			if ev.ts > 0 && (out.firstCommitMS == 0 || ev.ts < out.firstCommitMS) {
				out.firstCommitMS = ev.ts
			}
		}
	}
	return out
}

func triggerCoverageSection(s *Summary) reportSection {
	tc := s.TriggerCoverage
	h := s.Health
	t := table{Head: []string{"KPI", "Value", "Target"}}
	t.Rows = append(t.Rows,
		[]string{"v9 orchestration (/fyr-swarm / gate / land)", fmt.Sprintf("swarm-skill:%d, gate:%d, land:%d (tools:%d)", tc.SwarmSkillLoads, tc.GateVerbs, tc.LandVerbs, h.OrchestrationCalls), "-"},
		[]string{"Deterministic gate coverage", fmtPctRatio(tc.DeterministicGatePct, tc.DeterministicGateSessions, tc.BuildSessions), ">= 80%"},
		[]string{"PreInvocation timeout rate", fmtPctRatio(s.E.TimeoutRatePct, s.E.TimedOutTurns, s.E.TurnVets), "= 0%"},
		[]string{"PreInvocation latency (p50 / p95)", fmtP50P95MS(s.E.P50DurationMS, s.E.P95DurationMS), "< 250ms"},
		[]string{"Judge verdicts (PASS / FAIL)", fmt.Sprintf("%d / %d", tc.JudgePassed, tc.JudgeFailed), "-"},
		[]string{"Build-time questions", fmt.Sprint(tc.BuildTimeQuestions), "= 0"},
		[]string{"Search : grep", fmtSearchGrepKPI(tc), ">= 1.0"},
		[]string{"Read-ledger blocks", fmt.Sprint(tc.ReadLedgerBlocks), "> 0"},
		[]string{"fyr auto model runs", fmt.Sprintf("%.2f/wk (%d in %.1f wk)", tc.AutoModelRunsPerWeek, tc.AutoModelRuns, tc.WindowWeeks), "> 0/wk"},
		[]string{"DoD closed", fmtPctRatio(tc.DoDClosedPct, tc.DoDClosedSessions, tc.BuildSessions), ">= 80%"},
	)
	appendV9TriggerRows(&t, s)
	return reportSection{
		Title: "Trigger coverage",
		Note:  "KPIs computed from metrics.db against the autonomy and feature-use targets.",
		Table: t,
	}
}

func fmtOptMS(p *float64) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.0fms", *p)
}

func fmtP50P95MS(p50, p95 *float64) string {
	if p50 == nil && p95 == nil {
		return "n/a"
	}
	return fmt.Sprintf("%s / %s", fmtOptMS(p50), fmtOptMS(p95))
}

func appendV9TriggerRows(t *table, s *Summary) {
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

func fmtPctRatio(pct *float64, num, den int) string {
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
