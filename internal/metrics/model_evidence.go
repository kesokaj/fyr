package metrics

import (
	"database/sql"
	"math"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/metrics/show"
)

// RoleEvidence holds empirical run counts and decayed totals for one role partition.
type RoleEvidence struct {
	Runs             int     `json:"runs,omitempty"`
	Pass             int     `json:"pass,omitempty"`
	DecayedRuns      float64 `json:"decayed_runs,omitempty"`
	DecayedSuccesses float64 `json:"decayed_successes,omitempty"`
}

// ModelEvidence summarizes empirical worker, trial, judge, and rating performance
// for one model in [since, now), consumed by cmd/fyr for model selection.
type ModelEvidence struct {
	WorkRuns            int          `json:"work_runs"`
	WorkPass            int          `json:"work_pass"`
	WorkMeanSeconds     float64      `json:"work_mean_seconds"`
	MeanLatencySeconds  float64      `json:"mean_latency_seconds,omitempty"`
	EvalRuns            int          `json:"eval_runs"`
	EvalPass            int          `json:"eval_pass"`
	TrialRuns           int          `json:"trial_runs"`
	TrialPass           int          `json:"trial_pass"`
	JudgeRuns           int          `json:"judge_runs"`
	JudgePass           int          `json:"judge_pass"`
	RatingRuns          int          `json:"rating_runs"`
	RatingScoreSum      float64      `json:"rating_score_sum"`
	DecayedRuns         float64      `json:"decayed_runs"`
	DecayedSuccesses    float64      `json:"decayed_successes"`
	MeanTokenEfficiency float64      `json:"mean_token_efficiency,omitempty"`
	BuilderEvidence     RoleEvidence `json:"builder_evidence,omitzero"`
	JudgeEvidence       RoleEvidence `json:"judge_evidence,omitzero"`
}

type modelEvidenceAcc struct {
	ev         ModelEvidence
	workSecSum float64
	workTimed  int
	latSecSum  float64
	latTimed   int
	effSum     float64
	effCount   int
}

const (
	evidenceHalfLifeDays = 14.0
	evidenceWindowDays   = 56 // 4 * halfLife
	msPerDay             = 86_400_000.0
	trialEvidenceWeight  = 1.0
	benchEvidenceWeight  = 0.20
	judgeEvidenceWeight  = 0.9
	ratingEvidenceWeight = 0.75
)

// ModelEvidence aggregates work_waves, trials (honoring policy_model),
// worker-attributed judge_verdicts, and session_ratings over a 56-day decay window.
func (s *Store) ModelEvidence(since, now time.Time) (map[string]ModelEvidence, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if since.IsZero() {
		since = now.AddDate(0, 0, -evidenceWindowDays)
	}
	workDurs, err := s.queryWorkRunDurations()
	if err != nil {
		return nil, err
	}
	accs := map[string]*modelEvidenceAcc{}
	if err := s.accumulateWaveModelEvidence(accs, workDurs, since, now); err != nil {
		return nil, err
	}
	if err := s.accumulateEvalModelEvidence(accs, since, now); err != nil {
		return nil, err
	}
	if err := s.accumulateJudgeModelEvidence(accs, since, now); err != nil {
		return nil, err
	}
	if err := s.accumulateRatingModelEvidence(accs, since, now); err != nil {
		return nil, err
	}
	return finalizeModelEvidence(accs), nil
}

func finalizeModelEvidence(accs map[string]*modelEvidenceAcc) map[string]ModelEvidence {
	out := make(map[string]ModelEvidence, len(accs))
	for m, a := range accs {
		if a.workTimed > 0 {
			a.ev.WorkMeanSeconds = round4(a.workSecSum / float64(a.workTimed))
		}
		if a.latTimed > 0 {
			a.ev.MeanLatencySeconds = round4(a.latSecSum / float64(a.latTimed))
		}
		if a.effCount > 0 {
			eff := round4(a.effSum / float64(a.effCount))
			a.ev.MeanTokenEfficiency = eff
		}
		out[m] = a.ev
	}
	return out
}

