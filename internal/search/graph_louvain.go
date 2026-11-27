package search

import (
	"math"
	"sort"
)

const (
	louvainGamma    = 1.35
	louvainMaxIters = 35
)

// LouvainEdge is one raw weighted edge between node U and node V.
type LouvainEdge struct {
	U      int
	V      int
	Weight float64
}

type louvainNeighbor struct {
	to int
	w  float64
}

// LouvainCommunities partitions numNodes into deterministic modularity communities
// using edge weight 1 + log1p(w), resolution gamma = 1.35, and max_iters = 35,
// running Phase 1 local moving and Phase 2 super-node aggregation until convergence.
func LouvainCommunities(numNodes int, edges []LouvainEdge) []int {
	if numNodes <= 0 {
		return nil
	}
	curAdj, curDeg, m2 := buildLouvainAdj(numNodes, edges)
	nodeToSuper := make([]int, numNodes)
	for i := 0; i < numNodes; i++ {
		nodeToSuper[i] = i
	}
	if m2 <= 0 {
		return nodeToSuper
	}
	for level := 0; level < louvainMaxIters; level++ {
		curN := len(curAdj)
		comm := make([]int, curN)
		tot := make([]float64, curN)
		for i := 0; i < curN; i++ {
			comm[i] = i
			tot[i] = curDeg[i]
		}
		if !louvainMovePass(curAdj, curDeg, tot, comm, m2, louvainMaxIters) {
			break
		}
		dense, nextN := compactSupernodeIDs(comm)
		for i, s := range nodeToSuper {
			nodeToSuper[i] = dense[s]
		}
		if nextN >= curN || nextN <= 1 {
			break
		}
		curAdj, curDeg = coarsenLouvainGraph(curAdj, curDeg, dense, nextN)
	}
	return canonicalizeCommunities(nodeToSuper)
}

func compactSupernodeIDs(comm []int) ([]int, int) {
	remap := map[int]int{}
	dense := make([]int, len(comm))
	for i, c := range comm {
		id, ok := remap[c]
		if !ok {
			id = len(remap)
			remap[c] = id
		}
		dense[i] = id
	}
	return dense, len(remap)
}

func coarsenLouvainGraph(adj [][]louvainNeighbor, deg []float64, dense []int, nextN int) ([][]louvainNeighbor, []float64) {
	type pair struct{ a, b int }
	pairW := map[pair]float64{}
	nextDeg := make([]float64, nextN)
	for u := 0; u < len(adj); u++ {
		su := dense[u]
		nextDeg[su] += deg[u]
		for _, nb := range adj[u] {
			sv := dense[nb.to]
			if su < sv {
				pairW[pair{su, sv}] += nb.w
			}
		}
	}
	nextAdj := make([][]louvainNeighbor, nextN)
	for p, w := range pairW {
		nextAdj[p.a] = append(nextAdj[p.a], louvainNeighbor{to: p.b, w: w})
		nextAdj[p.b] = append(nextAdj[p.b], louvainNeighbor{to: p.a, w: w})
	}
	for i := range nextAdj {
		sort.Slice(nextAdj[i], func(a, b int) bool { return nextAdj[i][a].to < nextAdj[i][b].to })
	}
	return nextAdj, nextDeg
}

func buildLouvainAdj(numNodes int, edges []LouvainEdge) ([][]louvainNeighbor, []float64, float64) {
	type pair struct{ a, b int }
	rawPairs := map[pair]float64{}
	for _, e := range edges {
		if e.U < 0 || e.V < 0 || e.U >= numNodes || e.V >= numNodes || e.U == e.V || e.Weight <= 0 {
			continue
		}
		a, b := e.U, e.V
		if a > b {
			a, b = b, a
		}
		rawPairs[pair{a, b}] += e.Weight
	}
	adj := make([][]louvainNeighbor, numNodes)
	deg := make([]float64, numNodes)
	m2 := 0.0
	for p, rawW := range rawPairs {
		w := 1.0 + math.Log1p(rawW)
		adj[p.a] = append(adj[p.a], louvainNeighbor{to: p.b, w: w})
		adj[p.b] = append(adj[p.b], louvainNeighbor{to: p.a, w: w})
		deg[p.a] += w
		deg[p.b] += w
		m2 += 2.0 * w
	}
	for i := range adj {
		sort.Slice(adj[i], func(a, b int) bool { return adj[i][a].to < adj[i][b].to })
	}
	return adj, deg, m2
}

