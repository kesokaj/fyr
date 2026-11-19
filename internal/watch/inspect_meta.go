package watch

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

type metaTimeSources struct {
	hasRing   bool
	hasVet    bool
	hasLedger bool
}

func inspectSessionMeta(
	home, dir, convID, tmp string, now time.Time, summaries map[string]summaryRow, hasSum bool, targetSet map[string]bool,
) (int, []Finding, error) {
	scratch := filepath.Join(dir, ".agents", "scratch")
	fi, err := os.Stat(filepath.Join(scratch, "search.db"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil, nil
	}
	var searchDBMod time.Time
	if err == nil {
		searchDBMod = fi.ModTime()
	}
	db, err := search.OpenCopy(scratch, "search.db", tmp)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, fmt.Errorf("watch: copy search.db: %w", err)
	}
	defer db.Close()
	cols, err := tableColumns(db, "session_meta")
	if err != nil || !cols["conversation_id"] || !cols["turn_count"] {
		return 0, nil, err
	}
	return scanSessionMeta(db, home, cols, convID, now, searchDBMod, summaries, hasSum, targetSet)
}

func scanSessionMeta(
	db *sql.DB, home string, cols map[string]bool, convID string, now, searchDBMod time.Time,
	summaries map[string]summaryRow, hasSum bool, targetSet map[string]bool,
) (int, []Finding, error) {
	sources := loadMetaTimeSources(db)
	rows, err := db.Query(buildSessionMetaQuery(cols))
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	checked := 0
	var findings []Finding
	for rows.Next() {
		var id, rowAgent, rowParent string
		var turns, tools int
		if err := rows.Scan(&id, &turns, &tools, &rowAgent, &rowParent); err != nil {
			return checked, nil, err
		}
		if !validConvID(id) {
			continue
		}
		if !shouldCheckMetaRow(db, sources, home, id, convID, now, searchDBMod, summaries, hasSum, targetSet) {
			continue
		}
		checked++
		count := max(turns, tools)
		if f, ok := checkBudgetRow(id, count, rowAgent, rowParent, summaries, hasSum); ok {
			findings = append(findings, f)
		}
	}
	return checked, findings, rows.Err()
}

