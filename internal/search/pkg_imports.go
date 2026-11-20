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

// summarizePackages counts indexed production symbols per canonical package and
// builds the directed package dependency graph strictly over extracted
// import | call | type_ref edges (excluding inferred iface_call/implements).
func (g *symGraph) summarizePackages() ([]pkgStat, map[string][]string) {
	counts := map[string]int{}
	depSet := map[string]map[string]bool{}
	for _, f := range g.files {
		if f.pkg != "" && !isDocPath(f.path) && !isTestPath(f.path) && !isExcludedASTPath(f.path) {
			if _, exists := counts[f.pkg]; !exists {
				counts[f.pkg] = 0
			}
		}
	}
	for i, s := range g.syms {
		if s.Pkg == "" || isDocPath(s.Path) || isTestPath(s.Path) || isExcludedASTPath(s.Path) {
			continue
		}
		counts[s.Pkg]++
		sIsGo := isGoSource(s.Path)
		for _, e := range g.out[i] {
			to := g.syms[e.toIdx]
			if to.Pkg == "" || to.Pkg == s.Pkg || e.confidence != ConfidenceExtracted || !isStructuralCycleEdgeKind(e.kind) {
				continue
			}
			if isDocPath(to.Path) || isTestPath(to.Path) || isExcludedASTPath(to.Path) || sIsGo != isGoSource(to.Path) {
				continue
			}
			if sIsGo && !e.qualifiedPkg {
				continue
			}
			if depSet[s.Pkg] == nil {
				depSet[s.Pkg] = map[string]bool{}
			}
			depSet[s.Pkg][to.Pkg] = true
		}
	}
	for _, ie := range g.importEdges {
		if ie.fromPkg == "" || ie.toPkg == "" || ie.fromPkg == ie.toPkg || isDocPath(ie.fromPath) || isTestPath(ie.fromPath) {
			continue
		}
		if _, ok := counts[ie.toPkg]; ok {
			if depSet[ie.fromPkg] == nil {
				depSet[ie.fromPkg] = map[string]bool{}
			}
			depSet[ie.fromPkg][ie.toPkg] = true
		}
	}
	return sortedPkgStats(counts, depSet)
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

// findPackageCycles prunes non-cyclic edges via Tarjan SCC and formats canonical cycles.
func findPackageCycles(deps map[string][]string) []string {
	return formatSCCPackageCycles(deps)
}

func walkPackageCycles(start string, deps map[string][]string, path []string, onPath, seen map[string]bool, cycles *[]string) {
	cur := path[len(path)-1]
	for _, nxt := range deps[cur] {
		if len(*cycles) >= maxPackageCycles {
			return
		}
		if nxt == start && len(path) >= 2 {
			c := strings.Join(append(append([]string(nil), path...), start), " -> ")
			if !seen[c] {
				seen[c] = true
				*cycles = append(*cycles, c)
			}
			continue
		}
		if nxt > start && !onPath[nxt] {
			onPath[nxt] = true
			walkPackageCycles(start, deps, append(path, nxt), onPath, seen, cycles)
			delete(onPath, nxt)
		}
	}
}
