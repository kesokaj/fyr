package search

import "sort"

func isStructuralCycleEdgeKind(kind string) bool {
	return kind == "import" || kind == "call" || kind == "type_ref"
}

type tarjanState struct {
	adj     [][]int
	index   int
	indices []int
	lowlink []int
	onStack []bool
	stack   []int
	sccs    [][]int
}

// TarjanSCC returns strongly connected components (size >= 2) in deterministic ascending index order.
func TarjanSCC(numNodes int, adj [][]int) [][]int {
	if numNodes <= 1 {
		return nil
	}
	st := &tarjanState{
		adj:     adj,
		indices: make([]int, numNodes),
		lowlink: make([]int, numNodes),
		onStack: make([]bool, numNodes),
	}
	for i := range st.indices {
		st.indices[i] = -1
	}
	for u := 0; u < numNodes; u++ {
		if st.indices[u] == -1 {
			tarjanVisit(u, st)
		}
	}
	sort.Slice(st.sccs, func(i, j int) bool { return st.sccs[i][0] < st.sccs[j][0] })
	return st.sccs
}

func tarjanVisit(u int, st *tarjanState) {
	st.indices[u] = st.index
	st.lowlink[u] = st.index
	st.index++
	st.stack = append(st.stack, u)
	st.onStack[u] = true

	if u < len(st.adj) {
		for _, v := range st.adj[u] {
			if v < 0 || v >= len(st.indices) || v == u {
				continue
			}
			if st.indices[v] == -1 {
				tarjanVisit(v, st)
				if st.lowlink[v] < st.lowlink[u] {
					st.lowlink[u] = st.lowlink[v]
				}
			} else if st.onStack[v] && st.indices[v] < st.lowlink[u] {
				st.lowlink[u] = st.indices[v]
			}
		}
	}
	if st.lowlink[u] == st.indices[u] {
		var comp []int
		for {
			w := st.stack[len(st.stack)-1]
			st.stack = st.stack[:len(st.stack)-1]
			st.onStack[w] = false
			comp = append(comp, w)
			if w == u {
				break
			}
		}
		if len(comp) >= 2 {
			sort.Ints(comp)
			st.sccs = append(st.sccs, comp)
		}
	}
}

// TarjanPackageSCCs computes non-trivial SCCs over sorted package names and directed dependencies.
func TarjanPackageSCCs(pkgs []string, deps map[string][]string) [][]int {
	idxOf := make(map[string]int, len(pkgs))
	for i, p := range pkgs {
		idxOf[p] = i
	}
	adj := make([][]int, len(pkgs))
	for i, p := range pkgs {
		for _, d := range deps[p] {
			if j, ok := idxOf[d]; ok && j != i {
				adj[i] = append(adj[i], j)
			}
		}
		sort.Ints(adj[i])
	}
	return TarjanSCC(len(pkgs), adj)
}

func formatSCCPackageCycles(deps map[string][]string) []string {
	pkgSet := map[string]bool{}
	for p, ds := range deps {
		pkgSet[p] = true
		for _, d := range ds {
			pkgSet[d] = true
		}
	}
	pkgs := sortedMapKeys(pkgSet)
	sccs := TarjanPackageSCCs(pkgs, deps)
	if len(sccs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var cycles []string
	steps := 0
	for _, scc := range sccs {
		inSCC := make(map[string]bool, len(scc))
		for _, idx := range scc {
			inSCC[pkgs[idx]] = true
		}
		subDeps := make(map[string][]string, len(scc))
		for _, idx := range scc {
			p := pkgs[idx]
			for _, d := range deps[p] {
				if inSCC[d] {
					subDeps[p] = append(subDeps[p], d)
				}
			}
			sort.Strings(subDeps[p])
		}
		for _, idx := range scc {
			if len(cycles) >= maxPackageCycles || steps >= maxPackageCycleSteps {
				break
			}
			start := pkgs[idx]
			walkPackageCycles(start, subDeps, []string{start}, map[string]bool{start: true}, seen, &steps, &cycles)
		}
	}
	sort.Strings(cycles)
	return cycles
}
