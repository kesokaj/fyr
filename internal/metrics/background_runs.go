package metrics

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/metrics/report"
)

// BackgroundRunRow records one shape-only dream or auto background run in
// metrics.db (schema 10+, extended in schema 14 with halt_reason, ADR-528).
type BackgroundRunRow struct {
	ID               int64  `json:"id,omitempty"`
	TS               int64  `json:"ts"`
	Kind             string `json:"kind"`
	Outcome          string `json:"outcome"`
	HaltReason       string `json:"halt_reason,omitempty"`
	DurationMS       int64  `json:"duration_ms"`
	ProjectsChecked  int    `json:"projects_checked"`
	ProposalsWritten int    `json:"proposals_written"`
	RatingsWritten   int    `json:"ratings_written"`
	WorkerRan        int    `json:"worker_ran"`
	Applied          int    `json:"applied"`
}

// BackgroundKindStats aggregates shape-only background_runs rows for one kind.
type BackgroundKindStats = report.BackgroundKindStats

const bgRunTSMSExpr = `(CASE WHEN ts >= 100000000 AND ts < 100000000000 THEN ts * 1000 ELSE ts END)`

// RecordBackgroundRun validates and inserts row into <home>/<StateDir>/metrics.db.
func RecordBackgroundRun(home string, row BackgroundRunRow) error {
	if strings.TrimSpace(home) == "" {
		return errors.New("metrics: background_runs: home required")
	}
	s, err := Open(Path(home))
	if err != nil {
		return err
	}
	defer s.Close()
	return s.InsertBackgroundRun(row)
}

// InsertBackgroundRun validates and inserts row into s.
func (s *Store) InsertBackgroundRun(row BackgroundRunRow) error {
	norm, err := normalizeBackgroundRunRow(row)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO background_runs(ts, kind, outcome, halt_reason, duration_ms, projects_checked, proposals_written, ratings_written, worker_ran, applied) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		norm.TS, norm.Kind, norm.Outcome, norm.HaltReason, norm.DurationMS, norm.ProjectsChecked,
		norm.ProposalsWritten, norm.RatingsWritten, norm.WorkerRan, norm.Applied,
	)
	return err
}

func normalizeBackgroundRunRow(r BackgroundRunRow) (BackgroundRunRow, error) {
	if r.TS < 0 || r.DurationMS < 0 {
		return BackgroundRunRow{}, errors.New("metrics: background_runs: negative ts or duration_ms")
	}
	r.Kind = strings.ToLower(strings.TrimSpace(r.Kind))
	if r.Kind != "dream" && r.Kind != "auto" {
		return BackgroundRunRow{}, fmt.Errorf("metrics: background_runs: invalid kind %q", printable(r.Kind))
	}
	r.Outcome = strings.ToLower(strings.TrimSpace(r.Outcome))
	if !isValidShapeOutcome(r.Outcome) {
		return BackgroundRunRow{}, fmt.Errorf("metrics: background_runs: invalid outcome %q", printable(r.Outcome))
	}
	if r.ProjectsChecked < 0 || r.ProposalsWritten < 0 || r.RatingsWritten < 0 || r.WorkerRan < 0 || r.Applied < 0 {
		return BackgroundRunRow{}, errors.New("metrics: background_runs: negative count")
	}
	r.HaltReason = clipSanitizedText(r.HaltReason, 128)
	return r, nil
}

func isValidShapeOutcome(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// QueryBackgroundRuns reads background_runs rows in [since, until), newest first.
func (s *Store) QueryBackgroundRuns(since, until time.Time) ([]BackgroundRunRow, error) {
	var conds []string
	var args []any
	if !since.IsZero() {
		conds = append(conds, bgRunTSMSExpr+` >= ?`)
		args = append(args, since.UnixMilli())
	}
	if !until.IsZero() {
		conds = append(conds, bgRunTSMSExpr+` < ?`)
		args = append(args, until.UnixMilli())
	}
	haltCol := `''`
	if schemaTableCols(s.db, "background_runs")["halt_reason"] {
		haltCol = `COALESCE(halt_reason, '')`
	}
	q := `SELECT id, ts, kind, outcome, ` + haltCol + `, duration_ms, projects_checked, proposals_written, ratings_written, worker_ran, applied FROM background_runs`
	if len(conds) > 0 {
		q += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	q += ` ORDER BY ` + bgRunTSMSExpr + ` DESC, id DESC`
	return s.scanBackgroundRuns(q, args)
}

func (s *Store) scanBackgroundRuns(q string, args []any) ([]BackgroundRunRow, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackgroundRunRow
	for rows.Next() {
		var r BackgroundRunRow
		if err := rows.Scan(&r.ID, &r.TS, &r.Kind, &r.Outcome, &r.HaltReason, &r.DurationMS, &r.ProjectsChecked,
			&r.ProposalsWritten, &r.RatingsWritten, &r.WorkerRan, &r.Applied); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) populateBackgroundRuns(sum *Summary, o ShowOptions, since, now time.Time) error {
	if o.Label != "" {
		return nil
	}
	rows, err := s.QueryBackgroundRuns(since, now)
	if err != nil {
		return err
	}
	var dreamDur, autoDur int64
	for _, r := range rows {
		switch r.Kind {
		case "dream":
			accumulateKindStats(&sum.Health.DreamRuns, &dreamDur, r)
		case "auto":
			accumulateKindStats(&sum.Health.AutoRuns, &autoDur, r)
		}
	}
	if sum.Health.DreamRuns.Runs > 0 {
		sum.Health.DreamRuns.MeanDurationMS = round4(float64(dreamDur) / float64(sum.Health.DreamRuns.Runs))
	}
	if sum.Health.AutoRuns.Runs > 0 {
		sum.Health.AutoRuns.MeanDurationMS = round4(float64(autoDur) / float64(sum.Health.AutoRuns.Runs))
	}
	return nil
}

func accumulateKindStats(dst *BackgroundKindStats, durSum *int64, r BackgroundRunRow) {
	dst.Runs++
	if isBackgroundPassOutcome(r.Outcome) {
		dst.Passed++
	} else {
		dst.Failed++
	}
	*durSum += r.DurationMS
	dst.ProjectsChecked += r.ProjectsChecked
	dst.ProposalsWritten += r.ProposalsWritten
	dst.RatingsWritten += r.RatingsWritten
	dst.WorkersRan += r.WorkerRan
	dst.Applied += r.Applied
}

func isBackgroundPassOutcome(outcome string) bool {
	switch outcome {
	case "pass", "ok", "unchanged", "skipped", "noop", "no_op":
		return true
	default:
		return false
	}
}
