package metrics

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Trial is one run of a suite row (SPEC-fyr-eval), the unit every rate
// reads: fyr eval writes it after the trial's verdict, whether or not a
// session ran (the eval-metrics plan D13). Its sessions, when any ran, are
// ingested with Options.Run set to the same key.
type Trial struct {
	Run     string // '<stamp>/<row>/<i>', the trial's sandbox under the eval root; the key
	Label   string // 'eval=<row>'
	Harness string // gitShort(install source) plus ' (dirty)' when dirty, or 'embedded <version>'
	CLI     string // the first line of `agy --version`; "" when unknown
	Status  string // "pass", "fail" or "error"
	Cause   string // the closed set of SPEC-fyr-eval, one identifier; "" for pass
	// StartedAt and Seconds are the whole trial; SessionSeconds the agy
	// session(s), nil when none ran.
	StartedAt      time.Time
	Seconds        int64
	SessionSeconds *int64
	// The suite's per-row fields, written by fyr eval from ADR-085 on with
	// the suite's defaults applied; zero means NULL (a trial stored before
	// then, or not known at the run).
	RowVersion            int
	Kind, Cluster, Expect string
}

// trialText is the longest harness, cli or run value stored: each is a
// short hash, a version line or a sandbox key, never prose.
const trialText = 120

// validate is what a trial must look like to be stored: the closed status
// set, a cause only on a fail or an error and identifier-shaped, and every
// text column printable and short, so that no prose or escape byte reaches
// the store (SPEC-fyr-metrics § Invariants).
func (t Trial) validate() error {
	switch t.Status {
	case "pass":
		if t.Cause != "" {
			return errors.New("a pass carries no cause")
		}
	case "fail", "error":
		if t.Cause == "" {
			return fmt.Errorf("a %s needs a cause", t.Status)
		}
	default:
		return fmt.Errorf("status %q: want pass, fail or error", printable(t.Status))
	}
	if t.Cause != "" && identifier(t.Cause) == "" {
		return fmt.Errorf("cause %q: not an identifier", printable(t.Cause))
	}
	if t.Run == "" || t.Harness == "" {
		return errors.New("run and harness are required")
	}
	if k, v, ok := strings.Cut(t.Label, "="); !ok || k == "" || v == "" {
		return fmt.Errorf("label %q: want KEY=VALUE", printable(t.Label))
	}
	for name, v := range map[string]string{"run": t.Run, "label": t.Label, "harness": t.Harness, "cli": t.CLI, "kind": t.Kind, "cluster": t.Cluster, "expect": t.Expect} {
		if printable(v) != v || len(v) > trialText {
			return fmt.Errorf("%s: not printable or over %d bytes", name, trialText)
		}
	}
	if t.RowVersion < 0 || t.Seconds < 0 || (t.SessionSeconds != nil && *t.SessionSeconds < 0) {
		return errors.New("a count below 0")
	}
	// Go's zero time is before 1970: stored, it would sit outside every
	// window and the trial would vanish from every rate (BUG-181's shape).
	if t.StartedAt.IsZero() {
		return errors.New("no start time")
	}
	return nil
}

// RecordTrial stores t once: a run already stored is left as it is, so
// re-recording adds no row (INSERT OR IGNORE, the eval-metrics plan D17).
func (s *Store) RecordTrial(t Trial) error {
	if err := t.validate(); err != nil {
		return fmt.Errorf("metrics: trial: %w", err)
	}
	var sessionSeconds any
	if t.SessionSeconds != nil {
		sessionSeconds = *t.SessionSeconds
	}
	_, err := s.db.Exec(`INSERT OR IGNORE INTO trials(run, label, row_version, kind, cluster, expect, harness, cli,
		status, cause, started_at, seconds, session_seconds)
		VALUES (?, ?, NULLIF(?, 0), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), ?, ?, ?, ?, ?)`,
		t.Run, t.Label, t.RowVersion, t.Kind, t.Cluster, t.Expect, t.Harness, t.CLI,
		t.Status, t.Cause, t.StartedAt.UnixMilli(), t.Seconds, sessionSeconds)
	if err != nil {
		return fmt.Errorf("metrics: trial: %w", err)
	}
	return nil
}

// trial is a trials row as show reads it.
type trial struct{ label, run, status string }

// trialRuns is the run of every trial in the store, whatever its window: a
// session whose run is among them is counted by its trial, in the window
// the trial started in, never on its own; a session whose run names no
// trial (the trial's write failed) counts as one graded trial of its own,
// as a NULL-run session does (the review of 2026-10-31, finding 1).
func (s *Store) trialRuns() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT run FROM trials`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	claimed := map[string]bool{}
	for rows.Next() {
		var run string
		if err := rows.Scan(&run); err != nil {
			return nil, err
		}
		claimed[run] = true
	}
	return claimed, rows.Err()
}

// trialsIn reads the trials that started in the window [since, now) and
// pass the options' label filter: none in the default view, since every
// trial carries a label, and none under --project, since a trial belongs to
// no project root (its sessions do, through the clone).
func (s *Store) trialsIn(o ShowOptions, since, now time.Time) ([]trial, error) {
	if o.Project != "" || (o.Label == "" && !o.AllLabels) {
		return nil, nil
	}
	q := `SELECT label, run, status FROM trials WHERE started_at >= ? AND started_at < ?`
	args := []any{since.UnixMilli(), now.UnixMilli()}
	if o.Label != "" {
		q += ` AND label = ?`
		args = append(args, o.Label)
	}
	rows, err := s.db.Query(q+` ORDER BY started_at, run`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []trial
	for rows.Next() {
		var t trial
		if err := rows.Scan(&t.label, &t.run, &t.status); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RunModels is the distinct models of the sessions whose run is among runs,
// sorted; a session with no model is left out, and no run gives none. The
// testing record's header names them (SPEC-fyr-eval § The testing record).
func (s *Store) RunModels(runs []string) ([]string, error) {
	if len(runs) == 0 {
		return nil, nil
	}
	args := make([]any, len(runs))
	for i, r := range runs {
		args[i] = r
	}
	rows, err := s.db.Query(`SELECT DISTINCT model FROM sessions WHERE model IS NOT NULL AND model != ''
		AND run IN (?`+strings.Repeat(", ?", len(runs)-1)+`) ORDER BY model`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var models []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	return models, rows.Err()
}
