package state

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/redact"
)

type simNDJSONLine struct {
	Table string          `json:"table"`
	Row   json.RawMessage `json:"row"`
}

func encodeSIMNDJSON(exp brain.SIMStateExport) ([]byte, string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, t := range []struct {
		name string
		n    int
		at   func(int) any
	}{
		{"sim_problems", len(exp.Problems), func(i int) any { return sanitizeSIMProblem(exp.Problems[i]) }},
		{"sim_attempts", len(exp.Attempts), func(i int) any { return sanitizeSIMAttempt(exp.Attempts[i]) }},
		{"sim_aliases", len(exp.Aliases), func(i int) any { return exp.Aliases[i] }},
	} {
		for i := 0; i < t.n; i++ {
			raw, err := json.Marshal(t.at(i))
			if err != nil {
				return nil, "", err
			}
			if err := enc.Encode(simNDJSONLine{Table: t.name, Row: raw}); err != nil {
				return nil, "", err
			}
		}
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:]), nil
}

func sanitizeSIMProblem(p brain.SIMProblem) brain.SIMProblem {
	p.RootCause = scrubSIMText(p.RootCause)
	// NormRootCause is recomputed from RootCause on import; clearing it prevents
	// lowercased space-separated tokens or usernames leaking past redaction.
	p.NormRootCause = ""
	p.MechanismClass = scrubSIMText(p.MechanismClass)
	p.SignalID = scrubSIMText(p.SignalID)
	p.TargetPaths = scrubSIMText(p.TargetPaths)
	p.OpenedHarness = scrubSIMText(p.OpenedHarness)
	p.OpenedModel = scrubSIMText(p.OpenedModel)
	return p
}

func sanitizeSIMAttempt(a brain.SIMAttempt) brain.SIMAttempt {
	a.ProposalID = scrubSIMText(a.ProposalID)
	a.HarnessVersion = scrubSIMText(a.HarnessVersion)
	a.ProposerModel = scrubSIMText(a.ProposerModel)
	a.PolicyModel = scrubSIMText(a.PolicyModel)
	a.ProjectScope = scrubSIMText(a.ProjectScope)
	a.Files = scrubSIMStrings(a.Files)
	a.What = scrubSIMText(a.What)
	a.PredictedRows = scrubSIMStrings(a.PredictedRows)
	a.PredictedSignals = scrubSIMStrings(a.PredictedSignals)
	a.FixedRows = scrubSIMStrings(a.FixedRows)
	a.MissedRows = scrubSIMStrings(a.MissedRows)
	a.RegressedRows = scrubSIMStrings(a.RegressedRows)
	a.FieldMetric = scrubSIMText(a.FieldMetric)
	a.FieldModels = scrubSIMModels(a.FieldModels)
	return a
}

func scrubSIMText(s string) string {
	if s == "" {
		return ""
	}
	return metrics.ScrubHostHomePaths(redact.Redact(s))
}

func scrubSIMStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = scrubSIMText(s)
	}
	return out
}

func scrubSIMModels(items []brain.SIMModelStatus) []brain.SIMModelStatus {
	if len(items) == 0 {
		return nil
	}
	out := make([]brain.SIMModelStatus, len(items))
	for i, m := range items {
		m.Model = scrubSIMText(m.Model)
		out[i] = m
	}
	return out
}

func decodeSIMNDJSON(data []byte) (brain.SIMStateExport, error) {
	var out brain.SIMStateExport
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), maxScannerLineBytes)
	for lineNum := 1; sc.Scan(); lineNum++ {
		if raw := bytes.TrimSpace(sc.Bytes()); len(raw) > 0 {
			if err := decodeOneSIMLine(raw, &out); err != nil {
				return brain.SIMStateExport{}, fmt.Errorf("%s:%d: %w", SIMLedgerEntry, lineNum, err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return brain.SIMStateExport{}, fmt.Errorf("%s: %w", SIMLedgerEntry, err)
	}
	return out, nil
}

func decodeOneSIMLine(raw []byte, out *brain.SIMStateExport) error {
	var env simNDJSONLine
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	row := bytes.TrimSpace(env.Row)
	if len(row) == 0 || bytes.Equal(row, []byte("null")) {
		return fmt.Errorf("missing row for table %q", env.Table)
	}
	switch env.Table {
	case "sim_problems":
		return appendDecoded(row, &out.Problems)
	case "sim_attempts":
		return appendDecoded(row, &out.Attempts)
	case "sim_aliases":
		return appendDecoded(row, &out.Aliases)
	default:
		return fmt.Errorf("unknown sim table %q", env.Table)
	}
}

func decodeOptionalSIM(arch verifiedArchive) (brain.SIMStateExport, error) {
	if !arch.hasSIM {
		return brain.SIMStateExport{}, nil
	}
	return decodeSIMNDJSON(arch.simBytes)
}

func applySIMImport(opts ImportOptions, simExp brain.SIMStateExport, dryRun bool) (brain.StateImportStats, error) {
	if !opts.IncludeMemory || !hasSIMRows(simExp) {
		return brain.StateImportStats{}, nil
	}
	return brain.ImportStateSIM(opts.Home, simExp, brain.SIMStateImportOpts{DryRun: dryRun})
}

func hasSIMRows(exp brain.SIMStateExport) bool {
	return len(exp.Problems)+len(exp.Attempts)+len(exp.Aliases) > 0
}

func shouldOpenMetrics(mPath string, mexp metrics.SanitizedMetricsExport) bool {
	_, err := os.Lstat(mPath)
	return !errors.Is(err, os.ErrNotExist) || hasMetricsRows(mexp)
}

func buildImportResult(opts ImportOptions, bStats, simStats brain.StateImportStats, mCounts metrics.StateImportCounts) ImportResult {
	return ImportResult{
		BundlePath:        opts.BundlePath,
		DryRun:            opts.DryRun,
		Insert:            bStats.Inserted + simStats.Inserted + mCounts.Inserted,
		Updated:           bStats.Updated + simStats.Updated + mCounts.Updated,
		SkipDuplicate:     max(0, bStats.Skipped-bStats.Conflicts) + max(0, simStats.Skipped-simStats.Conflicts) + mCounts.Skipped,
		Conflict:          bStats.Conflicts + simStats.Conflicts,
		OperatorPreserved: bStats.OperatorPreserved + simStats.OperatorPreserved,
		Brain:             bStats,
		SIM:               simStats,
		Metrics:           mCounts,
	}
}
