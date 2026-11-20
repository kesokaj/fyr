package metrics

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

// SessionRating is one stale Antigravity conversation's outcome and token
// efficiency rating stored in session_ratings (metrics.db schema 8+, ADR-524).
type SessionRating struct {
	SessionID       string `json:"session_id"`
	ProjectRoot     string `json:"project_root"`
	RepoID          string `json:"repo_id,omitempty"`
	EvaluatedAt     int64  `json:"evaluated_at"`
	OutcomeScore    int    `json:"outcome_score"`
	TokenEfficiency int    `json:"token_efficiency"`
	PlanAlignment   string `json:"plan_alignment"`
	SkillAction     string `json:"skill_action"`
	TargetSkill     string `json:"target_skill"`
	Summary         string `json:"summary"`
}

func normalizeSessionRating(r SessionRating) (SessionRating, error) {
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" || len(r.SessionID) > 128 || printable(r.SessionID) != r.SessionID {
		return SessionRating{}, fmt.Errorf("metrics: session_rating: invalid session_id")
	}
	if r.EvaluatedAt < 0 {
		return SessionRating{}, fmt.Errorf("metrics: session_rating %s: negative evaluated_at", r.SessionID)
	}
	if r.OutcomeScore < 1 || r.OutcomeScore > 5 || r.TokenEfficiency < 1 || r.TokenEfficiency > 5 {
		return SessionRating{}, fmt.Errorf("metrics: session_rating %s: scores must be in 1..5", r.SessionID)
	}
	r.PlanAlignment = strings.ToLower(strings.TrimSpace(r.PlanAlignment))
	switch r.PlanAlignment {
	case "matched", "partial", "missed", "no-plan":
	default:
		return SessionRating{}, fmt.Errorf("metrics: session_rating %s: plan_alignment %q invalid", r.SessionID, printable(r.PlanAlignment))
	}
	r.SkillAction = strings.ToLower(strings.TrimSpace(r.SkillAction))
	if r.SkillAction == "" {
		r.SkillAction = "none"
	}
	switch r.SkillAction {
	case "none", "rewrite-skill", "new-skill":
	default:
		return SessionRating{}, fmt.Errorf("metrics: session_rating %s: skill_action %q invalid", r.SessionID, printable(r.SkillAction))
	}
	r.ProjectRoot = cleanRatingProject(r.ProjectRoot)
	r.RepoID = normalizeRatingRepoID(r)
	r.TargetSkill = printable(strings.TrimSpace(r.TargetSkill))
	if len(r.TargetSkill) > 200 {
		return SessionRating{}, fmt.Errorf("metrics: session_rating %s: target_skill over 200 bytes", r.SessionID)
	}
	r.Summary = printable(strings.TrimSpace(r.Summary))
	if len(r.Summary) > 400 {
		return SessionRating{}, fmt.Errorf("metrics: session_rating %s: summary over 400 bytes", r.SessionID)
	}
	return r, nil
}

func normalizeRatingRepoID(r SessionRating) string {
	if id := clipPrintable(r.RepoID, 256); id != "" {
		return id
	}
	if r.ProjectRoot == "" || r.ProjectRoot == search.GlobalWorkspace {
		return ""
	}
	return clipPrintable(ResolveRepoID(r.ProjectRoot), 256)
}

func cleanRatingProject(root string) string {
	root = strings.TrimSpace(root)
	if root == "" || root == search.GlobalWorkspace {
		return printable(root)
	}
	return printable(filepath.Clean(root))
}

