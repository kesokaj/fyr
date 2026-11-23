package watch

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

type metaCheckInput struct {
	home, dir, convID, tmp string
	idleSec                int
	now                    time.Time
	summaries              map[string]summaryRow
	hasSum                 bool
	targetSet              map[string]bool
	procs                  []ProcInfo
}

type metaTimeSources struct {
	hasRing   bool
	hasVet    bool
	hasLedger bool
}

func inspectSessionMeta(in metaCheckInput) (int, []Finding, error) {
	scratch := filepath.Join(in.dir, ".agents", "scratch")
	fi, err := os.Lstat(filepath.Join(scratch, "search.db"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil, nil
	}
	if inspectable, hasLive := procTableLiveness(in.procs); inspectable && !hasLive {
		return 0, nil, nil
	}
	var searchDBMod time.Time
	if err == nil {
		searchDBMod = fi.ModTime()
	}
	db, directRO, err := openSearchMetaDB(scratch, in.tmp)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	n, findings, scanErr := runSessionMetaScan(db, in, searchDBMod)
	_ = db.Close()
	if scanErr == nil || !directRO || in.tmp == "" || !isSQLiteROFallbackErr(scanErr) {
		return n, findings, scanErr
	}
	return inspectSessionMetaCopyFallback(scratch, in, searchDBMod)
}

func inspectSessionMetaCopyFallback(scratch string, in metaCheckInput, searchDBMod time.Time) (int, []Finding, error) {
	db, err := openMetaCopy(scratch, "search.db", in.tmp)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, fmt.Errorf("watch: copy search.db: %w", err)
	}
	defer db.Close()
	return runSessionMetaScan(db, in, searchDBMod)
}

func runSessionMetaScan(db *sql.DB, in metaCheckInput, searchDBMod time.Time) (int, []Finding, error) {
	cols, err := tableColumns(db, "session_meta")
	if err != nil || !cols["conversation_id"] || !cols["turn_count"] {
		return 0, nil, err
	}
	return scanSessionMeta(db, cols, in, searchDBMod)
}

