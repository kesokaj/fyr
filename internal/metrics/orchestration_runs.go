package metrics

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/search"
)

const (
	defaultOrchestrationRunLimit = 500
	maxOrchestrationRunLimit     = 5000
)

// OrchestrationRunRow records one shape-only relay, swarm, or workflow
// orchestration run in metrics.db (schema 14).
type OrchestrationRunRow struct {
	ID           string `json:"id"`
	TS           string `json:"ts"`
	Kind         string `json:"kind"`
	ProjectRoot  string `json:"project_root,omitempty"`
	RepoID       string `json:"repo_id,omitempty"`
	Status       string `json:"status"`
	HaltCode     string `json:"halt_code,omitempty"`
	Units        int    `json:"units"`
	DefectRounds int    `json:"defect_rounds"`
	Passed       int    `json:"passed"`
	Failed       int    `json:"failed"`
	DurationMS   int64  `json:"duration_ms"`
	WorkerModel  string `json:"worker_model,omitempty"`
	JudgeModel   string `json:"judge_model,omitempty"`
	Verdict      string `json:"verdict,omitempty"`
}

// RecordOrchestrationRun validates and upserts row into <home>/<StateDir>/metrics.db.
func RecordOrchestrationRun(home string, row OrchestrationRunRow) error {
	if strings.TrimSpace(home) == "" {
		return errors.New("metrics: orchestration_runs: home required")
	}
	s, err := Open(Path(home))
	if err != nil {
		return err
	}
	defer s.Close()
	return s.InsertOrchestrationRun(row)
}

// InsertOrchestrationRun validates and upserts row by id into s.
func (s *Store) InsertOrchestrationRun(row OrchestrationRunRow) error {
	if s == nil || s.db == nil {
		return errors.New("metrics: orchestration_runs: nil store")
	}
	norm, err := normalizeOrchestrationRunRow(row)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO orchestration_runs(
		id, ts, kind, project_root, repo_id, status, halt_code,
		units, defect_rounds, passed, failed, duration_ms, worker_model, judge_model, verdict)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		norm.ID, norm.TS, norm.Kind, norm.ProjectRoot, norm.RepoID, norm.Status, norm.HaltCode,
		norm.Units, norm.DefectRounds, norm.Passed, norm.Failed, norm.DurationMS,
		norm.WorkerModel, norm.JudgeModel, norm.Verdict,
	)
	return err
}

func normalizeOrchestrationRunRow(r OrchestrationRunRow) (OrchestrationRunRow, error) {
	if err := validateOrchestrationIDAndMeta(&r); err != nil {
		return OrchestrationRunRow{}, err
	}
	if r.Units < 0 || r.DefectRounds < 0 || r.Passed < 0 || r.Failed < 0 || r.DurationMS < 0 {
		return OrchestrationRunRow{}, errors.New("metrics: orchestration_runs: negative count or duration_ms")
	}
	if pr := strings.TrimSpace(r.ProjectRoot); pr != "" {
		if pr == search.GlobalWorkspace {
			r.ProjectRoot = pr
		} else {
			r.ProjectRoot = clipPrintable(filepath.Clean(pr), 256)
		}
	} else {
		r.ProjectRoot = ""
	}
	r.RepoID = clipSanitizedText(r.RepoID, 256)
	if r.RepoID == "" && r.ProjectRoot != "" && r.ProjectRoot != search.GlobalWorkspace {
		r.RepoID = clipSanitizedText(ResolveRepoID(r.ProjectRoot), 256)
	}
	r.WorkerModel = clipSanitizedText(r.WorkerModel, 128)
	r.JudgeModel = clipSanitizedText(r.JudgeModel, 128)
	r.Verdict = clipSanitizedText(r.Verdict, 64)
	return r, nil
}

func validateOrchestrationIDAndMeta(r *OrchestrationRunRow) error {
	r.ID = strings.TrimSpace(r.ID)
	if r.ID == "" || len(r.ID) > 128 || printable(r.ID) != r.ID ||
		strings.IndexFunc(r.ID, unicode.IsSpace) >= 0 || redact.Redact(r.ID) != r.ID {
		return errors.New("metrics: orchestration_runs: invalid id")
	}
	ts, err := normalizeOrchestrationTS(r.TS)
	if err != nil {
		return err
	}
	r.TS = ts
	r.Kind = strings.ToLower(strings.TrimSpace(r.Kind))
	if !isValidShapeOutcome(r.Kind) {
		return fmt.Errorf("metrics: orchestration_runs: invalid kind %q", printable(r.Kind))
	}
	r.Status = strings.TrimSpace(r.Status)
	if !isValidShapeCode(r.Status, 32, false) {
		return fmt.Errorf("metrics: orchestration_runs: invalid status %q", printable(r.Status))
	}
	r.HaltCode = strings.TrimSpace(r.HaltCode)
	if r.HaltCode != "" && (!isValidShapeCode(r.HaltCode, 64, true) || redact.Redact(r.HaltCode) != r.HaltCode) {
		return fmt.Errorf("metrics: orchestration_runs: invalid halt_code %q", printable(r.HaltCode))
	}
	return nil
}

func normalizeOrchestrationTS(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Now().UTC().Format(time.RFC3339), nil
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339), nil
		}
	}
	return "", fmt.Errorf("metrics: orchestration_runs: invalid ts %q", printable(s))
}