func loadMetaTimeSources(db *sql.DB) metaTimeSources {
	var src metaTimeSources
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name IN ('watchdog_ring', 'vet_history', 'read_ledger')`)
	if err != nil {
		return src
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			continue
		}
		switch name {
		case "watchdog_ring":
			src.hasRing = true
		case "vet_history":
			src.hasVet = true
		case "read_ledger":
			src.hasLedger = true
		}
	}
	return src
}

func buildSessionMetaQuery(cols map[string]bool) string {
	toolExpr := "0"
	switch {
	case cols["tool_count"]:
		toolExpr = "COALESCE(tool_count, 0)"
	case cols["tool_calls"]:
		toolExpr = "COALESCE(tool_calls, 0)"
	}
	agentExpr := "''"
	switch {
	case cols["agent_name"]:
		agentExpr = "COALESCE(agent_name, '')"
	case cols["subagent"]:
		agentExpr = "COALESCE(subagent, '')"
	}
	parentExpr := "''"
	if cols["parent_conversation_id"] {
		parentExpr = "COALESCE(parent_conversation_id, '')"
	}
	return fmt.Sprintf("SELECT conversation_id, COALESCE(turn_count, 0), %s, %s, %s FROM session_meta", toolExpr, agentExpr, parentExpr)
}

func shouldCheckMetaRow(
	db *sql.DB, sources metaTimeSources, home, id, convID string, now, searchDBMod time.Time,
	summaries map[string]summaryRow, hasSum bool, targetSet map[string]bool,
) bool {
	if convID != "" {
		return targetSet[id]
	}
	if _, known := summaries[id]; known {
		return targetSet[id]
	}
	cutoff := now.Add(-discoveryWindow)
	if mod, ok := metaRowModTime(db, sources, home, id); ok {
		return !mod.Before(cutoff)
	}
	if hasSum {
		return false
	}
	return !searchDBMod.IsZero() && !searchDBMod.Before(cutoff)
}

func metaRowModTime(db *sql.DB, sources metaTimeSources, home, id string) (time.Time, bool) {
	var latest time.Time
	if home != "" {
		brain := filepath.Join(home, ".gemini", "antigravity-cli", "brain", id)
		for _, p := range []string{
			filepath.Join(brain, ".system_generated", "logs", "transcript.jsonl"),
			brain,
		} {
			if fi, err := os.Stat(p); err == nil && fi.ModTime().After(latest) {
				latest = fi.ModTime()
			}
		}
	}
	if ts := maxConvUnixTime(db, sources, id); ts > 0 {
		if t := time.Unix(ts, 0); t.After(latest) {
			latest = t
		}
	}
	return latest, !latest.IsZero()
}

func maxConvUnixTime(db *sql.DB, sources metaTimeSources, id string) int64 {
	var best int64
	queries := make([]string, 0, 3)
	if sources.hasRing {
		queries = append(queries, `SELECT COALESCE(MAX(ts), 0) FROM watchdog_ring WHERE conversation_id = ?`)
	}
	if sources.hasVet {
		queries = append(queries, `SELECT COALESCE(MAX(created_at), 0) FROM vet_history WHERE conversation_id = ?`)
	}
	if sources.hasLedger {
		queries = append(queries, `SELECT COALESCE(MAX(updated_at), 0) FROM read_ledger WHERE conversation_id = ?`)
	}
	for _, q := range queries {
		var ts int64
		if db.QueryRow(q, id).Scan(&ts) == nil && ts > best {
			best = ts
		}
	}
	return best
}

func checkBudgetRow(
	id string, count int, rowAgent, rowParent string, summaries map[string]summaryRow, hasSum bool,
) (Finding, bool) {
	role, capLimit, ok := resolveSubagentBudget(id, rowAgent, rowParent, summaries, hasSum)
	if !ok || count*100 < capLimit*budgetWarnPct {
		return Finding{}, false
	}
	return Finding{
		Kind:   kindBudgetNearLimit,
		Target: id,
		Action: fmt.Sprintf("subagent %s at %d/%d tool calls (>= 80%% cap); instruct it to write final artifact now", role, count, capLimit),
	}, true
}

func resolveSubagentBudget(id, rowAgent, rowParent string, summaries map[string]summaryRow, hasSum bool) (string, int, bool) {
	agent, parent := strings.TrimSpace(rowAgent), strings.TrimSpace(rowParent)
	if sum, ok := summaries[id]; ok {
		if agent == "" {
			agent = strings.TrimSpace(sum.agent)
		}
		if parent == "" {
			parent = strings.TrimSpace(sum.parent)
		}
		if agent == "" && parent == "" {
			return "", 0, false
		}
	}
	lowAgent, lowID := strings.ToLower(agent), strings.ToLower(id)
	if agent != "" {
		if isAuditSubagentName(lowAgent) {
			return agent, auditSubagentCap, true
		}
		return agent, selfSubagentCap, true
	}
	if parent != "" {
		if isAuditSubagentName(lowID) {
			return "subagent", auditSubagentCap, true
		}
		return "self", selfSubagentCap, true
	}
	if lowID == "default" {
		return "", 0, false
	}
	if isAuditSubagentName(lowID) {
		return "subagent", auditSubagentCap, true
	}
	if strings.Contains(lowID, "self") {
		return "self", selfSubagentCap, true
	}
	if !hasSum && (strings.HasPrefix(lowID, "builder-") || strings.HasPrefix(lowID, "coder-")) {
		return id, selfSubagentCap, true
	}
	return "", 0, false
}

func isAuditSubagentName(low string) bool {
	for _, tok := range []string{"reviewer", "security", "verifier", "explorer", "dreamer", "documentation", "research", "subagent", "sub-"} {
		if strings.Contains(low, tok) {
			return true
		}
	}
	return false
}
