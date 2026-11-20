package search

import (
	"database/sql"
	"sort"
)

func edgeKindBit(kind string) int {
	switch kind {
	case "type_ref":
		return EdgeMaskType
	case "iface_call", "implements":
		return EdgeMaskIface
	case "import":
		return EdgeMaskImport
	default:
		return EdgeMaskCall
	}
}

func buildSnapshotEdgeTables(g *symGraph, symMap map[int]int, fileIdxOf, pkgIdxOf map[string]int, files []GraphFile, numPkgs int) ([]GraphEdge, map[[2]int]edgeAccum, map[[2]int]edgeAccum, [][]int) {
	symAcc := map[[2]int]edgeAccum{}
	fileAcc := map[[2]int]edgeAccum{}
	pkgAcc := map[[2]int]edgeAccum{}
	cycleSet := make([]map[int]bool, numPkgs)
	for oldU, edges := range g.out {
		uSym, okU := symMap[oldU]
		if !okU {
			continue
		}
		uFile := fileIdxOf[g.syms[oldU].Path]
		for _, e := range edges {
			if vSym, okV := symMap[e.toIdx]; okV {
				recordSnapshotSymEdge(e, uSym, vSym, uFile, fileIdxOf[g.syms[e.toIdx].Path], files, symAcc, fileAcc, pkgAcc, cycleSet)
			}
		}
	}
	mergeImportEdgesIntoTables(g, fileIdxOf, pkgIdxOf, files, fileAcc, pkgAcc, cycleSet)
	return finalizeGraphEdges(symAcc), fileAcc, pkgAcc, mapSetToSortedAdj(cycleSet)
}

func recordSnapshotSymEdge(e graphEdge, uSym, vSym, uFile, vFile int, files []GraphFile, symAcc, fileAcc, pkgAcc map[[2]int]edgeAccum, cycleSet []map[int]bool) {
	uPkg, vPkg := files[uFile].PkgIdx, files[vFile].PkgIdx
	isTest := files[uFile].IsTest
	bit := edgeKindBit(e.kind)
	addEdgeAccum(symAcc, uSym, vSym, isTest, bit)
	addEdgeAccum(fileAcc, uFile, vFile, isTest, bit)
	addEdgeAccum(pkgAcc, uPkg, vPkg, isTest, bit)
	if !isTest && !files[vFile].IsTest && !files[uFile].IsDoc && !files[vFile].IsDoc && uPkg != vPkg && isStructuralCycleEdgeKind(e.kind) && (!isGoSource(files[uFile].Path) || e.qualifiedPkg) {
		if cycleSet[uPkg] == nil {
			cycleSet[uPkg] = map[int]bool{}
		}
		cycleSet[uPkg][vPkg] = true
	}
}

func mergeImportEdgesIntoTables(g *symGraph, fileIdxOf, pkgIdxOf map[string]int, files []GraphFile, fileAcc, pkgAcc map[[2]int]edgeAccum, cycleSet []map[int]bool) {
	for _, ie := range g.importEdges {
		uFile, okU := fileIdxOf[ie.fromPath]
		vPkg, okVPkg := pkgIdxOf[ie.toPkg]
		if !okU || !okVPkg {
			continue
		}
		uPkg := files[uFile].PkgIdx
		isTest := files[uFile].IsTest
		if ie.toPath != "" {
			if vFile, okV := fileIdxOf[ie.toPath]; okV {
				addEdgeAccum(fileAcc, uFile, vFile, isTest, EdgeMaskImport)
			}
		}
		if uPkg != vPkg {
			addEdgeAccum(pkgAcc, uPkg, vPkg, isTest, EdgeMaskImport)
			if !isTest && !files[uFile].IsDoc {
				if cycleSet[uPkg] == nil {
					cycleSet[uPkg] = map[int]bool{}
				}
				cycleSet[uPkg][vPkg] = true
			}
		}
	}
}

func mapSetToSortedAdj(sets []map[int]bool) [][]int {
	adj := make([][]int, len(sets))
	for u, m := range sets {
		for v := range m {
			adj[u] = append(adj[u], v)
		}
		sort.Ints(adj[u])
	}
	return adj
}

