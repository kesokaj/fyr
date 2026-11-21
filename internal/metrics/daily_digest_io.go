package metrics

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

const maxDailyDigestBytes = 64 << 10

type digestASTCol struct {
	AST  search.StructuralSignals `json:"ast_snapshot"`
	Diff ASTStructuralDiff        `json:"ast_diff"`
}

type digestPreCol struct {
	Pre  PreInvocationSnapshot `json:"pre_invocation"`
	Diff PreInvocationDiff     `json:"pre_invocation_diff"`
}

type digestActionsCol struct {
	Actions         []DigestAppliedAction `json:"actions"`
	Signals         []DigestAction        `json:"signals"`
	NewRegressions  []string              `json:"new_regressions,omitempty"`
	ResolvedSignals []string              `json:"resolved_signals,omitempty"`
	PersistentHigh  []string              `json:"persistent_high,omitempty"`
}

// SaveDailyDigest persists d into the daily_digests table (upserting by day).
func SaveDailyDigest(s *Store, d DailyDigest) error {
	if s == nil || s.db == nil {
		return errors.New("metrics: nil store")
	}
	if strings.TrimSpace(d.Day) == "" {
		return errors.New("metrics: daily digest day required")
	}
	kpiRaw, astRaw, preRaw, actRaw, err := marshalDigestColumns(d)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO daily_digests(day, verdict, kpi_json, ast_json, preinv_json, actions_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(day) DO UPDATE SET verdict=excluded.verdict, kpi_json=excluded.kpi_json, ast_json=excluded.ast_json, preinv_json=excluded.preinv_json, actions_json=excluded.actions_json, created_at=excluded.created_at`,
		d.Day, d.Verdict, kpiRaw, astRaw, preRaw, actRaw, d.CreatedAt,
	)
	return err
}

func marshalDigestColumns(d DailyDigest) (kpiRaw, astRaw, preRaw, actRaw string, err error) {
	b1, err := json.Marshal(d.Rolling7d)
	if err != nil {
		return "", "", "", "", err
	}
	b2, err := json.Marshal(digestASTCol{d.ASTSnapshot, d.ASTDiff})
	if err != nil {
		return "", "", "", "", err
	}
	b3, err := json.Marshal(digestPreCol{d.PreInvocation, d.PreInvocationDiff})
	if err != nil {
		return "", "", "", "", err
	}
	b4, err := json.Marshal(digestActionsCol{
		Actions: d.Actions, Signals: d.Signals,
		NewRegressions: d.NewRegressions, ResolvedSignals: d.ResolvedSignals, PersistentHigh: d.PersistentHigh,
	})
	if err != nil {
		return "", "", "", "", err
	}
	return string(b1), string(b2), string(b3), string(b4), nil
}

// LatestDailyDigest loads the most recent DailyDigest from daily_digests, or nil when empty.
func LatestDailyDigest(s *Store) (*DailyDigest, error) {
	return queryDailyDigest(s, `SELECT day, verdict, kpi_json, ast_json, preinv_json, actions_json, created_at FROM daily_digests ORDER BY day DESC LIMIT 1`)
}

// PreviousDailyDigest loads the newest DailyDigest strictly before beforeDay, or nil when none exists.
func PreviousDailyDigest(s *Store, beforeDay string) (*DailyDigest, error) {
	return queryDailyDigest(s, `SELECT day, verdict, kpi_json, ast_json, preinv_json, actions_json, created_at FROM daily_digests WHERE day < ? ORDER BY day DESC LIMIT 1`, beforeDay)
}

func dailyDigestByDay(s *Store, day string) (*DailyDigest, error) {
	return queryDailyDigest(s, `SELECT day, verdict, kpi_json, ast_json, preinv_json, actions_json, created_at FROM daily_digests WHERE day = ? LIMIT 1`, day)
}

func queryDailyDigest(s *Store, q string, args ...any) (*DailyDigest, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("metrics: nil store")
	}
	var d DailyDigest
	var kpiRaw, astRaw, preRaw, actRaw string
	err := s.db.QueryRow(q, args...).Scan(&d.Day, &d.Verdict, &kpiRaw, &astRaw, &preRaw, &actRaw, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := decodeDigestColumns(&d, kpiRaw, astRaw, preRaw, actRaw); err != nil {
		return nil, err
	}
	return &d, nil
}

func decodeDigestColumns(d *DailyDigest, kpiRaw, astRaw, preRaw, actRaw string) error {
	if err := json.Unmarshal([]byte(kpiRaw), &d.Rolling7d); err != nil {
		return err
	}
	var astWrap digestASTCol
	if err := json.Unmarshal([]byte(astRaw), &astWrap); err != nil {
		return err
	}
	d.ASTSnapshot, d.ASTDiff = astWrap.AST, astWrap.Diff
	var preWrap digestPreCol
	if err := json.Unmarshal([]byte(preRaw), &preWrap); err != nil {
		return err
	}
	d.PreInvocation, d.PreInvocationDiff = preWrap.Pre, preWrap.Diff
	var actWrap digestActionsCol
	if err := json.Unmarshal([]byte(actRaw), &actWrap); err != nil {
		return err
	}
	d.Actions, d.Signals = actWrap.Actions, actWrap.Signals
	d.NewRegressions, d.ResolvedSignals, d.PersistentHigh = actWrap.NewRegressions, actWrap.ResolvedSignals, actWrap.PersistentHigh
	return nil
}

// RecordDigestAppliedAction persists one applied-action checkpoint into today's
// daily_digests row and refreshes reports/latest-daily-digest.json when home is set.
func RecordDigestAppliedAction(store *Store, home, repoRoot string, now time.Time, act DigestAppliedAction) error {
	if store == nil || store.db == nil {
		return errors.New("metrics: nil store")
	}
	if now.IsZero() {
		now = time.Now()
	}
	nowUTC := now.UTC()
	day := nowUTC.Format("2006-01-02")
	act = normalizeAppliedAction(act, day, nowUTC.UnixMilli())
	d, err := dailyDigestByDay(store, day)
	if err != nil {
		return err
	}
	if d == nil {
		built, berr := BuildDailyDigest(store, home, "", nowUTC)
		if berr != nil {
			return berr
		}
		d = &built
	}
	d.Actions = upsertDigestAppliedAction(d.Actions, act)
	d.Verdict = computeDigestVerdict(*d)
	if err := SaveDailyDigest(store, *d); err != nil {
		return err
	}
	if strings.TrimSpace(home) != "" {
		return WriteLatestDailyDigest(home, *d)
	}
	return nil
}

// LatestDailyDigestPath returns the path of the fail-open digest brief under home.
func LatestDailyDigestPath(home string) string {
	return filepath.Join(home, harness.StateDir, "reports", "latest-daily-digest.json")
}

// WriteLatestDailyDigest atomically writes d as compact 0600 JSON under home.
func WriteLatestDailyDigest(home string, d DailyDigest) error {
	if strings.TrimSpace(home) == "" {
		return errors.New("metrics: home required for latest-daily-digest.json")
	}
	p := LatestDailyDigestPath(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxDailyDigestBytes {
		compact, cerr := json.Marshal(d)
		if cerr != nil {
			return cerr
		}
		data = append(compact, '\n')
	}
	return harness.WriteAtomic(p, data, 0o600)
}

// ReadLatestDailyDigest reads latest-daily-digest.json fail-open (<= 64 KiB,
// regular non-symlink file via trail.OpenRegular). Missing, non-regular,
// oversized, or corrupt files return (nil, nil).
func ReadLatestDailyDigest(home string) (*DailyDigest, error) {
	if strings.TrimSpace(home) == "" {
		return nil, nil
	}
	f, err := trail.OpenRegular(LatestDailyDigestPath(home))
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDailyDigestBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxDailyDigestBytes {
		return nil, nil
	}
	var d DailyDigest
	if json.Unmarshal(data, &d) != nil || strings.TrimSpace(d.Day) == "" {
		return nil, nil
	}
	return &d, nil
}
