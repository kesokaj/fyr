package search

import (
	"database/sql"
	"path"
	"sort"
	"strings"
	"sync"
	"unicode"
)

const (
	ftsFallbackLimit   = 12
	ftsFallbackMinWord = 4
)

var (
	ftsTurnCacheMu sync.Mutex
	ftsTurnCache   = map[*sql.DB]map[string][]ftsChunkHit{}
)

func registerFTSTurnCache(db *sql.DB) {
	if db == nil {
		return
	}
	ftsTurnCacheMu.Lock()
	ftsTurnCache[db] = map[string][]ftsChunkHit{}
	ftsTurnCacheMu.Unlock()
}

func clearFTSTurnCache(db *sql.DB) {
	if db == nil {
		return
	}
	ftsTurnCacheMu.Lock()
	delete(ftsTurnCache, db)
	ftsTurnCacheMu.Unlock()
}

func lookupFTSTurnCache(db *sql.DB, key string) ([]ftsChunkHit, bool) {
	if db == nil {
		return nil, false
	}
	ftsTurnCacheMu.Lock()
	defer ftsTurnCacheMu.Unlock()
	if m, ok := ftsTurnCache[db]; ok {
		hits, found := m[key]
		return hits, found
	}
	return nil, false
}

func storeFTSTurnCache(db *sql.DB, key string, hits []ftsChunkHit) {
	if db == nil {
		return
	}
	ftsTurnCacheMu.Lock()
	if m, ok := ftsTurnCache[db]; ok {
		m[key] = hits
	}
	ftsTurnCacheMu.Unlock()
}

var ftsFallbackStopwords = map[string]bool{
	"about": true, "added": true, "adding": true, "after": true, "again": true, "also": true, "always": true,
	"another": true, "based": true, "before": true, "being": true, "below": true, "better": true,
	"between": true, "both": true, "broken": true, "build": true, "change": true, "changes": true,
	"check": true, "checks": true, "clean": true, "cleanup": true, "could": true, "count": true,
	"create": true, "current": true, "debug": true, "delete": true, "does": true, "doing": true,
	"done": true, "each": true, "else": true, "enough": true, "ensure": true, "error": true,
	"errors": true, "even": true, "every": true, "example": true, "existing": true, "explain": true,
	"failed": true, "failing": true, "fails": true, "find": true, "first": true,
	"from": true, "further": true, "give": true, "good": true, "great": true,
	"have": true, "having": true, "help": true, "here": true, "however": true,
	"implement": true, "improve": true, "improved": true, "improving": true, "inspect": true, "into": true, "investigate": true, "issue": true, "just": true, "keep": true, "know": true,
	"like": true, "list": true, "loading": true, "look": true, "looking": true,
	"make": true, "making": true, "many": true, "modify": true, "more": true, "most": true,
	"much": true, "must": true, "need": true, "never": true, "next": true,
	"note": true, "notes": true, "often": true, "only": true, "other": true,
	"over": true, "part": true, "please": true, "problem": true, "question": true,
	"refactor": true, "remove": true, "rename": true, "return": true, "returns": true, "review": true, "same": true,
	"section": true, "several": true, "should": true, "show": true, "since": true,
	"skill": true, "skills": true, "some": true, "something": true, "still": true,
	"such": true, "sure": true, "take": true, "tell": true, "than": true,
	"that": true, "their": true, "them": true, "then": true, "there": true,
	"these": true, "they": true, "thing": true, "think": true, "this": true,
	"those": true, "through": true, "trace": true, "under": true, "until": true, "update": true, "upon": true,
	"used": true, "using": true, "value": true, "values": true, "verify": true, "very": true, "want": true, "wants": true,
	"well": true, "were": true, "what": true, "when": true, "where": true,
	"whether": true, "which": true, "while": true, "with": true, "without": true,
	"work": true, "working": true, "works": true, "would": true, "write": true,
	"your": true,
}

type ftsChunkHit struct {
	path    string
	line    int
	heading string
	body    string
}

func (sc *scope) ftsFallbackRanked(g *symGraph, prompt string, active map[string]bool) ([]RankedSymbol, bool) {
	if sc == nil || sc.st == nil || g == nil || len(g.syms) == 0 {
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
		if active[h.path] {
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
	var db *sql.DB
	if sc != nil && sc.st != nil {
		db = sc.st.db
	}
	if cached, ok := lookupFTSTurnCache(db, cacheKey); ok {
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
	storeFTSTurnCache(db, cacheKey, hits)
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
		if rows.Scan(&h.path, &h.line, &h.heading, &h.body) != nil {
			continue
		}
		if !allowFTSFallbackPath(h.path, promptLower) {
			continue
		}
		if minMatched > 1 && countMatchedFallbackWords(h.heading+"\n"+h.body, words) < minMatched {
			continue
		}
		out = append(out, h)
	}
	return out
}

func mapChunkToGraphSymbols(g *symGraph, h ftsChunkHit) []int {
	idxs := g.byPath[h.path]
	if len(idxs) == 0 {
		return nil
	}
	cStart, cEnd := h.line, h.line+chunkLines-1
	var matched []int
	for _, idx := range idxs {
		s := g.syms[idx]
		if s.StartLine <= cEnd && s.EndLine >= cStart {
			matched = append(matched, idx)
		}
	}
	if len(matched) == 0 {
		for _, idx := range idxs {
			s := g.syms[idx]
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
		si, sj := g.syms[matched[i]], g.syms[matched[j]]
		return (si.EndLine - si.StartLine) < (sj.EndLine - sj.StartLine)
	})
	return matched[:2]
}

func rankWeightedSymbols(g *symGraph, scores map[int]float64) []RankedSymbol {
	out := make([]RankedSymbol, 0, len(scores))
	for idx, sc := range scores {
		s := g.syms[idx]
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