// UpsertSessionRatings validates and stores rows in session_ratings,
// updating existing ratings for the same session_id.
func (s *Store) UpsertSessionRatings(rows []SessionRating) error {
	if len(rows) == 0 {
		return nil
	}
	norm := make([]SessionRating, len(rows))
	for i, r := range rows {
		n, err := normalizeSessionRating(r)
		if err != nil {
			return err
		}
		norm[i] = n
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range norm {
		if err := upsertOneSessionRating(tx, r); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func upsertOneSessionRating(tx *sql.Tx, r SessionRating) error {
	const q = `INSERT INTO session_ratings(
		session_id, project_root, repo_id, evaluated_at, outcome_score, token_efficiency,
		plan_alignment, skill_action, target_skill, summary)
		VALUES (?, ?, CASE
			WHEN ? != '' THEN ?
			WHEN ? = ? THEN ''
			ELSE COALESCE((SELECT repo_id FROM sessions WHERE id = ?), '')
		END, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET (
			project_root, repo_id, evaluated_at, outcome_score, token_efficiency,
			plan_alignment, skill_action, target_skill, summary
		) = (
			excluded.project_root, excluded.repo_id, excluded.evaluated_at, excluded.outcome_score, excluded.token_efficiency,
			excluded.plan_alignment, excluded.skill_action, excluded.target_skill, excluded.summary
		)
		WHERE session_ratings.project_root = '' OR session_ratings.project_root = excluded.project_root`
	_, err := tx.Exec(q, r.SessionID, r.ProjectRoot, r.RepoID, r.RepoID, r.ProjectRoot, search.GlobalWorkspace, r.SessionID,
		r.EvaluatedAt, r.OutcomeScore, r.TokenEfficiency, r.PlanAlignment, r.SkillAction, r.TargetSkill, r.Summary)
	return err
}

const evalTimeMSExpr = `(CASE WHEN evaluated_at >= 100000000 AND evaluated_at < 100000000000 THEN evaluated_at * 1000 ELSE evaluated_at END)`

// SessionRatings reads stored conversation ratings matching project (empty for
// all, GlobalWorkspace for workspace-less conversations, else root or subpath)
// and optional [since, now) evaluated_at bounds.
func (s *Store) SessionRatings(project string, since, now time.Time) ([]SessionRating, error) {
	where, args := sessionRatingsWhere(project, since, now)
	q := `SELECT session_id, project_root, COALESCE(repo_id, ''), evaluated_at, outcome_score, token_efficiency,
		plan_alignment, skill_action, target_skill, summary
		FROM session_ratings`
	if where != "" {
		q += ` WHERE ` + where
	}
	q += ` ORDER BY ` + evalTimeMSExpr + ` DESC, session_id ASC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionRating
	for rows.Next() {
		var r SessionRating
		if err := rows.Scan(&r.SessionID, &r.ProjectRoot, &r.RepoID, &r.EvaluatedAt, &r.OutcomeScore,
			&r.TokenEfficiency, &r.PlanAlignment, &r.SkillAction, &r.TargetSkill, &r.Summary); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func sessionRatingsWhere(project string, since, now time.Time) (string, []any) {
	var conds []string
	var args []any
	if !since.IsZero() {
		conds = append(conds, evalTimeMSExpr+` >= ?`)
		args = append(args, since.UnixMilli())
	}
	if !now.IsZero() {
		conds = append(conds, evalTimeMSExpr+` < ?`)
		args = append(args, now.UnixMilli())
	}
	if project != "" {
		if project == search.GlobalWorkspace {
			conds = append(conds, `project_root = ?`)
			args = append(args, search.GlobalWorkspace)
		} else {
			p := filepath.Clean(project)
			conds = append(conds, `(project_root = ? OR substr(project_root, 1, length(?)) = ?)`)
			args = append(args, p, p+"/", p+"/")
		}
	}
	return strings.Join(conds, ` AND `), args
}

func (s *Store) populateSessionRatings(sum *Summary, o ShowOptions, since, now time.Time) error {
	if o.Label != "" {
		return nil
	}
	rows, err := s.SessionRatings(o.Project, since, now)
	if err != nil {
		return err
	}
	sum.A.RatingsCount = len(rows)
	if len(rows) == 0 {
		return nil
	}
	var outSum, effSum int
	for _, r := range rows {
		outSum += r.OutcomeScore
		effSum += r.TokenEfficiency
	}
	mo := round4(float64(outSum) / float64(len(rows)))
	me := round4(float64(effSum) / float64(len(rows)))
	sum.A.MeanOutcomeScore = &mo
	sum.A.MeanTokenEfficiency = &me
	return nil
}
