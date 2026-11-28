package search

import (
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/symgraph"
)

func (sc *scope) sessionActiveFiles(sessionID string, extra map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range extra {
		if v {
			out[strings.TrimPrefix(filepath.ToSlash(k), "./")] = true
		}
	}
	if sessionID == "" || sc.st == nil {
		return out
	}
	rows, err := sc.st.db.Query(`SELECT DISTINCT resolved_path FROM read_ledger WHERE conversation_id = ? AND start_line >= -1`, sessionID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			out[filepath.ToSlash(p)] = true
			for _, root := range sc.projects {
				if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
					out[filepath.ToSlash(rel)] = true
				}
			}
		}
	}
	return out
}

func parseQueryTerms(query string) []string {
	return symgraph.ParseQueryTerms(query)
}

func matchesPromptTerms(s RankedSymbol, terms []string) bool {
	return symgraph.MatchesPromptTerms(s, terms)
}
