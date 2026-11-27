package auto

import (
	"fmt"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/redact"
)

const recentSignalWindow = 7 * 24 * time.Hour

func (r *run) projectSummary(root string) *metrics.Summary {
	if r.sweepCache != nil && r.sweepCache.root == root && r.sweepCache.summaryLoaded {
		return r.sweepCache.summary
	}
	s, closeSnap := r.sweepMetricsSnapshot()
	if s == nil {
		return nil
	}
	defer closeSnap()
	now := r.o.now()
	sum, err := s.Show(metrics.ShowOptions{
		Home:    r.o.Home,
		Since:   now.AddDate(0, 0, -7),
		Now:     now,
		Project: root,
	})
	if err != nil {
		sum = nil
	}
	if r.sweepCache != nil && r.sweepCache.root == root {
		r.sweepCache.summaryLoaded = true
		r.sweepCache.summary = sum
	}
	return sum
}

func (r *run) projectDiagnosticReport(root string) metrics.DiagnosticReport {
	sum := r.projectSummary(root)
	if sum == nil {
		return metrics.DiagnosticReport{}
	}
	if sum.Diagnostics != nil {
		return *sum.Diagnostics
	}
	return metrics.BuildDiagnosticReport(*sum, nil, "project", root)
}

func (r *run) unaddressedDiagnosticSignals(root string) []metrics.DiagnosticSignal {
	rep := r.projectDiagnosticReport(root)
	var highMed []metrics.DiagnosticSignal
	for _, sig := range rep.Signals {
		if (sig.Severity == "high" || sig.Severity == "medium") && sig.SignalID != "" {
			highMed = append(highMed, sig)
		}
	}
	if len(highMed) == 0 {
		return nil
	}
	seen := r.addressedSignalIDs(root)
	var out []metrics.DiagnosticSignal
	for _, sig := range highMed {
		if !seen[sig.SignalID] {
			out = append(out, sig)
		}
	}
	return out
}

func (r *run) addressedSignalIDs(root string) map[string]bool {
	seen := map[string]bool{}
	cutoff := r.o.now().Add(-recentSignalWindow)
	if props, err := r.sweepOpenProposals(); err == nil {
		for _, rec := range props {
			collectSignalTokens(seen, rec.Title+"\n"+rec.Body)
		}
	}
	g := brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}
	if recs, err := brain.List(g, brain.ListFilter{Kind: "proposal"}); err == nil {
		for _, rec := range recs {
			if rec.Status != "rejected" && recordUpdatedAfter(rec, cutoff) {
				collectSignalTokens(seen, rec.Title+"\n"+rec.Body)
			}
		}
	}
	if root != "" {
		p := brain.Options{Home: r.o.Home, Dir: root, Now: r.o.Now}
		if recs, err := brain.List(p, brain.ListFilter{}); err == nil {
			for _, rec := range recs {
				if rec.Status == "open" || rec.Status == "queued" || recordUpdatedAfter(rec, cutoff) {
					collectSignalTokens(seen, rec.Title+"\n"+rec.Body)
				}
			}
		}
	}
	return seen
}

func recordUpdatedAfter(rec brain.Record, cutoff time.Time) bool {
	t, err := time.Parse(time.RFC3339, rec.Updated)
	return err == nil && t.After(cutoff)
}

func collectSignalTokens(dst map[string]bool, text string) {
	for _, tok := range strings.FieldsFunc(text, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != ':' && r != '_' && r != '-'
	}) {
		if strings.HasPrefix(tok, "sig:") && len(tok) >= 5 && len(tok) <= 96 {
			dst[tok] = true
		}
	}
}

func (r *run) redactedDiagnosticSection(root string) string {
	rep := r.projectDiagnosticReport(root)
	if len(rep.Signals) == 0 {
		return ""
	}
	var lines []string
	for _, sig := range rep.Signals {
		line := fmt.Sprintf("- %s [%s] rule=%s metric=%s: %s",
			oneLine(sig.SignalID), oneLine(sig.Severity), oneLine(sig.Rule), oneLine(sig.Metric), oneLine(sig.Summary))
		lines = append(lines, redact.Redact(line))
	}
	return strings.Join(lines, "\n")
}