func decayWeight(rawTS int64, now time.Time) float64 {
	tsMS := normalizeMilli(rawTS)
	ageDays := float64(now.UnixMilli()-tsMS) / msPerDay
	if ageDays < 0 {
		ageDays = 0
	}
	return math.Pow(0.5, ageDays/evidenceHalfLifeDays)
}

func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func ensureModelAcc(accs map[string]*modelEvidenceAcc, model string) *modelEvidenceAcc {
	a := accs[model]
	if a == nil {
		a = &modelEvidenceAcc{}
		accs[model] = a
	}
	return a
}

func isAuditRoleSignal(agentType, label string) bool {
	for _, s := range []string{strings.ToLower(strings.TrimSpace(agentType)), strings.ToLower(strings.TrimSpace(label))} {
		if s == "" {
			continue
		}
		for _, kw := range []string{"judge", "reviewer", "verifier", "security"} {
			if strings.Contains(s, kw) {
				return true
			}
		}
	}
	return false
}

func addRoleObservation(a *modelEvidenceAcc, audit bool, passed bool, weight, successWeight float64) {
	dst := &a.ev.BuilderEvidence
	if audit {
		dst = &a.ev.JudgeEvidence
	}
	dst.Runs++
	if passed {
		dst.Pass++
	}
	dst.DecayedRuns += weight
	dst.DecayedSuccesses += successWeight
}