func louvainMovePass(adj [][]louvainNeighbor, deg, tot []float64, comm []int, m2 float64, maxIters int) bool {
	anyMoved := false
	for iter := 0; iter < maxIters; iter++ {
		moved := false
		for u := 0; u < len(comm); u++ {
			if len(adj[u]) == 0 {
				continue
			}
			cOld := comm[u]
			tot[cOld] -= deg[u]
			bestC := bestLouvainCommunity(cOld, deg[u], adj[u], comm, tot, m2)
			comm[u] = bestC
			tot[bestC] += deg[u]
			if bestC != cOld {
				moved = true
				anyMoved = true
			}
		}
		if !moved {
			break
		}
	}
	return anyMoved
}

func bestLouvainCommunity(cOld int, degU float64, neighbors []louvainNeighbor, comm []int, tot []float64, m2 float64) int {
	kIn := map[int]float64{}
	var cands []int
	seen := map[int]bool{cOld: true}
	cands = append(cands, cOld)
	for _, nb := range neighbors {
		c := comm[nb.to]
		kIn[c] += nb.w
		if !seen[c] {
			seen[c] = true
			cands = append(cands, c)
		}
	}
	sort.Ints(cands)
	baseScore := kIn[cOld] - louvainGamma*degU*tot[cOld]/m2
	bestC := cOld
	bestGain := 1e-12
	for _, c := range cands {
		if c == cOld {
			continue
		}
		gain := (kIn[c] - louvainGamma*degU*tot[c]/m2) - baseScore
		if gain > bestGain+1e-12 || (math.Abs(gain-bestGain) <= 1e-12 && bestC != cOld && c < bestC) {
			bestGain = gain
			bestC = c
		}
	}
	return bestC
}

func canonicalizeCommunities(comm []int) []int {
	type groupMeta struct {
		oldID int
		size  int
		first int
	}
	metaByOld := map[int]*groupMeta{}
	var metas []*groupMeta
	for i, c := range comm {
		m, ok := metaByOld[c]
		if !ok {
			m = &groupMeta{oldID: c, first: i}
			metaByOld[c] = m
			metas = append(metas, m)
		}
		m.size++
	}
	sort.Slice(metas, func(i, j int) bool {
		if metas[i].size != metas[j].size {
			return metas[i].size > metas[j].size
		}
		return metas[i].first < metas[j].first
	})
	remap := make(map[int]int, len(metas))
	for newID, m := range metas {
		remap[m.oldID] = newID
	}
	out := make([]int, len(comm))
	for i, c := range comm {
		out[i] = remap[c]
	}
	return out
}

func (g *symGraph) packageCommunities() map[string]int {
	if g == nil {
		return nil
	}
	pkgs, rawWeights := g.collectPackageEdgeWeights(false)
	if len(pkgs) == 0 {
		return nil
	}
	idxOf := make(map[string]int, len(pkgs))
	for i, p := range pkgs {
		idxOf[p] = i
	}
	var edges []LouvainEdge
	for pair, w := range rawWeights {
		edges = append(edges, LouvainEdge{U: idxOf[pair[0]], V: idxOf[pair[1]], Weight: float64(w)})
	}
	comms := LouvainCommunities(len(pkgs), edges)
	out := make(map[string]int, len(pkgs))
	for i, p := range pkgs {
		out[p] = comms[i]
	}
	return out
}
