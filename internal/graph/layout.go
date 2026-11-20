package graph

import (
	"math"
	"sort"
)

func normalizeEdges(in []EdgeInput, nodeCount int) [][5]int {
	type edgeAcc struct{ pw, tw, mask int }
	merged := map[[2]int]edgeAcc{}
	for _, e := range in {
		u, v, pw, tw, mask := e[0], e[1], e[2], e[3], e[4]
		if u < 0 || v < 0 || u >= nodeCount || v >= nodeCount || u == v || (pw <= 0 && tw <= 0) {
			continue
		}
		if mask == 0 {
			mask = KindCall
		}
		cur := merged[[2]int{u, v}]
		cur.pw += max(0, pw)
		cur.tw += max(0, tw)
		cur.mask |= mask
		merged[[2]int{u, v}] = cur
	}
	out := make([][5]int, 0, len(merged))
	for pair, a := range merged {
		out = append(out, [5]int{pair[0], pair[1], a.pw, a.tw, a.mask})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

func applyEdgeDegreesPkg(pkgs []PackageNode, edges [][5]int) {
	inDeg, outDeg := make([]int, len(pkgs)), make([]int, len(pkgs))
	for _, e := range edges {
		outDeg[e[0]]++
		inDeg[e[1]]++
		if e[2] > 0 {
			pkgs[e[0]].ProdOutDeg++
			pkgs[e[1]].ProdInDeg++
		}
	}
	for i := range pkgs {
		if pkgs[i].InDegree == 0 {
			pkgs[i].InDegree = inDeg[i]
		}
		if pkgs[i].OutDegree == 0 {
			pkgs[i].OutDegree = outDeg[i]
		}
	}
}

func applyEdgeDegreesFile(files []FileNode, edges [][5]int) {
	inDeg, outDeg := make([]int, len(files)), make([]int, len(files))
	for _, e := range edges {
		outDeg[e[0]]++
		inDeg[e[1]]++
		if e[2] > 0 {
			files[e[0]].ProdOutDeg++
			files[e[1]].ProdInDeg++
		}
	}
	for i := range files {
		if files[i].InDegree == 0 {
			files[i].InDegree = inDeg[i]
		}
		if files[i].OutDegree == 0 {
			files[i].OutDegree = outDeg[i]
		}
	}
}

func applyEdgeDegreesSym(syms []SymbolNode, edges [][5]int) {
	inDeg, outDeg := make([]int, len(syms)), make([]int, len(syms))
	for _, e := range edges {
		outDeg[e[0]]++
		inDeg[e[1]]++
		if e[2] > 0 {
			syms[e[0]].ProdOutDeg++
			syms[e[1]].ProdInDeg++
		}
	}
	for i := range syms {
		if syms[i].InDegree == 0 {
			syms[i].InDegree = inDeg[i]
		}
		if syms[i].OutDegree == 0 {
			syms[i].OutDegree = outDeg[i]
		}
	}
}

func ensureTop10GodNodes(pkgs []PackageNode, files []FileNode, syms []SymbolNode) {
	anyPkg := false
	for _, p := range pkgs {
		anyPkg = anyPkg || p.GodNode
	}
	if !anyPkg {
		for _, i := range topRankedIndices(len(pkgs), func(i int) (bool, float64) { return !pkgs[i].IsTest, pkgs[i].PageRank }, 10) {
			pkgs[i].GodNode = true
		}
	}
	anyFile := false
	for _, f := range files {
		anyFile = anyFile || f.GodNode
	}
	if !anyFile {
		for _, i := range topRankedIndices(len(files), func(i int) (bool, float64) { return !files[i].IsTest, files[i].PageRank }, 10) {
			files[i].GodNode = true
		}
	}
	anySym := false
	for _, s := range syms {
		anySym = anySym || s.GodNode
	}
	if !anySym {
		for _, i := range topRankedIndices(len(syms), func(i int) (bool, float64) { return !syms[i].IsTest, syms[i].PageRank }, 10) {
			syms[i].GodNode = true
		}
	}
}

func topRankedIndices(n int, info func(int) (prod bool, pr float64), limit int) []int {
	var cands []int
	for i := 0; i < n; i++ {
		if prod, pr := info(i); prod && pr > 0 {
			cands = append(cands, i)
		}
	}
	sort.Slice(cands, func(a, b int) bool {
		_, pra := info(cands[a])
		_, prb := info(cands[b])
		if pra != prb {
			return pra > prb
		}
		return cands[a] < cands[b]
	})
	if len(cands) > limit {
		cands = cands[:limit]
	}
	return cands
}

func spiralPositions(n int, commOf func(int) int, prOf func(int) float64, radiusScale float64) [][2]float64 {
	pos := make([][2]float64, n)
	if n == 0 {
		return pos
	}
	groups := map[int][]int{}
	var commKeys []int
	for i := 0; i < n; i++ {
		c := commOf(i)
		if _, ok := groups[c]; !ok {
			commKeys = append(commKeys, c)
		}
		groups[c] = append(groups[c], i)
	}
	sort.Ints(commKeys)
	k := len(commKeys)
	ringR := radiusScale * (0.65 + 0.04*float64(min(k, 12)))
	golden := math.Pi * (3.0 - math.Sqrt(5.0))
	for ci, c := range commKeys {
		cx, cy := 0.0, 0.0
		if k > 1 {
			ang := (2.0*math.Pi*float64(ci))/float64(k) - math.Pi/2.0
			cx, cy = ringR*math.Cos(ang), ringR*math.Sin(ang)
		}
		members := groups[c]
		sort.Slice(members, func(a, b int) bool {
			pa, pb := prOf(members[a]), prOf(members[b])
			if pa != pb {
				return pa > pb
			}
			return members[a] < members[b]
		})
		placeGroupSpiral(pos, members, c, cx, cy, golden)
	}
	return pos
}

func placeGroupSpiral(pos [][2]float64, members []int, comm int, cx, cy, golden float64) {
	m := len(members)
	spread := 28.0 + 18.0*math.Sqrt(float64(m))
	for rankIdx, nodeIdx := range members {
		if rankIdx == 0 && m > 1 {
			pos[nodeIdx] = [2]float64{math.Round(cx*10) / 10, math.Round(cy*10) / 10}
			continue
		}
		r := spread * math.Sqrt((float64(rankIdx)+0.5)/float64(max(1, m)))
		theta := float64(rankIdx)*golden + float64(comm)*0.7
		pos[nodeIdx] = [2]float64{
			math.Round((cx+r*math.Cos(theta))*10) / 10,
			math.Round((cy+r*math.Sin(theta))*10) / 10,
		}
	}
}

func placePkgPositions(pkgs []PackageNode) {
	pts := spiralPositions(len(pkgs), func(i int) int { return pkgs[i].Community }, func(i int) float64 { return pkgs[i].PageRank }, 380.0)
	for i := range pkgs {
		pkgs[i].X, pkgs[i].Y = pts[i][0], pts[i][1]
	}
}

func placeFilePositions(files []FileNode) {
	pts := spiralPositions(len(files), func(i int) int { return files[i].Community }, func(i int) float64 { return files[i].PageRank }, 620.0)
	for i := range files {
		files[i].X, files[i].Y = pts[i][0], pts[i][1]
	}
}

func placeSymPositions(syms []SymbolNode) {
	pts := spiralPositions(len(syms), func(i int) int { return syms[i].Community }, func(i int) float64 { return syms[i].PageRank }, 780.0)
	for i := range syms {
		syms[i].X, syms[i].Y = pts[i][0], pts[i][1]
	}
}

func normalizeCycles(in [][]int, pkgCount int) [][]int {
	out := make([][]int, 0, len(in))
	for _, comp := range in {
		var clean []int
		seen := map[int]bool{}
		for _, idx := range comp {
			if idx >= 0 && idx < pkgCount && !seen[idx] {
				seen[idx] = true
				clean = append(clean, idx)
			}
		}
		if len(clean) > 1 {
			sort.Ints(clean)
			out = append(out, clean)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i][0] < out[j][0]
	})
	return out
}

func normalizeHiddenCoupling(in []HiddenCouplingInput, fileCount int) []HiddenCouplingTuple {
	out := make([]HiddenCouplingTuple, 0, len(in))
	for _, h := range in {
		u, v := h.FileAIdx, h.FileBIdx
		if u < 0 || v < 0 || u >= fileCount || v >= fileCount || u == v || h.Count <= 0 {
			continue
		}
		if u > v {
			u, v = v, u
		}
		kind := h.Kind
		if kind == "" {
			kind = "co_activity"
		}
		out = append(out, HiddenCouplingTuple{U: u, V: v, Count: h.Count, Kind: kind})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].U != out[j].U {
			return out[i].U < out[j].U
		}
		return out[i].V < out[j].V
	})
	return out
}

func buildMeta(repo string, palette []string, layerColors map[string]string, pkgs []PackageNode, files []FileNode, syms []SymbolNode, pkgEdges, fileEdges, symEdges [][5]int, cycles [][]int) Meta {
	prodPkgs, prodFiles, prodSyms := 0, 0, 0
	for _, p := range pkgs {
		if !p.IsTest {
			prodPkgs++
		}
	}
	for _, f := range files {
		if !f.IsTest {
			prodFiles++
		}
	}
	for _, s := range syms {
		if !s.IsTest {
			prodSyms++
		}
	}
	return Meta{
		Repo: repo, Palette: palette, LayerColors: layerColors,
		TotalPackages: len(pkgs), ProdPackages: prodPkgs,
		TotalFiles: len(files), ProdFiles: prodFiles,
		TotalSymbols: len(syms), ProdSymbols: prodSyms,
		PkgEdges: len(pkgEdges), FileEdges: len(fileEdges), SymEdges: len(symEdges),
		PkgCycles: cycles,
	}
}
