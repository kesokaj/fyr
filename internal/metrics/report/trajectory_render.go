package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/search"
)

// WriteTrajectoriesJSON encodes trajectories as indented JSON to w.
func WriteTrajectoriesJSON(w io.Writer, list []SessionTrajectory) error {
	if list == nil {
		list = []SessionTrajectory{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(list)
}

// WriteTrajectoriesText renders trajectories in human-readable form to w.
func WriteTrajectoriesText(w io.Writer, list []SessionTrajectory) {
	if len(list) == 0 {
		fmt.Fprintln(w, "(no sessions)")
		return
	}
	for i, t := range list {
		if i > 0 {
			fmt.Fprintln(w)
		}
		writeOneTrajectoryText(w, t)
	}
}

func writeOneTrajectoryText(w io.Writer, t SessionTrajectory) {
	status := printableList(t.Anomalies, "OK")
	proj := search.Printable(RedactHomePath(t.Project))
	if proj == "" {
		proj = "-"
	}
	fmt.Fprintf(w, "%s  %s  project=%s  model=%s  dur=%ds  steps=%d  peak=%s  cache=%.0f%%  [%s]\n",
		search.Printable(shortID(t.SessionID)), search.Printable(t.StartedAt), proj, search.Printable(t.Model), t.DurationSec, t.Steps,
		fmtTokens(t.PeakContextTok), t.CacheHitPct, status)
	if len(t.TurnTokens) > 0 {
		parts := make([]string, len(t.TurnTokens))
		for i, tok := range t.TurnTokens {
			parts[i] = fmt.Sprintf("t%d=%s", i+1, fmtTokens(tok))
		}
		knee := t.KneeTokens
		if knee <= 0 {
			knee = models.ContextKneeTokens(t.Model)
		}
		fmt.Fprintf(w, "  turns: %s (knee %s)\n", strings.Join(parts, " -> "), fmtTokens(knee))
	}
	for _, sub := range t.Subagents {
		writeSubagentText(w, sub)
	}
}

func writeSubagentText(w io.Writer, sub SessionTrajectory) {
	subStatus := printableList(sub.Anomalies, "OK")
	agent := search.Printable(sub.AgentType)
	if agent == "" {
		agent = "subagent"
	}
	fmt.Fprintf(w, "  └─ %s (%s)  model=%s  dur=%ds  steps=%d  peak=%s  cache=%.0f%%  [%s]\n",
		search.Printable(shortID(sub.SessionID)), agent, search.Printable(sub.Model), sub.DurationSec, sub.Steps,
		fmtTokens(sub.PeakContextTok), sub.CacheHitPct, subStatus)
}

func printableList(items []string, empty string) string {
	if len(items) == 0 {
		return empty
	}
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = search.Printable(item)
	}
	return strings.Join(parts, ",")
}

func fmtTokens(n int64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
