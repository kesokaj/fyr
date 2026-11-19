package brain

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/kesokaj/fyr/internal/search"
)

// Refs returns the records that name ref as written — a record ID, a path,
// or a directory when it ends in "/" — the ID's own record first, from the
// project's and the global scope, with Global also every registered
// project's (searchScopes; D30: the
// trail is records, and the skills ask `fyr search --refs <path> --kind bug`
// before they touch an area). FTS narrows the candidates; the exact text
// decides: a path is matched only where no path character stands before
// it, nor after it unless it is a directory.
func Refs(o Options, ref, kind string, limit int) ([]Hit, error) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "./")
	if ref == "" || (kind != "" && kind != "trail" && !knownKind(kind)) {
		return nil, nil
	}
	st, project, scopes, err := openScopes(o)
	if err != nil || st == nil {
		return nil, err
	}
	defer st.close()
	// The ID's own record first; its text need not name itself.
	hits, seen, err := ownHits(st, o, project, scopes, ref, kind, limit)
	if err != nil {
		return nil, err
	}
	words := strings.FieldsFunc(ref, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	q := search.FTSQuery(words, " ")
	if q == "" {
		return hits, nil
	}
	sqlq := `SELECT r.rid, r.scope, r.id, r.kind, r.status, r.date, r.title, r.project, r.title || char(10) || r.body
		FROM records_fts JOIN records r ON r.rid = records_fts.rowid WHERE records_fts MATCH ?`
	args := []any{q}
	f, args := scopeFilter(scopes, args)
	sqlq += f
	f, args = kindFilter(kind, args)
	sqlq += f + ` ORDER BY r.date DESC, r.id`
	rows, err := st.db.Query(sqlq, args...)
	if err != nil {
		return nil, fmt.Errorf("brain: %w", err)
	}
	defer rows.Close()
	for rows.Next() && len(hits) < limit {
		var h Hit
		var rid int64
		var text string
		if err := rows.Scan(&rid, &h.Scope, &h.ID, &h.Kind, &h.Status, &h.Date, &h.Title, &h.Project, &text); err != nil {
			return nil, err
		}
		line, ok := naming(text, ref)
		if seen[rid] || !ok {
			continue
		}
		h.Snippet = search.CleanSnippet(line)
		h.Label = label(h.Scope, project, o.Global) + h.ID
		hits = append(hits, h)
	}
	decorateMemoryHits(st, o, hits)
	return hits, rows.Err()
}

// ownHits is the record whose ID is ref, in each scope Refs searches, and
// the row IDs it returned.
func ownHits(st *store, o Options, project string, scopes []string, ref, kind string, limit int) ([]Hit, map[int64]bool, error) {
	var hits []Hit
	seen := map[int64]bool{}
	own := `SELECT r.rid, r.scope, r.id, r.kind, r.status, r.date, r.title, r.project FROM records r WHERE r.id = ?`
	oargs := []any{ref}
	var f string
	f, oargs = scopeFilter(scopes, oargs)
	own += f
	f, oargs = kindFilter(kind, oargs)
	orows, err := st.db.Query(own+f+` ORDER BY r.scope`, oargs...)
	if err != nil {
		return nil, nil, fmt.Errorf("brain: %w", err)
	}
	defer orows.Close()
	for orows.Next() && len(hits) < limit {
		var h Hit
		var rid int64
		if err := orows.Scan(&rid, &h.Scope, &h.ID, &h.Kind, &h.Status, &h.Date, &h.Title, &h.Project); err != nil {
			return nil, nil, err
		}
		seen[rid] = true
		h.Label = label(h.Scope, project, o.Global) + h.ID
		hits = append(hits, h)
	}
	if err := orows.Err(); err != nil {
		return nil, nil, fmt.Errorf("brain: %w", err)
	}
	return hits, seen, nil
}

// pathChar is a character a path or an ID can hold, so a match of ref
// that one touches is part of a longer name.
func pathChar(c byte) bool {
	return c == '/' || c == '.' || c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// naming reports whether text names ref as a whole name, and returns the
// line it does it on, cut to a snippet's length.
func naming(text, ref string) (string, bool) {
	dir := strings.HasSuffix(ref, "/")
	for i := 0; ; {
		j := strings.Index(text[i:], ref)
		if j < 0 {
			return "", false
		}
		at, end := i+j, i+j+len(ref)
		// A "./" in front is the same path written relative (`./cmd/x.go`).
		dotSlash := at >= 2 && text[at-2:at] == "./" && (at == 2 || !pathChar(text[at-3]))
		before := at == 0 || !pathChar(text[at-1]) || dotSlash
		// A trailing "." or ":" ends a sentence or starts a line number.
		after := dir || end == len(text) || !pathChar(text[end]) ||
			((text[end] == '.' || text[end] == ':') && (end+1 == len(text) || !pathChar(text[end+1]) || text[end] == ':'))
		if before && after {
			ls := strings.LastIndexByte(text[:at], '\n') + 1
			le := strings.IndexByte(text[at:], '\n')
			line := text[ls:]
			if le >= 0 {
				line = text[ls : at+le]
			}
			if len(line) > 240 {
				line = line[:240]
			}
			return strings.TrimSpace(line), true
		}
		i = at + 1
	}
}