func (s *Store) queryWorkRunDurations() (map[string]float64, error) {
	rows, err := s.db.Query(`SELECT run, COALESCE(duration_ms, 0), COALESCE(started_at, 0), COALESCE(ended_at, 0)
		FROM sessions WHERE (parent_id IS NULL OR parent_id = '') AND run IS NOT NULL AND substr(run, 1, 5) = 'work:'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var run string
		var durMS, startMS, endMS int64
		if err := rows.Scan(&run, &durMS, &startMS, &endMS); err != nil {
			return nil, err
		}
		sec := float64(durMS) / 1000.0
		if sec <= 0 && startMS > 0 && endMS > startMS {
			sec = float64(endMS-startMS) / 1000.0
		}
		if sec > 0 {
			out[run] += sec
		}
	}
	return out, rows.Err()
}

func (s *Store) accumulateWaveModelEvidence(accs map[string]*modelEvidenceAcc, workDurs map[string]float64, since, now time.Time) error {
	where, args := windowAndProjectWhere("updated_at", ShowOptions{}, since, now)
	q := `SELECT stamp, worker, state, model, COALESCE(setup_s, 0), COALESCE(agy_s, 0), COALESCE(check_s, 0) FROM work_waves WHERE ` + where
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scanWaveModelRow(rows, accs, workDurs); err != nil {
			return err
		}
	}
	return rows.Err()
}

func scanWaveModelRow(rows *sql.Rows, accs map[string]*modelEvidenceAcc, workDurs map[string]float64) error {
	var stamp, worker, state, model string
	var setupS, agyS, checkS int64
	if err := rows.Scan(&stamp, &worker, &state, &model, &setupS, &agyS, &checkS); err != nil {
		return err
	}
	m := strings.TrimSpace(model)
	st := strings.ToUpper(strings.TrimSpace(state))
	if m == "" || strings.EqualFold(m, "default") || (st != "PASS" && st != "FAIL") {
		return nil
	}
	a := ensureModelAcc(accs, m)
	a.ev.WorkRuns++
	if st == "PASS" {
		a.ev.WorkPass++
	}
	sec := float64(setupS + agyS + checkS)
	if sec <= 0 {
		sec = workDurs[workRunKey(stamp, worker)]
	}
	if sec > 0 {
		a.workSecSum += sec
		a.workTimed++
		a.latSecSum += sec
		a.latTimed++
	}
	return nil
}

func (s *Store) accumulateEvalModelEvidence(accs map[string]*modelEvidenceAcc, since, now time.Time) error {
	modelExpr := `COALESCE(TRIM(s.model), '')`
	if tableCols(s.db, "trials")["policy_model"] {
		modelExpr = `COALESCE(NULLIF(TRIM(t.policy_model), ''), COALESCE(TRIM(s.model), ''))`
	}
	q := `SELECT t.run, LOWER(t.status), ` + modelExpr + `, COALESCE(t.started_at, 0),
		LOWER(COALESCE(t.kind, '')), LOWER(COALESCE(t.label, '')),
		LOWER(COALESCE(s.agent_type, '')), COALESCE(t.seconds, 0)
		FROM trials t
		LEFT JOIN sessions s ON s.run = t.run AND (s.parent_id IS NULL OR s.parent_id = '')
		WHERE t.started_at >= ? AND t.started_at < ?
		  AND LOWER(t.status) IN ('pass', 'fail')
		ORDER BY t.run, CASE WHEN TRIM(COALESCE(s.model, '')) != '' THEN 0 ELSE 1 END, COALESCE(s.started_at, 0) ASC`
	rows, err := s.db.Query(q, since.UnixMilli(), now.UnixMilli())
	if err != nil {
		return err
	}
	defer rows.Close()
	seenRuns := map[string]bool{}
	for rows.Next() {
		if err := scanEvalTrialRow(rows, seenRuns, accs, now); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return s.accumulateUnclaimedEvalSessions(accs, since, now)
}

func scanEvalTrialRow(rows *sql.Rows, seenRuns map[string]bool, accs map[string]*modelEvidenceAcc, now time.Time) error {
	var run, status, model, kind, label, agentType string
	var startedAt, seconds int64
	if err := rows.Scan(&run, &status, &model, &startedAt, &kind, &label, &agentType, &seconds); err != nil {
		return err
	}
	if seenRuns[run] {
		return nil
	}
	m := strings.TrimSpace(model)
	if m == "" || strings.EqualFold(m, "default") {
		return nil
	}
	seenRuns[run] = true
	baseW := trialEvidenceWeight
	if kind == "bench" {
		baseW = benchEvidenceWeight
	}
	a := ensureModelAcc(accs, m)
	a.ev.EvalRuns++
	a.ev.TrialRuns++
	w := baseW * decayWeight(startedAt, now)
	a.ev.DecayedRuns += w
	ok := status == "pass"
	succW := 0.0
	if ok {
		a.ev.EvalPass++
		a.ev.TrialPass++
		a.ev.DecayedSuccesses += w
		succW = w
	}
	addRoleObservation(a, isAuditRoleSignal(agentType, label), ok, w, succW)
	if seconds > 0 {
		a.latSecSum += float64(seconds)
		a.latTimed++
	}
	return nil
}

func (s *Store) accumulateJudgeModelEvidence(accs map[string]*modelEvidenceAcc, since, now time.Time) error {
	q := `SELECT j.ts, j.verdict,
		COALESCE(NULLIF(TRIM(j.worker_model), ''), COALESCE((SELECT s.model FROM sessions s WHERE j.worker_session_id != '' AND s.id = j.worker_session_id), '')),
		COALESCE((SELECT LOWER(s.agent_type) FROM sessions s WHERE j.worker_session_id != '' AND s.id = j.worker_session_id), '')
		FROM judge_verdicts j
		WHERE ` + verdictTSMSExpr + ` >= ? AND ` + verdictTSMSExpr + ` < ?`
	rows, err := s.db.Query(q, since.UnixMilli(), now.UnixMilli())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ts int64
		var verdict, workerModel, agentType string
		if err := rows.Scan(&ts, &verdict, &workerModel, &agentType); err != nil {
			return err
		}
		m := strings.TrimSpace(workerModel)
		v := show.NormJudgeVerdict(verdict)
		if m == "" || strings.EqualFold(m, "default") || (v != "PASS" && v != "FAIL") {
			continue
		}
		a := ensureModelAcc(accs, m)
		a.ev.JudgeRuns++
		w := judgeEvidenceWeight * decayWeight(ts, now)
		a.ev.DecayedRuns += w
		ok := v == "PASS"
		succW := 0.0
		if ok {
			a.ev.JudgePass++
			a.ev.DecayedSuccesses += w
			succW = w
		}
		addRoleObservation(a, isAuditRoleSignal(agentType, ""), ok, w, succW)
	}
	return rows.Err()
}

func (s *Store) accumulateRatingModelEvidence(accs map[string]*modelEvidenceAcc, since, now time.Time) error {
	rTS := `(CASE WHEN r.evaluated_at >= 100000000 AND r.evaluated_at < 100000000000 THEN r.evaluated_at * 1000 ELSE r.evaluated_at END)`
	q := `SELECT r.evaluated_at, r.outcome_score, COALESCE(r.token_efficiency, 0), s.model,
		LOWER(COALESCE(s.agent_type, '')), LOWER(COALESCE(s.label, ''))
		FROM session_ratings r JOIN sessions s ON s.id = r.session_id
		WHERE ` + rTS + ` >= ? AND ` + rTS + ` < ?
		  AND s.model IS NOT NULL AND s.model != ''`
	rows, err := s.db.Query(q, since.UnixMilli(), now.UnixMilli())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var evalAt int64
		var outcomeScore, tokEff int
		var model, agentType, label string
		if err := rows.Scan(&evalAt, &outcomeScore, &tokEff, &model, &agentType, &label); err != nil {
			return err
		}
		m := strings.TrimSpace(model)
		if m == "" || strings.EqualFold(m, "default") {
			continue
		}
		score := clampUnit((float64(outcomeScore) - 1.0) / 4.0)
		w := ratingEvidenceWeight * decayWeight(evalAt, now)
		a := ensureModelAcc(accs, m)
		a.ev.RatingRuns++
		a.ev.RatingScoreSum += score
		a.ev.DecayedRuns += w
		a.ev.DecayedSuccesses += w * score
		if tokEff > 0 {
			a.effSum += float64(tokEff)
			a.effCount++
		}
		addRoleObservation(a, isAuditRoleSignal(agentType, label), outcomeScore >= 4, w, w*score)
	}
	return rows.Err()
}

func (s *Store) accumulateUnclaimedEvalSessions(accs map[string]*modelEvidenceAcc, since, now time.Time) error {
	rows, err := s.db.Query(`SELECT COALESCE(started_at, ended_at, 0), LOWER(outcome), model,
		LOWER(COALESCE(label, '')), LOWER(COALESCE(agent_type, '')) FROM sessions
		WHERE COALESCE(started_at, ended_at, 0) >= ? AND COALESCE(started_at, ended_at, 0) < ?
		  AND (parent_id IS NULL OR parent_id = '')
		  AND (run IS NULL OR run = '')
		  AND substr(COALESCE(label, ''), 1, 5) = 'eval='
		  AND LOWER(COALESCE(outcome, '')) IN ('pass', 'fail')
		  AND model IS NOT NULL AND model != ''`,
		since.UnixMilli(), now.UnixMilli())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ts int64
		var outcome, model, label, agentType string
		if err := rows.Scan(&ts, &outcome, &model, &label, &agentType); err != nil {
			return err
		}
		m := strings.TrimSpace(model)
		if m == "" || strings.EqualFold(m, "default") {
			continue
		}
		a := ensureModelAcc(accs, m)
		a.ev.EvalRuns++
		w := trialEvidenceWeight * decayWeight(ts, now)
		a.ev.DecayedRuns += w
		ok := outcome == "pass"
		succW := 0.0
		if ok {
			a.ev.EvalPass++
			a.ev.DecayedSuccesses += w
			succW = w
		}
		addRoleObservation(a, isAuditRoleSignal(agentType, label), ok, w, succW)
	}
	return rows.Err()
}
