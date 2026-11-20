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
func (sc *scope) find(o Options, q string, hits []hit, perFile map[string]int, terms []string) ([]hit, error) {
	if q == "" || len(hits) >= o.Limit {
		return hits, nil
	}
	have := chunkIDs(hits)
	first := len(hits)
	// Snippets are made only for the hits that are printed, below.
	// The full-text match runs once, first: joined directly, the planner
	// drove the loop from files and ran the match per row (seconds).
	rows, err := sc.st.db.Query(`WITH m AS MATERIALIZED (
			SELECT rowid AS id, bm25(chunks_fts, 2.0, 1.0, 0.8) AS score FROM chunks_fts WHERE chunks_fts MATCH ?)
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
	if err := sc.snippets(q, hits[first:], ids, terms); err != nil {
		return nil, err
	}
	return hits, nil
}

// chunkIDs is the set of chunks hits already holds. Keyed by chunk, never
// by line: the every-word round shifts a hit's line to its matched term, so
// the any-word round saw the same chunk at its first line as new and
// printed it twice (audit 2026-09-30, SD2).
func chunkIDs(hits []hit) map[int64]bool {
	have := map[int64]bool{}
	for _, h := range hits {
		have[h.id] = true
	}
	return have
}

type snippetRow struct {
	body, snip string
}

// snippets sets each new hit's snippet and moves its line to the snippet's
// first matched term in a single batched query; ids[i] is the chunk of hits[i].
func (sc *scope) snippets(q string, hits []hit, ids []int64, terms []string) error {
	if len(ids) == 0 {
		return nil
	}
	matchQ := ftsQuery(terms, " OR ")
	if matchQ == "" {
		matchQ = q
	}
	args := make([]any, 0, 1+len(ids))
	args = append(args, matchQ)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := sc.st.db.Query(`SELECT chunks_fts.rowid, c.body, snippet(chunks_fts, 1, char(1), char(2), '…', 24)
		FROM chunks_fts JOIN chunks c ON c.id = chunks_fts.rowid
		WHERE chunks_fts MATCH ? AND chunks_fts.rowid IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := make(map[int64]snippetRow, len(ids))
	for rows.Next() {
		var id int64
		var r snippetRow
		if err := rows.Scan(&id, &r.body, &r.snip); err != nil {
			return err
		}
		byID[id] = r
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i, id := range ids {
		if r, ok := byID[id]; ok {
			hits[i].line += matchOffset(r.body, r.snip, terms)
			hits[i].snippet = cleanSnippet(r.snip)
		}
	}
	return nil
}

// matchOffset is the 0-based line of body holding the snippet's first
// marked term, falling back to any query term when markers are absent.
func matchOffset(body, snip string, terms []string) int {
	a := strings.IndexByte(snip, 1)
	b := strings.IndexByte(snip, 2)
	if a >= 0 && b > a {
		term := strings.ToLower(snip[a+1 : b])
		for i, l := range strings.Split(body, "\n") {
			if strings.Contains(strings.ToLower(l), term) {
				return i
			}
		}
		return 0
	}
	return firstMatchingLine(body, terms)
}

func firstMatchingLine(body string, terms []string) int {
	if len(terms) == 0 {
		return 0
	}
	lowerTerms := make([]string, 0, len(terms))
	for _, t := range terms {
		if lt := strings.ToLower(strings.TrimSpace(t)); lt != "" {
			lowerTerms = append(lowerTerms, lt)
		}
	}
	for i, l := range strings.Split(body, "\n") {
		ll := strings.ToLower(l)
		for _, lt := range lowerTerms {
			if strings.Contains(ll, lt) {
				return i
			}
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

var queryStopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "because": true, "been": true, "being": true, "but": true, "by": true,
	"can": true, "could": true, "did": true, "do": true, "does": true, "doing": true, "done": true,
	"each": true, "every": true, "for": true, "from": true,
	"had": true, "has": true, "have": true, "having": true, "how": true,
	"if": true, "in": true, "into": true, "is": true, "it": true, "its": true,
	"just": true, "may": true, "more": true, "most": true, "must": true,
	"no": true, "not": true, "of": true, "on": true, "one": true, "only": true, "or": true, "other": true, "our": true, "out": true, "over": true,
	"should": true, "so": true, "some": true, "such": true,
	"than": true, "that": true, "the": true, "their": true, "them": true, "then": true, "there": true, "these": true, "they": true, "this": true, "those": true, "through": true, "to": true,
	"under": true, "until": true, "up": true, "was": true, "were": true,
	"what": true, "when": true, "where": true, "which": true, "while": true, "who": true, "whom": true, "why": true, "will": true, "with": true, "without": true, "would": true, "your": true,
}

func isStopOrShortWord(w string) bool {
	return strings.IndexFunc(w, isWordRune) < 0 || len([]rune(w)) <= 1 || queryStopWords[strings.ToLower(w)]
}

// filterQueryWords drops 1-char noise and common stop words from words,
// falling back to words if filtering removes everything.
func filterQueryWords(words []string) []string {
	var kept []string
	for _, w := range words {
		if !isStopOrShortWord(w) {
			kept = append(kept, w)
		}
	}
	if len(kept) == 0 {
		return words
	}
	return kept
}

func prepareQueryTerms(words []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(t string) {
		if lt := strings.ToLower(t); !seen[lt] {
			seen[lt] = true
			out = append(out, t)
		}
	}
	for _, w := range filterQueryWords(words) {
		parts := splitQueryIdent(w)
		if len(parts) == 0 {
			add(w)
			continue
		}
		var sub []string
		for _, p := range parts {
			if len([]rune(p)) >= 2 {
				sub = append(sub, p)
			}
		}
		if len(sub) == 0 {
			add(w)
			continue
		}
		for _, p := range sub {
			add(p)
		}
	}
	return out
}

// words ranks chunks holding every word first, then fills the remaining
// slots using distinct-term file reranking across the file's chunks.
func (sc *scope) words(o Options, out io.Writer) error {
	phrase := strings.Join(o.Words, " ")
	terms := prepareQueryTerms(o.Words)
	perFile := map[string]int{}
	hits, err := sc.find(o, ftsQuery(terms, " "), nil, perFile, terms)
	if err != nil {
		return err
	}
	all := len(hits)
	if len(terms) > 1 && len(hits) < o.Limit {
		if hits, err = sc.findDistinctFiles(o, terms, hits, perFile); err != nil {
			return err
		}
	}
	header := fmt.Sprintf("search: %d hits for %q, %d with every word (%s)", len(hits), phrase, all, sc.status())
	fmt.Fprintln(out, header)
	for _, h := range hits {
		fmt.Fprintf(out, "%s\n    %s\n", sc.label(h.project, h.path, h.line), h.snippet)
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
	start = min(start, at) // a target longer than the snippet starts at itself
	end := max(min(len(r), start+snippetMax), at+n)
	out := string(r[start:at]) + "»" + string(r[at:at+n]) + "«" + string(r[at+n:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(r) {
		out += "…"
	}
	return out
}
