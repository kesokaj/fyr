package symgraph

import (
	"path/filepath"
	"sort"
)

// Edge kind bitmask constants for GraphEdge[4] (kindMask).
const (
	EdgeMaskCall   = 1
	EdgeMaskType   = 2
	EdgeMaskIface  = 4
	EdgeMaskImport = 8
)

// GraphOptions controls test, documentation, and vendored skill inclusion for LoadGraphSnapshot.
type GraphOptions struct {
	IncludeTests    bool
	IncludeDocs     bool
	IncludeVendored bool
}

// GraphEdge encodes [u, v, prodW, testW, kindMask] directly as a 5-int tuple.
type GraphEdge [5]int

// GraphPackage is one canonical package node in GraphSnapshot.
type GraphPackage struct {
	Idx         int     `json:"idx"`
	Path        string  `json:"path"`
	Layer       string  `json:"layer"`
	FileCount   int     `json:"fileCount"`
	SymbolCount int     `json:"symbolCount"`
	LineCount   int     `json:"lineCount"`
	PageRank    float64 `json:"pageRank"`
	Community   int     `json:"community"`
	InDegree    int     `json:"inDegree"`
	OutDegree   int     `json:"outDegree"`
	ClampHits   int     `json:"clampHits"`
	RepeatReads int     `json:"repeatReads"`
	ChurnScore  int     `json:"churnScore"`
	GodNode     bool    `json:"godNode,omitempty"`
}

// GraphFile is one indexed file node in GraphSnapshot.
type GraphFile struct {
	Idx         int     `json:"idx"`
	Path        string  `json:"path"`
	Pkg         string  `json:"pkg"`
	PkgIdx      int     `json:"pkgIdx"`
	Layer       string  `json:"layer"`
	IsTest      bool    `json:"isTest,omitempty"`
	IsDoc       bool    `json:"isDoc,omitempty"`
	SymbolCount int     `json:"symbolCount"`
	LineCount   int     `json:"lineCount"`
	PageRank    float64 `json:"pageRank"`
	Community   int     `json:"community"`
	InDegree    int     `json:"inDegree"`
	OutDegree   int     `json:"outDegree"`
	ClampHits   int     `json:"clampHits"`
	RepeatReads int     `json:"repeatReads"`
	ChurnScore  int     `json:"churnScore"`
	GodNode     bool    `json:"godNode,omitempty"`
}

// GraphSymbol is one AST declaration node in GraphSnapshot.
type GraphSymbol struct {
	Idx       int     `json:"idx"`
	Name      string  `json:"name"`
	QualName  string  `json:"qualName"`
	Recv      string  `json:"recv,omitempty"`
	Kind      string  `json:"kind"`
	Pkg       string  `json:"pkg"`
	PkgIdx    int     `json:"pkgIdx"`
	File      string  `json:"file"`
	FileIdx   int     `json:"fileIdx"`
	StartLine int     `json:"startLine"`
	EndLine   int     `json:"endLine"`
	Sig       string  `json:"sig"`
	Exported  bool    `json:"exported"`
	IsTest    bool    `json:"isTest,omitempty"`
	PageRank  float64 `json:"pageRank"`
	Community int     `json:"community"`
	InRefs    int     `json:"inRefs"`
	OutRefs   int     `json:"outRefs"`
	GodNode   bool    `json:"godNode,omitempty"`
}

// GraphHiddenCoupling is a co-activity file pair lacking an AST edge.
type GraphHiddenCoupling struct {
	FileAIdx int    `json:"fileAIdx"`
	FileBIdx int    `json:"fileBIdx"`
	FileA    string `json:"fileA"`
	FileB    string `json:"fileB"`
	Count    int    `json:"count"`
	Kind     string `json:"kind"`
}

