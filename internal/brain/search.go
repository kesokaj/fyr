package brain

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"

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
	st, project, err := openForSearch(o)
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
		// BM25 (negative: lower is better) weighted up to 10% by recency,
		// fading linearly over a year: of two near-equal matches the newer
		// record, the trail's current state, comes first; a clearly better
		// older match still wins (D31).
		sqlq += ` ORDER BY bm25(records_fts, 2.0, 1.0) * (1 + 0.1 * max(0, 1 - (julianday(?) - julianday(r.date)) / 365.0)), r.id`
		args = append(args, o.now().Format("2006-01-02"))
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

// openForSearch opens the store for a search from o.Dir, loading the
// project's changed trail.jsonl first, and returns the project's scope ("" outside
// a checkout). A nil store means there is nothing to search: no brain.db and
// no trail.jsonl (nothing is created), or outside a checkout without
// --global, where the file search refuses too.
func openForSearch(o Options) (*store, string, error) {
	var p *session
	if scope, top, err := trail.Roots(o.Dir); err == nil {
		p = &session{scope: scope, top: top}
	} else if !o.Global {
		return nil, "", nil
	}
	_, statErr := os.Lstat(Path(o.Home))
	hasTrail := false
	if p != nil {
		if err := guardExportPath(p.top); err != nil {
			return nil, "", err
		}
		_, err := os.Lstat(trailPath(p.top))
		hasTrail = err == nil
	}
	if errors.Is(statErr, os.ErrNotExist) && !hasTrail {
		return nil, "", nil
	}
	st, err := openStore(o.Home)
	if err != nil {
		return nil, "", err
	}
	if p != nil && hasTrail {
		p.st = st
		if err := p.sync(); err != nil {
			st.close()
			return nil, "", err
		}
	}
	project := ""
	if p != nil {
		project = p.scope
	}
	return st, project, nil
}

// scopeFilter is the SQL that keeps a search to the project's and the global
// scope, or with Global every scope; kindFilter keeps one record kind.
func scopeFilter(o Options, project string, args []any) (string, []any) {
	if o.Global {
		return "", args
	}
	return ` AND r.scope IN (?, '')`, append(args, project)
}

func kindFilter(kind string, args []any) (string, []any) {
	if kind == "" || kind == "trail" {
		return "", args
	}
	return ` AND r.kind = ?`, append(args, kind)
}

// Refs returns the records that name ref as written — a record ID, a path,
// or a directory when it ends in "/" — the ID's own record first, from the
// project's and the global scope, or every scope with Global (D30: the
// trail is records, and the skills ask `fyr search --refs <path> --kind bug`
// before they touch an area). FTS narrows the candidates; the exact text
// decides: a path is matched only where no path character stands before
// it, nor after it unless it is a directory.
func Refs(o Options, ref, kind string, limit int) ([]Hit, error) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "./")
	if ref == "" || (kind != "" && kind != "trail" && !knownKind(kind)) {
		return nil, nil
	}
	st, project, err := openForSearch(o)
	if err != nil || st == nil {
		return nil, err
	}
	defer st.close()
	var hits []Hit
	seen := map[int64]bool{}
	// The ID's own record first; its text need not name itself.
	own := `SELECT r.rid, r.scope, r.id, r.kind, r.status, r.date, r.title, r.project FROM records r WHERE r.id = ?`
	oargs := []any{ref}
	var f string
	f, oargs = scopeFilter(o, project, oargs)
	own += f
	f, oargs = kindFilter(kind, oargs)
	orows, err := st.db.Query(own+f+` ORDER BY r.scope`, oargs...)
	if err != nil {
		return nil, fmt.Errorf("brain: %w", err)
	}
	for orows.Next() && len(hits) < limit {
		var h Hit
		var rid int64
		if err := orows.Scan(&rid, &h.Scope, &h.ID, &h.Kind, &h.Status, &h.Date, &h.Title, &h.Project); err != nil {
			orows.Close()
			return nil, err
		}
		seen[rid] = true
		h.Label = label(h.Scope, project, o.Global) + h.ID
		hits = append(hits, h)
	}
	orows.Close()
	words := strings.FieldsFunc(ref, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	q := search.FTSQuery(words, " ")
	if q == "" {
		return nil, nil
	}
	sqlq := `SELECT r.rid, r.scope, r.id, r.kind, r.status, r.date, r.title, r.project, r.title || char(10) || r.body
		FROM records_fts JOIN records r ON r.rid = records_fts.rowid WHERE records_fts MATCH ?`
	args := []any{q}
	f, args = scopeFilter(o, project, args)
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
	return hits, rows.Err()
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
		before := at == 0 || !pathChar(text[at-1])
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

// OpenRecords returns the open records (the status table's "Open when"):
// the project's and the global scope's, or every scope with Global (D30).
func OpenRecords(o Options, kind string, limit int) ([]Hit, error) {
	if kind != "" && kind != "trail" && !knownKind(kind) {
		return nil, nil
	}
	st, project, err := openForSearch(o)
	if err != nil || st == nil {
		return nil, err
	}
	defer st.close()
	sqlq := `SELECT r.scope, r.id, r.kind, r.status, r.date, r.title, r.project FROM records r WHERE ` + strings.ReplaceAll(openSQL, "kind", "r.kind")
	var args []any
	var f string
	f, args = scopeFilter(o, project, args)
	sqlq += f
	f, args = kindFilter(kind, args)
	sqlq += f + ` ORDER BY r.date DESC, r.id`
	rows, err := st.db.Query(sqlq, args...)
	if err != nil {
		return nil, fmt.Errorf("brain: %w", err)
	}
	defer rows.Close()
	var hits []Hit
	for rows.Next() && len(hits) < limit {
		var h Hit
		if err := rows.Scan(&h.Scope, &h.ID, &h.Kind, &h.Status, &h.Date, &h.Title, &h.Project); err != nil {
			return nil, err
		}
		h.Label = label(h.Scope, project, o.Global) + h.ID
		hits = append(hits, h)
	}
	return hits, rows.Err()
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
