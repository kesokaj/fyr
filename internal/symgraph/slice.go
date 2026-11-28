package symgraph

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

const (
	DefaultSliceHops   = 2
	DefaultSliceBudget = 2000
)

// Hop1Cand is one 1-hop neighbor candidate in a context slice.
type Hop1Cand struct {
	Idx        int
	CallLine   int
	CallPath   string
	Confidence string
}

// SliceTiers partitions selected symbol indices into 0/1/2-hop tiers.
type SliceTiers struct {
	Hop0 []int
	Hop1 []Hop1Cand
	Hop2 []int
}

// BuildContextSlice emits a tiered 0/1/2-hop context capsule within o.Budget tokens (chars / 4).
func (sc *Scope) BuildContextSlice(o SliceOptions, out io.Writer) error {
	if o.Hops <= 0 {
		o.Hops = DefaultSliceHops
	}
	if o.Budget <= 0 {
		o.Budget = DefaultSliceBudget
	}
	active := sc.activeFiles(o.Session, o.ActiveFiles)
	var (
		g   *Graph
		err error
	)
	if (strings.TrimSpace(o.Slice) != "" || len(active) > 0) && o.Hops <= 2 {
		g, err = sc.LoadBoundedSymGraph(o.Slice, active, o.IncludeDocs, o.Hops)
	} else {
		g, err = sc.LoadSymGraphOpts(o.IncludeDocs)
	}
	if err != nil {
		return err
	}
	if len(g.Syms) == 0 {
		return nil
	}
	ranked := g.Rank(o.Slice, active)
	tiers := g.SelectSliceTiers(o.Slice, ranked, o.Hops)
	maxBytes := max(200, o.Budget*4)
	content := sc.renderSliceCapsule(o, g, tiers, maxBytes)
	_, err = io.WriteString(out, content)
	return err
}

// SelectSliceTiers selects hop-0, hop-1, and hop-2 symbol indices.
func (g *Graph) SelectSliceTiers(query string, ranked []RankedSymbol, maxHops int) SliceTiers {
	terms := ParseQueryTerms(query)
	hop := map[int]int{}
	var t SliceTiers
	for _, s := range ranked {
		if MatchesPromptTerms(s, terms) {
			hop[s.Idx] = 0
			t.Hop0 = append(t.Hop0, s.Idx)
			if len(t.Hop0) >= 5 {
				break
			}
		}
	}
	if len(t.Hop0) == 0 {
		for i := 0; i < min(3, len(ranked)); i++ {
			hop[ranked[i].Idx] = 0
			t.Hop0 = append(t.Hop0, ranked[i].Idx)
		}
	}
	if maxHops >= 1 {
		t.Hop1 = g.collectHop1(t.Hop0, hop)
	}
	if maxHops >= 2 {
		t.Hop2 = g.collectHop2(t.Hop1, hop)
	}
	return t
}

func (g *Graph) collectHop1(hop0 []int, hop map[int]int) []Hop1Cand {
	var out []Hop1Cand
	for _, u := range hop0 {
		for _, e := range g.Out[u] {
			if _, seen := hop[e.ToIdx]; !seen {
				hop[e.ToIdx] = 1
				out = append(out, Hop1Cand{Idx: e.ToIdx, CallLine: e.Line, CallPath: e.FromPath, Confidence: e.Confidence})
			}
		}
		for _, e := range g.In[u] {
			if e.FromIdx >= 0 {
				if _, seen := hop[e.FromIdx]; !seen {
					hop[e.FromIdx] = 1
					out = append(out, Hop1Cand{Idx: e.FromIdx, CallLine: e.Line, CallPath: e.FromPath, Confidence: e.Confidence})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return g.Syms[out[i].Idx].Score > g.Syms[out[j].Idx].Score
	})
	return out
}

func (g *Graph) collectHop2(hop1 []Hop1Cand, hop map[int]int) []int {
	var out []int
	for _, c := range hop1 {
		for _, e := range g.Out[c.Idx] {
			if _, seen := hop[e.ToIdx]; !seen {
				hop[e.ToIdx] = 2
				out = append(out, e.ToIdx)
			}
		}
		for _, e := range g.In[c.Idx] {
			if e.FromIdx >= 0 {
				if _, seen := hop[e.FromIdx]; !seen {
					hop[e.FromIdx] = 2
					out = append(out, e.FromIdx)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return g.Syms[out[i]].Score > g.Syms[out[j]].Score
	})
	return out
}

// BuildFileSkeleton writes the symbol line-range skeleton for targetPath.
func (sc *Scope) BuildFileSkeleton(targetPath string, out io.Writer) error {
	rel := sc.NormalizePath(targetPath)
	inSQL, projArgs := projectsInClause(sc.Projects)
	q := `SELECT f.path, sy.start_line, sy.end_line, sy.sig
		FROM symbols sy JOIN files f ON f.oid = sy.oid
		WHERE f.project IN ` + inSQL + ` AND (f.path = ? OR f.path LIKE '%' || ?)
		ORDER BY CASE WHEN f.path = ? THEN 0 ELSE 1 END, f.path, sy.start_line, sy.end_line`
	args := append(projArgs, rel, "/"+rel, rel)
	rows, err := sc.DB.Query(q, args...)
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

// NormalizePath normalizes p relative to sc.Projects.
func (sc *Scope) NormalizePath(p string) string {
	p = strings.TrimSpace(p)
	for _, root := range sc.Projects {
		if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return strings.TrimPrefix(filepath.ToSlash(p), "./")
}

func (sc *Scope) activeFiles(sessionID string, extra map[string]bool) map[string]bool {
	if sc != nil && sc.ActiveFiles != nil {
		return sc.ActiveFiles(sessionID, extra)
	}
	return extra
}
