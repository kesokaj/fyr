package symgraph

import (
	"fmt"
	"io"
	"path"
	"strings"
)

const maxSymbolPathHops = 6

// FindSymbolPath prints the shortest directed (or undirected fallback) symbol path between o.Path and o.To.
func (sc *Scope) FindSymbolPath(o SliceOptions, out io.Writer) error {
	fromSpec, toSpec := o.Path, o.To
	g, err := sc.LoadSymGraphOpts(o.IncludeDocs)
	if err != nil {
		return err
	}
	srcs := g.matchSymbolNodes(fromSpec)
	dsts := g.matchSymbolNodes(toSpec)
	if len(srcs) == 0 {
		return fmt.Errorf("search: symbol %q not found", fromSpec)
	}
	if len(dsts) == 0 {
		return fmt.Errorf("search: symbol %q not found", toSpec)
	}
	steps := g.bfsShortestPath(srcs, dsts, false)
	if len(steps) == 0 {
		steps = g.bfsShortestPath(srcs, dsts, true)
	}
	if len(steps) == 0 {
		fmt.Fprintf(out, "# path: %s -> %s (no path within %d hops)\n", fromSpec, toSpec, maxSymbolPathHops)
		return nil
	}
	first := g.Syms[steps[0].ToIdx]
	fmt.Fprintf(out, "# path: %s -> %s (%d hops)\n", fromSpec, toSpec, len(steps)-1)
	fmt.Fprintf(out, "0. %s:%d  %s\n", first.Path, first.StartLine, first.QualName())
	for i := 1; i < len(steps); i++ {
		e := steps[i]
		dst := g.Syms[e.ToIdx]
		fmt.Fprintf(out, "%d. %s:%d --%s [%s]-> %s:%d  %s\n",
			i, e.FromPath, e.Line, e.Kind, e.Confidence, dst.Path, dst.StartLine, dst.QualName())
	}
	return nil
}

func (g *Graph) matchSymbolNodes(spec string) []int {
	spec = strings.TrimSpace(spec)
	if idxs := g.byQual[spec]; len(idxs) > 0 {
		return idxs
	}
	if idxs := g.ByName[spec]; len(idxs) > 0 {
		return idxs
	}
	low := strings.ToLower(spec)
	var out []int
	for i, s := range g.Syms {
		if strings.ToLower(s.QualName()) == low || strings.ToLower(s.Name) == low || strings.ToLower(s.Pkg+"."+s.Name) == low {
			out = append(out, i)
		}
	}
	return out
}

type prevStep struct {
	prev int
	edge Edge
}

