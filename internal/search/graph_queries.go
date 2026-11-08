package search

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

const maxSymbolPathHops = 6

// FindSymbolPath finds the shortest call/type/interface-dispatch path (<= 6 hops) between two symbols.
func FindSymbolPath(o Options, out io.Writer) error {
	sc, err := prepare(o, out)
	if err != nil {
		return err
	}
	defer sc.close()
	return sc.findSymbolPath(o.Path, o.To, out)
}

// FindAffected computes the reverse-BFS blast radius (<= 2 hops) for a file or symbol.
func FindAffected(o Options, out io.Writer) error {
	sc, err := prepare(o, out)
	if err != nil {
		return err
	}
	defer sc.close()
	return sc.findAffected(o, out)
}

// RepoMapSummary emits the top-10 God Nodes by PageRank, package dependency summary, and import-cycle check.
func RepoMapSummary(o Options, out io.Writer) error {
	sc, err := prepare(o, out)
	if err != nil {
		return err
	}
	defer sc.close()
	return sc.repoMapSummary(o, out)
}

func (sc *scope) findSymbolPath(fromSpec, toSpec string, out io.Writer) error {
	g, err := sc.loadSymGraph()
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
	first := g.syms[steps[0].toIdx]
	fmt.Fprintf(out, "# path: %s -> %s (%d hops)\n", fromSpec, toSpec, len(steps)-1)
	fmt.Fprintf(out, "0. %s:%d  %s\n", first.Path, first.StartLine, first.QualName())
	for i := 1; i < len(steps); i++ {
		e := steps[i]
		dst := g.syms[e.toIdx]
		fmt.Fprintf(out, "%d. %s:%d --%s [%s]-> %s:%d  %s\n",
			i, e.fromPath, e.line, e.kind, e.confidence, dst.Path, dst.StartLine, dst.QualName())
	}
	return nil
}

func (g *symGraph) matchSymbolNodes(spec string) []int {
	spec = strings.TrimSpace(spec)
	if idxs := g.byQual[spec]; len(idxs) > 0 {
		return idxs
	}
	if idxs := g.byName[spec]; len(idxs) > 0 {
		return idxs
	}
	low := strings.ToLower(spec)
	var out []int
	for i, s := range g.syms {
		if strings.ToLower(s.QualName()) == low || strings.ToLower(s.Name) == low || strings.ToLower(s.Pkg+"."+s.Name) == low {
			out = append(out, i)
		}
	}
	return out
}

type prevStep struct {
	prev int
	edge graphEdge
}

func (g *symGraph) bfsShortestPath(srcs, dsts []int, undirected bool) []graphEdge {
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
			return []graphEdge{{toIdx: s}}
		}
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		if dist[u] >= maxSymbolPathHops {
			continue
		}
		for _, e := range g.neighborsForBFS(u, undirected) {
			if _, seen := dist[e.toIdx]; seen {
				continue
			}
			dist[e.toIdx] = dist[u] + 1
			parent[e.toIdx] = prevStep{prev: u, edge: e}
			if isDst[e.toIdx] {
				return reconstructPath(e.toIdx, parent)
			}
			queue = append(queue, e.toIdx)
		}
	}
	return nil
}

func (g *symGraph) neighborsForBFS(u int, undirected bool) []graphEdge {
	if !undirected {
		return g.out[u]
	}
	edges := append([]graphEdge(nil), g.out[u]...)
	for _, e := range g.in[u] {
		if e.fromIdx >= 0 {
			edges = append(edges, graphEdge{
				fromIdx: u, toIdx: e.fromIdx, kind: e.kind, confidence: e.confidence,
				weight: e.weight, line: e.line, fromPath: e.fromPath,
			})
		}
	}
	return edges
}

