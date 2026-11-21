package metrics

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/search"
)

// TrialInput describes one benchmark or eval-gate trial and its optional
// session link for RecordTrial / Store.RecordTrialInput.
type TrialInput struct {
	Run            string    `json:"run"`
	Label          string    `json:"label,omitempty"`
	Harness        string    `json:"harness,omitempty"`
	CLI            string    `json:"cli,omitempty"`
	Status         string    `json:"status"`
	Cause          string    `json:"cause,omitempty"`
	StartedAt      time.Time `json:"started_at,omitzero"`
	Seconds        int64     `json:"seconds,omitempty"`
	SetupSeconds   *int64    `json:"setup_seconds,omitempty"`
	SessionSeconds *int64    `json:"session_seconds,omitempty"`
	CheckSeconds   *int64    `json:"check_seconds,omitempty"`
	RowVersion     int       `json:"row_version,omitempty"`
	Kind           string    `json:"kind,omitempty"`
	Cluster        string    `json:"cluster,omitempty"`
	Expect         string    `json:"expect,omitempty"`
	SessionID      string    `json:"session_id,omitempty"`
	Model          string    `json:"model,omitempty"`
	Vendor         string    `json:"vendor,omitempty"`
	ProjectRoot    string    `json:"project_root,omitempty"`
	RepoID         string    `json:"repo_id,omitempty"`
	DurationMS     int64     `json:"duration_ms,omitempty"`
}

type normalizedTrialInput struct {
	trial       Trial
	sessionID   string
	vendor      string
	model       string
	projectRoot string
	repoID      string
	durationMS  int64
}

// RecordTrial opens <home>/<StateDir>/metrics.db and records row's trial and
// optional session link inside a single transaction without leaking content.
func RecordTrial(home string, row TrialInput) error {
	if strings.TrimSpace(home) == "" {
		return errors.New("metrics: trial: home required")
	}
	s, err := Open(Path(home))
	if err != nil {
		return err
	}
	defer s.Close()
	return s.RecordTrialInput(row)
}

