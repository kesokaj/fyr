package search

import (
	"io"
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

// GraphOptions controls test and documentation inclusion for LoadGraphSnapshot.
type GraphOptions struct {
	IncludeTests bool
	IncludeDocs  bool
}

// GraphEdge encodes [u, v, prodW, testW, kindMask] directly as a 5-int tuple.
type GraphEdge [5]int

func (e GraphEdge) From() int     { return e[0] }
func (e GraphEdge) To() int       { return e[1] }
func (e GraphEdge) ProdW() int    { return e[2] }
func (e GraphEdge) TestW() int    { return e[3] }
func (e GraphEdge) KindMask() int { return e[4] }

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

// LoadGraphSnapshot reconciles the checkout index and returns its AST + telemetry snapshot.
func LoadGraphSnapshot(opts Options, gopts GraphOptions) (*GraphSnapshot, error) {
	includeDocs := opts.IncludeDocs || gopts.IncludeDocs
	prepOpts := opts
	prepOpts.Global = false
	prepOpts.IncludeDocs = includeDocs
	sc, err := prepare(prepOpts, io.Discard)
	if err != nil {
		return nil, err
	}
	defer sc.close()
	root := ""
	if len(sc.projects) > 0 {
		root = sc.projects[0]
	}
	goMods := loadGoModules(sc)
	g, err := buildSymGraphFromDB(sc.st.db, sc.projects, goMods, includeDocs)
	if err != nil {
		return nil, err
	}
	prodGraph := g
	if includeDocs {
		prodGraph, err = buildSymGraphFromDB(sc.st.db, sc.projects, goMods, false)
		if err != nil {
			return nil, err
		}
	}
	sig := computeStructuralSignalsFromGraph(sc.st.db, root, prodGraph)
	return assembleGraphSnapshot(sc, root, g, gopts, includeDocs, sig), nil
}

func assembleGraphSnapshot(sc *scope, root string, g *symGraph, gopts GraphOptions, includeDocs bool, sig StructuralSignals) *GraphSnapshot {
	_ = g.rank("", nil)
	churnMap, _ := queryFileChurnStats(sc.st.db, root)
	files, fileIdxOf, pkgs, pkgIdxOf := buildSnapshotFilesAndPkgs(g, gopts.IncludeTests, includeDocs, churnMap)
	syms, symMap := buildSnapshotSymbols(g, fileIdxOf, pkgIdxOf, files, pkgs)
	symEdges, fileAcc, pkgAcc, prodCycleAdj := buildSnapshotEdgeTables(g, symMap, fileIdxOf, pkgIdxOf, files, len(pkgs))
	fileEdges := finalizeGraphEdges(fileAcc)
	pkgEdges := finalizeGraphEdges(pkgAcc)
	assignSnapshotDegreesAndCommunities(pkgs, pkgEdges, files, fileEdges, syms, symEdges)
	markSnapshotGodNodes(pkgs, files, syms)
	pkgCycles := TarjanSCC(len(pkgs), prodCycleAdj)
	hidden := buildSnapshotHiddenCoupling(sc.st.db, root, fileIdxOf, fileEdges, collectUndirectedASTFilePairs(g))
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

func buildSnapshotFilesAndPkgs(g *symGraph, includeTests, includeDocs bool, churn map[string]fileChurnStat) ([]GraphFile, map[string]int, []GraphPackage, map[string]int) {
	pkgSet := map[string]bool{}
	var keptFiles []indexedFileMeta
	for _, f := range g.files {
		if (!includeTests && isTestPath(f.path)) || (!includeDocs && isDocPath(f.path)) {
			continue
		}
		if f.pkg == "" {
			f.pkg = canonicalPkgDir(f.path)
		}
		keptFiles = append(keptFiles, f)
		pkgSet[f.pkg] = true
	}
	pkgNames := sortedMapKeys(pkgSet)
	pkgs := make([]GraphPackage, len(pkgNames))
	pkgIdxOf := make(map[string]int, len(pkgNames))
	for i, name := range pkgNames {
		pkgIdxOf[name] = i
		pkgs[i] = GraphPackage{Idx: i, Path: name, Layer: topLevelLayer(name)}
	}
	files := make([]GraphFile, len(keptFiles))
	fileIdxOf := make(map[string]int, len(keptFiles))
	for i, f := range keptFiles {
		fileIdxOf[f.path] = i
		pIdx := pkgIdxOf[f.pkg]
		ch := churn[f.path]
		files[i] = GraphFile{
			Idx: i, Path: f.path, Pkg: f.pkg, PkgIdx: pIdx, Layer: topLevelLayer(f.path),
			IsTest: isTestPath(f.path), IsDoc: isDocPath(f.path), LineCount: f.lineCount,
			ClampHits: ch.clampHits, RepeatReads: ch.repeatReads, ChurnScore: ch.clampHits + ch.repeatReads,
		}
		pkgs[pIdx].FileCount++
		pkgs[pIdx].LineCount += f.lineCount
		pkgs[pIdx].ClampHits += ch.clampHits
		pkgs[pIdx].RepeatReads += ch.repeatReads
		pkgs[pIdx].ChurnScore += ch.clampHits + ch.repeatReads
	}
	return files, fileIdxOf, pkgs, pkgIdxOf
}

func buildSnapshotSymbols(g *symGraph, fileIdxOf, pkgIdxOf map[string]int, files []GraphFile, pkgs []GraphPackage) ([]GraphSymbol, map[int]int) {
	symMap := map[int]int{}
	var syms []GraphSymbol
	for oldIdx, s := range g.syms {
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
			Exported: s.Exported, IsTest: files[fIdx].IsTest, PageRank: roundSignal4(s.Score),
		})
		files[fIdx].SymbolCount++
		files[fIdx].PageRank = roundSignal4(files[fIdx].PageRank + s.Score)
		pkgs[pIdx].SymbolCount++
		pkgs[pIdx].PageRank = roundSignal4(pkgs[pIdx].PageRank + s.Score)
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
