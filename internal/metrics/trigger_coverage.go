package metrics

import (
	"fmt"
	"strings"
	"time"
)

// TriggerCoverage aggregates the autonomy and feature-use KPIs computed from
// metrics.db for Summary and rendered in Markdown and HTML reports.
type TriggerCoverage struct {
	WorkerPhases             int      `json:"worker_phases"`
	QualifyingPhases         int      `json:"qualifying_phases"`
	QualifyingWorkerPhasePct *float64 `json:"qualifying_worker_phase_pct"`
	ConfiguredWorkerPhases   int      `json:"configured_worker_phases"`
	ConfiguredWorkerPhasePct *float64 `json:"configured_worker_phase_pct"`
	WorkerPassed             int      `json:"worker_passed"`
	BaseCheckPassed          int      `json:"base_check_passed"`
	BaseCheckFailed          int      `json:"base_check_failed"`
	JudgePassed              int      `json:"judge_passed"`
	JudgeFailed              int      `json:"judge_failed"`
	BuildTimeQuestions       int      `json:"build_time_questions"`
	SearchCalls              int      `json:"search_calls"`
	GrepCalls                int      `json:"grep_calls"`
	SearchGrepRatio          *float64 `json:"search_grep_ratio"`
	ReadLedgerBlocks         int      `json:"read_ledger_blocks"`
	AutoModelRuns            int      `json:"auto_model_runs"`
	WindowWeeks              float64  `json:"window_weeks"`
	AutoModelRunsPerWeek     float64  `json:"auto_model_runs_per_week"`
	DoDClosedSessions        int      `json:"dod_closed_sessions"`
	BuildSessions            int      `json:"build_sessions"`
	DoDClosedPct             *float64 `json:"dod_closed_pct"`
	SessionsOverKnee         int      `json:"sessions_over_knee"`
	MainSessions             int      `json:"main_sessions"`
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
	seenQ := map[string]bool{}
	handBuildSessions := 0
	for _, x := range all {
		ev := sessionCommitSummaryFromEvents(eventsByID[x.id])
		hasWorkVerb := countSessionBuildQuestions(tc, x, stepsByID[x.id], ev.firstCommitMS, seenQ)
		if x.parent == "" && ev.hasNonAmbigCommit {
			tc.BuildSessions++
			if ev.hasHandoff {
				tc.DoDClosedSessions++
			}
			if !hasWorkVerb && x.label == "" {
				handBuildSessions++
			}
		}
	}
	tc.QualifyingPhases = tc.WorkerPhases + handBuildSessions
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
	t := table{Head: []string{"KPI", "Value", "Target"}}
	if s.F.Waves > 0 || s.F.Workers > 0 || tc.WorkerPhases > 0 {
		t.Rows = append(t.Rows,
			[]string{"Qualifying phases run as workers", fmtPctRatio(tc.QualifyingWorkerPhasePct, tc.WorkerPhases, tc.QualifyingPhases), ">= 80%"},
			[]string{"Worker phases with model: + judge: + base-check:", fmtPctRatio(tc.ConfiguredWorkerPhasePct, tc.ConfiguredWorkerPhases, tc.WorkerPhases), "= 100%"},
			[]string{"Worker verdicts (PASS / total)", fmt.Sprintf("%d / %d", tc.WorkerPassed, tc.WorkerPhases), "-"},
			[]string{"Base-Check verdicts (PASS / FAIL)", fmt.Sprintf("%d / %d", tc.BaseCheckPassed, tc.BaseCheckFailed), "-"},
		)
	}
	t.Rows = append(t.Rows,
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
