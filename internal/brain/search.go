package brain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// Hit is one record a search found.
type Hit struct {
	Record
	Label   string // the ID, prefixed with its scope when not the project's ("global:", "<name>:")
	Snippet string // one printable line, matched terms in »«
}

// Search ranks records by BM25 over title (weighted twice) and body: the
// project's and the global scope's, or with Global every scope. Records
// holding every word rank first, then the remaining slots go to records
// holding any of them, as `fyr search` does for files. kind is a search
// --kind: a record kind filters, "trail" or "" takes every record, and a
// file-only kind (code, doc) none.
//
// It creates no store: with no brain.db and no trail.jsonl to load there is
// nothing to search. A project's changed trail.jsonl is loaded first, and
// an unmigrated project (D16) is searched without loading.
func Search(o Options, words []string, kind string, limit int) ([]Hit, error) {
	if kind != "" && kind != "trail" && !knownKind(kind) {
		return nil, nil
	}
	var p *session
	if scope, top, err := trail.Roots(o.Dir); err == nil {
		p = &session{scope: scope, top: top}
	} else if !o.Global {
		// Outside a checkout the file search refuses; there is no project
		// scope to answer from either.
		return nil, nil
	}
	_, statErr := os.Lstat(Path(o.Home))
	hasTrail := false
	if p != nil {
		if err := guardExportPath(p.top); err != nil {
			return nil, err
		}
		_, err := os.Lstat(trailPath(p.top))
		hasTrail = err == nil
	}
	if errors.Is(statErr, os.ErrNotExist) && !hasTrail {
		return nil, nil
	}
	st, err := openStore(o.Home)
	if err != nil {
		return nil, err
	}
	defer st.close()
	if p != nil && hasTrail {
		p.st = st
		if err := p.sync(); err != nil {
			return nil, err
		}
	}
	project := ""
	if p != nil {
		project = p.scope
	}
	var hits []Hit
	seen := map[int64]bool{}
	for _, op := range []string{" ", " OR "} {
		if len(hits) >= limit || (op == " OR " && len(words) < 2) {
			break
		}
		q := search.FTSQuery(words, op)
		if q == "" {
			break
		}
		sqlq := `SELECT r.rid, r.scope, r.id, r.kind, r.status, r.date, r.title, r.project,
				snippet(records_fts, 1, char(1), char(2), '…', 24)
			FROM records_fts JOIN records r ON r.rid = records_fts.rowid
			WHERE records_fts MATCH ?`
		args := []any{q}
		if !o.Global {
			sqlq += ` AND r.scope IN (?, '')`
			args = append(args, project)
		}
		if kind != "" && kind != "trail" {
			sqlq += ` AND r.kind = ?`
			args = append(args, kind)
		}
		sqlq += ` ORDER BY bm25(records_fts, 2.0, 1.0), r.id`
		rows, err := st.db.Query(sqlq, args...)
		if err != nil {
			return nil, fmt.Errorf("brain: %w", err)
		}
		for rows.Next() && len(hits) < limit {
			var h Hit
			var rid int64
			var snip string
			if err := rows.Scan(&rid, &h.Scope, &h.ID, &h.Kind, &h.Status, &h.Date, &h.Title, &h.Project, &snip); err != nil {
				rows.Close()
				return nil, err
			}
			if seen[rid] {
				continue
			}
			seen[rid] = true
			h.Snippet = search.CleanSnippet(snip)
			h.Label = label(h.Scope, project, o.Global) + h.ID
			hits = append(hits, h)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return hits, nil
}

// label is the scope prefix of a hit's ID: none for the project's own
// records, "global:" for the global scope, and with --global the base name
// of any other project's root, as file hits print it.
func label(scope, project string, global bool) string {
	switch {
	case scope == "":
		return "global:"
	case scope == project && !global:
		return ""
	}
	return filepath.Base(scope) + ":"
}

// FormatHit is a record hit's line, `ID  kind status  date  title`, as a file
// hit prints `path:line  kind status  date`; printable.
func FormatHit(h Hit) string {
	kind := h.Kind
	if h.Status != "" {
		kind += " " + h.Status
	}
	return search.Printable(strings.Join([]string{h.Label, kind, h.Date, h.Title}, "  "))
}
