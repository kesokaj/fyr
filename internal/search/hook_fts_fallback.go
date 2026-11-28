package search

import (
	"database/sql"
	"path"
	"sort"
	"strings"
	"unicode"
)

const (
	ftsFallbackLimit   = 12
	ftsFallbackMinWord = 4
)

// registerFTSTurnCache and clearFTSTurnCache are compatibility shims kept for
// callers outside this slice's write lease; FTS turn caching is scoped per *store.
func registerFTSTurnCache(_ *sql.DB) {}

func clearFTSTurnCache(_ *sql.DB) {}

func (s *store) clearFTSTurnCache() {
	if s == nil {
		return
	}
	s.ftsTurnCacheMu.Lock()
	s.ftsTurnCache = nil
	s.ftsTurnCacheMu.Unlock()
}

func (s *store) lookupFTSTurnCache(key string) ([]ftsChunkHit, bool) {
	if s == nil {
		return nil, false
	}
	s.ftsTurnCacheMu.Lock()
	defer s.ftsTurnCacheMu.Unlock()
	if s.ftsTurnCache == nil {
		return nil, false
	}
	hits, found := s.ftsTurnCache[key]
	return hits, found
}

func (s *store) storeFTSTurnCache(key string, hits []ftsChunkHit) {
	if s == nil {
		return
	}
	s.ftsTurnCacheMu.Lock()
	if s.ftsTurnCache == nil {
		s.ftsTurnCache = map[string][]ftsChunkHit{}
	}
	s.ftsTurnCache[key] = hits
	s.ftsTurnCacheMu.Unlock()
}

func (sc *scope) ftsFallbackRanked(g *symGraph, prompt string, active map[string]bool) ([]RankedSymbol, bool) {
	if sc == nil || sc.st == nil || g == nil || len(g.Syms) == 0 {
		return nil, false
	}
	words := extractFTSFallbackWords(prompt)
	if len(words) < 2 {
		return nil, false
	}
	promptLower := strings.ToLower(prompt)
	hits := sc.queryFTSFallbackChunks(words, promptLower)
	if len(hits) == 0 {
		return nil, false
	}
	scores := map[int]float64{}
	for rankIdx, h := range hits {
		base := 10.0 / float64(rankIdx+1)
		if active[h.Path] {
			base *= 2.0
		}
		for _, idx := range mapChunkToGraphSymbols(g, h) {
			scores[idx] += base
		}
	}
	if len(scores) == 0 {
		return nil, false
	}
	return rankWeightedSymbols(g, scores), true
}

func (sc *scope) queryFTSFallbackChunks(words []string, promptLower string) []ftsChunkHit {
	cacheKey := strings.Join(words, "\x00") + "\x01" + promptLower
	var st *store
	if sc != nil {
		st = sc.st
	}
	if cached, ok := st.lookupFTSTurnCache(cacheKey); ok {
		return cached
	}
	minMatched := 2
	if len(words) < 2 {
		minMatched = 1
	}
	var hits []ftsChunkHit
	if len(words) >= 2 {
		hits = sc.runFTSFallbackSQL(ftsQuery(words, " AND "), words, promptLower, minMatched)
	}
	if len(hits) == 0 {
		hits = sc.runFTSFallbackSQL(ftsQuery(words, " OR "), words, promptLower, minMatched)
	}
	st.storeFTSTurnCache(cacheKey, hits)
	return hits
}

