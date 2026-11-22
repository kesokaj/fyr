// Package state exports and merges offline portable state zip bundles across
// ~/.config/fyr/brain.db and ~/.config/fyr/metrics.db (ADR-531, PRD R4/R7/R8).
package state

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
)

const (
	ManifestEntry         = "manifest.json"
	BrainRecordsEntry     = "brain_records.ndjson"
	MetricsSanitizedEntry = "metrics_sanitized.ndjson"
	SIMLedgerEntry        = "sim_ledger.ndjson"
	MaxBrainSchema        = 5
	MaxMetricsSchema      = 13
)

var (
	maxArchiveBytes     int64 = 256 << 20
	maxManifestBytes    int64 = 4 << 20
	maxEntryBytes       int64 = 256 << 20
	maxTotalBytes       int64 = 512 << 20
	maxScannerLineBytes       = 4 << 20
)

type Manifest struct {
	FyrVersion    string            `json:"fyr_version"`
	BrainSchema   int               `json:"brain_schema"`
	MetricsSchema int               `json:"metrics_schema"`
	ExportedAt    string            `json:"exported_at"`
	IncludeMemory bool              `json:"include_memory"`
	IncludeSIM    bool              `json:"include_sim,omitempty"`
	RecordClasses []string          `json:"record_classes"`
	RepoIDs       []string          `json:"repo_ids"`
	Counts        ManifestCounts    `json:"counts"`
	SHA256        map[string]string `json:"sha256"`
}

type ManifestCounts struct {
	BrainRecords   int `json:"brain_records"`
	Sessions       int `json:"sessions"`
	Steps          int `json:"steps"`
	Events         int `json:"events"`
	Trials         int `json:"trials"`
	TurnVets       int `json:"turn_vets"`
	SessionRatings int `json:"session_ratings"`
	JudgeVerdicts  int `json:"judge_verdicts"`
	BackgroundRuns int `json:"background_runs"`
	MetricsRows    int `json:"metrics_rows"`
	SIMProblems    int `json:"sim_problems,omitempty"`
	SIMAttempts    int `json:"sim_attempts,omitempty"`
	SIMAliases     int `json:"sim_aliases,omitempty"`
}

type ExportOptions struct {
	Home, Dir, Repo, OutPath, FyrVersion string
	Since                                time.Time
	IncludeMemory                        bool
	Now                                  func() time.Time
}

type ExportResult struct {
	OutPath  string   `json:"out_path"`
	Manifest Manifest `json:"manifest"`
}

type ImportOptions struct {
	Home, Dir, BundlePath, Repo string
	IncludeMemory, DryRun       bool
}

type ImportResult struct {
	BundlePath        string                    `json:"bundle_path"`
	DryRun            bool                      `json:"dry_run"`
	Insert            int                       `json:"insert"`
	Updated           int                       `json:"updated"`
	SkipDuplicate     int                       `json:"skip_duplicate"`
	Conflict          int                       `json:"conflict"`
	OperatorPreserved int                       `json:"operator_preserved"`
	Brain             brain.StateImportStats    `json:"brain"`
	SIM               brain.StateImportStats    `json:"sim,omitempty"`
	Metrics           metrics.StateImportCounts `json:"metrics"`
}

func ResolveRepoFilter(rawRepo, dir string) (string, error) {
	trimmed := strings.TrimSpace(rawRepo)
	if trimmed == "" {
		return "", nil
	}
	if strings.IndexFunc(trimmed, unicode.IsSpace) >= 0 || strings.IndexFunc(trimmed, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("state: invalid --repo %q", rawRepo)
	}
	if trimmed != "current" {
		return trimmed, nil
	}
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("state: --repo current requires a working directory")
	}
	if id := brain.ResolveRepoID(dir); id != "" {
		return id, nil
	}
	return "", fmt.Errorf("state: could not resolve repo_id for %s", dir)
}

func DefaultBundlePath(dir string, now time.Time) string {
	name := fmt.Sprintf("fyr-state-%s.zip", now.UTC().Format("20060102-150405"))
	if strings.TrimSpace(dir) == "" {
		return name
	}
	return filepath.Join(dir, name)
}

func manifestCounts(brainCount int, m metrics.SanitizedMetricsExport, sim brain.SIMStateExport) ManifestCounts {
	total := len(m.Sessions) + len(m.Steps) + len(m.Events) + len(m.Trials) +
		len(m.TurnVets) + len(m.SessionRatings) + len(m.JudgeVerdicts) + len(m.BackgroundRuns)
	return ManifestCounts{
		BrainRecords: brainCount, Sessions: len(m.Sessions), Steps: len(m.Steps),
		Events: len(m.Events), Trials: len(m.Trials), TurnVets: len(m.TurnVets),
		SessionRatings: len(m.SessionRatings), JudgeVerdicts: len(m.JudgeVerdicts),
		BackgroundRuns: len(m.BackgroundRuns), MetricsRows: total,
		SIMProblems: len(sim.Problems), SIMAttempts: len(sim.Attempts), SIMAliases: len(sim.Aliases),
	}
}