func scanSessionMeta(db *sql.DB, cols map[string]bool, in metaCheckInput, searchDBMod time.Time) (int, []Finding, error) {
	sources := loadMetaTimeSources(db)
	ended := openMetricsEndedLookup(in.home)
	defer ended.close()
	rows, err := db.Query(buildSessionMetaQuery(cols))
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	checked := 0
	var findings []Finding
	for rows.Next() {
		var id, rowRole, rowAgent, rowParent string
		var turns, tools int
		var rowUpdated any
		if err := rows.Scan(&id, &turns, &tools, &rowRole, &rowAgent, &rowParent, &rowUpdated); err != nil {
			return checked, nil, err
		}
		if !validConvID(id) || !isActiveMetaRow(db, sources, ended, in, id, rowUpdated, searchDBMod) {
			continue
		}
		checked++
		if f, ok := checkBudgetRow(id, max(turns, tools), rowRole, rowAgent, rowParent, in.summaries, in.hasSum); ok {
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
	case cols["tool_calls"] && cols["tool_count"]:
		toolExpr = "MAX(COALESCE(tool_calls, 0), COALESCE(tool_count, 0))"
	case cols["tool_calls"]:
		toolExpr = "COALESCE(tool_calls, 0)"
	case cols["tool_count"]:
		toolExpr = "COALESCE(tool_count, 0)"
	}
	roleExpr := "''"
	if cols["agent_role"] {
		roleExpr = "COALESCE(agent_role, '')"
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
	updatedExpr := "''"
	if cols["updated_at"] {
		updatedExpr = "COALESCE(updated_at, '')"
	}
	return fmt.Sprintf(
		"SELECT conversation_id, COALESCE(turn_count, 0), %s, %s, %s, %s, %s FROM session_meta",
		toolExpr, roleExpr, agentExpr, parentExpr, updatedExpr,
	)
}

func activeMetaWindow(idleSec int) time.Duration {
	return time.Duration(max(idleSec*2, minActiveMetaWindowSec)) * time.Second
}

func isActiveMetaRow(
	db *sql.DB, sources metaTimeSources, ended *metricsEndedLookup,
	in metaCheckInput, id string, rowUpdated any, searchDBMod time.Time,
) bool {
	if in.convID != "" {
		return in.targetSet[id] && !ended.isEnded(id)
	}
	if _, known := in.summaries[id]; known && !in.targetSet[id] {
		return false
	}
	if ended.isEnded(id) {
		return false
	}
	cutoff := in.now.Add(-activeMetaWindow(in.idleSec))
	if lastAct, ok := metaRowLastActivity(db, sources, in.home, id, rowUpdated, in.summaries); ok {
		return !lastAct.Before(cutoff)
	}
	if in.hasSum {
		return false
	}
	return !searchDBMod.IsZero() && !searchDBMod.Before(cutoff)
}

func metaRowLastActivity(
	db *sql.DB, sources metaTimeSources, home, id string, rowUpdated any, summaries map[string]summaryRow,
) (time.Time, bool) {
	var latest time.Time
	if t, ok := parseMetaTimestamp(rowUpdated); ok && t.After(latest) {
		latest = t
	}
	hasSumMod := false
	if sum, ok := summaries[id]; ok && !sum.modified.IsZero() {
		hasSumMod = true
		if sum.modified.After(latest) {
			latest = sum.modified
		}
	}
	if mod, ok := metaRowModTime(db, sources, home, id, !hasSumMod && latest.IsZero()); ok && mod.After(latest) {
		latest = mod
	}
	return latest, !latest.IsZero()
}

func metaRowModTime(db *sql.DB, sources metaTimeSources, home, id string, allowBrainDir bool) (time.Time, bool) {
	var latest time.Time
	if home != "" {
		brain := filepath.Join(home, ".gemini", "antigravity-cli", "brain", id)
		trPath := filepath.Join(brain, ".system_generated", "logs", "transcript.jsonl")
		if fi, err := os.Stat(trPath); err == nil {
			latest = fi.ModTime()
		} else if allowBrainDir {
			if fi, err := os.Stat(brain); err == nil {
				latest = fi.ModTime()
			}
		}
	}
	if ts := maxConvUnixTime(db, sources, id); ts > 0 {
		if t, ok := unixToTime(ts); ok && t.After(latest) {
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

func parseMetaTimestamp(v any) (time.Time, bool) {
	switch x := v.(type) {
	case nil:
		return time.Time{}, false
	case int64:
		return unixToTime(x)
	case float64:
		return unixToTime(int64(x))
	case time.Time:
		return search.AgyTime(x)
	case string:
		return parseMetaTimeString(x)
	case []byte:
		return parseMetaTimeString(string(x))
	default:
		return time.Time{}, false
	}
}

func parseMetaTimeString(raw string) (time.Time, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return unixToTime(n)
	}
	return search.AgyTime(s)
}

func unixToTime(n int64) (time.Time, bool) {
	if n <= 0 {
		return time.Time{}, false
	}
	if n >= 1_000_000_000_000 {
		return time.UnixMilli(n), true
	}
	return time.Unix(n, 0), true
}

func checkBudgetRow(
	id string, count int, rowRole, rowAgent, rowParent string, summaries map[string]summaryRow, hasSum bool,
) (Finding, bool) {
	role, capLimit, ok := resolveSubagentBudget(id, rowRole, rowAgent, rowParent, summaries, hasSum)
	if !ok || count*100 < capLimit*budgetWarnPct {
		return Finding{}, false
	}
	return Finding{
		Kind:   kindBudgetNearLimit,
		Target: id,
		Action: fmt.Sprintf("subagent %s at %d/%d tool calls (>= 80%% cap); instruct it to write final artifact now", role, count, capLimit),
	}, true
}

func resolveSubagentBudget(
	id, rowRole, rowAgent, rowParent string, summaries map[string]summaryRow, hasSum bool,
) (string, int, bool) {
	roleField, agent, parent := strings.TrimSpace(rowRole), strings.TrimSpace(rowAgent), strings.TrimSpace(rowParent)
	if agent == "" && roleField != "" && !strings.EqualFold(roleField, "self") && !strings.EqualFold(roleField, "main") {
		agent = roleField
	}
	if sum, ok := summaries[id]; ok {
		if agent == "" {
			agent = strings.TrimSpace(sum.agent)
		}
		if parent == "" {
			parent = strings.TrimSpace(sum.parent)
		}
		if roleField == "" && agent == "" && parent == "" {
			return "", 0, false
		}
	}
	if agent == "" {
		agent = roleField
	}
	if strings.EqualFold(agent, "main") {
		return "", 0, false
	}
	if agent != "" {
		_, capLimit := search.SubagentBudgetCap(agent)
		return agent, capLimit, true
	}
	return resolveAnonymousSubagentBudget(id, parent, hasSum)
}

func resolveAnonymousSubagentBudget(id, parent string, hasSum bool) (string, int, bool) {
	lowID := strings.ToLower(id)
	if parent != "" {
		return subagentRoleByID(lowID, "self")
	}
	if lowID == "default" {
		return "", 0, false
	}
	if isAuditSubagentName(lowID) || strings.Contains(lowID, "self") {
		return subagentRoleByID(lowID, "self")
	}
	if !hasSum && search.IsBuilderSubagentRole(lowID) {
		_, capLimit := search.SubagentBudgetCap(lowID)
		return id, capLimit, true
	}
	return "", 0, false
}

func subagentRoleByID(lowID, fallback string) (string, int, bool) {
	if isAuditSubagentName(lowID) {
		_, capLimit := search.SubagentBudgetCap("subagent")
		return "subagent", capLimit, true
	}
	_, capLimit := search.SubagentBudgetCap(fallback)
	return fallback, capLimit, true
}

func isAuditSubagentName(low string) bool {
	for _, tok := range []string{"reviewer", "security", "verifier", "explorer", "dreamer", "documentation", "research", "subagent", "sub-"} {
		if strings.Contains(low, tok) {
			return true
		}
	}
	return false
}
