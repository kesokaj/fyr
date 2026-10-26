package search

import (
	"fmt"
	"io"
	"strings"
	"unicode"
)

// ftsQuery quotes each word as an FTS5 string, so no character of it is
// read as query syntax, joined with AND (a space) or OR.
func ftsQuery(words []string, op string) string {
	var q []string
	for _, w := range words {
		if strings.IndexFunc(w, isWordRune) < 0 {
			continue
		}
		q = append(q, `"`+strings.ReplaceAll(w, `"`, `""`)+`"`)
	}
	return strings.Join(q, op)
}

func isWordRune(r rune) bool {
	return r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127
}

type hit struct {
	project, path, oid string
	id                 int64 // the chunk: what a hit IS, whatever line it prints
	line               int
	snippet            string
}

// find appends to hits the ranked chunks matching q, skipping any already
// in hits, until o.Limit; perFile carries the per-file cap across calls.
func (sc *scope) find(o Options, q string, hits []hit, perFile map[string]int) ([]hit, error) {
	if q == "" || len(hits) >= o.Limit {
		return hits, nil
	}
	// Keyed by chunk, never by line: the every-word round shifts a hit's
	// line to its matched term, so the any-word round saw the same chunk
	// at its first line as new and printed it twice (audit 2026-09-30, SD2).
	have := map[int64]bool{}
	for _, h := range hits {
		have[h.id] = true
	}
	first := len(hits)
	// Snippets are made only for the hits that are printed, below.
	// The full-text match runs once, first: joined directly, the planner
	// drove the loop from files and ran the match per row (seconds).
	rows, err := sc.st.db.Query(`WITH m AS MATERIALIZED (
			SELECT rowid AS id, bm25(chunks_fts, 2.0, 1.0) AS score FROM chunks_fts WHERE chunks_fts MATCH ?)
		SELECT f.project, f.path, c.oid, c.line, c.id
		FROM m JOIN chunks c ON c.id = m.id JOIN files f ON f.oid = c.oid
		WHERE f.project IN `+sc.in()+`
		ORDER BY m.score, f.path`, append([]any{q}, sc.keys()...)...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	var ids []int64
	for rows.Next() && len(hits) < o.Limit {
		var h hit
		var id int64
		if err := rows.Scan(&h.project, &h.path, &h.oid, &h.line, &id); err != nil {
			rows.Close()
			return nil, err
		}
		key := h.project + "\x00" + h.path
		if have[id] || perFile[key] >= perFileCap || !sc.kindMatches(o, h.project, h.path) {
			continue
		}
		h.id = id
		perFile[key]++
		hits = append(hits, h)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i, id := range ids {
		i += first
		var body, snip string
		err := sc.st.db.QueryRow(`SELECT c.body, snippet(chunks_fts, 1, char(1), char(2), '…', 24)
			FROM chunks_fts JOIN chunks c ON c.id = chunks_fts.rowid WHERE chunks_fts MATCH ? AND chunks_fts.rowid = ?`, q, id).Scan(&body, &snip)
		if err != nil {
			return nil, err
		}
		hits[i].line += matchOffset(body, snip)
		hits[i].snippet = cleanSnippet(snip)
	}
	return hits, nil
}

// matchOffset is the 0-based line of body holding the snippet's first
// marked term, or 0.
func matchOffset(body, snip string) int {
	a := strings.IndexByte(snip, 1)
	b := strings.IndexByte(snip, 2)
	if a < 0 || b <= a {
		return 0
	}
	term := strings.ToLower(snip[a+1 : b])
	for i, l := range strings.Split(body, "\n") {
		if strings.Contains(strings.ToLower(l), term) {
			return i
		}
	}
	return 0
}

// printable replaces control characters (C0, DEL, C1), Unicode's format
// characters, line separators and paragraph separators (classes Cf, Zl, Zp)
// and its other default-ignorable characters (variation selectors, fillers)
// with '?', so indexed text cannot drive the terminal the answer is printed
// to, forge a line of it, or carry text nobody sees (BUG-152; security review,
// finding 4). Tabs and newlines become '?' too, except in snippets, whose
// callers fold whitespace to single spaces first.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return '?'
		}
		// Unicode's own line and paragraph separators forge a line as a
		// newline does. The whole format class goes too, not a list of its
		// members: direction marks and overrides reorder what a terminal
		// shows, and zero-width and tag characters spell text nobody sees.
		if unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || unicode.Is(unicode.Cf, r) {
			return '?'
		}
		// And what Unicode itself calls ignorable outside that class: the
		// variation selectors, the combining grapheme joiner, the fillers.
		// By property, not by a list: an ordinary combining accent stays.
		if unicode.Is(unicode.Variation_Selector, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) {
			return '?'
		}
		return r
	}, s)
}

func cleanSnippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = printable(strings.NewReplacer("\x01", "»", "\x02", "«").Replace(s))
	if r := []rune(s); len(r) > snippetMax {
		s = string(r[:snippetMax-1]) + "…"
	}
	return s
}

// words ranks chunks holding every word first, then fills the remaining
// slots with chunks holding any of them: an AND of many words alone often
// matched two chunks and stopped there (recall test, D27).
func (sc *scope) words(o Options, out io.Writer) error {
	phrase := strings.Join(o.Words, " ")
	perFile := map[string]int{}
	hits, err := sc.find(o, ftsQuery(o.Words, " "), nil, perFile)
	if err != nil {
		return err
	}
	all := len(hits)
	if len(o.Words) > 1 {
		if hits, err = sc.find(o, ftsQuery(o.Words, " OR "), hits, perFile); err != nil {
			return err
		}
	}
	header := fmt.Sprintf("search: %d hits for %q, %d with every word (%s)", len(hits), phrase, all, sc.status())
	fmt.Fprintln(out, header)
	for _, h := range hits {
		fmt.Fprintf(out, "%s\n    %s\n", sc.label(h.project, h.path, h.oid, h.line), h.snippet)
	}
	return nil
}

// lineText returns line n (1-based) of the blob oid from its chunks.
func (sc *scope) lineText(oid string, n int) string {
	var start int
	var body string
	err := sc.st.db.QueryRow(`SELECT line, body FROM chunks WHERE oid = ? AND line <= ? ORDER BY line DESC LIMIT 1`, oid, n).Scan(&start, &body)
	if err != nil {
		return ""
	}
	lines := strings.Split(body, "\n")
	if i := n - start; i >= 0 && i < len(lines) {
		return lines[i]
	}
	return ""
}

// markTarget wraps the first occurrence of target in »« and cuts the line
// to snippetMax runes around it, so a long line keeps what it was listed for.
func markTarget(line, target string) string {
	r := []rune(printable(strings.Join(strings.Fields(line), " ")))
	i := strings.Index(string(r), target)
	if i < 0 {
		if len(r) > snippetMax {
			return string(r[:snippetMax-1]) + "…"
		}
		return string(r)
	}
	at := len([]rune(string(r)[:i]))
	n := len([]rune(target))
	start := max(0, min(at-(snippetMax-n)/2, len(r)-snippetMax))
	end := min(len(r), start+snippetMax)
	out := string(r[start:at]) + "»" + string(r[at:at+n]) + "«" + string(r[at+n:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(r) {
		out += "…"
	}
	return out
}