func isValidShapeCode(s string, maxLen int, allowColonDot bool) bool {
	if s == "" || len(s) > maxLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		isPunct := c == '_' || c == '-' || (allowColonDot && (c == ':' || c == '.'))
		if !isAlphaNum && !isPunct {
			return false
		}
	}
	return true
}

// QueryOrchestrationRuns reads orchestration_runs matching optional kind and
// [since, until), newest first, bounded by limit (default 500, max 5000).
func (s *Store) QueryOrchestrationRuns(kind string, since, until time.Time, limit int) ([]OrchestrationRunRow, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("metrics: orchestration_runs: nil store")
	}
	if limit <= 0 {
		limit = defaultOrchestrationRunLimit
	} else if limit > maxOrchestrationRunLimit {
		limit = maxOrchestrationRunLimit
	}
	var conds []string
	var args []any
	if k := strings.ToLower(strings.TrimSpace(kind)); k != "" {
		conds = append(conds, `kind = ?`)
		args = append(args, k)
	}
	if !since.IsZero() {
		conds = append(conds, `ts >= ?`)
		args = append(args, since.UTC().Format(time.RFC3339))
	}
	if !until.IsZero() {
		conds = append(conds, `ts < ?`)
		args = append(args, until.UTC().Format(time.RFC3339))
	}
	q := `SELECT id, ts, kind, COALESCE(project_root, ''), COALESCE(repo_id, ''), status, COALESCE(halt_code, ''), units, defect_rounds, passed, failed, duration_ms, COALESCE(worker_model, ''), COALESCE(judge_model, ''), COALESCE(verdict, '') FROM orchestration_runs`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	q += ` ORDER BY ts DESC, id DESC LIMIT ?`
	args = append(args, limit)
	return scanOrchestrationRuns(s.db, q, args)
}

func scanOrchestrationRuns(db *sql.DB, q string, args []any) ([]OrchestrationRunRow, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrchestrationRunRow
	for rows.Next() {
		var r OrchestrationRunRow
		if err := rows.Scan(
			&r.ID, &r.TS, &r.Kind, &r.ProjectRoot, &r.RepoID, &r.Status, &r.HaltCode,
			&r.Units, &r.DefectRounds, &r.Passed, &r.Failed, &r.DurationMS,
			&r.WorkerModel, &r.JudgeModel, &r.Verdict,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func exportSanitizedOrchestrationRuns(db *sql.DB, repoID string, since time.Time) ([]OrchestrationRunRow, error) {
	if len(schemaTableCols(db, "orchestration_runs")) == 0 {
		return nil, nil
	}
	var conds []string
	var args []any
	if repoID != "" {
		conds = append(conds, `repo_id = ?`)
		args = append(args, repoID)
	}
	if !since.IsZero() {
		conds = append(conds, `ts >= ?`)
		args = append(args, since.UTC().Format(time.RFC3339))
	}
	q := `SELECT id, ts, kind, '', COALESCE(repo_id, ''), status, COALESCE(halt_code, ''), units, defect_rounds, passed, failed, duration_ms, COALESCE(worker_model, ''), COALESCE(judge_model, ''), COALESCE(verdict, '') FROM orchestration_runs`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	q += ` ORDER BY ts, id`
	raw, err := scanOrchestrationRuns(db, q, args)
	if err != nil {
		return nil, err
	}
	out := make([]OrchestrationRunRow, 0, len(raw))
	for _, r := range raw {
		r.ProjectRoot = ""
		r.ID = clipSanitizedText(r.ID, 128)
		ts, err := normalizeOrchestrationTS(r.TS)
		if r.ID == "" || err != nil {
			continue
		}
		r.TS = ts
		r.Kind = clipSanitizedText(r.Kind, 32)
		r.RepoID = clipSanitizedText(r.RepoID, 256)
		r.Status = clipSanitizedText(r.Status, 32)
		r.HaltCode = clipSanitizedText(r.HaltCode, 64)
		r.WorkerModel = clipSanitizedText(r.WorkerModel, 128)
		r.JudgeModel = clipSanitizedText(r.JudgeModel, 128)
		r.Verdict = clipSanitizedText(r.Verdict, 64)
		r.Units, r.DefectRounds = max(0, r.Units), max(0, r.DefectRounds)
		r.Passed, r.Failed, r.DurationMS = max(0, r.Passed), max(0, r.Failed), max(0, r.DurationMS)
		out = append(out, r)
	}
	return out, nil
}

func importOneOrchestrationRunTx(tx *sql.Tx, r OrchestrationRunRow, c *StateImportCounts) error {
	r.ProjectRoot = ""
	norm, err := normalizeOrchestrationRunRow(r)
	if err != nil {
		c.Skipped++
		return nil
	}
	var exists int
	if tx.QueryRow(`SELECT 1 FROM orchestration_runs WHERE id = ? LIMIT 1`, norm.ID).Scan(&exists) == nil {
		c.Skipped++
		return nil
	}
	if _, err := tx.Exec(`INSERT INTO orchestration_runs(
		id, ts, kind, project_root, repo_id, status, halt_code,
		units, defect_rounds, passed, failed, duration_ms, worker_model, judge_model, verdict)
		VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		norm.ID, norm.TS, norm.Kind, norm.RepoID, norm.Status, norm.HaltCode,
		norm.Units, norm.DefectRounds, norm.Passed, norm.Failed, norm.DurationMS,
		norm.WorkerModel, norm.JudgeModel, norm.Verdict,
	); err != nil {
		return err
	}
	c.OrchestrationRuns++
	c.Inserted++
	return nil
}