// RecordTrialInput validates and records row's trial and optional session link in s.
func (s *Store) RecordTrialInput(row TrialInput) error {
	norm, err := normalizeTrialInput(row)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertTrialTx(tx, norm.trial); err != nil {
		return err
	}
	if norm.sessionID != "" {
		if err := upsertTrialSessionTx(tx, norm); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func normalizeTrialInput(row TrialInput) (normalizedTrialInput, error) {
	tr, err := buildValidatedTrial(row)
	if err != nil {
		return normalizedTrialInput{}, err
	}
	if row.DurationMS < 0 {
		return normalizedTrialInput{}, errors.New("metrics: trial: negative duration_ms")
	}
	model, err := cleanOptionalIdentifier("model", row.Model)
	if err != nil {
		return normalizedTrialInput{}, err
	}
	vendor, err := cleanOptionalIdentifier("vendor", row.Vendor)
	if err != nil {
		return normalizedTrialInput{}, err
	}
	if vendor == "" {
		vendor = "agy"
	}
	projRoot, repoID := resolveTrialRepo(row.ProjectRoot, row.RepoID)
	sid, err := resolveTrialSessionID(row.SessionID, tr.Run, model != "" || repoID != "" || projRoot != "")
	if err != nil {
		return normalizedTrialInput{}, err
	}
	durMS := row.DurationMS
	if durMS == 0 && tr.Seconds > 0 {
		durMS = tr.Seconds * 1000
	}
	return normalizedTrialInput{
		trial: tr, sessionID: sid, vendor: vendor, model: model,
		projectRoot: projRoot, repoID: repoID, durationMS: durMS,
	}, nil
}

func buildValidatedTrial(row TrialInput) (Trial, error) {
	run := strings.TrimSpace(row.Run)
	if run == "" || redact.Redact(run) != run {
		return Trial{}, errors.New("metrics: trial: valid secret-free run required")
	}
	status := strings.ToLower(strings.TrimSpace(row.Status))
	cause := normalizeTrialCause(status, row.Cause)
	if status == "pass" && strings.TrimSpace(row.Cause) != "" {
		return Trial{}, errors.New("metrics: trial: a pass carries no cause")
	}
	label := normalizeTrialLabel(row.Label)
	harnessVal := clipSanitizedText(row.Harness, trialText)
	if harnessVal == "" {
		harnessVal = "fyr"
	}
	started := row.StartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}
	tr := Trial{
		Run: run, Label: label, Harness: harnessVal,
		CLI: clipSanitizedText(row.CLI, trialText), Status: status, Cause: cause,
		StartedAt: started, Seconds: row.Seconds, SetupSeconds: row.SetupSeconds,
		SessionSeconds: row.SessionSeconds, CheckSeconds: row.CheckSeconds,
		RowVersion: row.RowVersion, Kind: clipSanitizedText(row.Kind, trialText),
		Cluster: clipSanitizedText(row.Cluster, trialText), Expect: clipSanitizedText(row.Expect, trialText),
	}
	if err := tr.validate(); err != nil {
		return Trial{}, fmt.Errorf("metrics: trial: %w", err)
	}
	return tr, nil
}

func normalizeTrialCause(status, rawCause string) string {
	if status != "fail" && status != "error" {
		return strings.TrimSpace(rawCause)
	}
	c := strings.TrimSpace(rawCause)
	if c == "" {
		return "failed"
	}
	if id := identifier(c); id != "" && redact.Redact(id) == id {
		return id
	}
	return "failed"
}

func normalizeTrialLabel(raw string) string {
	l := clipSanitizedText(raw, trialText)
	if l == "" {
		return "trial=bench"
	}
	if k, v, ok := strings.Cut(l, "="); ok && k != "" && v != "" {
		return l
	}
	return clipPrintable("trial="+l, trialText)
}

func cleanOptionalIdentifier(field, raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if id := identifier(s); id != "" && redact.Redact(id) == id {
		return id, nil
	}
	return "", fmt.Errorf("metrics: trial: invalid %s", field)
}

func resolveTrialSessionID(rawSID, run string, wantLink bool) (string, error) {
	sid := strings.TrimSpace(rawSID)
	if sid == "" {
		if !wantLink {
			return "", nil
		}
		return clipPrintable("trial:"+run, 128), nil
	}
	if len(sid) > 128 || printable(sid) != sid || redact.Redact(sid) != sid {
		return "", errors.New("metrics: trial: invalid session_id")
	}
	return sid, nil
}

func resolveTrialRepo(rawRoot, rawRepoID string) (string, string) {
	repoID := clipSanitizedText(rawRepoID, 256)
	root := strings.TrimSpace(rawRoot)
	if root == "" || root == search.GlobalWorkspace {
		return "", repoID
	}
	if redact.Redact(root) != root {
		return "", repoID
	}
	clean := clipPrintable(filepath.Clean(root), 256)
	if repoID == "" {
		repoID = clipPrintable(ResolveRepoID(clean), 256)
	}
	return clean, repoID
}

func insertTrialTx(tx *sql.Tx, t Trial) error {
	optInt := func(p *int64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	_, err := tx.Exec(`INSERT OR IGNORE INTO trials(run, label, row_version, kind, cluster, expect, harness, cli,
		status, cause, started_at, seconds, setup_seconds, session_seconds, check_seconds)
		VALUES (?, ?, NULLIF(?, 0), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?)`,
		t.Run, t.Label, t.RowVersion, t.Kind, t.Cluster, t.Expect, t.Harness, t.CLI,
		t.Status, t.Cause, t.StartedAt.UnixMilli(), t.Seconds, optInt(t.SetupSeconds), optInt(t.SessionSeconds), optInt(t.CheckSeconds))
	if err != nil {
		return fmt.Errorf("metrics: trial: %w", err)
	}
	return nil
}

func upsertTrialSessionTx(tx *sql.Tx, n normalizedTrialInput) error {
	startMS := n.trial.StartedAt.UnixMilli()
	endMS := startMS + n.durationMS
	_, err := tx.Exec(`INSERT INTO sessions(id, vendor, project_root, repo_id, model, label, outcome, started_at, ended_at, duration_ms, run)
		VALUES (?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, NULLIF(?, 0), ?)
		ON CONFLICT(id) DO UPDATE SET
			repo_id = CASE WHEN sessions.repo_id = '' THEN excluded.repo_id ELSE sessions.repo_id END,
			model = COALESCE(NULLIF(sessions.model, ''), excluded.model),
			label = COALESCE(NULLIF(sessions.label, ''), excluded.label),
			outcome = COALESCE(NULLIF(sessions.outcome, ''), excluded.outcome),
			started_at = COALESCE(sessions.started_at, excluded.started_at),
			ended_at = MAX(COALESCE(sessions.ended_at, 0), COALESCE(excluded.ended_at, 0)),
			duration_ms = COALESCE(sessions.duration_ms, excluded.duration_ms),
			run = COALESCE(NULLIF(sessions.run, ''), excluded.run)`,
		n.sessionID, n.vendor, n.projectRoot, n.repoID, n.model, n.trial.Label, n.trial.Status,
		startMS, endMS, n.durationMS, n.trial.Run)
	return err
}

func clipSanitizedText(raw string, maxLen int) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	red := strings.TrimSpace(redact.Redact(s))
	if strings.Contains(red, "[redacted by fyr") {
		return ""
	}
	red = ScrubHostHomePaths(red)
	red = strings.NewReplacer("\r", " ", "\n", " ").Replace(red)
	return clipPrintable(red, maxLen)
}

// ScrubHostHomePaths replaces the operator home directory and /home/<user> or
// /Users/<user> path prefixes with ~ before metrics or state leave the host.
func ScrubHostHomePaths(s string) string {
	if h, err := os.UserHomeDir(); err == nil {
		if h = strings.TrimRight(strings.TrimSpace(h), "/"); h != "" {
			s = strings.ReplaceAll(s, h, "~")
		}
	}
	for _, rootPrefix := range []string{"/home/", "/Users/"} {
		for {
			idx := strings.Index(s, rootPrefix)
			if idx < 0 {
				break
			}
			after := idx + len(rootPrefix)
			end := after
			for end < len(s) && s[end] != '/' && s[end] != ' ' && s[end] != '\t' && s[end] != '"' && s[end] != '\'' && s[end] != ')' && s[end] != ',' && s[end] != ':' {
				end++
			}
			if end == after {
				break
			}
			s = s[:idx] + "~" + s[end:]
		}
	}
	return s
}
