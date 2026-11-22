package state

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/redact"
)

type metricsNDJSONLine struct {
	Table string          `json:"table"`
	Row   json.RawMessage `json:"row"`
}

type zipStream struct {
	name string
	data []byte
}

// Export writes an offline state zip bundle (mode 0600, atomically renamed)
// holding manifest.json, brain_records.ndjson, metrics_sanitized.ndjson, and
// (when opts.IncludeMemory is true) sim_ledger.ndjson.
func Export(opts ExportOptions) (ExportResult, error) {
	if strings.TrimSpace(opts.Home) == "" {
		return ExportResult{}, errors.New("state export: home directory required")
	}
	repoID, err := ResolveRepoFilter(opts.Repo, opts.Dir)
	if err != nil {
		return ExportResult{}, err
	}
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	outPath := strings.TrimSpace(opts.OutPath)
	if outPath == "" {
		outPath = DefaultBundlePath(opts.Dir, now)
	}
	recs, err := brain.ExportStateRecords(opts.Home, brain.StateExportOpts{
		RepoID: repoID, IncludeMemory: opts.IncludeMemory, Since: opts.Since,
	})
	if err != nil {
		return ExportResult{}, err
	}
	mexp, err := exportMetricsIfPresent(opts.Home, repoID, opts.Since)
	if err != nil {
		return ExportResult{}, err
	}
	var simExp brain.SIMStateExport
	if opts.IncludeMemory {
		if simExp, err = brain.ExportStateSIM(opts.Home, opts.Since); err != nil {
			return ExportResult{}, err
		}
	}
	return writeExportBundle(outPath, now, opts, recs, mexp, simExp)
}

func writeExportBundle(outPath string, now time.Time, opts ExportOptions, recs []brain.Record, mexp metrics.SanitizedMetricsExport, simExp brain.SIMStateExport) (ExportResult, error) {
	brainBytes, brainSHA, err := encodeBrainNDJSON(recs)
	if err != nil {
		return ExportResult{}, err
	}
	metricsBytes, metricsSHA, err := encodeMetricsNDJSON(mexp)
	if err != nil {
		return ExportResult{}, err
	}
	digests := map[string]string{BrainRecordsEntry: brainSHA, MetricsSanitizedEntry: metricsSHA}
	streams := []zipStream{{BrainRecordsEntry, brainBytes}, {MetricsSanitizedEntry, metricsBytes}}
	if opts.IncludeMemory {
		simBytes, simSHA, err := encodeSIMNDJSON(simExp)
		if err != nil {
			return ExportResult{}, err
		}
		digests[SIMLedgerEntry] = simSHA
		streams = append(streams, zipStream{SIMLedgerEntry, simBytes})
	}
	manifest := buildExportManifest(now, opts, recs, mexp, simExp, digests)
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return ExportResult{}, err
	}
	allStreams := append([]zipStream{{ManifestEntry, append(manifestBytes, '\n')}}, streams...)
	if err := writeZipAtomically(outPath, now, allStreams); err != nil {
		return ExportResult{}, err
	}
	return ExportResult{OutPath: outPath, Manifest: manifest}, nil
}

func buildExportManifest(now time.Time, opts ExportOptions, recs []brain.Record, mexp metrics.SanitizedMetricsExport, simExp brain.SIMStateExport, digests map[string]string) Manifest {
	ver := strings.TrimSpace(opts.FyrVersion)
	if ver == "" {
		ver = "devel"
	}
	mSchema := max(mexp.Schema, 0)
	if mSchema == 0 {
		mSchema = MaxMetricsSchema
	}
	return Manifest{
		FyrVersion: ver, BrainSchema: MaxBrainSchema, MetricsSchema: mSchema,
		ExportedAt: now.Format(time.RFC3339), IncludeMemory: opts.IncludeMemory, IncludeSIM: opts.IncludeMemory,
		RecordClasses: collectRecordClasses(recs), RepoIDs: collectRepoIDs(recs, mexp),
		Counts: manifestCounts(len(recs), mexp, simExp), SHA256: digests,
	}
}

func exportMetricsIfPresent(home, repoID string, since time.Time) (metrics.SanitizedMetricsExport, error) {
	path := metrics.Path(home)
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return metrics.SanitizedMetricsExport{Schema: MaxMetricsSchema, RepoID: repoID}, nil
	}
	if err != nil {
		return metrics.SanitizedMetricsExport{}, err
	}
	if !fi.Mode().IsRegular() {
		return metrics.SanitizedMetricsExport{}, fmt.Errorf("state export: %s is not a regular file", path)
	}
	s, err := metrics.Open(path)
	if err != nil {
		return metrics.SanitizedMetricsExport{}, err
	}
	defer s.Close()
	return metrics.ExportSanitizedState(s, repoID, since)
}

