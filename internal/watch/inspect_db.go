package watch

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

type summaryRow struct {
	id       string
	modified time.Time
	uris     string
	agent    string
	parent   string
}

func loadAndDiscoverTargets(o Options, tmp string, now time.Time) (map[string]summaryRow, bool, []string, error) {
	summaries, hasSum, err := loadSummaries(o.Home, tmp)
	if err != nil {
		return nil, false, nil, err
	}
	if o.ConvID != "" {
		return summaries, hasSum, collectConvTree(o.ConvID, summaries), nil
	}
	var normDir string
	if o.Dir != "" {
		normDir = normalizePath(o.Dir)
	}
	cutoff := now.Add(-discoveryWindow)
	var targets []string
	for id, row := range summaries {
		if row.modified.IsZero() || row.modified.Before(cutoff) {
			continue
		}
		if workspaceMatchesDir(row.uris, normDir) {
			targets = append(targets, id)
		}
	}
	sort.Strings(targets)
	return summaries, hasSum, targets, nil
}

func loadSummaries(home, tmp string) (map[string]summaryRow, bool, error) {
	if home == "" || tmp == "" {
		return nil, false, nil
	}
	agyDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if _, err := os.Stat(filepath.Join(agyDir, "conversation_summaries.db")); errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	db, err := search.OpenCopy(agyDir, "conversation_summaries.db", tmp)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("watch: copy conversation_summaries.db: %w", err)
	}
	defer db.Close()
	rows, hasAgent, hasParent, err := querySummaries(db)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	return scanSummaries(rows, hasAgent, hasParent)
}

func querySummaries(db *sql.DB) (*sql.Rows, bool, bool, error) {
	cols, err := tableColumns(db, "conversation_summaries")
	if err != nil {
		return nil, false, false, err
	}
	if !cols["conversation_id"] {
		return nil, false, false, errors.New("watch: conversation_summaries missing conversation_id")
	}
	sel := "conversation_id, COALESCE(last_modified_time, ''), COALESCE(workspace_uris, '')"
	hasAgent, hasParent := cols["agent_name"], cols["parent_conversation_id"]
	if hasAgent {
		sel += ", COALESCE(agent_name, '')"
	}
	if hasParent {
		sel += ", COALESCE(parent_conversation_id, '')"
	}
	rows, err := db.Query("SELECT " + sel + " FROM conversation_summaries")
	return rows, hasAgent, hasParent, err
}

func scanSummaries(rows *sql.Rows, hasAgent, hasParent bool) (map[string]summaryRow, bool, error) {
	out := map[string]summaryRow{}
	for rows.Next() {
		var r summaryRow
		var mod any
		dest := []any{&r.id, &mod, &r.uris}
		if hasAgent {
			dest = append(dest, &r.agent)
		}
		if hasParent {
			dest = append(dest, &r.parent)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, false, err
		}
		if !validConvID(r.id) {
			continue
		}
		if t, ok := search.AgyTime(mod); ok {
			r.modified = t
		}
		out[r.id] = r
	}
	return out, true, rows.Err()
}

func collectConvTree(rootID string, summaries map[string]summaryRow) []string {
	children := map[string][]string{}
	for id, row := range summaries {
		if row.parent != "" {
			children[row.parent] = append(children[row.parent], id)
		}
	}
	seen := map[string]bool{rootID: true}
	queue := []string{rootID}
	var out []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)
		kids := children[cur]
		sort.Strings(kids)
		for _, k := range kids {
			if !seen[k] {
				seen[k] = true
				queue = append(queue, k)
			}
		}
	}
	return out
}

func workspaceMatchesDir(urisJSON, normDir string) bool {
	if normDir == "" {
		return true
	}
	var list []string
	if json.Unmarshal([]byte(urisJSON), &list) != nil {
		return false
	}
	for _, raw := range list {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "file" || u.Path == "" {
			continue
		}
		ws := normalizePath(u.Path)
		if ws == normDir || strings.HasPrefix(ws, normDir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func normalizePath(p string) string {
	clean := filepath.Clean(p)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(real)
	}
	return clean
}

func inspectSessionMeta(
	dir, convID, tmp string, summaries map[string]summaryRow, hasSum bool, targetSet map[string]bool,
) (int, []Finding, error) {
	scratch := filepath.Join(dir, ".agents", "scratch")
	if _, err := os.Stat(filepath.Join(scratch, "search.db")); errors.Is(err, os.ErrNotExist) {
		return 0, nil, nil
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
	return scanSessionMeta(db, cols, convID, summaries, hasSum, targetSet)
}

func scanSessionMeta(
	db *sql.DB, cols map[string]bool, convID string, summaries map[string]summaryRow, hasSum bool, targetSet map[string]bool,
) (int, []Finding, error) {
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
		if !shouldCheckMetaRow(id, convID, summaries, targetSet) {
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

func shouldCheckMetaRow(id, convID string, summaries map[string]summaryRow, targetSet map[string]bool) bool {
	if convID != "" {
		return targetSet[id]
	}
	if _, known := summaries[id]; known {
		return targetSet[id]
	}
	return true
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
	if lowAgent == "self" || (lowAgent == "" && strings.Contains(lowID, "self")) {
		return "self", selfSubagentCap, true
	}
	if agent != "" || parent != "" {
		if agent == "" {
			agent = "subagent"
		}
		return agent, auditSubagentCap, true
	}
	if lowID == "default" {
		return "", 0, false
	}
	if isAuditSubagentName(lowID) || !hasSum {
		return "subagent", auditSubagentCap, true
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

func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[strings.ToLower(name)] = true
	}
	return cols, rows.Err()
}

func validConvID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`)
}
