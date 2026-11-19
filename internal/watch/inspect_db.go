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
	"github.com/kesokaj/fyr/internal/trail"
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
	cutoff := now.Add(-discoveryWindow)
	var targets []string
	for id, row := range summaries {
		if row.modified.IsZero() || row.modified.Before(cutoff) {
			continue
		}
		if workspaceMatchesDir(row.uris, o.Dir) {
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

func workspaceMatchesDir(urisJSON, dir string) bool {
	if dir == "" {
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
		if pathSameOrUnder(u.Path, dir) {
			return true
		}
	}
	return false
}

func pathSameOrUnder(candidate, dir string) bool {
	for p := filepath.Clean(candidate); ; {
		if trail.SameDir(p, dir) {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
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
