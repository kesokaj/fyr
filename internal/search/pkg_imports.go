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

func isTestdataGo(p string) bool {
	return isGoSource(p) && (strings.HasPrefix(p, "testdata/") || strings.Contains(p, "/testdata/"))
}

// summarizePackages counts indexed symbols per package and builds the package
// dependency graph: Go sources connect only via qualified extracted Go
// references (excluding inferred interface edges, unqualified receiver calls,
// and Go testdata fixtures), while non-Go sources keep extracted same-world
// (non-Go) edges so TS/JS/Python module cycles remain visible in mixed repos.
func (g *symGraph) summarizePackages() ([]pkgStat, map[string][]string) {
	counts := map[string]int{}
	depSet := map[string]map[string]bool{}
	for i, s := range g.syms {
		if s.Pkg == "" || isTestdataGo(s.Path) {
			continue
		}
		counts[s.Pkg]++
		sIsGo := isGoSource(s.Path)
		for _, e := range g.out[i] {
			to := g.syms[e.toIdx]
			if to.Pkg == "" || to.Pkg == s.Pkg || e.confidence != ConfidenceExtracted || isTestdataGo(to.Path) {
				continue
			}
			if sIsGo != isGoSource(to.Path) {
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

// findPackageCycles reports canonical elementary directed cycles (2+ hops) in deps.
func findPackageCycles(deps map[string][]string) []string {
	starts := make([]string, 0, len(deps))
	for p := range deps {
		starts = append(starts, p)
	}
	sort.Strings(starts)
	seen := map[string]bool{}
	var cycles []string
	for _, start := range starts {
		if len(cycles) >= maxPackageCycles {
			break
		}
		walkPackageCycles(start, deps, []string{start}, map[string]bool{start: true}, seen, &cycles)
	}
	sort.Strings(cycles)
	return cycles
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
