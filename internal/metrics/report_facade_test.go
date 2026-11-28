package metrics

import (
	"strings"

	"github.com/kesokaj/fyr/internal/metrics/report"
)

const (
	healthFileLimit     = report.HealthFileLimit
	maxDailyDigestBytes = report.MaxDailyDigestBytes
)

type plot struct{ inner report.Plot }

func newPlot(days int, top float64) plot { return plot{inner: report.NewPlot(days, top)} }

func (p plot) grid(b *strings.Builder, days []Day) { p.inner.Grid(b, days) }

func esc(s string) string { return report.EscapeSVG(s) }

func passKChart(p map[string]PassK) report.Chart { return report.PassKChart(p) }

func copySQLiteTempParent(scratchHome, dbPath string) (string, []string, bool) {
	return report.CopySQLiteTempParent(scratchHome, dbPath)
}

func cleanupCreatedDirs(dirs []string) { report.CleanupCreatedDirs(dirs) }

func computeSignalTransitions(signals []DigestAction, prev *DailyDigest) (newReg, resolved, persistHigh []string) {
	return report.ComputeSignalTransitions(signals, prev)
}

func computeDigestVerdict(d DailyDigest) string { return report.ComputeDigestVerdict(d) }

func parseAppliedActionRecord(id, kind, status, date, title, body string) (DigestAppliedAction, bool) {
	return report.ParseAppliedActionRecord(id, kind, status, date, title, body)
}

func extractRuleFromSignalID(sigID string) string { return report.ExtractRuleFromSignalID(sigID) }

func readBrainAppliedActions(home, repoRoot, sinceDay string) []DigestAppliedAction {
	return report.ReadBrainAppliedActions(home, repoRoot, sinceDay)
}

func normalizeAppliedAction(act DigestAppliedAction, day string, nowMS int64) DigestAppliedAction {
	return report.NormalizeAppliedAction(act, day, nowMS)
}

func mergeAppliedAction(base, incoming DigestAppliedAction) DigestAppliedAction {
	return report.MergeAppliedAction(base, incoming)
}
