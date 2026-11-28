package hooks

import (
	"path"
	"sort"
	"strings"
	"unicode"

	"github.com/kesokaj/fyr/internal/symgraph"
)

const (
	// FTSFallbackLimit caps the number of FTS chunks examined during AST fallback.
	FTSFallbackLimit   = 12
	ftsFallbackMinWord = 4
)

// FTSFallbackRanked scores graph symbols via FTS5 chunk fallback when AST ranking is unconfident.
func FTSFallbackRanked(d Deps, sc *Scope, g *symgraph.Graph, prompt string, active map[string]bool) ([]symgraph.RankedSymbol, bool) {
	if sc == nil || sc.St == nil || sc.St.SQLDB == nil || g == nil || len(g.Syms) == 0 {
		return nil, false
	}
	words := ExtractFTSFallbackWords(prompt)
	if len(words) < 2 {
		return nil, false
	}
	promptLower := strings.ToLower(prompt)
	hits := QueryFTSFallbackChunks(d, sc, words, promptLower)
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

// QueryFTSFallbackChunks queries cached or live FTS5 fallback chunks for words.
func QueryFTSFallbackChunks(d Deps, sc *Scope, words []string, promptLower string) []symgraph.FTSChunkHit {
	ck := strings.Join(words, "\x00") + "\x01" + promptLower
	var st *Store
	if sc != nil {
		st = sc.St
	}
	if cached, ok := st.LookupFTSTurnCache(ck); ok {
		return cached
	}
	minMatched := 2
	if len(words) < 2 {
		minMatched = 1
	}
	var hits []symgraph.FTSChunkHit
	if len(words) >= 2 && d.FTSQuery != nil {
		hits = runFTSFallbackSQL(sc, d.FTSQuery(words, " AND "), words, promptLower, minMatched)
	}
	if len(hits) == 0 && d.FTSQuery != nil {
		hits = runFTSFallbackSQL(sc, d.FTSQuery(words, " OR "), words, promptLower, minMatched)
	}
	st.StoreFTSTurnCache(ck, hits)
	return hits
}

func runFTSFallbackSQL(sc *Scope, matchExpr string, words []string, promptLower string, minMatched int) []symgraph.FTSChunkHit {
	if matchExpr == "" || sc == nil || sc.St == nil || sc.St.SQLDB == nil {
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
		WHERE f.project IN ` + sc.inClause() + `
		ORDER BY m.score`
	args := append([]any{matchExpr, FTSFallbackLimit * 8}, sc.projectArgs()...)
	rows, err := sc.St.SQLDB.Query(q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []symgraph.FTSChunkHit
	for rows.Next() && len(out) < FTSFallbackLimit {
		var h symgraph.FTSChunkHit
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

func mapChunkToGraphSymbols(g *symgraph.Graph, h symgraph.FTSChunkHit) []int {
	idxs := g.ByPath[h.Path]
	if len(idxs) == 0 {
		return nil
	}
	cStart, cEnd := h.Line, h.Line+symgraph.ChunkLines-1
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

func rankWeightedSymbols(g *symgraph.Graph, scores map[int]float64) []symgraph.RankedSymbol {
	out := make([]symgraph.RankedSymbol, 0, len(scores))
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

// ExtractFTSFallbackWords extracts up to 8 meaningful query words from prompt.
func ExtractFTSFallbackWords(prompt string) []string {
	seen := map[string]bool{}
	var out []string
	for _, tok := range strings.Fields(strings.ToLower(prompt)) {
		norm := normalizeFTSFallbackToken(tok)
		if norm == "" {
			continue
		}
		for _, p := range strings.FieldsFunc(norm, isNotFTSWordRune) {
			if len(p) < ftsFallbackMinWord || symgraph.FTSFallbackStopwords[p] || isAllFTSDigits(p) || seen[p] {
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

func isAllFTSDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

func allowFTSFallbackPath(p, promptLower string) bool {
	if symgraph.IsExcludedASTPath(p) {
		return false
	}
	if symgraph.IsDocPath(p) {
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
