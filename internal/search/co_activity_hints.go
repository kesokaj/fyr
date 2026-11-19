package search

import (
	"fmt"
	"sort"
	"strings"
)

func (sc *scope) activeCoChangeHints(paths map[string]bool) []string {
	if len(paths) == 0 {
		return nil
	}
	if hints := sc.queryCoChangeHints(paths, "co_modified", 3, 5); len(hints) > 0 {
		return hints
	}
	return sc.queryCoChangeHints(paths, "", 3, 5)
}

func (sc *scope) queryCoChangeHints(paths map[string]bool, kind string, minCount, limit int) []string {
	if len(paths) == 0 || sc == nil || sc.st == nil {
		return nil
	}
	pathList := sortedMapKeys(paths)
	if len(pathList) > 200 {
		pathList = pathList[:200]
	}
	ph := placeholders(len(pathList))
	q := `SELECT file_a, file_b, kind, count FROM co_activity WHERE count >= ?`
	args := []any{minCount}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	q += ` AND (file_a IN (` + ph + `) OR file_b IN (` + ph + `)) ORDER BY count DESC, file_a, file_b`
	for _, p := range pathList {
		args = append(args, p)
	}
	for _, p := range pathList {
		args = append(args, p)
	}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	return sc.scanCoChangeHintRows(q, args)
}

func (sc *scope) scanCoChangeHintRows(q string, args []any) []string {
	rows, err := sc.st.db.Query(q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a, b, kind string
		var count int
		if err := rows.Scan(&a, &b, &kind, &count); err == nil {
			out = append(out, fmt.Sprintf("%s <-> %s (%s: %d)", a, b, kind, count))
		}
	}
	return out
}

func (sc *scope) activeLinkedRecords(paths map[string]bool, brainRefs func(string) []LinkedRecord) []LinkedRecord {
	seen := map[string]LinkedRecord{}
	for _, r := range sc.linkedRecordsForPaths(paths, nil) {
		seen[r.ID] = r
	}
	if brainRefs != nil {
		for _, p := range sortedMapKeys(paths) {
			for _, r := range brainRefs(p) {
				if r.ID != "" {
					seen[r.ID] = r
				}
			}
		}
		refIDs := make([]string, 0, len(seen))
		for id, cur := range seen {
			if cur.Status == "ref" {
				refIDs = append(refIDs, id)
			}
		}
		sort.Strings(refIDs)
		for _, id := range refIDs {
			for _, r := range brainRefs(id) {
				if r.ID == id {
					seen[id] = r
				}
			}
		}
	}
	var out []LinkedRecord
	for _, r := range seen {
		if isActiveLinkedStatus(r.Status) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func isActiveLinkedStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "ref", "open", "accepted", "current":
		return true
	default:
		return false
	}
}
