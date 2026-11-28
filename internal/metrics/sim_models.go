package metrics

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// ModelFieldStatus summarizes per-model post-apply recovery status over agy sessions.
type ModelFieldStatus struct {
	Model    string `json:"model"`
	Status   string `json:"status"`
	Sessions int    `json:"sessions"`
}

// DominantModelSince returns the dominant non-empty agy main-session model by
// session count from since onward, or "" if no qualifying sessions exist.
func DominantModelSince(s *Store, since time.Time) (string, error) {
	if s == nil || s.db == nil {
		return "", errors.New("metrics: nil store")
	}
	var sinceMS int64
	if !since.IsZero() {
		sinceMS = since.UnixMilli()
	}
	var model string
	var cnt int
	err := s.db.QueryRow(`SELECT TRIM(model), COUNT(*) AS cnt
		FROM sessions
		WHERE vendor = 'agy'
			AND COALESCE(label, '') = ''
			AND COALESCE(parent_id, '') = ''
			AND TRIM(COALESCE(model, '')) != ''
			AND COALESCE(started_at, ended_at, 0) >= ?
		GROUP BY TRIM(model)
		ORDER BY cnt DESC, TRIM(model) ASC
		LIMIT 1`, sinceMS).Scan(&model, &cnt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return clipPrintable(model, trialText), nil
}

// RecentModelsSince returns the sorted distinct non-empty agy main-session
// models with session timestamp >= sinceMS.
func RecentModelsSince(s *Store, sinceMS int64) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("metrics: nil store")
	}
	if sinceMS < 0 {
		sinceMS = 0
	}
	rows, err := s.db.Query(`SELECT DISTINCT TRIM(model)
		FROM sessions
		WHERE vendor = 'agy'
			AND COALESCE(label, '') = ''
			AND COALESCE(parent_id, '') = ''
			AND TRIM(COALESCE(model, '')) != ''
			AND COALESCE(started_at, ended_at, 0) >= ?
		ORDER BY TRIM(model) ASC`, sinceMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		if c := clipPrintable(m, trialText); c != "" {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

// StratifiedFieldRecovery groups post-apply agy main sessions (timestamp in
// [sinceMS, now]) by non-empty model and evaluates per-model recovery status.
func StratifiedFieldRecovery(s *Store, metric string, baseline *float64, sinceMS int64, now time.Time) ([]ModelFieldStatus, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("metrics: nil store")
	}
	if now.IsZero() {
		now = time.Now()
	}
	nowUTC := now.UTC()
	if sinceMS < 0 {
		sinceMS = 0
	}
	nowMS := nowUTC.UnixMilli()
	if sinceMS > nowMS {
		return nil, nil
	}
	all, err := postApplyAgySessions(s, sinceMS, nowMS)
	if err != nil || len(all) == 0 {
		return nil, err
	}
	data, err := s.loadShowData(all)
	if err != nil {
		return nil, err
	}
	byModel, models := groupSessionsByModel(all)
	since := time.UnixMilli(sinceMS).UTC()
	out := make([]ModelFieldStatus, 0, len(models))
	for _, m := range models {
		grp := byModel[m]
		st := StatusPendingData
		if len(grp) >= minRecoverySessions {
			sum, serr := summarizeModelGroup(s, grp, data, since, nowUTC)
			if serr != nil {
				return nil, serr
			}
			st = evaluateModelGroupStatus(sum, grp, metric, baseline)
		}
		out = append(out, ModelFieldStatus{Model: m, Status: st, Sessions: len(grp)})
	}
	return out, nil
}

func postApplyAgySessions(s *Store, sinceMS, nowMS int64) ([]sess, error) {
	return s.sessionsIn(`SELECT id, vendor, COALESCE(project_root,''), COALESCE(parent_id,''), COALESCE(agent_type,''),
		TRIM(model), COALESCE(label,''), COALESCE(outcome,''), COALESCE(run,''),
		COALESCE(started_at,0), COALESCE(ended_at,0), cost_usd
		FROM sessions
		WHERE vendor = 'agy'
			AND COALESCE(label, '') = ''
			AND COALESCE(parent_id, '') = ''
			AND TRIM(COALESCE(model, '')) != ''
			AND COALESCE(started_at, ended_at, 0) >= ?
			AND COALESCE(started_at, ended_at, 0) <= ?
		ORDER BY started_at, id`, []any{sinceMS, nowMS})
}

func groupSessionsByModel(all []sess) (map[string][]sess, []string) {
	byModel := make(map[string][]sess)
	for _, x := range all {
		m := clipPrintable(x.Model, trialText)
		if m == "" {
			continue
		}
		byModel[m] = append(byModel[m], x)
	}
	models := make([]string, 0, len(byModel))
	for m := range byModel {
		models = append(models, m)
	}
	sort.Strings(models)
	return byModel, models
}

func summarizeModelGroup(s *Store, grp []sess, data *showData, since, now time.Time) (*Summary, error) {
	sum := newSummary(since, now)
	sum.B.ContextTarget = DefaultContextTarget
	for _, x := range grp {
		sum.Add(x, data.Steps[x.ID])
		sum.AddSessionEvents(x, data.Events[x.ID])
	}
	sum.Finish(grp, nil, nil, showEst())
	populateAdherenceVerbs(sum, grp, data.Steps)
	collectStepHealth(&sum.Health, sum, grp, data.Steps, "")
	ids := distinctSessionIDs(grp)
	if len(ids) > 0 {
		if err := populateGroupTurnVets(s, sum, ids); err != nil {
			return nil, err
		}
		if err := populateGroupRatings(s, sum, ids); err != nil {
			return nil, err
		}
	}
	return sum, nil
}

func populateGroupTurnVets(s *Store, sum *Summary, ids []string) error {
	var turns, timedOut, scoreSum, ledgerSaved int
	for off := 0; off < len(ids); off += showBatchChunk {
		ph, args := inPlaceholders(ids[off:min(off+showBatchChunk, len(ids))])
		var t, to, sc, ls sql.NullInt64
		err := s.db.QueryRow(
			`SELECT COUNT(*), SUM(CASE WHEN timed_out != 0 THEN 1 ELSE 0 END), SUM(score), SUM(ledger_saved) FROM turn_vets WHERE conversation_id IN (`+ph+`)`,
			args...,
		).Scan(&t, &to, &sc, &ls)
		if err != nil {
			return err
		}
		turns += int(t.Int64)
		timedOut += int(to.Int64)
		scoreSum += int(sc.Int64)
		ledgerSaved += int(ls.Int64)
	}
	sum.E.TurnVets, sum.E.TimedOutTurns, sum.E.LedgerSaved = turns, timedOut, ledgerSaved
	if turns > 0 {
		tr := round4(float64(timedOut) * 100.0 / float64(turns))
		ms := round4(float64(scoreSum) / float64(turns))
		sum.E.TimeoutRatePct, sum.E.MeanVetScore = &tr, &ms
	}
	return nil
}

func populateGroupRatings(s *Store, sum *Summary, ids []string) error {
	var count, outSum, effSum int
	for off := 0; off < len(ids); off += showBatchChunk {
		ph, args := inPlaceholders(ids[off:min(off+showBatchChunk, len(ids))])
		var n, osum, esum sql.NullInt64
		err := s.db.QueryRow(
			`SELECT COUNT(*), SUM(outcome_score), SUM(token_efficiency) FROM session_ratings WHERE session_id IN (`+ph+`)`,
			args...,
		).Scan(&n, &osum, &esum)
		if err != nil {
			return err
		}
		count += int(n.Int64)
		outSum += int(osum.Int64)
		effSum += int(esum.Int64)
	}
	sum.A.RatingsCount = count
	if count > 0 {
		mo := round4(float64(outSum) / float64(count))
		me := round4(float64(effSum) / float64(count))
		sum.A.MeanOutcomeScore, sum.A.MeanTokenEfficiency = &mo, &me
	}
	return nil
}

func evaluateModelGroupStatus(sum *Summary, grp []sess, metric string, baseline *float64) string {
	m := strings.TrimSpace(metric)
	if m != "" && baseline != nil {
		if curVal, ok := lookupSummaryMetric(sum, m); ok {
			return pickSev(isMetricBetter(m, curVal, *baseline), StatusRecovered, StatusStillRegressing)
		}
		if m == "C.search_grep_ratio.agy" && sum.C.GrepCalls["agy"] == 0 && sum.C.SQLiteVerbs["agy"] > 0 {
			return StatusRecovered
		}
		return StatusPendingData
	}
	return evaluateFallbackGroupStatus(sum, grp)
}

func evaluateFallbackGroupStatus(sum *Summary, grp []sess) string {
	if sum.A.MeanOutcomeScore != nil {
		return pickSev(*sum.A.MeanOutcomeScore >= 3.5, StatusRecovered, StatusStillRegressing)
	}
	pass, fail := 0, 0
	for _, x := range grp {
		switch strings.ToLower(strings.TrimSpace(x.Outcome)) {
		case "pass":
			pass++
		case "fail", "error":
			fail++
		}
	}
	if pass+fail > 0 {
		return pickSev(pass > fail, StatusRecovered, StatusStillRegressing)
	}
	if er := sum.D.ToolErrorRate["agy"]; er != nil {
		return pickSev(*er < 0.08, StatusRecovered, StatusStillRegressing)
	}
	return StatusPendingData
}

// LatestTrialModel opens a read-only copy of evalHome's metrics.db and returns
// the newest policy or session model for eval=<row> started at or after sinceMS.
func LatestTrialModel(evalHome, row string, sinceMS int64) string {
	evalHome = strings.TrimSpace(evalHome)
	r := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(row), evalPrefix))
	if evalHome == "" || r == "" {
		return ""
	}
	db, cleanup, ok := openCopySQLiteFile(evalHome, evalHome, harness.StateDir, "metrics.db")
	if !ok {
		return ""
	}
	defer cleanup()
	pmExpr := `NULL`
	if hasTrialPolicyModelCol(db) {
		pmExpr = `NULLIF(TRIM(t.policy_model), '')`
	}
	q := `SELECT COALESCE(` + pmExpr + `,
		(SELECT TRIM(s.model) FROM sessions s
		 WHERE s.run = t.run AND TRIM(COALESCE(s.model, '')) != ''
		 ORDER BY CASE WHEN COALESCE(s.parent_id, '') = '' THEN 0 ELSE 1 END, COALESCE(s.started_at, 0) DESC
		 LIMIT 1), '')
		FROM trials t
		WHERE t.label = ? AND t.started_at >= ?
		ORDER BY t.started_at DESC, t.run DESC
		LIMIT 1`
	var model string
	if err := db.QueryRow(q, evalPrefix+r, sinceMS).Scan(&model); err != nil {
		return ""
	}
	return clipPrintable(model, trialText)
}

func hasTrialPolicyModelCol(db *sql.DB) bool {
	rows, err := db.Query(`PRAGMA table_info(trials)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk) == nil && name == "policy_model" {
			return true
		}
	}
	return false
}

// LookupObservedMetric reads metric from the latest 7-day Summary via lookupSummaryMetric.
func LookupObservedMetric(s *Store, home, metric string, now time.Time) (float64, bool) {
	metric = strings.TrimSpace(metric)
	if s == nil || s.db == nil || metric == "" {
		return 0, false
	}
	if now.IsZero() {
		now = time.Now()
	}
	nowUTC := now.UTC()
	sum, err := s.Show(ShowOptions{
		Home:  home,
		Since: nowUTC.Add(-7 * 24 * time.Hour),
		Now:   nowUTC,
	})
	if err != nil || sum == nil {
		return 0, false
	}
	return lookupSummaryMetric(sum, metric)
}
