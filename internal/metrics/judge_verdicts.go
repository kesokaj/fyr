package metrics

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// JudgeVerdictRow records one cross-family gate or judge verdict in metrics.db
// (schema 9+, extended in schema 11 with repo_id and worker attribution).
type JudgeVerdictRow struct {
	ID              int64  `json:"id,omitempty"`
	TS              int64  `json:"ts"`
	ProjectRoot     string `json:"project_root"`
	RepoID          string `json:"repo_id,omitempty"`
	Target          string `json:"target"`
	Model           string `json:"model"`
	WorkerModel     string `json:"worker_model,omitempty"`
	WorkerSessionID string `json:"worker_session_id,omitempty"`
	FallbackReason  string `json:"fallback_reason"`
	CriterionHash   string `json:"criterion_hash"`
	Verdict         string `json:"verdict"`
}

const verdictTSMSExpr = `(CASE WHEN ts >= 100000000 AND ts < 100000000000 THEN ts * 1000 ELSE ts END)`

// RecordJudgeVerdict validates and inserts row into <home>/<StateDir>/metrics.db.
func RecordJudgeVerdict(home string, row JudgeVerdictRow) error {
	if strings.TrimSpace(home) == "" {
		return errors.New("metrics: judge_verdicts: home required")
	}
	s, err := Open(Path(home))
	if err != nil {
		return err
	}
	defer s.Close()
	return s.RecordJudgeVerdict(row)
}

// RecordJudgeVerdict validates and inserts row into s.
func (s *Store) RecordJudgeVerdict(row JudgeVerdictRow) error {
	norm, err := normalizeJudgeVerdictRow(row)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO judge_verdicts(ts, project_root, repo_id, target, model, worker_model, worker_session_id, fallback_reason, criterion_hash, verdict) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		norm.TS, norm.ProjectRoot, norm.RepoID, norm.Target, norm.Model, norm.WorkerModel, norm.WorkerSessionID, norm.FallbackReason, norm.CriterionHash, norm.Verdict,
	)
	return err
}

func normalizeJudgeVerdictRow(r JudgeVerdictRow) (JudgeVerdictRow, error) {
	if r.TS < 0 {
		return JudgeVerdictRow{}, errors.New("metrics: judge_verdicts: negative ts")
	}
	if r.TS == 0 {
		r.TS = time.Now().UnixMilli()
	}
	r.Model = clipPrintable(r.Model, 128)
	r.CriterionHash = clipPrintable(r.CriterionHash, 128)
	r.Verdict = clipPrintable(r.Verdict, 64)
	if r.Model == "" || r.CriterionHash == "" || r.Verdict == "" {
		return JudgeVerdictRow{}, errors.New("metrics: judge_verdicts: model, criterion_hash, and verdict are required")
	}
	if pr := strings.TrimSpace(r.ProjectRoot); pr != "" {
		r.ProjectRoot = clipPrintable(filepath.Clean(pr), 256)
	} else {
		r.ProjectRoot = ""
	}
	r.RepoID = clipPrintable(r.RepoID, 256)
	if r.RepoID == "" && r.ProjectRoot != "" {
		r.RepoID = clipPrintable(ResolveRepoID(r.ProjectRoot), 256)
	}
	r.WorkerModel = clipPrintable(r.WorkerModel, 128)
	r.WorkerSessionID = clipPrintable(r.WorkerSessionID, 128)
	r.Target = clipPrintable(r.Target, 256)
	r.FallbackReason = clipPrintable(r.FallbackReason, 400)
	return r, nil
}

func clipPrintable(s string, maxLen int) string {
	return truncateUTF8(printable(strings.TrimSpace(s)), maxLen)
}

func truncateUTF8(s string, maxLen int) string {
	for len(s) > maxLen {
		_, sz := utf8.DecodeLastRuneInString(s)
		if sz <= 0 {
			return s[:maxLen]
		}
		s = s[:len(s)-sz]
	}
	return s
}

// JudgeVerdicts reads stored judge verdicts from s matching optional project
// and [since, now) bounds, newest first.
func (s *Store) JudgeVerdicts(project string, since, now time.Time) ([]JudgeVerdictRow, error) {
	where, args := judgeVerdictsWhere(project, since, now)
	q := `SELECT id, ts, project_root, COALESCE(repo_id, ''), target, model, COALESCE(worker_model, ''), COALESCE(worker_session_id, ''), fallback_reason, criterion_hash, verdict FROM judge_verdicts`
	if where != "" {
		q += ` WHERE ` + where
	}
	q += ` ORDER BY ` + verdictTSMSExpr + ` DESC, id DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JudgeVerdictRow
	for rows.Next() {
		var r JudgeVerdictRow
		if err := rows.Scan(&r.ID, &r.TS, &r.ProjectRoot, &r.RepoID, &r.Target, &r.Model, &r.WorkerModel, &r.WorkerSessionID, &r.FallbackReason, &r.CriterionHash, &r.Verdict); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InferWorkerAttribution best-effort resolves (workerModel, workerSessionID) from
// <home>/<StateDir>/metrics.db for a gate/land target when not explicitly set.
func InferWorkerAttribution(home, projectRoot, target string) (string, string) {
	if strings.TrimSpace(home) == "" || strings.TrimSpace(target) == "" {
		return "", ""
	}
	s, err := Open(Path(home))
	if err != nil {
		return "", ""
	}
	defer s.Close()
	return s.inferWorkerAttribution(projectRoot, target)
}

func (s *Store) inferWorkerAttribution(projectRoot, target string) (string, string) {
	tgt := strings.TrimSpace(target)
	cleanTgt := filepath.Clean(tgt)
	var sid, mod string
	if err := s.db.QueryRow(
		`SELECT id, COALESCE(model, '') FROM sessions WHERE project_root = ? AND COALESCE(model, '') != '' ORDER BY COALESCE(ended_at, started_at, 0) DESC LIMIT 1`,
		cleanTgt,
	).Scan(&sid, &mod); err == nil && mod != "" {
		return mod, sid
	}
	workerCand := strings.TrimPrefix(tgt, "work/")
	if _, base, ok := strings.Cut(tgt, "/"); ok && !strings.HasPrefix(tgt, "/") {
		workerCand = base
	}
	proj := ""
	if strings.TrimSpace(projectRoot) != "" {
		proj = filepath.Clean(projectRoot)
	}
	_ = s.db.QueryRow(
		`SELECT model FROM work_waves WHERE (worker = ? OR worker = ? OR stamp || '/' || worker = ?) AND (? = '' OR project_root = '' OR project_root = ?) AND COALESCE(model, '') != '' ORDER BY updated_at DESC LIMIT 1`,
		tgt, workerCand, tgt, proj, proj,
	).Scan(&mod)
	return mod, sid
}

func judgeVerdictsWhere(project string, since, now time.Time) (string, []any) {
	var conds []string
	var args []any
	if !since.IsZero() {
		conds = append(conds, verdictTSMSExpr+` >= ?`)
		args = append(args, since.UnixMilli())
	}
	if !now.IsZero() {
		conds = append(conds, verdictTSMSExpr+` < ?`)
		args = append(args, now.UnixMilli())
	}
	if strings.TrimSpace(project) != "" {
		p := filepath.Clean(project)
		conds = append(conds, `(project_root = ? OR substr(project_root, 1, length(?)) = ?)`)
		args = append(args, p, p+"/", p+"/")
	}
	return strings.Join(conds, ` AND `), args
}
