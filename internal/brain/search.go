package brain

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

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
// project's and the global scope's, with Global also every registered
// project's (searchScopes). Records
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
	st, project, scopes, err := openScopes(o)
	if err != nil || st == nil {
		return nil, err
	}
	defer st.close()
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
		sqlq, args := searchQuery(o, q, kind, scopes)
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
	decorateMemoryHits(st, o, hits)
	return hits, nil
}

// searchQuery is one ranking pass's SQL and arguments: q over scopes, of
// one kind when kind is a record kind.
func searchQuery(o Options, q, kind string, scopes []string) (string, []any) {
	sqlq := `SELECT r.rid, r.scope, r.id, r.kind, r.status, r.date, r.title, r.project,
				snippet(records_fts, 1, char(1), char(2), '…', 24)
			FROM records_fts JOIN records r ON r.rid = records_fts.rowid
			WHERE records_fts MATCH ?`
	args := []any{q}
	var f string
	f, args = scopeFilter(scopes, args)
	sqlq += f
	f, args = kindFilter(kind, args)
	sqlq += f
	// BM25 (negative: lower is better) weighted up to 10% by recency,
	// fading linearly over a year: of two near-equal matches the newer
	// record, the trail's current state, comes first; a clearly better
	// older match still wins (D31).
	sqlq += ` ORDER BY bm25(records_fts, 2.0, 1.0) * (1 + 0.1 * min(1, max(0, 1 - (julianday(?) - julianday(r.date)) / 365.0))), r.id`
	return sqlq, append(args, o.now().Format("2006-01-02"))
}

// openForSearch opens the store for a search from o.Dir and returns the
// project's scope ("" outside a checkout). A nil store means there is
// nothing to search: no brain.db (nothing is created), or outside a
// checkout without --global, where the file search refuses too.
func openForSearch(o Options) (*store, string, error) {
	var p *session
	if scope, top, err := trail.Roots(o.Dir); err == nil {
		p = &session{scope: scope, top: top}
	} else if !o.Global {
		return nil, "", nil
	}
	if p != nil {
		if err := guardExportPath(p.top); err != nil {
			return nil, "", err
		}
	}
	if _, err := os.Lstat(Path(o.Home)); errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	st, err := openStore(o.Home)
	if err != nil {
		return nil, "", err
	}
	project := ""
	if p != nil {
		project = p.scope
	}
	return st, project, nil
}

func decorateMemoryHits(st *store, o Options, hits []Hit) {
	_, defTop, _ := trail.Roots(o.Dir)
	for i := range hits {
		if hits[i].Kind != "memory" {
			continue
		}
		var body, updated string
		if err := st.db.QueryRow(`SELECT body, updated FROM records WHERE scope = ? AND id = ?`, hits[i].Scope, hits[i].ID).Scan(&body, &updated); err != nil {
			continue
		}
		top := defTop
		if hits[i].Scope != "" {
			if fi, err := os.Stat(hits[i].Scope); err == nil && fi.IsDir() {
				top = hits[i].Scope
			}
		}
		r := hits[i].Record
		r.Body = body
		r.Updated = updated
		if stale := MemoryStalenessTag(top, r); stale != "" {
			if !strings.Contains(hits[i].Title, stale) {
				hits[i].Title += " " + stale
			}
			if hits[i].Snippet != "" && !strings.Contains(hits[i].Snippet, stale) {
				hits[i].Snippet += " " + stale
			}
		}
	}
}

// OpenRecords returns the open records (the status table's "Open when"):
// the project's and the global scope's, with Global also every registered
// project's (searchScopes; D30).
func OpenRecords(o Options, kind string, limit int) ([]Hit, error) {
	if kind != "" && kind != "trail" && !knownKind(kind) {
		return nil, nil
	}
	st, project, scopes, err := openScopes(o)
	if err != nil || st == nil {
		return nil, err
	}
	defer st.close()
	sqlq := `SELECT r.scope, r.id, r.kind, r.status, r.date, r.title, r.project, r.updated FROM records r WHERE ` + strings.ReplaceAll(openSQL, "kind", "r.kind")
	var args []any
	var f string
	f, args = scopeFilter(scopes, args)
	sqlq += f
	f, args = kindFilter(kind, args)
	sqlq += f + ` ORDER BY r.date DESC, r.updated DESC, r.id DESC`
	rows, err := st.db.Query(sqlq, args...)
	if err != nil {
		return nil, fmt.Errorf("brain: %w", err)
	}
	defer rows.Close()
	var hits []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Scope, &h.ID, &h.Kind, &h.Status, &h.Date, &h.Title, &h.Project, &h.Updated); err != nil {
			return nil, err
		}
		h.Label = label(h.Scope, project, o.Global) + h.ID
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}
	sort.Slice(hits, func(i, j int) bool { return recordDescLess(hits[i].Record, hits[j].Record) })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// label is the scope prefix of a hit's ID: none for the project's own
// records, "global:" for the global scope, and any other project's full
// root: two clones of one base name must not print alike (security review
// 2026-10-25, finding 6).
func label(scope, project string, global bool) string {
	switch {
	case scope == "":
		return "global:"
	case scope == project && !global:
		return ""
	}
	return scope + ":"
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

// LastRetro is when the learning loop last ran by the brain store: the newest
// update of a global retro record (each /retro and /distill stores one as the
// cross-project index), false with none. It creates no store (D32).
func LastRetro(home string) (time.Time, bool) {
	if _, err := os.Lstat(Path(home)); err != nil {
		return time.Time{}, false
	}
	st, err := openStore(home)
	if err != nil {
		return time.Time{}, false
	}
	defer st.close()
	var updated sql.NullString
	if err := st.db.QueryRow(`SELECT max(updated) FROM records WHERE scope = '' AND kind = 'retro'`).Scan(&updated); err != nil || !updated.Valid {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, updated.String)
	return t, err == nil
}
