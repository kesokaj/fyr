package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

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
	localScope, localRepoID := resolveLocalTarget(opts.Dir)
	remapLocalRepoScope(recs, localScope, localRepoID)
	if arch.manifest.MetricsSchema < 1 && len(bytes.TrimSpace(arch.metricsBytes)) > 0 {
		return ImportResult{}, fmt.Errorf("state import: %s: metrics payload requires metrics_schema >= 1", opts.BundlePath)
	}
	mexp, err := decodeMetricsNDJSON(arch.metricsBytes, arch.manifest.MetricsSchema)
	if err != nil {
		return ImportResult{}, fmt.Errorf("state import: %s: %w", opts.BundlePath, err)
	}
	if repoID != "" {
		mexp = filterMetricsByRepo(mexp, repoID)
	}
	simExp, err := decodeOptionalSIM(arch)
	if err != nil {
		return ImportResult{}, fmt.Errorf("state import: %s: %w", opts.BundlePath, err)
	}
	return applyStateStores(opts, repoID, localScope, localRepoID, recs, simExp, mexp)
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
	case "orchestration_runs":
		return appendDecoded(env.Row, &out.OrchestrationRuns)
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
	for _, or := range in.OrchestrationRuns {
		if or.RepoID == repoID {
			out.OrchestrationRuns = append(out.OrchestrationRuns, or)
		}
	}
	return out
}

func applyStateStores(opts ImportOptions, repoID, localScope, localRepoID string, recs []brain.Record, simExp brain.SIMStateExport, mexp metrics.SanitizedMetricsExport) (ImportResult, error) {
	bOpts := brain.StateImportOpts{
		RepoID: repoID, LocalScope: localScope, LocalRepoID: localRepoID,
		IncludeMemory: opts.IncludeMemory, DryRun: true,
	}
	dryBrain, err := brain.ImportStateRecords(opts.Home, recs, bOpts)
	if err != nil {
		return ImportResult{}, err
	}
	drySIM, err := applySIMImport(opts, simExp, true)
	if err != nil {
		return ImportResult{}, err
	}
	if opts.DryRun {
		mCounts, err := dryRunMetricsImport(opts.Home, mexp)
		return buildImportResult(opts, dryBrain, drySIM, mCounts), err
	}
	var ms *metrics.Store
	if mPath := metrics.Path(opts.Home); shouldOpenMetrics(mPath, mexp) {
		if ms, err = metrics.Open(mPath); err != nil {
			return ImportResult{}, err
		}
		defer ms.Close()
	}
	bOpts.DryRun = false
	bStats, err := brain.ImportStateRecords(opts.Home, recs, bOpts)
	if err != nil {
		return ImportResult{}, err
	}
	simStats, err := applySIMImport(opts, simExp, false)
	if err != nil || ms == nil {
		return buildImportResult(opts, bStats, simStats, metrics.StateImportCounts{}), err
	}
	mCounts, err := metrics.ImportSanitizedState(ms, mexp, false)
	return buildImportResult(opts, bStats, simStats, mCounts), err
}

func dryRunMetricsImport(home string, mexp metrics.SanitizedMetricsExport) (metrics.StateImportCounts, error) {
	mPath := metrics.Path(home)
	_, statErr := os.Lstat(mPath)
	if errors.Is(statErr, os.ErrNotExist) {
		if !hasMetricsRows(mexp) {
			return metrics.StateImportCounts{}, nil
		}
		ms, err := metrics.OpenMemory()
		if err != nil {
			return metrics.StateImportCounts{}, err
		}
		defer ms.Close()
		return metrics.ImportSanitizedState(ms, mexp, true)
	}
	sandbox, err := ensureSafeSandboxDir(home, filepath.Dir(mPath))
	if err != nil {
		return metrics.StateImportCounts{}, err
	}
	tmpDir, err := os.MkdirTemp(sandbox, ".fyr-state-dryrun-*")
	if err != nil {
		return metrics.StateImportCounts{}, err
	}
	defer os.RemoveAll(tmpDir)
	tmpDB := filepath.Join(tmpDir, "metrics.db")
	if err := copyMetricsSnapshot(mPath, tmpDB); err != nil {
		return metrics.StateImportCounts{}, err
	}
	ms, err := metrics.Open(tmpDB)
	if err != nil {
		return metrics.StateImportCounts{}, err
	}
	defer ms.Close()
	return metrics.ImportSanitizedState(ms, mexp, true)
}

func ensureSafeSandboxDir(home, fyrDir string) (string, error) {
	cleanHome := filepath.Clean(home)
	if hst, err := os.Lstat(cleanHome); err == nil {
		if hst.Mode()&os.ModeSymlink != 0 || !hst.IsDir() {
			return "", fmt.Errorf("state dry-run sandbox %s: symlink or non-directory refused", cleanHome)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	sandbox := filepath.Join(fyrDir, "sandbox")
	rel, err := filepath.Rel(cleanHome, sandbox)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("state dry-run sandbox %s: outside home %s", sandbox, cleanHome)
	}
	cur := cleanHome
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		if st, err := os.Lstat(cur); err == nil {
			if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
				return "", fmt.Errorf("state dry-run sandbox %s: symlink or non-directory refused", cur)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if err := os.MkdirAll(sandbox, 0o700); err != nil {
		return "", err
	}
	return sandbox, nil
}

func copyMetricsSnapshot(mPath, tmpDB string) error {
	if err := copyFileIfRegular(mPath, tmpDB); err != nil {
		return err
	}
	if _, werr := os.Lstat(mPath + "-wal"); werr == nil {
		return copyFileIfRegular(mPath+"-wal", tmpDB+"-wal")
	}
	return nil
}

func copyFileIfRegular(src, dst string) error {
	f, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("state import: %s is not a regular file", src)
	}
	if fi.Size() > maxArchiveBytes {
		return fmt.Errorf("state import: %s exceeds maximum snapshot size (%d bytes)", src, maxArchiveBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxArchiveBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxArchiveBytes {
		return fmt.Errorf("state import: %s exceeds maximum snapshot size (%d bytes)", src, maxArchiveBytes)
	}
	return os.WriteFile(dst, data, 0o600)
}

func hasMetricsRows(m metrics.SanitizedMetricsExport) bool {
	return len(m.Sessions)+len(m.Steps)+len(m.Events)+len(m.Trials)+
		len(m.TurnVets)+len(m.SessionRatings)+len(m.JudgeVerdicts)+
		len(m.BackgroundRuns)+len(m.OrchestrationRuns) > 0
}

func resolveLocalTarget(dir string) (string, string) {
	if strings.TrimSpace(dir) == "" {
		return "", ""
	}
	localScope, _, err := trail.Roots(dir)
	if err != nil || localScope == "" {
		return "", ""
	}
	return localScope, brain.ResolveRepoID(localScope)
}

func remapLocalRepoScope(recs []brain.Record, localScope, localRepoID string) {
	if localScope == "" || localRepoID == "" {
		return
	}
	for i := range recs {
		if recs[i].RepoID == localRepoID {
			recs[i].Scope = localScope
		}
	}
}