func reconstructPath(target int, parent map[int]prevStep) []graphEdge {
	var rev []graphEdge
	cur := target
	for {
		p, ok := parent[cur]
		if !ok {
			rev = append(rev, graphEdge{toIdx: cur})
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

func (sc *scope) findAffected(o Options, out io.Writer) error {
	g, err := sc.loadSymGraph()
	if err != nil {
		return err
	}
	seeds, paths := g.resolveAffectedSeeds(sc.normalizePath(o.Affected))
	if len(seeds) == 0 {
		return fmt.Errorf("search: no indexed file or symbol matching %q", o.Affected)
	}
	direct, transitive, tests := g.collectReverseBlast(seeds, paths)
	fmt.Fprintf(out, "# affected: %s\n", o.Affected)
	writeAffectedList(out, "direct_callers:", direct)
	writeAffectedList(out, "transitive_callers:", transitive)
	writeAffectedList(out, "tests_to_run:", tests)
	fmt.Fprintln(out, "linked_records:")
	recs := sc.activeLinkedRecords(paths, o.BrainRefs)
	if len(recs) == 0 {
		fmt.Fprintln(out, "  (none)")
	}
	for _, r := range recs {
		fmt.Fprintf(out, "  %s [%s] %s\n", r.ID, r.Status, r.Title)
	}
	if hints := sc.activeCoChangeHints(paths); len(hints) > 0 {
		writeAffectedList(out, "co_change_hint:", hints)
	}
	return nil
}

func (g *symGraph) resolveAffectedSeeds(target string) ([]int, map[string]bool) {
	paths := map[string]bool{}
	seen := map[int]bool{}
	var seeds []int
	for p, idxs := range g.byPath {
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
		paths[g.syms[idx].Path] = true
		if !seen[idx] {
			seen[idx] = true
			seeds = append(seeds, idx)
		}
	}
	return seeds, paths
}

func (g *symGraph) collectReverseBlast(seeds []int, paths map[string]bool) ([]string, []string, []string) {
	hop := map[int]int{}
	for _, s := range seeds {
		hop[s] = 0
	}
	var hop1 []int
	var direct, transitive, tests []string
	seenTest := map[string]bool{}
	for _, u := range seeds {
		for _, e := range g.in[u] {
			if e.fromIdx < 0 {
				if !strings.HasSuffix(e.fromPath, "_test.go") {
					direct = append(direct, fmt.Sprintf("%s:%d [%s]", e.fromPath, e.line, e.confidence))
				}
				continue
			}
			if _, seen := hop[e.fromIdx]; !seen {
				hop[e.fromIdx] = 1
				hop1 = append(hop1, e.fromIdx)
				direct, tests = g.appendCallerHit(e, paths, seenTest, direct, tests)
			}
		}
	}
	for _, u := range hop1 {
		for _, e := range g.in[u] {
			if e.fromIdx >= 0 {
				if _, seen := hop[e.fromIdx]; !seen {
					hop[e.fromIdx] = 2
					transitive, tests = g.appendCallerHit(e, paths, seenTest, transitive, tests)
				}
			}
		}
	}
	return direct, transitive, tests
}

func (g *symGraph) appendCallerHit(e graphEdge, paths, seenTest map[string]bool, callers, tests []string) ([]string, []string) {
	s := g.syms[e.fromIdx]
	paths[s.Path] = true
	line := fmt.Sprintf("%s:%d-%d (%s) [%s]", s.Path, s.StartLine, s.EndLine, s.QualName(), e.confidence)
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

func (sc *scope) repoMapSummary(o Options, out io.Writer) error {
	g, err := sc.loadSymGraph()
	if err != nil {
		return err
	}
	ranked := g.rank("", nil)
	fmt.Fprintln(out, "god_nodes (top 10 by PageRank):")
	for i := 0; i < min(10, len(ranked)); i++ {
		s := ranked[i]
		fmt.Fprintf(out, "  %d. %s:%d-%d (%s)  score=%.4f refs=%d\n", i+1, s.Path, s.StartLine, s.EndLine, s.QualName(), s.Score, s.InRefs)
	}
	pkgs, pkgDeps := g.summarizePackages()
	fmt.Fprintln(out, "packages:")
	for _, p := range pkgs {
		deps := pkgDeps[p.name]
		if len(deps) > 0 {
			fmt.Fprintf(out, "  %s (%d symbols) -> %s\n", p.name, p.count, strings.Join(deps, ", "))
		} else {
			fmt.Fprintf(out, "  %s (%d symbols)\n", p.name, p.count)
		}
	}
	cycles := findPackageCycles(pkgDeps)
	fmt.Fprintln(out, "import_cycles:")
	if len(cycles) == 0 {
		fmt.Fprintln(out, "  none (DAG verified)")
	}
	for _, c := range cycles {
		fmt.Fprintf(out, "  %s\n", c)
	}
	return nil
}

type pkgStat struct {
	name  string
	count int
}

func (g *symGraph) summarizePackages() ([]pkgStat, map[string][]string) {
	counts := map[string]int{}
	depSet := map[string]map[string]bool{}
	for i, s := range g.syms {
		if s.Pkg == "" {
			continue
		}
		counts[s.Pkg]++
		for _, e := range g.out[i] {
			toPkg := g.syms[e.toIdx].Pkg
			if toPkg != "" && toPkg != s.Pkg && e.confidence == ConfidenceExtracted {
				if depSet[s.Pkg] == nil {
					depSet[s.Pkg] = map[string]bool{}
				}
				depSet[s.Pkg][toPkg] = true
			}
		}
	}
	var pkgs []pkgStat
	deps := map[string][]string{}
	for p, c := range counts {
		pkgs = append(pkgs, pkgStat{name: p, count: c})
		for d := range depSet[p] {
			deps[p] = append(deps[p], d)
		}
		sort.Strings(deps[p])
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].name < pkgs[j].name })
	return pkgs, deps
}

func findPackageCycles(deps map[string][]string) []string {
	var cycles []string
	seen := map[string]bool{}
	for start, list := range deps {
		for _, next := range list {
			for _, back := range deps[next] {
				if back == start && start < next {
					c := fmt.Sprintf("%s -> %s -> %s", start, next, start)
					if !seen[c] {
						seen[c] = true
						cycles = append(cycles, c)
					}
				}
			}
		}
	}
	sort.Strings(cycles)
	return cycles
}

func parseQueryTerms(query string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(query, func(r rune) bool {
		return !isWordRune(r) && r != '.' && r != '/' && r != '-'
	}) {
		if f = strings.Trim(f, "./-"); f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func matchesPromptTerms(s RankedSymbol, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	nameLow, qualLow, pathLow := strings.ToLower(s.Name), strings.ToLower(s.QualName()), strings.ToLower(s.Path)
	baseLow := strings.ToLower(strings.TrimSuffix(filepath.Base(s.Path), filepath.Ext(s.Path)))
	for _, t := range terms {
		tl := strings.ToLower(t)
		if nameLow == tl || qualLow == tl || pathLow == tl || baseLow == tl {
			return true
		}
		if len(tl) >= 4 && (strings.Contains(nameLow, tl) || strings.Contains(qualLow, tl)) {
			return true
		}
	}
	return false
}
