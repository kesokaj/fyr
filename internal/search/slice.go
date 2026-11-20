package search

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultSliceHops   = 2
	defaultSliceBudget = 2000
)

type hop1Cand struct {
	idx        int
	callLine   int
	callPath   string
	confidence string
}

type sliceTiers struct {
	hop0 []int
	hop1 []hop1Cand
	hop2 []int
}

// BuildContextSlice emits a tiered 0/1/2-hop context capsule within o.Budget tokens (chars / 4).
func BuildContextSlice(o Options, out io.Writer) error {
	sc, err := prepare(o, out)
	if err != nil {
		return err
	}
	defer sc.close()
	return sc.buildContextSlice(o, out)
}

func (sc *scope) buildContextSlice(o Options, out io.Writer) error {
	if o.Hops <= 0 {
		o.Hops = defaultSliceHops
	}
	if o.Budget <= 0 {
		o.Budget = defaultSliceBudget
	}
	g, err := sc.loadSymGraphOpts(o.IncludeDocs)
	if err != nil {
		return err
	}
	if len(g.syms) == 0 {
		return nil
	}
	active := sc.sessionActiveFiles(o.Session, o.ActiveFiles)
	ranked := g.rank(o.Slice, active)
	tiers := g.selectSliceTiers(o.Slice, ranked, o.Hops)
	maxBytes := max(200, o.Budget*4)
	content := sc.renderSliceCapsule(o, g, tiers, maxBytes)
	_, err = io.WriteString(out, content)
	return err
}

func (g *symGraph) selectSliceTiers(query string, ranked []RankedSymbol, maxHops int) sliceTiers {
	terms := parseQueryTerms(query)
	hop := map[int]int{}
	var t sliceTiers
	for _, s := range ranked {
		if matchesPromptTerms(s, terms) {
			hop[s.Idx] = 0
			t.hop0 = append(t.hop0, s.Idx)
			if len(t.hop0) >= 5 {
				break
			}
		}
	}
	if len(t.hop0) == 0 {
		for i := 0; i < min(3, len(ranked)); i++ {
			hop[ranked[i].Idx] = 0
			t.hop0 = append(t.hop0, ranked[i].Idx)
		}
	}
	if maxHops >= 1 {
		t.hop1 = g.collectHop1(t.hop0, hop)
	}
	if maxHops >= 2 {
		t.hop2 = g.collectHop2(t.hop1, hop)
	}
	return t
}

func (g *symGraph) collectHop1(hop0 []int, hop map[int]int) []hop1Cand {
	var out []hop1Cand
	for _, u := range hop0 {
		for _, e := range g.out[u] {
			if _, seen := hop[e.toIdx]; !seen {
				hop[e.toIdx] = 1
				out = append(out, hop1Cand{idx: e.toIdx, callLine: e.line, callPath: e.fromPath, confidence: e.confidence})
			}
		}
		for _, e := range g.in[u] {
			if e.fromIdx >= 0 {
				if _, seen := hop[e.fromIdx]; !seen {
					hop[e.fromIdx] = 1
					out = append(out, hop1Cand{idx: e.fromIdx, callLine: e.line, callPath: e.fromPath, confidence: e.confidence})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return g.syms[out[i].idx].Score > g.syms[out[j].idx].Score
	})
	return out
}

func (g *symGraph) collectHop2(hop1 []hop1Cand, hop map[int]int) []int {
	var out []int
	for _, c := range hop1 {
		for _, e := range g.out[c.idx] {
			if _, seen := hop[e.toIdx]; !seen {
				hop[e.toIdx] = 2
				out = append(out, e.toIdx)
			}
		}
		for _, e := range g.in[c.idx] {
			if e.fromIdx >= 0 {
				if _, seen := hop[e.fromIdx]; !seen {
					hop[e.fromIdx] = 2
					out = append(out, e.fromIdx)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return g.syms[out[i]].Score > g.syms[out[j]].Score
	})
	return out
}

func (sc *scope) buildFileSkeleton(targetPath string, out io.Writer) error {
	rel := sc.normalizePath(targetPath)
	q := `SELECT f.path, sy.start_line, sy.end_line, sy.sig
		FROM symbols sy JOIN files f ON f.oid = sy.oid
		WHERE f.project IN ` + sc.in() + ` AND (f.path = ? OR f.path LIKE '%' || ?)
		ORDER BY CASE WHEN f.path = ? THEN 0 ELSE 1 END, f.path, sy.start_line, sy.end_line`
	args := append(sc.keys(), rel, "/"+rel, rel)
	rows, err := sc.st.db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	chosenPath := ""
	count := 0
	for rows.Next() {
		var p, sig string
		var start, end int
		if err := rows.Scan(&p, &start, &end, &sig); err != nil {
			return err
		}
		if chosenPath == "" {
			chosenPath = p
			fmt.Fprintf(out, "# %s\n", p)
		}
		if p != chosenPath {
			break
		}
		fmt.Fprintf(out, "%d-%d  %s\n", start, end, sig)
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("search: no indexed symbols for %s", targetPath)
	}
	return nil
}

func (sc *scope) normalizePath(p string) string {
	p = strings.TrimSpace(p)
	for _, root := range sc.projects {
		if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return strings.TrimPrefix(filepath.ToSlash(p), "./")
}