func (g *Graph) bfsShortestPath(srcs, dsts []int, undirected bool) []Edge {
	isDst := map[int]bool{}
	for _, d := range dsts {
		isDst[d] = true
	}
	dist := map[int]int{}
	parent := map[int]prevStep{}
	var queue []int
	for _, s := range srcs {
		dist[s] = 0
		queue = append(queue, s)
		if isDst[s] {
			return []Edge{{ToIdx: s}}
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if dist[u] >= maxSymbolPathHops {
			continue
		}
		for _, e := range g.neighborsForBFS(u, undirected) {
			if _, seen := dist[e.ToIdx]; seen {
				continue
			}
			dist[e.ToIdx] = dist[u] + 1
			parent[e.ToIdx] = prevStep{prev: u, edge: e}
			if isDst[e.ToIdx] {
				return reconstructPath(e.ToIdx, parent)
			}
			queue = append(queue, e.ToIdx)
		}
	}
	return nil
}

func (g *Graph) neighborsForBFS(u int, undirected bool) []Edge {
	if !undirected {
		return g.Out[u]
	}
	edges := append([]Edge(nil), g.Out[u]...)
	for _, e := range g.In[u] {
		if e.FromIdx >= 0 {
			edges = append(edges, Edge{
				FromIdx: u, ToIdx: e.FromIdx, Kind: e.Kind, Confidence: e.Confidence,
				Weight: e.Weight, Line: e.Line, FromPath: e.FromPath,
			})
		}
	}
	return edges
}

func reconstructPath(target int, parent map[int]prevStep) []Edge {
	var rev []Edge
	cur := target
	for {
		p, ok := parent[cur]
		if !ok {
			rev = append(rev, Edge{ToIdx: cur})
			break
		}
		rev = append(rev, p.edge)
		cur = p.prev
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// FindAffected prints the 2-hop reverse blast radius, tests, linked records, and co-change hints for o.Affected.
func (sc *Scope) FindAffected(o SliceOptions, out io.Writer) error {
	g, err := sc.LoadSymGraphOpts(o.IncludeDocs)
	if err != nil {
		return err
	}
	seeds, paths := g.resolveAffectedSeeds(sc.NormalizePath(o.Affected))
	if len(seeds) == 0 {
		return fmt.Errorf("search: no indexed file or symbol matching %q", o.Affected)
	}
	direct, transitive, tests := g.collectReverseBlast(seeds, paths)
	fmt.Fprintf(out, "# affected: %s\n", o.Affected)
	writeAffectedList(out, "direct_callers:", direct)
	writeAffectedList(out, "transitive_callers:", transitive)
	writeAffectedList(out, "tests_to_run:", tests)
	fmt.Fprintln(out, "linked_records:")
	recs := sc.linkedRecords(paths, o.BrainRefs)
	if len(recs) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	for _, r := range recs {
		fmt.Fprintf(out, "  %s [%s] %s\n", r.ID, r.Status, r.Title)
	}
	if hints := sc.coChangeHints(paths); len(hints) > 0 {
		writeAffectedList(out, "co_change_hint:", hints)
	}
	return nil
}

func (sc *Scope) linkedRecords(paths map[string]bool, brainRefs func(string) []LinkedRecord) []LinkedRecord {
	if sc == nil || sc.LinkedRecords == nil {
		return nil
	}
	return sc.LinkedRecords(paths, brainRefs)
}

func (sc *Scope) coChangeHints(paths map[string]bool) []string {
	if sc == nil || sc.CoChangeHints == nil {
		return nil
	}
	return sc.CoChangeHints(paths)
}

func (g *Graph) resolveAffectedSeeds(target string) ([]int, map[string]bool) {
	paths := map[string]bool{}
	seen := map[int]bool{}
	var seeds []int
	for p, idxs := range g.ByPath {
		if p == target || strings.HasSuffix(p, "/"+target) {
			paths[p] = true
			for _, idx := range idxs {
				if !seen[idx] {
					seen[idx] = true
					seeds = append(seeds, idx)
				}
			}
		}
	}
	for _, idx := range g.matchSymbolNodes(target) {
		paths[g.Syms[idx].Path] = true
		if !seen[idx] {
			seen[idx] = true
			seeds = append(seeds, idx)
		}
	}
	return seeds, paths
}

func (g *Graph) collectReverseBlast(seeds []int, paths map[string]bool) ([]string, []string, []string) {
	hop := make(map[int]int, len(seeds))
	for _, s := range seeds {
		hop[s] = 0
	}
	seenTest := map[string]bool{}
	hop1, direct, tests := g.collectDirectBlast(seeds, hop, paths, seenTest)
	transitive, tests := g.collectTransitiveBlast(hop1, hop, paths, seenTest, tests)
	return direct, transitive, tests
}

func (g *Graph) collectDirectBlast(seeds []int, hop map[int]int, paths, seenTest map[string]bool) ([]int, []string, []string) {
	var hop1 []int
	var direct, tests []string
	for _, u := range seeds {
		for _, e := range g.In[u] {
			if e.FromIdx < 0 {
				if !strings.HasSuffix(e.FromPath, "_test.go") {
					direct = append(direct, fmt.Sprintf("%s:%d [%s]", e.FromPath, e.Line, e.Confidence))
				}
				continue
			}
			if _, seen := hop[e.FromIdx]; seen {
				continue
			}
			hop[e.FromIdx] = 1
			hop1 = append(hop1, e.FromIdx)
			direct, tests = g.appendCallerHit(e, paths, seenTest, direct, tests)
		}
	}
	return hop1, direct, tests
}

func (g *Graph) collectTransitiveBlast(hop1 []int, hop map[int]int, paths, seenTest map[string]bool, tests []string) ([]string, []string) {
	var transitive []string
	for _, u := range hop1 {
		for _, e := range g.In[u] {
			if e.FromIdx < 0 {
				continue
			}
			if _, seen := hop[e.FromIdx]; seen {
				continue
			}
			hop[e.FromIdx] = 2
			transitive, tests = g.appendCallerHit(e, paths, seenTest, transitive, tests)
		}
	}
	return transitive, tests
}

func (g *Graph) appendCallerHit(e Edge, paths, seenTest map[string]bool, callers, tests []string) ([]string, []string) {
	s := g.Syms[e.FromIdx]
	paths[s.Path] = true
	line := fmt.Sprintf("%s:%d-%d (%s) [%s]", s.Path, s.StartLine, s.EndLine, s.QualName(), e.Confidence)
	if isTestSymbol(s) {
		if !seenTest[line] {
			seenTest[line] = true
			tests = append(tests, line)
		}
		return callers, tests
	}
	return append(callers, line), tests
}

func isTestSymbol(s RankedSymbol) bool {
	return strings.HasSuffix(s.Path, "_test.go") || strings.HasPrefix(s.Name, "Test")
}

func writeAffectedList(out io.Writer, header string, items []string) {
	fmt.Fprintln(out, header)
	if len(items) == 0 {
		fmt.Fprintln(out, "  (none)")
		return
	}
	for _, it := range items {
		fmt.Fprintf(out, "  %s\n", it)
	}
}

// RepoMapSummary prints the top-10 God Nodes, package import map, and package dependency cycles.
func (sc *Scope) RepoMapSummary(o SliceOptions, out io.Writer) error {
	g, err := sc.LoadSymGraphOpts(o.IncludeDocs)
	if err != nil {
		return err
	}
	ranked := g.Rank("", nil)
	fmt.Fprintln(out, "god_nodes (top 10 by PageRank):")
	for i := 0; i < min(10, len(ranked)); i++ {
		s := ranked[i]
		fmt.Fprintf(out, "  %d. %s:%d-%d (%s)  score=%.4f refs=%d\n", i+1, s.Path, s.StartLine, s.EndLine, s.QualName(), s.Score, s.InRefs)
	}
	pkgs, pkgDeps := g.SummarizePackages()
	fmt.Fprintln(out, "packages:")
	for _, p := range pkgs {
		deps := pkgDeps[p.Name]
		if len(deps) > 0 {
			fmt.Fprintf(out, "  %s (%d symbols) -> %s\n", p.Name, p.Count, strings.Join(deps, ", "))
		} else {
			fmt.Fprintf(out, "  %s (%d symbols)\n", p.Name, p.Count)
		}
	}
	cycles := FindPackageCycles(pkgDeps)
	fmt.Fprintln(out, "import_cycles:")
	if len(cycles) == 0 {
		fmt.Fprintln(out, "  none (DAG verified)")
	}
	for _, c := range cycles {
		fmt.Fprintf(out, "  %s\n", formatCycleDisplay(c))
	}
	return nil
}

func formatCycleDisplay(c string) string {
	parts := strings.Split(c, " -> ")
	if len(parts) < 3 {
		return c
	}
	bases := make([]string, len(parts))
	seen := map[string]bool{}
	for i, p := range parts {
		b := path.Base(p)
		bases[i] = b
		if i < len(parts)-1 {
			if seen[b] {
				return c
			}
			seen[b] = true
		}
	}
	short := strings.Join(bases, " -> ")
	if short == c {
		return c
	}
	return c + " (" + short + ")"
}