// GraphSnapshot holds the complete multi-resolution AST and behavioral snapshot for a repository.
type GraphSnapshot struct {
	RepoRoot          string                `json:"repoRoot"`
	RepoName          string                `json:"repoName"`
	Packages          []GraphPackage        `json:"packages"`
	Files             []GraphFile           `json:"files"`
	Symbols           []GraphSymbol         `json:"symbols"`
	PkgEdges          []GraphEdge           `json:"pkgEdges"`
	FileEdges         []GraphEdge           `json:"fileEdges"`
	SymEdges          []GraphEdge           `json:"symEdges"`
	PkgCycles         [][]int               `json:"pkgCycles"`
	HiddenCoupling    []GraphHiddenCoupling `json:"hiddenCoupling"`
	StructuralSignals StructuralSignals     `json:"structuralSignals"`
}

// LoadGraphSnapshot builds an AST + behavioral telemetry snapshot from an open Scope.
func (sc *Scope) LoadGraphSnapshot(gopts GraphOptions, includeDocs bool) (*GraphSnapshot, error) {
	root := ""
	if len(sc.Projects) > 0 {
		root = sc.Projects[0]
	}
	goMods := LoadGoModules(sc)
	g, err := BuildSymGraphFromDBOpts(sc.DB, sc.Projects, goMods, includeDocs, gopts.IncludeVendored)
	if err != nil {
		return nil, err
	}
	prodGraph := g
	if includeDocs || gopts.IncludeVendored {
		prodGraph, err = BuildSymGraphFromDBOpts(sc.DB, sc.Projects, goMods, false, false)
		if err != nil {
			return nil, err
		}
	}
	var sig StructuralSignals
	if sc.ComputeSignals != nil {
		sig = sc.ComputeSignals(sc.DB, root, prodGraph)
	}
	return assembleGraphSnapshot(sc, root, g, gopts, includeDocs, sig), nil
}

func assembleGraphSnapshot(sc *Scope, root string, g *Graph, gopts GraphOptions, includeDocs bool, sig StructuralSignals) *GraphSnapshot {
	_ = g.Rank("", nil)
	var churnMap map[string]FileChurnStat
	if sc.QueryFileChurnStats != nil {
		churnMap, _ = sc.QueryFileChurnStats(sc.DB, root)
	}
	files, fileIdxOf, pkgs, pkgIdxOf := buildSnapshotFilesAndPkgs(g, gopts.IncludeTests, includeDocs, churnMap)
	syms, symMap := buildSnapshotSymbols(g, fileIdxOf, pkgIdxOf, files, pkgs)
	symEdges, fileAcc, pkgAcc, prodCycleAdj := buildSnapshotEdgeTables(g, symMap, fileIdxOf, pkgIdxOf, files, len(pkgs))
	fileEdges := finalizeGraphEdges(fileAcc)
	pkgEdges := finalizeGraphEdges(pkgAcc)
	assignSnapshotDegreesAndCommunities(pkgs, pkgEdges, files, fileEdges, syms, symEdges)
	markSnapshotGodNodes(pkgs, files, syms)
	pkgCycles := TarjanSCC(len(pkgs), prodCycleAdj)
	hidden := buildSnapshotHiddenCoupling(sc, root, fileIdxOf, fileEdges, g.CollectUndirectedASTFilePairs())
	return &GraphSnapshot{
		RepoRoot:          root,
		RepoName:          filepath.Base(root),
		Packages:          pkgs,
		Files:             files,
		Symbols:           syms,
		PkgEdges:          pkgEdges,
		FileEdges:         fileEdges,
		SymEdges:          symEdges,
		PkgCycles:         pkgCycles,
		HiddenCoupling:    hidden,
		StructuralSignals: sig,
	}
}

