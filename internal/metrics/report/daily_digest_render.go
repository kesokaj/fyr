package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
)

type dailyBannerView struct {
	Day             string
	Verdict         string
	NewRegressions  string
	ResolvedSignals string
	PersistentHigh  string
	Actions         []string
	Signals         []string
	Info            []string
}

// RenderDailyDigestJSON writes d as indented JSON to w.
func RenderDailyDigestJSON(w io.Writer, d DailyDigest) error {
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// RenderDailyDigestText renders a human-readable summary of d.
func RenderDailyDigestText(d DailyDigest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Daily Health Digest (%s): %s\n", search.Printable(d.Day), search.Printable(d.Verdict))
	fmt.Fprintf(&b, "  7d sessions: %.0f (%+.0f vs prior 7d) | bash_read_skips: %.0f (%+.0f)\n",
		d.Rolling7d.Sessions.Current, d.Rolling7d.Sessions.Delta,
		d.Rolling7d.BashReadSkips.Current, d.Rolling7d.BashReadSkips.Delta)
	fmt.Fprintf(&b, "  AST: cycles=%d (%+d) co_edit=%d (%+d) large_files=%d/%d (%+d)\n",
		d.ASTSnapshot.ProdPkgCycles, d.ASTDiff.ProdPkgCycles,
		d.ASTSnapshot.HiddenCouplingEditPairs, d.ASTDiff.HiddenCouplingEditPairs,
		d.ASTSnapshot.LargeFilesOver350, d.ASTSnapshot.TotalCodeFiles, d.ASTDiff.LargeFilesOver350)
	fmt.Fprintf(&b, "  PreInvocation: vets=%d timeouts=%d (%s) p50=%s p95=%s\n",
		d.PreInvocation.TurnVets, d.PreInvocation.TimedOutTurns,
		FmtFloatPtr(d.PreInvocation.TimeoutRatePct, "%.1f%%"),
		FmtOptMS(d.PreInvocation.P50DurationMS), FmtOptMS(d.PreInvocation.P95DurationMS))
	appendDigestActionLines(&b, d)
	appendDigestSignalLines(&b, d)
	return b.String()
}

func appendDigestActionLines(b *strings.Builder, d DailyDigest) {
	b.WriteString("  I did:\n")
	if len(d.Actions) == 0 {
		b.WriteString("    - (none)\n")
		return
	}
	for _, a := range d.Actions {
		fmt.Fprintf(b, "    - %s\n", formatBannerAction(a))
	}
}

func appendDigestSignalLines(b *strings.Builder, d DailyDigest) {
	var actionSigs, infoSigs []DigestAction
	for _, s := range d.Signals {
		if s.Severity == SeverityInfo {
			infoSigs = append(infoSigs, s)
		} else {
			actionSigs = append(actionSigs, s)
		}
	}
	b.WriteString("  We need to do:\n")
	if len(actionSigs) == 0 {
		b.WriteString("    - (none)\n")
	} else {
		for _, s := range actionSigs {
			fmt.Fprintf(b, "    - %s\n", formatDigestSignalLine(s))
		}
	}
	if len(infoSigs) > 0 {
		b.WriteString("  Info:\n")
		for _, s := range infoSigs {
			fmt.Fprintf(b, "    - %s\n", formatDigestSignalLine(s))
		}
	}
}

func formatDigestSignalLine(s DigestAction) string {
	act := search.Printable(s.RecommendedAction)
	if act == "" {
		act = search.Printable(s.Summary)
	}
	return fmt.Sprintf("[%s/%s] %s (%s): %s",
		search.Printable(s.Severity), search.Printable(s.ActionScope), search.Printable(s.Rule), search.Printable(s.SignalID), act)
}

func buildDailyBannerView(d *DailyDigest) *dailyBannerView {
	if d == nil {
		return nil
	}
	v := &dailyBannerView{
		Day:             search.Printable(d.Day),
		Verdict:         search.Printable(d.Verdict),
		NewRegressions:  joinPrintableList(d.NewRegressions),
		ResolvedSignals: joinPrintableList(d.ResolvedSignals),
		PersistentHigh:  joinPrintableList(d.PersistentHigh),
	}
	for _, a := range d.Actions {
		v.Actions = append(v.Actions, formatBannerAction(a))
	}
	v.Signals = formatBannerSignals(d.Signals, 5)
	v.Info = formatBannerInfoSignals(d.Signals, 5)
	return v
}

func joinPrintableList(items []string) string {
	if len(items) == 0 {
		return ""
	}
	cleaned := make([]string, 0, len(items))
	for _, item := range items {
		if c := search.Printable(strings.TrimSpace(item)); c != "" {
			cleaned = append(cleaned, c)
		}
	}
	return strings.Join(cleaned, ", ")
}

func formatBannerAction(a DigestAppliedAction) string {
	pend, rev := "", ""
	if a.InstallPending {
		pend = " [install pending]"
	}
	if a.RevertCmd != "" {
		rev = " revert=" + search.Printable(a.RevertCmd)
	}
	return fmt.Sprintf("[%s] %s (%s, %s)%s%s",
		search.Printable(a.Status), search.Printable(a.SignalID), search.Printable(a.ActionScope), search.Printable(a.ReportDate), pend, rev)
}

func formatBannerSignals(signals []DigestAction, limit int) []string {
	var out []string
	for _, s := range signals {
		if s.Severity != "high" && s.Severity != "medium" {
			continue
		}
		out = append(out, formatDigestSignalLine(s))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func formatBannerInfoSignals(signals []DigestAction, limit int) []string {
	var out []string
	for _, s := range signals {
		if s.Severity != SeverityInfo {
			continue
		}
		out = append(out, formatDigestSignalLine(s))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func writeMDDailyBanner(b *strings.Builder, d *DailyDigest) {
	v := buildDailyBannerView(d)
	if v == nil {
		return
	}
	b.WriteString("\n## Daily Direction & Actions\n\n")
	fmt.Fprintf(b, "- Direction: **%s** (%s)\n", mdCell(v.Verdict), mdCell(v.Day))
	if v.NewRegressions != "" {
		fmt.Fprintf(b, "- new_regressions: %s\n", mdCell(v.NewRegressions))
	}
	if v.ResolvedSignals != "" {
		fmt.Fprintf(b, "- resolved_signals: %s\n", mdCell(v.ResolvedSignals))
	}
	if v.PersistentHigh != "" {
		fmt.Fprintf(b, "- persistent_high: %s\n", mdCell(v.PersistentHigh))
	}
	for _, a := range v.Actions {
		fmt.Fprintf(b, "- Applied action: %s\n", mdCell(a))
	}
	for _, s := range v.Signals {
		fmt.Fprintf(b, "- Signal action: %s\n", mdCell(s))
	}
	for _, info := range v.Info {
		fmt.Fprintf(b, "- Info notice: %s\n", mdCell(info))
	}
}
