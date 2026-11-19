package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
	proj := printable(redactHomePath(t.Project))
	if proj == "" {
		proj = "-"
	}
	fmt.Fprintf(w, "%s  %s  project=%s  model=%s  dur=%ds  steps=%d  peak=%s  cache=%.0f%%  [%s]\n",
		printable(shortID(t.SessionID)), printable(t.StartedAt), proj, printable(t.Model), t.DurationSec, t.Steps,
		fmtTokens(t.PeakContextTok), t.CacheHitPct, status)
	if len(t.TurnTokens) > 0 {
		parts := make([]string, len(t.TurnTokens))
		for i, tok := range t.TurnTokens {
			parts[i] = fmt.Sprintf("t%d=%s", i+1, fmtTokens(tok))
		}
		fmt.Fprintf(w, "  turns: %s (knee %s)\n", strings.Join(parts, " -> "), fmtTokens(ContextKneeTokens))
	}
	for _, sub := range t.Subagents {
		writeSubagentText(w, sub)
	}
}

func writeSubagentText(w io.Writer, sub SessionTrajectory) {
	subStatus := printableList(sub.Anomalies, "OK")
	agent := printable(sub.AgentType)
	if agent == "" {
		agent = "subagent"
	}
	fmt.Fprintf(w, "  └─ %s (%s)  model=%s  dur=%ds  steps=%d  peak=%s  cache=%.0f%%  [%s]\n",
		printable(shortID(sub.SessionID)), agent, printable(sub.Model), sub.DurationSec, sub.Steps,
		fmtTokens(sub.PeakContextTok), sub.CacheHitPct, subStatus)
}

func printableList(items []string, empty string) string {
	if len(items) == 0 {
		return empty
	}
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = printable(item)
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
