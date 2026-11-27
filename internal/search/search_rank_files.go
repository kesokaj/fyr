package search

import (
	"fmt"
	"sort"
)

const (
	distinctTermLimit     = 150
	distinctTermKindLimit = 400
)

type chunkScore struct {
	hit          hit
	matchedTerms map[int]bool
	scoreSum     float64
}

type fileAgg struct {
	project, path string
	matchedTerms  map[int]bool
	termBest      map[int]float64
	chunks        map[int64]*chunkScore
}

func (f *fileAgg) totalBM25() float64 {
	var sum float64
	for _, s := range f.termBest {
		sum += s
	}
	return sum
}

// findDistinctFiles fills remaining hit slots when len(terms) > 1 by
// aggregating bounded per-term FTS5 matches at the file level and sorting by
// distinct query term count DESC, accumulated BM25 ASC, and path ASC.
func (sc *scope) findDistinctFiles(o Options, terms []string, hits []hit, perFile map[string]int) ([]hit, error) {
	if len(terms) <= 1 || len(hits) >= o.Limit {
		return hits, nil
	}
	rowLimit := distinctTermLimit
	if o.Kind != "" {
		rowLimit = distinctTermKindLimit
	}
	files := map[string]*fileAgg{}
	for ti, term := range terms {
		if err := sc.collectTermMatches(o, ti, term, rowLimit, files); err != nil {
			return nil, err
		}
	}
	if len(files) == 0 {
		return hits, nil
	}
	first := len(hits)
	ranked := sortRankedFiles(files)
	hits, ids := appendDistinctFileHits(o, ranked, hits, perFile)
	if err := sc.snippets(ftsQuery(terms, " OR "), hits[first:], ids, terms); err != nil {
		return nil, err
	}
	return hits, nil
}

func (sc *scope) collectTermMatches(o Options, ti int, term string, rowLimit int, files map[string]*fileAgg) error {
	tq := ftsQuery([]string{term}, " ")
	if tq == "" {
		return nil
	}
	q := `WITH m AS MATERIALIZED (
			SELECT rowid AS id, bm25(chunks_fts, 2.0, 1.0, 0.8) AS score
			FROM chunks_fts WHERE chunks_fts MATCH ?
		)
		SELECT f.project, f.path, c.oid, c.line, c.id, m.score
		FROM m JOIN chunks c ON c.id = m.id JOIN files f ON f.oid = c.oid
		WHERE f.project IN ` + sc.in() + `
		ORDER BY m.score, f.path`
	args := append([]any{tq}, sc.keys()...)
	rows, err := sc.st.db.Query(q, args...)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	defer rows.Close()
	kept := 0
	for rows.Next() {
		var h hit
		var score float64
		if err := rows.Scan(&h.project, &h.path, &h.oid, &h.line, &h.id, &score); err != nil {
			return err
		}
		if sc.kindMatches(o, h.path) {
			recordChunkMatch(files, h.project+"\x00"+h.path, ti, h, score)
			kept++
			if kept >= rowLimit {
				break
			}
		}
	}
	return rows.Err()
}

func recordChunkMatch(files map[string]*fileAgg, key string, ti int, h hit, score float64) {
	fa := files[key]
	if fa == nil {
		fa = &fileAgg{
			project:      h.project,
			path:         h.path,
			matchedTerms: map[int]bool{},
			termBest:     map[int]float64{},
			chunks:       map[int64]*chunkScore{},
		}
		files[key] = fa
	}
	fa.matchedTerms[ti] = true
	if prev, ok := fa.termBest[ti]; !ok || score < prev {
		fa.termBest[ti] = score
	}
	cs := fa.chunks[h.id]
	if cs == nil {
		cs = &chunkScore{hit: h, matchedTerms: map[int]bool{}}
		fa.chunks[h.id] = cs
	}
	if !cs.matchedTerms[ti] {
		cs.matchedTerms[ti] = true
		cs.scoreSum += score
	}
}

func sortRankedFiles(files map[string]*fileAgg) []*fileAgg {
	ranked := make([]*fileAgg, 0, len(files))
	for _, fa := range files {
		ranked = append(ranked, fa)
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if len(a.matchedTerms) != len(b.matchedTerms) {
			return len(a.matchedTerms) > len(b.matchedTerms)
		}
		sa, sb := a.totalBM25(), b.totalBM25()
		if sa != sb {
			return sa < sb
		}
		if a.path != b.path {
			return a.path < b.path
		}
		return a.project < b.project
	})
	return ranked
}

func appendDistinctFileHits(o Options, ranked []*fileAgg, hits []hit, perFile map[string]int) ([]hit, []int64) {
	have := chunkIDs(hits)
	var ids []int64
	for _, fa := range ranked {
		if len(hits) >= o.Limit {
			break
		}
		key := fa.project + "\x00" + fa.path
		if perFile[key] >= perFileCap {
			continue
		}
		for _, cs := range sortedFileChunks(fa.chunks) {
			if len(hits) >= o.Limit || perFile[key] >= perFileCap {
				break
			}
			if have[cs.hit.id] {
				continue
			}
			have[cs.hit.id] = true
			perFile[key]++
			hits = append(hits, cs.hit)
			ids = append(ids, cs.hit.id)
		}
	}
	return hits, ids
}

func sortedFileChunks(chunks map[int64]*chunkScore) []*chunkScore {
	out := make([]*chunkScore, 0, len(chunks))
	for _, cs := range chunks {
		out = append(out, cs)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if len(a.matchedTerms) != len(b.matchedTerms) {
			return len(a.matchedTerms) > len(b.matchedTerms)
		}
		if a.scoreSum != b.scoreSum {
			return a.scoreSum < b.scoreSum
		}
		if a.hit.line != b.hit.line {
			return a.hit.line < b.hit.line
		}
		return a.hit.id < b.hit.id
	})
	return out
}
