package graph

import (
	"fmt"
	"hash/fnv"
	"path"
	"sort"
	"strings"
)

// DefaultPalette holds the 12 high-contrast community colors legible on both
// dark (#0b0f17) and light (#f8fafc) backgrounds.
var DefaultPalette = []string{
	"#7aa2f7", "#9ece6a", "#bb9af7", "#ff9e64",
	"#7dcfff", "#f7768e", "#e0af68", "#73daca",
	"#f5a97f", "#c0caf5", "#a6e3a1", "#f38ba8",
}

var presetLayerColors = map[string]string{
	"cmd":      "#ff9e64",
	"internal": "#7aa2f7",
	"pkg":      "#73daca",
	"web":      "#bb9af7",
	"src":      "#9ece6a",
	"api":      "#e0af68",
	"deploy":   "#f7768e",
	"tools":    "#7dcfff",
	"testdata": "#a6adc8",
	"root":     "#94a3b8",
	"other":    "#94a3b8",
}

// Build normalizes Input into a deterministic, self-contained Payload.
func Build(in Input) *Payload {
	layerColors := seedLayerColors()
	pkgs := buildPackageNodes(in.Packages, layerColors)
	fileLinksOut := map[string]RecordLinks{}
	files := buildFileNodes(in.Files, in.FileLinks, pkgs, layerColors, fileLinksOut)
	syms := buildSymbolNodes(in.Symbols, files, pkgs, layerColors)

	pkgEdges := normalizeEdges(in.PkgEdges, len(pkgs))
	fileEdges := normalizeEdges(in.FileEdges, len(files))
	symEdges := normalizeEdges(in.SymEdges, len(syms))

	applyEdgeDegreesPkg(pkgs, pkgEdges)
	applyEdgeDegreesFile(files, fileEdges)
	applyEdgeDegreesSym(syms, symEdges)
	ensureTop10GodNodes(pkgs, files, syms)

	placePkgPositions(pkgs)
	placeFilePositions(files)
	placeSymPositions(syms)

	cycles := normalizeCycles(in.PkgCycles, len(pkgs))
	hidden := normalizeHiddenCoupling(in.HiddenCoupling, len(files))
	repo := strings.TrimSpace(in.RepoName)
	if repo == "" {
		repo = "repository"
	}
	palette := append([]string(nil), DefaultPalette...)
	return &Payload{
		Meta:           buildMeta(repo, palette, layerColors, pkgs, files, syms, pkgEdges, fileEdges, symEdges, cycles),
		Packages:       pkgs,
		Files:          files,
		Symbols:        syms,
		PkgEdges:       pkgEdges,
		FileEdges:      fileEdges,
		SymEdges:       symEdges,
		HiddenCoupling: hidden,
		BrainLinks:     fileLinksOut,
	}
}

func seedLayerColors() map[string]string {
	m := make(map[string]string, len(presetLayerColors))
	for k, v := range presetLayerColors {
		m[k] = v
	}
	return m
}

func resolveLayerName(explicit, p string) string {
	l := strings.TrimSpace(explicit)
	if l == "" || l == "." {
		slash := strings.TrimPrefix(p, "./")
		if idx := strings.IndexByte(slash, '/'); idx > 0 {
			l = slash[:idx]
		} else if slash != "" && !strings.Contains(slash, ".") {
			l = slash
		} else {
			l = "root"
		}
	}
	return l
}

func colorForLayer(layer string) string {
	if c, ok := presetLayerColors[layer]; ok {
		return c
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(layer)) // hash.Hash.Write never returns an error
	return DefaultPalette[int(h.Sum32())%len(DefaultPalette)]
}

func inferFileLang(filePath string, isDoc bool) string {
	if isDoc || strings.HasSuffix(filePath, ".md") || strings.HasSuffix(filePath, ".mdx") {
		return "doc"
	}
	switch path.Ext(filePath) {
	case ".go":
		return "go"
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return "ts"
	default:
		return "other"
	}
}