func (sc *scope) runFTSFallbackSQL(matchExpr string, words []string, promptLower string, minMatched int) []ftsChunkHit {
	if matchExpr == "" {
		return nil
	}
	q := `WITH m AS MATERIALIZED (
			SELECT rowid AS id, bm25(chunks_fts, 5.0, 1.0, 1.0) AS score
			FROM chunks_fts WHERE chunks_fts MATCH ?
			ORDER BY score LIMIT ?
		)
		SELECT f.path, c.line, c.heading, c.body
		FROM m
		JOIN chunks c ON c.id = m.id
		JOIN files f ON f.oid = c.oid
		WHERE f.project IN ` + sc.in() + `
		ORDER BY m.score`
	args := append([]any{matchExpr, ftsFallbackLimit * 8}, sc.keys()...)
	rows, err := sc.st.db.Query(q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ftsChunkHit
	for rows.Next() && len(out) < ftsFallbackLimit {
		var h ftsChunkHit
		if rows.Scan(&h.Path, &h.Line, &h.Heading, &h.Body) != nil {
			continue
		}
		if !allowFTSFallbackPath(h.Path, promptLower) {
			continue
		}
		if minMatched > 1 && countMatchedFallbackWords(h.Heading+"\n"+h.Body, words) < minMatched {
			continue
		}
		out = append(out, h)
	}
	return out
}

func mapChunkToGraphSymbols(g *symGraph, h ftsChunkHit) []int {
	idxs := g.ByPath[h.Path]
	if len(idxs) == 0 {
		return nil
	}
	cStart, cEnd := h.Line, h.Line+chunkLines-1
	var matched []int
	for _, idx := range idxs {
		s := g.Syms[idx]
		if s.StartLine <= cEnd && s.EndLine >= cStart {
			matched = append(matched, idx)
		}
	}
	if len(matched) == 0 {
		for _, idx := range idxs {
			s := g.Syms[idx]
			if s.StartLine >= cStart && s.StartLine <= cEnd+15 {
				matched = append(matched, idx)
				break
			}
		}
	}
	if len(matched) <= 2 {
		return matched
	}
	sort.SliceStable(matched, func(i, j int) bool {
		si, sj := g.Syms[matched[i]], g.Syms[matched[j]]
		return (si.EndLine - si.StartLine) < (sj.EndLine - sj.StartLine)
	})
	return matched[:2]
}

func rankWeightedSymbols(g *symGraph, scores map[int]float64) []RankedSymbol {
	out := make([]RankedSymbol, 0, len(scores))
	for idx, sc := range scores {
		s := g.Syms[idx]
		s.Score = sc
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Idx < out[j].Idx
	})
	return out
}

func extractFTSFallbackWords(prompt string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tok := range strings.Fields(strings.ToLower(prompt)) {
		norm := normalizeFTSFallbackToken(tok)
		if norm == "" {
			continue
		}
		for _, p := range strings.FieldsFunc(norm, isNotFTSWordRune) {
			if len(p) < ftsFallbackMinWord || ftsFallbackStopwords[p] || isAllDigits(p) || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
			if len(out) >= 8 {
				return out
			}
		}
	}
	return out
}

func normalizeFTSFallbackToken(tok string) string {
	if !strings.Contains(tok, "/") {
		return tok
	}
	trimmed := strings.TrimRight(tok, "/")
	if trimmed == "" {
		return ""
	}
	base := path.Base(trimmed)
	stem := strings.TrimSuffix(base, path.Ext(base))
	if base == "." || base == ".." || stem == "" {
		return ""
	}
	return stem
}

func isNotFTSWordRune(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

func allowFTSFallbackPath(p, promptLower string) bool {
	if isExcludedASTPath(p) {
		return false
	}
	if isDocPath(p) {
		return strings.Contains(promptLower, ".md") || strings.Contains(promptLower, "readme") || strings.Contains(promptLower, "docs")
	}
	if strings.HasPrefix(p, ".agents/") || strings.Contains(p, "/.agents/") {
		return strings.Contains(promptLower, ".agents") || strings.Contains(promptLower, "skill") || strings.Contains(promptLower, "rule")
	}
	if strings.HasSuffix(p, "_test.go") {
		return strings.Contains(promptLower, "test")
	}
	if strings.HasSuffix(p, ".sh") {
		return strings.Contains(promptLower, "shell") || strings.Contains(promptLower, "script") || strings.Contains(promptLower, ".sh")
	}
	return true
}

func countMatchedFallbackWords(text string, words []string) int {
	chunkWords := strings.FieldsFunc(strings.ToLower(text), isNotFTSWordRune)
	matched := 0
	for _, qw := range words {
		if chunkHasQueryWord(chunkWords, qw) {
			matched++
		}
	}
	return matched
}

func chunkHasQueryWord(chunkWords []string, qw string) bool {
	stemLen := min(len(qw), 5)
	for _, cw := range chunkWords {
		if cw == qw || (len(cw) >= stemLen && strings.HasPrefix(cw, qw[:stemLen])) {
			return true
		}
	}
	return false
}
