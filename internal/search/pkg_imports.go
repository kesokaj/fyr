package search

import (
	"sort"
	"strings"
)

const maxPackageCycles = 64

type pkgStat struct {
	name  string
	count int
}

func isGoSource(p string) bool { return strings.HasSuffix(p, ".go") }

func isProdPkgPath(pkg, p string) bool {
	return pkg != "" && !isDocPath(p) && !isTestPath(p) && !isExcludedASTPath(p)
}

func addPkgDep(depSet map[string]map[string]bool, fromPkg, toPkg string) {
	if depSet[fromPkg] == nil {
		depSet[fromPkg] = map[string]bool{}
	}
	depSet[fromPkg][toPkg] = true
}

// summarizePackages counts indexed production symbols per canonical package and
// builds the directed package dependency graph strictly over extracted
// import | call | type_ref edges (excluding inferred iface_call/implements).
func (g *symGraph) summarizePackages() ([]pkgStat, map[string][]string) {
	counts := g.seedProdPackages()
	depSet := map[string]map[string]bool{}
	g.collectSymbolPkgDeps(counts, depSet)
	g.collectImportPkgDeps(counts, depSet)
	return sortedPkgStats(counts, depSet)
}

func (g *symGraph) seedProdPackages() map[string]int {
	counts := map[string]int{}
	for _, f := range g.files {
		if isProdPkgPath(f.pkg, f.path) {
			counts[f.pkg] = 0
		}
	}
	return counts
}

func (g *symGraph) isCrossPkgStructuralEdge(from RankedSymbol, fromIsGo bool, e graphEdge) (string, bool) {
	to := g.syms[e.toIdx]
	if to.Pkg == from.Pkg || e.confidence != ConfidenceExtracted || !isStructuralCycleEdgeKind(e.kind) {
		return "", false
	}
	if !isProdPkgPath(to.Pkg, to.Path) || fromIsGo != isGoSource(to.Path) {
		return "", false
	}
	if fromIsGo && !e.qualifiedPkg {
		return "", false
	}
	return to.Pkg, true
}

func (g *symGraph) collectSymbolPkgDeps(counts map[string]int, depSet map[string]map[string]bool) {
	for i, s := range g.syms {
		if !isProdPkgPath(s.Pkg, s.Path) {
			continue
		}
		counts[s.Pkg]++
		sIsGo := isGoSource(s.Path)
		for _, e := range g.out[i] {
			if toPkg, ok := g.isCrossPkgStructuralEdge(s, sIsGo, e); ok {
				addPkgDep(depSet, s.Pkg, toPkg)
			}
		}
	}
}

func (g *symGraph) collectImportPkgDeps(counts map[string]int, depSet map[string]map[string]bool) {
	for _, ie := range g.importEdges {
		if ie.fromPkg == "" || ie.toPkg == "" || ie.fromPkg == ie.toPkg || isDocPath(ie.fromPath) || isTestPath(ie.fromPath) {
			continue
		}
		if _, ok := counts[ie.toPkg]; ok {
			addPkgDep(depSet, ie.fromPkg, ie.toPkg)
		}
	}
}

func sortedPkgStats(counts map[string]int, depSet map[string]map[string]bool) ([]pkgStat, map[string][]string) {
	pkgs := make([]pkgStat, 0, len(counts))
	deps := make(map[string][]string, len(counts))
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

const maxPackageCycleSteps = 10000

// findPackageCycles prunes non-cyclic edges via Tarjan SCC and formats canonical cycles.
func findPackageCycles(deps map[string][]string) []string {
	return formatSCCPackageCycles(deps)
}

func recordPackageCycle(path []string, start string, seen map[string]bool, cycles *[]string) {
	c := strings.Join(append(append([]string(nil), path...), start), " -> ")
	if !seen[c] {
		seen[c] = true
		*cycles = append(*cycles, c)
	}
}

func walkPackageCycles(start string, deps map[string][]string, path []string, onPath, seen map[string]bool, steps *int, cycles *[]string) {
	if *steps >= maxPackageCycleSteps || len(*cycles) >= maxPackageCycles {
		return
	}
	*steps++
	cur := path[len(path)-1]
	for _, nxt := range deps[cur] {
		if *steps >= maxPackageCycleSteps || len(*cycles) >= maxPackageCycles {
			return
		}
		if nxt == start && len(path) >= 2 {
			recordPackageCycle(path, start, seen, cycles)
			continue
		}
		if nxt > start && !onPath[nxt] {
			onPath[nxt] = true
			walkPackageCycles(start, deps, append(path, nxt), onPath, seen, steps, cycles)
			delete(onPath, nxt)
		}
	}
}