func normalizeLink(in RecordLinks) RecordLinks {
	if len(in.IDs) == 0 {
		return RecordLinks{Bugs: in.Bugs, ADRs: in.ADRs, Memories: in.Memories}
	}
	seen := make(map[string]bool, len(in.IDs))
	ids := make([]string, 0, len(in.IDs))
	for _, id := range in.IDs {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	bugs, adrs, mems := countIDKinds(ids)
	return RecordLinks{
		Bugs:     max(in.Bugs, bugs),
		ADRs:     max(in.ADRs, adrs),
		Memories: max(in.Memories, mems),
		IDs:      ids,
	}
}

func countIDKinds(ids []string) (bugs, adrs, mems int) {
	for _, id := range ids {
		switch {
		case strings.HasPrefix(id, "BUG-"):
			bugs++
		case strings.HasPrefix(id, "ADR-"):
			adrs++
		case strings.HasPrefix(id, "MEMORY-"):
			mems++
		default:
			_ = fmt.Sprint(id)
		}
	}
	return bugs, adrs, mems
}

func buildPackageNodes(in []PackageInput, layerColors map[string]string) []PackageNode {
	pkgs := make([]PackageNode, len(in))
	for i, p := range in {
		layer := resolveLayerName(p.Layer, p.Path)
		layerColors[layer] = colorForLayer(layer)
		churn := p.ChurnScore
		if churn == 0 {
			churn = p.ClampHits + p.RepeatReads
		}
		pkgs[i] = PackageNode{
			ID: p.Path, Label: p.Path, Layer: layer, Lang: "go",
			FileCount: p.FileCount, SymbolCount: p.SymbolCount, LineCount: p.LineCount,
			PageRank: p.PageRank, Community: max(0, p.Community),
			InDegree: p.InDegree, OutDegree: p.OutDegree,
			ClampHits: p.ClampHits, RepeatReads: p.RepeatReads, ChurnScore: churn,
			GodNode: p.GodNode,
		}
	}
	return pkgs
}

type pkgLinkAcc struct {
	ids                    map[string]bool
	extraB, extraA, extraM int
	langCounts             map[string]int
}

func buildFileNodes(in []FileInput, links map[string]RecordLinks, pkgs []PackageNode, layerColors map[string]string, linksOut map[string]RecordLinks) []FileNode {
	files := make([]FileNode, len(in))
	acc := make([]pkgLinkAcc, len(pkgs))
	for i := range acc {
		acc[i].ids = map[string]bool{}
		acc[i].langCounts = map[string]int{}
	}
	for i, f := range in {
		layer := resolveLayerName(f.Layer, f.Path)
		layerColors[layer] = colorForLayer(layer)
		lang := inferFileLang(f.Path, f.IsDoc)
		churn := f.ChurnScore
		if churn == 0 {
			churn = f.ClampHits + f.RepeatReads
		}
		normPath := strings.TrimPrefix(f.Path, "./")
		fl := normalizeLink(lookupFileLink(links, f.Path, normPath))
		if fl.Bugs+fl.ADRs+fl.Memories+len(fl.IDs) > 0 {
			linksOut[normPath] = fl
		}
		files[i] = FileNode{
			ID: f.Path, Label: path.Base(f.Path), Pkg: f.Pkg, PkgIdx: f.PkgIdx,
			Lang: lang, Layer: layer, IsTest: f.IsTest, IsDoc: f.IsDoc,
			SymbolCount: f.SymbolCount, LineCount: f.LineCount,
			PageRank: f.PageRank, Community: max(0, f.Community),
			InDegree: f.InDegree, OutDegree: f.OutDegree,
			ClampHits: f.ClampHits, RepeatReads: f.RepeatReads, ChurnScore: churn,
			GodNode: f.GodNode, BrainLinks: fl,
		}
		accumulatePkgFromFile(pkgs, acc, f, lang, fl)
	}
	finalizePackagesFromFiles(pkgs, acc)
	return files
}

func lookupFileLink(links map[string]RecordLinks, rawPath, normPath string) RecordLinks {
	if len(links) == 0 {
		return RecordLinks{}
	}
	if v, ok := links[normPath]; ok {
		return v
	}
	return links[rawPath]
}

func accumulatePkgFromFile(pkgs []PackageNode, acc []pkgLinkAcc, f FileInput, lang string, fl RecordLinks) {
	if f.PkgIdx < 0 || f.PkgIdx >= len(pkgs) {
		return
	}
	p := &pkgs[f.PkgIdx]
	if f.IsTest {
		p.TestFileCount++
	} else {
		p.ProdFileCount++
	}
	a := &acc[f.PkgIdx]
	a.langCounts[lang]++
	for _, id := range fl.IDs {
		a.ids[id] = true
	}
	idB, idA, idM := countIDKinds(fl.IDs)
	a.extraB += max(0, fl.Bugs-idB)
	a.extraA += max(0, fl.ADRs-idA)
	a.extraM += max(0, fl.Memories-idM)
}

func finalizePackagesFromFiles(pkgs []PackageNode, acc []pkgLinkAcc) {
	for i := range pkgs {
		p := &pkgs[i]
		if p.ProdFileCount+p.TestFileCount > 0 {
			p.FileCount = p.ProdFileCount + p.TestFileCount
			p.IsTest = p.ProdFileCount == 0
		}
		p.Lang = dominantLang(acc[i].langCounts)
		var ids []string
		for id := range acc[i].ids {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		b, a, m := countIDKinds(ids)
		p.BrainLinks = RecordLinks{
			Bugs:     b + acc[i].extraB,
			ADRs:     a + acc[i].extraA,
			Memories: m + acc[i].extraM,
			IDs:      ids,
		}
	}
}

func dominantLang(counts map[string]int) string {
	best, bestN := "go", 0
	for _, k := range []string{"go", "ts", "doc", "other"} {
		if counts[k] > bestN {
			best, bestN = k, counts[k]
		}
	}
	return best
}

func buildSymbolNodes(in []SymbolInput, files []FileNode, pkgs []PackageNode, layerColors map[string]string) []SymbolNode {
	syms := make([]SymbolNode, len(in))
	for i, s := range in {
		disp := s.Name
		if s.Recv != "" {
			disp = s.Recv + "." + s.Name
		}
		lang, layer := inferFileLang(s.File, false), resolveLayerName("", s.File)
		if s.FileIdx >= 0 && s.FileIdx < len(files) {
			lang = files[s.FileIdx].Lang
			layer = files[s.FileIdx].Layer
		}
		layerColors[layer] = colorForLayer(layer)
		endLine := max(s.StartLine, s.EndLine)
		syms[i] = SymbolNode{
			Name: s.Name, QualName: s.QualName, Recv: s.Recv, Disp: disp, Kind: s.Kind,
			Pkg: s.Pkg, PkgIdx: s.PkgIdx, File: s.File, FileIdx: s.FileIdx,
			Lang: lang, Layer: layer, IsTest: s.IsTest,
			StartLine: s.StartLine, EndLine: endLine, Sig: strings.TrimSpace(s.Sig),
			Exported: s.Exported, PageRank: s.PageRank, Community: max(0, s.Community),
			InDegree: s.InRefs, OutDegree: s.OutRefs, GodNode: s.GodNode,
		}
		if !s.IsTest && s.PkgIdx >= 0 && s.PkgIdx < len(pkgs) {
			pkgs[s.PkgIdx].ProdSymCount++
		}
	}
	for i := range pkgs {
		if pkgs[i].ProdSymCount == 0 && !pkgs[i].IsTest {
			pkgs[i].ProdSymCount = pkgs[i].SymbolCount
		}
	}
	return syms
}
