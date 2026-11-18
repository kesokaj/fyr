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

func (s *Store) populateTriggerCoverage(sum *Summary, o ShowOptions, all []sess, trials []trial, since, now time.Time) error {
	var tc TriggerCoverage
	var rawWeeks float64
	if d := now.Sub(since); d > 0 {
		rawWeeks = d.Hours() / (24 * 7)
		tc.WindowWeeks = round4(rawWeeks)
	}
	if err := s.addWaveTriggerCoverage(&tc, o, since, now); err != nil {
		return err
	}
	for _, tr := range trials {
		if strings.HasPrefix(tr.label, "auto=") {
			tc.AutoModelRuns++
		}
	}
	if rawWeeks > 0 && tc.AutoModelRuns > 0 {
		tc.AutoModelRunsPerWeek = round4(float64(tc.AutoModelRuns) / rawWeeks)
	}
	if err := s.addSessionTriggerCoverage(&tc, all); err != nil {
		return err
	}
	tc.SearchCalls = sum.C.SQLiteVerbs["agy"]
	tc.GrepCalls = sum.C.GrepCalls["agy"]
	if tc.GrepCalls > 0 {
		r := round4(float64(tc.SearchCalls) / float64(tc.GrepCalls))
		tc.SearchGrepRatio = &r
	}
	tc.ReadLedgerBlocks = sum.E.LedgerSaved
	finalizeTriggerCoverageRates(&tc)
	sum.TriggerCoverage = tc
	return nil
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

func (s *Store) addWaveTriggerCoverage(tc *TriggerCoverage, o ShowOptions, since, now time.Time) error {
	where, args := windowAndProjectWhere("updated_at", o, since, now)
	rows, err := s.db.Query(`SELECT stamp, worker, state, model, base_check, judge_model, judge_verdict FROM work_waves WHERE `+where, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var stamp, worker, state, model, bc, jm, jv string
		if err := rows.Scan(&stamp, &worker, &state, &model, &bc, &jm, &jv); err != nil {
			return err
		}
		if !waveMatchesLabel(stamp, o) {
			continue
		}
		recordWaveCoverageRow(tc, worker, state, model, bc, jm, jv)
	}
	return rows.Err()
}

func recordWaveCoverageRow(tc *TriggerCoverage, worker, state, model, bc, jm, jv string) {
	if strings.HasPrefix(strings.TrimSpace(worker), "auto-") {
		tc.AutoModelRuns++
		return
	}
	tc.WorkerPhases++
	m := strings.TrimSpace(model)
	bcTrim := strings.TrimSpace(bc)
	jmTrim := strings.TrimSpace(jm)
	jvTrim := strings.TrimSpace(jv)
	if m != "" && !strings.EqualFold(m, "default") && bcTrim != "" && (jmTrim != "" || jvTrim != "") {
		tc.ConfiguredWorkerPhases++
	}
	var ws WorkSummary
	addWaveWorkerCounts(&ws, state, bc, jm, jv)
	tc.WorkerPassed += ws.Passed
	tc.BaseCheckPassed += ws.BaseCheckPassed
	tc.BaseCheckFailed += ws.BaseCheckFailed
	tc.JudgePassed += ws.JudgePassed
	tc.JudgeFailed += ws.JudgeFailed
}

func (s *Store) addSessionTriggerCoverage(tc *TriggerCoverage, all []sess) error {
	seenQ := map[string]bool{}
	handBuildSessions := 0
	for _, x := range all {
		steps, err := s.stepsOf(x.id)
		if err != nil {
			return err
		}
		ev, err := s.querySessionCommitSummary(x.id)
		if err != nil {
			return err
		}
		hasWorkVerb := countSessionBuildQuestions(tc, x, steps, ev.firstCommitMS, seenQ)
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
	return nil
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

func (s *Store) querySessionCommitSummary(sessionID string) (sessionCommitSummary, error) {
	rows, err := s.db.Query(`SELECT kind, COALESCE(value, ''), COALESCE(ts, 0) FROM events WHERE session_id = ? AND kind IN ('commit', 'handoff')`, sessionID)
	if err != nil {
		return sessionCommitSummary{}, err
	}
	defer rows.Close()
	var out sessionCommitSummary
	for rows.Next() {
		var kind, val string
		var ts int64
		if err := rows.Scan(&kind, &val, &ts); err != nil {
			return sessionCommitSummary{}, err
		}
		if kind == "handoff" {
			out.hasHandoff = true
		} else if kind == "commit" && val != "ambiguous" {
			out.hasNonAmbigCommit = true
			if ts > 0 && (out.firstCommitMS == 0 || ts < out.firstCommitMS) {
				out.firstCommitMS = ts
			}
		}
	}
	return out, rows.Err()
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
		[]string{"Hook blocks (clamped / ledger / guards)", fmt.Sprintf("%d / %d / %d (total %d)", h.ClampedSaves, s.E.LedgerSaved, h.HookBlocksGuards, h.HookDenials), "-"},
		[]string{"Model-family share", formatModelFamilyShare(h), "-"},
		[]string{"Guarded subagent downgrades", fmt.Sprint(h.GuardedDowngrades), "= 0"},
		[]string{"Live quota pools", formatPoolQuotas(h.PoolQuotas), "-"},
		[]string{"Sessions >= 180K knee", fmt.Sprintf("%d / %d", h.SessionsOverKnee, h.MainSessions), "= 0"},
		[]string{"Subagents over budget", fmt.Sprint(h.SubagentsOverBudget), "= 0"},
	)
}

func (s *Store) populateJudgeVerdictsHealth(sum *Summary, o ShowOptions, since, now time.Time) error {
	if o.Label != "" {
		return nil
	}
	rows, err := s.JudgeVerdicts(o.Project, since, now)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return err
	}
	for _, r := range rows {
		v := strings.ToUpper(strings.TrimSpace(r.Verdict))
		switch {
		case v == "PASS":
			sum.Health.JudgePassed++
			sum.F.JudgePassed++
			sum.TriggerCoverage.JudgePassed++
		case strings.Contains(v, "FAIL"):
			sum.Health.JudgeFailed++
			sum.F.JudgeFailed++
			sum.TriggerCoverage.JudgeFailed++
		}
		if strings.TrimSpace(r.FallbackReason) != "" || strings.Contains(strings.ToLower(r.Model), "gemini") {
			sum.Health.JudgeFallback++
			sum.F.JudgeFallback++
		}
	}
	return nil
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
