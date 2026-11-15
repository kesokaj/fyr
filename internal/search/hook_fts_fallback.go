package search

import (
	"sort"
	"strings"
	"unicode"
)

const (
	ftsFallbackLimit   = 12
	ftsFallbackMinWord = 4
)

var ftsFallbackStopwords = map[string]bool{
	"about": true, "after": true, "again": true, "also": true, "always": true,
	"another": true, "before": true, "being": true, "below": true, "better": true,
	"between": true, "both": true, "could": true, "does": true, "doing": true,
	"done": true, "each": true, "else": true, "enough": true, "even": true,
	"every": true, "example": true, "explain": true, "find": true, "first": true,
	"from": true, "further": true, "give": true, "good": true, "great": true,
	"have": true, "having": true, "help": true, "here": true, "however": true,
	"into": true, "investigate": true, "just": true, "keep": true, "know": true,
	"like": true, "list": true, "loading": true, "look": true, "looking": true,
	"make": true, "making": true, "many": true, "more": true, "most": true,
	"much": true, "must": true, "need": true, "never": true, "next": true,
	"note": true, "notes": true, "often": true, "only": true, "other": true,
	"over": true, "part": true, "please": true, "question": true, "same": true,
	"section": true, "several": true, "should": true, "show": true, "since": true,
	"skill": true, "skills": true, "some": true, "something": true, "still": true,
	"such": true, "sure": true, "take": true, "tell": true, "than": true,
	"that": true, "their": true, "them": true, "then": true, "there": true,
	"these": true, "they": true, "thing": true, "think": true, "this": true,
	"those": true, "through": true, "under": true, "until": true, "upon": true,
	"used": true, "using": true, "very": true, "want": true, "wants": true,
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
	hits := sc.runFTSFallbackSQL(ftsQuery(words, " AND "), words, promptLower, 1)
	if len(hits) == 0 {
		hits = sc.runFTSFallbackSQL(ftsQuery(words, " OR "), words, promptLower, 2)
	}
	return hits
}

func (sc *scope) runFTSFallbackSQL(matchExpr string, words []string, promptLower string, minMatched int) []ftsChunkHit {
	if matchExpr == "" {
		return nil
	}
	q := `SELECT f.path, c.line, c.heading, c.body
		FROM chunks_fts
		JOIN chunks c ON c.id = chunks_fts.rowid
		JOIN files f ON f.oid = c.oid
		WHERE f.project IN ` + sc.in() + ` AND chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts, 5.0, 1.0)
		LIMIT ?`
	args := append(sc.keys(), matchExpr, ftsFallbackLimit)
	rows, err := sc.st.db.Query(q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ftsChunkHit
	for rows.Next() {
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
	parts := strings.FieldsFunc(strings.ToLower(prompt), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	seen := map[string]bool{}
	var out []string
	for _, p := range parts {
		if len(p) < ftsFallbackMinWord || ftsFallbackStopwords[p] || isAllDigits(p) || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) >= 8 {
			break
		}
	}
	return out
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
	chunkWords := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
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
