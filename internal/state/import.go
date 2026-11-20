package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/trail"
)

// Import verifies bundle safety, manifest schema caps, and all SHA-256 entry
// digests before opening any SQLite database, then previews (--dry-run) or
// transactionally merges records and sanitized telemetry into opts.Home.
func Import(opts ImportOptions) (ImportResult, error) {
	if strings.TrimSpace(opts.Home) == "" {
		return ImportResult{}, errors.New("state import: home directory required")
	}
	repoID, err := ResolveRepoFilter(opts.Repo, opts.Dir)
	if err != nil {
		return ImportResult{}, err
	}
	arch, err := readAndVerifyArchive(opts.BundlePath)
	if err != nil {
		return ImportResult{}, err
	}
	recs, err := decodeBrainNDJSON(arch.brainBytes)
	if err != nil {
		return ImportResult{}, fmt.Errorf("state import: %s: %w", opts.BundlePath, err)
	}
	remapLocalRepoScope(recs, opts.Dir)
	mexp, err := decodeMetricsNDJSON(arch.metricsBytes, arch.manifest.MetricsSchema)
	if err != nil {
		return ImportResult{}, fmt.Errorf("state import: %s: %w", opts.BundlePath, err)
	}
	if repoID != "" {
		mexp = filterMetricsByRepo(mexp, repoID)
	}
	bStats, mCounts, err := applyStateStores(opts, repoID, recs, mexp)
	if err != nil {
		return ImportResult{}, err
	}
	return ImportResult{
		BundlePath: opts.BundlePath, DryRun: opts.DryRun,
		Insert: bStats.Inserted + mCounts.Inserted, Updated: bStats.Updated + mCounts.Updated,
		SkipDuplicate: max(0, bStats.Skipped-bStats.Conflicts) + mCounts.Skipped,
		Conflict:      bStats.Conflicts, OperatorPreserved: bStats.OperatorPreserved,
		Brain: bStats, Metrics: mCounts,
	}, nil
}

func decodeBrainNDJSON(data []byte) ([]brain.Record, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), maxScannerLineBytes)
	var out []brain.Record
	for lineNum := 1; sc.Scan(); lineNum++ {
		if raw := bytes.TrimSpace(sc.Bytes()); len(raw) > 0 {
			var r brain.Record
			if err := json.Unmarshal(raw, &r); err != nil {
				return nil, fmt.Errorf("%s:%d: %w", BrainRecordsEntry, lineNum, err)
			}
			out = append(out, r)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", BrainRecordsEntry, err)
	}
	return out, nil
}

