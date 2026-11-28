package search

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/trail"
)

// LocalRetentionRow is one top-level Antigravity conversation summary read
// from a local snapshot of conversation_summaries.db for retention fallback.
type LocalRetentionRow struct {
	ID            string
	ProjectID     string
	WorkspaceRoot string
	UpdatedAt     time.Time
	Archived      bool
}

type searchRetResolvedWS struct {
	root   string
	exists bool
}

// ConversationWorkspaceRoot resolves the normalized workspace root for convID
// from a read-only snapshot of Antigravity's conversation_summaries.db.
func ConversationWorkspaceRoot(home, convID string) (root string, exists bool, err error) {
	if !searchRetValidConvID(convID) {
		return "", false, nil
	}
	err = withAgyTemp(home, "ret-ws-", func(src, tmp string) error {
		var rerr error
		root, exists, rerr = searchRetReadWorkspace(src, tmp, convID)
		return rerr
	})
	return root, exists, err
}

// ListOrphanChildConversations returns sorted unique IDs of child conversations
// whose parent_conversation_id is absent from conversation_summaries and whose
// last_modified_time is strictly before olderThan.
func ListOrphanChildConversations(home string, olderThan time.Time) ([]string, error) {
	var out []string
	err := withAgyTemp(home, "ret-orphans-", func(src, tmp string) error {
		var rerr error
		out, rerr = searchRetReadOrphans(src, tmp, olderThan)
		return rerr
	})
	return out, err
}

// ListLocalRetentionRows reads top-level conversation summaries from a local
// snapshot of conversation_summaries.db for SessionStart Path B fallback.
func ListLocalRetentionRows(home string) ([]LocalRetentionRow, error) {
	var out []LocalRetentionRow
	err := withAgyTemp(home, "ret-rows-", func(src, tmp string) error {
		var rerr error
		out, rerr = searchRetReadLocalRows(src, tmp)
		return rerr
	})
	return out, err
}

func searchRetValidConvID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`)
}

func searchRetReadWorkspace(src, tmp, convID string) (string, bool, error) {
	db, err := openCopy(src, "conversation_summaries.db", tmp)
	if err != nil {
		return "", false, err
	}
	defer db.Close()
	var uris string
	err = db.QueryRow(
		`SELECT COALESCE(workspace_uris, '') FROM conversation_summaries WHERE conversation_id = ?`,
		convID,
	).Scan(&uris)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading conversation workspace %s: %w", convID, err)
	}
	root, exists := searchRetResolveWorkspace(uris, nil)
	return root, exists, nil
}

func searchRetResolveWorkspace(uris string, cache map[string]searchRetResolvedWS) (string, bool) {
	if strings.TrimSpace(uris) == "" {
		return GlobalWorkspace, true
	}
	var list []string
	if json.Unmarshal([]byte(uris), &list) != nil {
		return "", false
	}
	if len(list) == 0 {
		return GlobalWorkspace, true
	}
	for _, u := range list {
		if ws, ok := parseFileWorkspace(u); ok {
			return searchRetNormalizeDir(ws, cache)
		}
	}
	return "", false
}

func searchRetNormalizeDir(ws string, cache map[string]searchRetResolvedWS) (string, bool) {
	clean := filepath.Clean(ws)
	if cache != nil {
		if cached, ok := cache[clean]; ok {
			return cached.root, cached.exists
		}
	}
	fi, err := os.Stat(clean)
	res := searchRetResolvedWS{root: clean, exists: err == nil && fi.IsDir()}
	if res.exists {
		if m, _, rerr := trail.Roots(clean); rerr == nil && m != "" {
			res.root = m
		}
	}
	if cache != nil {
		cache[clean] = res
	}
	return res.root, res.exists
}

func searchRetSummaryColumns(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(conversation_summaries)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		cols[strings.ToLower(name)] = true
	}
	return cols, rows.Err()
}

func searchRetReadOrphans(src, tmp string, olderThan time.Time) ([]string, error) {
	db, err := openCopy(src, "conversation_summaries.db", tmp)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	cols, err := searchRetSummaryColumns(db)
	if err != nil || !cols["parent_conversation_id"] {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT COALESCE(conversation_id, ''), last_modified_time
		 FROM conversation_summaries
		 WHERE COALESCE(parent_conversation_id, '') != ''
		   AND parent_conversation_id NOT IN (
		     SELECT conversation_id FROM conversation_summaries WHERE conversation_id IS NOT NULL
		   )`,
	)
	if err != nil {
		return nil, fmt.Errorf("reading orphan child conversations: %w", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		var modified any
		if err := rows.Scan(&id, &modified); err != nil {
			return nil, err
		}
		if !searchRetValidConvID(id) {
			continue
		}
		if t, ok := agyTime(modified); ok && t.Before(olderThan) {
			seen[id] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(seen) == 0 {
		return nil, nil
	}
	return slices.Sorted(maps.Keys(seen)), nil
}

func searchRetLocalRowsQuery(cols map[string]bool) string {
	projectExpr := "''"
	if cols["project_id"] {
		projectExpr = "COALESCE(project_id, '')"
	}
	archivedExpr := "0"
	if cols["archived"] {
		archivedExpr = "COALESCE(archived, 0)"
	}
	where := ""
	if cols["parent_conversation_id"] {
		where = " WHERE COALESCE(parent_conversation_id, '') = ''"
	}
	return "SELECT COALESCE(conversation_id, ''), " + projectExpr +
		", COALESCE(workspace_uris, ''), last_modified_time, " + archivedExpr +
		" FROM conversation_summaries" + where
}

func searchRetReadLocalRows(src, tmp string) ([]LocalRetentionRow, error) {
	db, err := openCopy(src, "conversation_summaries.db", tmp)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	cols, err := searchRetSummaryColumns(db)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(searchRetLocalRowsQuery(cols))
	if err != nil {
		return nil, fmt.Errorf("reading local retention summaries: %w", err)
	}
	defer rows.Close()
	cache := map[string]searchRetResolvedWS{}
	var out []LocalRetentionRow
	for rows.Next() {
		var id, projectID, uris string
		var modified, archivedVal any
		if err := rows.Scan(&id, &projectID, &uris, &modified, &archivedVal); err != nil {
			return nil, err
		}
		t, ok := agyTime(modified)
		if !searchRetValidConvID(id) || !ok {
			continue
		}
		wsRoot, _ := searchRetResolveWorkspace(uris, cache)
		out = append(out, LocalRetentionRow{
			ID:            id,
			ProjectID:     projectID,
			WorkspaceRoot: wsRoot,
			UpdatedAt:     t,
			Archived:      searchRetBool(archivedVal),
		})
	}
	return out, rows.Err()
}

func searchRetBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		return s == "1" || s == "true"
	case []byte:
		return searchRetBool(string(x))
	}
	return false
}