func buildSnapshotFilesAndPkgs(g *Graph, includeTests, includeDocs bool, churn map[string]FileChurnStat) ([]GraphFile, map[string]int, []GraphPackage, map[string]int) {
	pkgSet := map[string]bool{}
	var keptFiles []FileMeta
	for _, f := range g.Files {
		if (!includeTests && IsTestPath(f.Path)) || (!includeDocs && IsDocPath(f.Path)) {
			continue
		}
		if f.Pkg == "" {
			f.Pkg = CanonicalPkgDir(f.Path)
		}
		keptFiles = append(keptFiles, f)
		pkgSet[f.Pkg] = true
	}
	pkgNames := SortedMapKeys(pkgSet)
	pkgs := make([]GraphPackage, len(pkgNames))
	pkgIdxOf := make(map[string]int, len(pkgNames))
	for i, name := range pkgNames {
		pkgIdxOf[name] = i
		pkgs[i] = GraphPackage{Idx: i, Path: name, Layer: topLevelLayer(name)}
	}
	files := make([]GraphFile, len(keptFiles))
	fileIdxOf := make(map[string]int, len(keptFiles))
	for i, f := range keptFiles {
		fileIdxOf[f.Path] = i
		pIdx := pkgIdxOf[f.Pkg]
		ch := churn[f.Path]
		files[i] = GraphFile{
			Idx: i, Path: f.Path, Pkg: f.Pkg, PkgIdx: pIdx, Layer: topLevelLayer(f.Path),
			IsTest: IsTestPath(f.Path), IsDoc: IsDocPath(f.Path), LineCount: f.LineCount,
			ClampHits: ch.ClampHits, RepeatReads: ch.RepeatReads, ChurnScore: ch.ClampHits + ch.RepeatReads,
		}
		pkgs[pIdx].FileCount++
		pkgs[pIdx].LineCount += f.LineCount
		pkgs[pIdx].ClampHits += ch.ClampHits
		pkgs[pIdx].RepeatReads += ch.RepeatReads
		pkgs[pIdx].ChurnScore += ch.ClampHits + ch.RepeatReads
	}
	return files, fileIdxOf, pkgs, pkgIdxOf
}

func buildSnapshotSymbols(g *Graph, fileIdxOf, pkgIdxOf map[string]int, files []GraphFile, pkgs []GraphPackage) ([]GraphSymbol, map[int]int) {
	symMap := map[int]int{}
	var syms []GraphSymbol
	for oldIdx, s := range g.Syms {
		fIdx, ok := fileIdxOf[s.Path]
		if !ok {
			continue
		}
		pkg := files[fIdx].Pkg
		pIdx := pkgIdxOf[pkg]
		newIdx := len(syms)
		symMap[oldIdx] = newIdx
		syms = append(syms, GraphSymbol{
			Idx: newIdx, Name: s.Name, QualName: s.QualName(), Recv: s.Recv, Kind: s.Kind,
			Pkg: pkg, PkgIdx: pIdx, File: s.Path, FileIdx: fIdx,
			StartLine: s.StartLine, EndLine: s.EndLine, Sig: s.Sig,
			Exported: s.Exported, IsTest: files[fIdx].IsTest, PageRank: RoundSignal4(s.Score),
		})
		files[fIdx].SymbolCount++
		files[fIdx].PageRank = RoundSignal4(files[fIdx].PageRank + s.Score)
		pkgs[pIdx].SymbolCount++
		pkgs[pIdx].PageRank = RoundSignal4(pkgs[pIdx].PageRank + s.Score)
	}
	return syms, symMap
}

type edgeAccum struct {
	prodW int
	testW int
	mask  int
}

func addEdgeAccum(m map[[2]int]edgeAccum, u, v int, isTest bool, bit int) {
	if u < 0 || v < 0 || u == v {
		return
	}
	cur := m[[2]int{u, v}]
	if isTest {
		cur.testW++
	} else {
		cur.prodW++
	}
	cur.mask |= bit
	m[[2]int{u, v}] = cur
}

func finalizeGraphEdges(acc map[[2]int]edgeAccum) []GraphEdge {
	out := make([]GraphEdge, 0, len(acc))
	for pair, a := range acc {
		out = append(out, GraphEdge{pair[0], pair[1], a.prodW, a.testW, a.mask})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}