func assignSnapshotDegreesAndCommunities(pkgs []GraphPackage, pkgEdges []GraphEdge, files []GraphFile, fileEdges []GraphEdge, syms []GraphSymbol, symEdges []GraphEdge) {
	pkgComms := LouvainCommunities(len(pkgs), graphEdgesToLouvain(pkgEdges))
	for i := range pkgs {
		pkgs[i].Community = pkgComms[i]
	}
	for _, e := range pkgEdges {
		pkgs[e[0]].OutDegree++
		pkgs[e[1]].InDegree++
	}
	fileComms := LouvainCommunities(len(files), graphEdgesToLouvain(fileEdges))
	for i := range files {
		files[i].Community = fileComms[i]
	}
	for _, e := range fileEdges {
		files[e[0]].OutDegree++
		files[e[1]].InDegree++
	}
	symComms := LouvainCommunities(len(syms), graphEdgesToLouvain(symEdges))
	for i := range syms {
		syms[i].Community = symComms[i]
	}
	for _, e := range symEdges {
		w := e[2] + e[3]
		syms[e[0]].OutRefs += w
		syms[e[1]].InRefs += w
	}
}

func graphEdgesToLouvain(edges []GraphEdge) []LouvainEdge {
	out := make([]LouvainEdge, 0, len(edges))
	for _, e := range edges {
		out = append(out, LouvainEdge{U: e[0], V: e[1], Weight: float64(e[2] + e[3])})
	}
	return out
}

func markSnapshotGodNodes(pkgs []GraphPackage, files []GraphFile, syms []GraphSymbol) {
	for _, idx := range topPageRankIndices(len(syms), func(i int) (float64, bool) {
		return syms[i].PageRank, !syms[i].IsTest && syms[i].Kind != "heading"
	}) {
		syms[idx].GodNode = true
	}
	for _, idx := range topPageRankIndices(len(files), func(i int) (float64, bool) {
		return files[i].PageRank, !files[i].IsTest && !files[i].IsDoc
	}) {
		files[idx].GodNode = true
	}
	for _, idx := range topPageRankIndices(len(pkgs), func(i int) (float64, bool) {
		return pkgs[i].PageRank, true
	}) {
		pkgs[idx].GodNode = true
	}
}

func topPageRankIndices(n int, scoreFn func(int) (float64, bool)) []int {
	var order []int
	for i := 0; i < n; i++ {
		if _, ok := scoreFn(i); ok {
			order = append(order, i)
		}
	}
	sort.Slice(order, func(a, b int) bool {
		sa, _ := scoreFn(order[a])
		sb, _ := scoreFn(order[b])
		if sa != sb {
			return sa > sb
		}
		return order[a] < order[b]
	})
	if len(order) > 10 {
		order = order[:10]
	}
	return order
}

func buildSnapshotHiddenCoupling(db *sql.DB, root string, fileIdxOf map[string]int, fileEdges []GraphEdge, astLinked map[[2]string]bool) []GraphHiddenCoupling {
	linkedIdx := map[[2]int]bool{}
	for _, e := range fileEdges {
		a, b := e[0], e[1]
		if a > b {
			a, b = b, a
		}
		linkedIdx[[2]int{a, b}] = true
	}
	hints, _ := queryHiddenCouplingPairs(db, root, defaultHiddenCouplingMinCount, astLinked)
	var out []GraphHiddenCoupling
	for _, h := range hints {
		uIdx, okU := fileIdxOf[h.FileA]
		vIdx, okV := fileIdxOf[h.FileB]
		if !okU || !okV || uIdx == vIdx {
			continue
		}
		a, b := uIdx, vIdx
		if a > b {
			a, b = b, a
		}
		if linkedIdx[[2]int{a, b}] {
			continue
		}
		out = append(out, GraphHiddenCoupling{
			FileAIdx: uIdx, FileBIdx: vIdx,
			FileA: h.FileA, FileB: h.FileB,
			Count: h.Count, Kind: h.Kind,
		})
	}
	return out
}