func decodeMetricsNDJSON(data []byte, schema int) (metrics.SanitizedMetricsExport, error) {
	out := metrics.SanitizedMetricsExport{Schema: schema}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), maxScannerLineBytes)
	for lineNum := 1; sc.Scan(); lineNum++ {
		if raw := bytes.TrimSpace(sc.Bytes()); len(raw) > 0 {
			if err := decodeOneMetricsLine(raw, &out); err != nil {
				return metrics.SanitizedMetricsExport{}, fmt.Errorf("%s:%d: %w", MetricsSanitizedEntry, lineNum, err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return metrics.SanitizedMetricsExport{}, fmt.Errorf("%s: %w", MetricsSanitizedEntry, err)
	}
	return out, nil
}

func decodeOneMetricsLine(raw []byte, out *metrics.SanitizedMetricsExport) error {
	var env metricsNDJSONLine
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if len(env.Row) == 0 {
		return fmt.Errorf("missing row for table %q", env.Table)
	}
	switch env.Table {
	case "sessions":
		return appendDecoded(env.Row, &out.Sessions)
	case "steps":
		return appendDecoded(env.Row, &out.Steps)
	case "events":
		return appendDecoded(env.Row, &out.Events)
	case "trials":
		return appendDecoded(env.Row, &out.Trials)
	case "turn_vets":
		return appendDecoded(env.Row, &out.TurnVets)
	case "session_ratings":
		return appendDecoded(env.Row, &out.SessionRatings)
	case "judge_verdicts":
		return appendDecoded(env.Row, &out.JudgeVerdicts)
	case "background_runs":
		return appendDecoded(env.Row, &out.BackgroundRuns)
	default:
		return fmt.Errorf("unknown metrics table %q", env.Table)
	}
}

func appendDecoded[T any](raw json.RawMessage, dst *[]T) error {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*dst = append(*dst, v)
	return nil
}

func filterMetricsByRepo(in metrics.SanitizedMetricsExport, repoID string) metrics.SanitizedMetricsExport {
	out := metrics.SanitizedMetricsExport{Schema: in.Schema, RepoID: repoID, SinceMS: in.SinceMS}
	sids, runs := map[string]bool{}, map[string]bool{}
	for _, s := range in.Sessions {
		if s.RepoID == repoID {
			out.Sessions = append(out.Sessions, s)
			sids[s.ID] = true
			runs[s.Run] = s.Run != ""
		}
	}
	for _, st := range in.Steps {
		if sids[st.SessionID] {
			out.Steps = append(out.Steps, st)
		}
	}
	for _, ev := range in.Events {
		if sids[ev.SessionID] {
			out.Events = append(out.Events, ev)
		}
	}
	for _, tr := range in.Trials {
		if runs[tr.Run] {
			out.Trials = append(out.Trials, tr)
		}
	}
	return filterMetricsLedgersByRepo(in, out, repoID, sids)
}

func filterMetricsLedgersByRepo(in, out metrics.SanitizedMetricsExport, repoID string, sids map[string]bool) metrics.SanitizedMetricsExport {
	for _, tv := range in.TurnVets {
		if tv.RepoID == repoID || (tv.RepoID == "" && sids[tv.ConversationID]) {
			out.TurnVets = append(out.TurnVets, tv)
		}
	}
	for _, sr := range in.SessionRatings {
		if sr.RepoID == repoID || (sr.RepoID == "" && sids[sr.SessionID]) {
			out.SessionRatings = append(out.SessionRatings, sr)
		}
	}
	for _, jv := range in.JudgeVerdicts {
		if jv.RepoID == repoID {
			out.JudgeVerdicts = append(out.JudgeVerdicts, jv)
		}
	}
	return out
}

func applyStateStores(opts ImportOptions, repoID string, recs []brain.Record, mexp metrics.SanitizedMetricsExport) (brain.StateImportStats, metrics.StateImportCounts, error) {
	bOpts := brain.StateImportOpts{RepoID: repoID, IncludeMemory: opts.IncludeMemory, DryRun: true}
	dryBrain, err := brain.ImportStateRecords(opts.Home, recs, bOpts)
	if err != nil {
		return brain.StateImportStats{}, metrics.StateImportCounts{}, err
	}
	if opts.DryRun {
		mCounts, err := dryRunMetricsImport(opts.Home, opts.BundlePath, mexp)
		return dryBrain, mCounts, err
	}
	mPath := metrics.Path(opts.Home)
	_, statErr := os.Lstat(mPath)
	var ms *metrics.Store
	if !errors.Is(statErr, os.ErrNotExist) || hasMetricsRows(mexp) {
		if ms, err = metrics.Open(mPath); err != nil {
			return brain.StateImportStats{}, metrics.StateImportCounts{}, err
		}
		defer ms.Close()
	}
	bOpts.DryRun = false
	bStats, err := brain.ImportStateRecords(opts.Home, recs, bOpts)
	if err != nil || ms == nil {
		return bStats, metrics.StateImportCounts{}, err
	}
	mCounts, err := metrics.ImportSanitizedState(ms, mexp, false)
	return bStats, mCounts, err
}

func dryRunMetricsImport(home, bundlePath string, mexp metrics.SanitizedMetricsExport) (metrics.StateImportCounts, error) {
	mPath := metrics.Path(home)
	_, statErr := os.Lstat(mPath)
	if errors.Is(statErr, os.ErrNotExist) && !hasMetricsRows(mexp) {
		return metrics.StateImportCounts{}, nil
	}
	absBundle, _ := filepath.Abs(bundlePath)
	tmpDir, err := os.MkdirTemp(filepath.Dir(absBundle), ".fyr-state-dryrun-*")
	if err != nil {
		if tmpDir, err = os.MkdirTemp(home, ".fyr-state-dryrun-*"); err != nil {
			return metrics.StateImportCounts{}, err
		}
	}
	defer os.RemoveAll(tmpDir)
	tmpDB := filepath.Join(tmpDir, "metrics.db")
	if !errors.Is(statErr, os.ErrNotExist) {
		if err := copyFileIfRegular(mPath, tmpDB); err != nil {
			return metrics.StateImportCounts{}, err
		}
		if _, werr := os.Lstat(mPath + "-wal"); werr == nil {
			if err := copyFileIfRegular(mPath+"-wal", tmpDB+"-wal"); err != nil {
				return metrics.StateImportCounts{}, err
			}
		}
	}
	ms, err := metrics.Open(tmpDB)
	if err != nil {
		return metrics.StateImportCounts{}, err
	}
	defer ms.Close()
	return metrics.ImportSanitizedState(ms, mexp, true)
}

func copyFileIfRegular(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("state import: %s is not a regular file", src)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

func hasMetricsRows(m metrics.SanitizedMetricsExport) bool {
	return len(m.Sessions)+len(m.Steps)+len(m.Events)+len(m.Trials)+
		len(m.TurnVets)+len(m.SessionRatings)+len(m.JudgeVerdicts)+len(m.BackgroundRuns) > 0
}

func remapLocalRepoScope(recs []brain.Record, dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	localScope, _, err := trail.Roots(dir)
	if err != nil || localScope == "" {
		return
	}
	localRepoID := brain.ResolveRepoID(localScope)
	for i := range recs {
		if localRepoID != "" && recs[i].Scope != "" && recs[i].RepoID == localRepoID {
			recs[i].Scope = localScope
		}
	}
}
