package metrics

import (
	"database/sql"
	"math"
	"strings"
	"time"
)

// ModelEvidence summarizes empirical worker, trial, judge, and rating performance
// for one model in [since, now), consumed by cmd/fyr for model selection.
type ModelEvidence struct {
	WorkRuns         int     `json:"work_runs"`
	WorkPass         int     `json:"work_pass"`
	WorkMeanSeconds  float64 `json:"work_mean_seconds"`
	EvalRuns         int     `json:"eval_runs"`
	EvalPass         int     `json:"eval_pass"`
	TrialRuns        int     `json:"trial_runs"`
	TrialPass        int     `json:"trial_pass"`
	JudgeRuns        int     `json:"judge_runs"`
	JudgePass        int     `json:"judge_pass"`
	RatingRuns       int     `json:"rating_runs"`
	RatingScoreSum   float64 `json:"rating_score_sum"`
	DecayedRuns      float64 `json:"decayed_runs"`
	DecayedSuccesses float64 `json:"decayed_successes"`
}

type modelEvidenceAcc struct {
	ev         ModelEvidence
	workSecSum float64
	workTimed  int
}

const (
	evidenceHalfLifeDays = 14.0
	msPerDay             = 86_400_000.0
	trialEvidenceWeight  = 1.0
	judgeEvidenceWeight  = 0.9
	ratingEvidenceWeight = 0.6
)

// ModelEvidence aggregates legacy work_waves/evals and 14-day exponentially
// decayed trials, worker-attributed judge_verdicts, and session_ratings in [since, now).
func (s *Store) ModelEvidence(since, now time.Time) (map[string]ModelEvidence, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if since.IsZero() {
		since = now.AddDate(0, 0, -30)
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
	}
	return nil
}

func (s *Store) accumulateEvalModelEvidence(accs map[string]*modelEvidenceAcc, since, now time.Time) error {
	rows, err := s.db.Query(`SELECT DISTINCT t.run, LOWER(t.status), s.model, COALESCE(t.started_at, 0)
		FROM trials t JOIN sessions s ON s.run = t.run
		WHERE t.started_at >= ? AND t.started_at < ?
		  AND LOWER(t.status) IN ('pass', 'fail')
		  AND (s.parent_id IS NULL OR s.parent_id = '')
		  AND s.model IS NOT NULL AND s.model != ''`,
		since.UnixMilli(), now.UnixMilli())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var run, status, model string
		var startedAt int64
		if err := rows.Scan(&run, &status, &model, &startedAt); err != nil {
			return err
		}
		m := strings.TrimSpace(model)
		if m == "" || strings.EqualFold(m, "default") {
			continue
		}
		a := ensureModelAcc(accs, m)
		a.ev.EvalRuns++
		a.ev.TrialRuns++
		w := trialEvidenceWeight * decayWeight(startedAt, now)
		a.ev.DecayedRuns += w
		if status == "pass" {
			a.ev.EvalPass++
			a.ev.TrialPass++
			a.ev.DecayedSuccesses += w
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return s.accumulateUnclaimedEvalSessions(accs, since, now)
}

func (s *Store) accumulateJudgeModelEvidence(accs map[string]*modelEvidenceAcc, since, now time.Time) error {
	q := `SELECT j.ts, j.verdict,
		COALESCE(NULLIF(TRIM(j.worker_model), ''), COALESCE((SELECT s.model FROM sessions s WHERE j.worker_session_id != '' AND s.id = j.worker_session_id), ''))
		FROM judge_verdicts j
		WHERE ` + verdictTSMSExpr + ` >= ? AND ` + verdictTSMSExpr + ` < ?`
	rows, err := s.db.Query(q, since.UnixMilli(), now.UnixMilli())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ts int64
		var verdict, workerModel string
		if err := rows.Scan(&ts, &verdict, &workerModel); err != nil {
			return err
		}
		m := strings.TrimSpace(workerModel)
		v := normJudgeVerdict(verdict)
		if m == "" || strings.EqualFold(m, "default") || (v != "PASS" && v != "FAIL") {
			continue
		}
		a := ensureModelAcc(accs, m)
		a.ev.JudgeRuns++
		w := judgeEvidenceWeight * decayWeight(ts, now)
		a.ev.DecayedRuns += w
		if v == "PASS" {
			a.ev.JudgePass++
			a.ev.DecayedSuccesses += w
		}
	}
	return rows.Err()
}

func (s *Store) accumulateRatingModelEvidence(accs map[string]*modelEvidenceAcc, since, now time.Time) error {
	rTS := `(CASE WHEN r.evaluated_at >= 100000000 AND r.evaluated_at < 100000000000 THEN r.evaluated_at * 1000 ELSE r.evaluated_at END)`
	q := `SELECT r.evaluated_at, r.outcome_score, s.model
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
		var outcomeScore int
		var model string
		if err := rows.Scan(&evalAt, &outcomeScore, &model); err != nil {
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
	}
	return rows.Err()
}

func (s *Store) accumulateUnclaimedEvalSessions(accs map[string]*modelEvidenceAcc, since, now time.Time) error {
	rows, err := s.db.Query(`SELECT COALESCE(started_at, ended_at, 0), LOWER(outcome), model FROM sessions
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
		var outcome, model string
		if err := rows.Scan(&ts, &outcome, &model); err != nil {
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
		if outcome == "pass" {
			a.ev.EvalPass++
			a.ev.DecayedSuccesses += w
		}
	}
	return rows.Err()
}