func encodeBrainNDJSON(recs []brain.Record) ([]byte, string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, r := range recs {
		if err := enc.Encode(sanitizeExportRecord(r)); err != nil {
			return nil, "", err
		}
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:]), nil
}

func sanitizeExportRecord(r brain.Record) brain.Record {
	title, body := r.Title, r.Body
	if filepath.IsAbs(r.Scope) && len(r.Scope) > 1 {
		title = strings.ReplaceAll(title, r.Scope, "<repo>")
		body = strings.ReplaceAll(body, r.Scope, "<repo>")
	}
	r.Title = metrics.ScrubHostHomePaths(redact.Redact(title))
	r.Body = metrics.ScrubHostHomePaths(redact.Redact(body))
	if r.RepoID != "" {
		r.Scope = ""
		r.Project = ""
	} else {
		r.Scope = metrics.ScrubHostHomePaths(r.Scope)
		r.Project = metrics.ScrubHostHomePaths(r.Project)
	}
	return r
}

func encodeMetricsNDJSON(exp metrics.SanitizedMetricsExport) ([]byte, string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, t := range []struct {
		name string
		n    int
		at   func(int) any
	}{
		{"sessions", len(exp.Sessions), func(i int) any { return exp.Sessions[i] }},
		{"steps", len(exp.Steps), func(i int) any { return exp.Steps[i] }},
		{"events", len(exp.Events), func(i int) any { return exp.Events[i] }},
		{"trials", len(exp.Trials), func(i int) any { return exp.Trials[i] }},
		{"turn_vets", len(exp.TurnVets), func(i int) any { return exp.TurnVets[i] }},
		{"session_ratings", len(exp.SessionRatings), func(i int) any { return exp.SessionRatings[i] }},
		{"judge_verdicts", len(exp.JudgeVerdicts), func(i int) any { return exp.JudgeVerdicts[i] }},
		{"background_runs", len(exp.BackgroundRuns), func(i int) any { return exp.BackgroundRuns[i] }},
	} {
		for i := 0; i < t.n; i++ {
			raw, err := json.Marshal(t.at(i))
			if err != nil {
				return nil, "", err
			}
			if err := enc.Encode(metricsNDJSONLine{Table: t.name, Row: raw}); err != nil {
				return nil, "", err
			}
		}
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:]), nil
}

func collectRecordClasses(recs []brain.Record) []string {
	seen := map[string]bool{}
	for _, r := range recs {
		if r.Kind != "" {
			seen[r.Kind] = true
		}
	}
	return sortedKeys(seen)
}

func collectRepoIDs(recs []brain.Record, exp metrics.SanitizedMetricsExport) []string {
	seen := map[string]bool{}
	add := func(id string) {
		if id = strings.TrimSpace(id); id != "" {
			seen[id] = true
		}
	}
	for _, r := range recs {
		add(r.RepoID)
	}
	for _, s := range exp.Sessions {
		add(s.RepoID)
	}
	for _, tv := range exp.TurnVets {
		add(tv.RepoID)
	}
	for _, sr := range exp.SessionRatings {
		add(sr.RepoID)
	}
	for _, jv := range exp.JudgeVerdicts {
		add(jv.RepoID)
	}
	return sortedKeys(seen)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func writeZipAtomically(destPath string, modTime time.Time, streams []zipStream) error {
	if fi, err := os.Lstat(destPath); err == nil && (fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular()) {
		return fmt.Errorf("state export: %s is not a regular file", destPath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".fyr-state-*.zip.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := writeBundleStreams(tmp, modTime, streams); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

func writeBundleStreams(tmp *os.File, modTime time.Time, streams []zipStream) error {
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	zw := zip.NewWriter(tmp)
	for _, st := range streams {
		fh := &zip.FileHeader{Name: st.name, Method: zip.Deflate, Modified: modTime.UTC()}
		fh.SetMode(0o600)
		w, err := zw.CreateHeader(fh)
		if err == nil {
			_, err = w.Write(st.data)
		}
		if err != nil {
			_ = zw.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	return tmp.Close()
}
