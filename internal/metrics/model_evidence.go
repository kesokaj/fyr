package metrics

import (
	"database/sql"
	"strings"
	"time"
)

// ModelEvidence summarizes empirical worker and eval performance for one model
// in [since, now), consumed by cmd/fyr for model selection and fyr models.
type ModelEvidence struct {
	WorkRuns        int     `json:"work_runs"`
	WorkPass        int     `json:"work_pass"`
	WorkMeanSeconds float64 `json:"work_mean_seconds"`
	EvalRuns        int     `json:"eval_runs"`
	EvalPass        int     `json:"eval_pass"`
}

type modelEvidenceAcc struct {
	ev         ModelEvidence
	workSecSum float64
	workTimed  int
}

// ModelEvidence aggregates graded work_waves and eval trials per model in [since, now).
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
	out := make(map[string]ModelEvidence, len(accs))
	for m, a := range accs {
		if a.workTimed > 0 {
			a.ev.WorkMeanSeconds = round4(a.workSecSum / float64(a.workTimed))
		}
		out[m] = a.ev
	}
	return out, nil
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
	q := `SELECT stamp, worker, state, model, 0, 0, 0 FROM work_waves WHERE ` + where
	if s.hasWorkWaveStageCols() {
		q = `SELECT stamp, worker, state, model, COALESCE(setup_s, 0), COALESCE(agy_s, 0), COALESCE(check_s, 0) FROM work_waves WHERE ` + where
	}
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

func (s *Store) hasWorkWaveStageCols() bool {
	rows, err := s.db.Query(`PRAGMA table_info(work_waves)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err == nil && name == "agy_s" {
			return true
		}
	}
	return false
}

func (s *Store) accumulateEvalModelEvidence(accs map[string]*modelEvidenceAcc, since, now time.Time) error {
	rows, err := s.db.Query(`SELECT DISTINCT t.run, LOWER(t.status), s.model
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
		if err := rows.Scan(&run, &status, &model); err != nil {
			return err
		}
		a := ensureModelAcc(accs, strings.TrimSpace(model))
		a.ev.EvalRuns++
		if status == "pass" {
			a.ev.EvalPass++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return s.accumulateUnclaimedEvalSessions(accs, since, now)
}

func (s *Store) accumulateUnclaimedEvalSessions(accs map[string]*modelEvidenceAcc, since, now time.Time) error {
	rows, err := s.db.Query(`SELECT LOWER(outcome), model FROM sessions
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
		var outcome, model string
		if err := rows.Scan(&outcome, &model); err != nil {
			return err
		}
		a := ensureModelAcc(accs, strings.TrimSpace(model))
		a.ev.EvalRuns++
		if outcome == "pass" {
			a.ev.EvalPass++
		}
	}
	return rows.Err()
}
