package symgraph

import (
	"database/sql"
	"strings"
)

// RawSymRef is one scanned row from sym_refs joined with files(path).
type RawSymRef struct {
	Path, OID, FromSym, ToPkg, ToName, Kind, Confidence string
	Weight                                              float64
	Line                                                int
}

func queryRawSymRefsDB(db *sql.DB, projects []string, includeDocs bool) ([]RawSymRef, error) {
	inSQL, args := projectsInClause(projects)
	q := `SELECT f.path, r.oid, r.from_sym, r.to_pkg, r.to_name, r.kind, r.confidence, r.weight, r.line
		FROM sym_refs r JOIN files f ON f.oid = r.oid WHERE f.project IN ` + inSQL + `
		ORDER BY f.path, r.line, r.from_sym, r.to_name`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RawSymRef
	for rows.Next() {
		var r RawSymRef
		if err := rows.Scan(&r.Path, &r.OID, &r.FromSym, &r.ToPkg, &r.ToName, &r.Kind, &r.Confidence, &r.Weight, &r.Line); err != nil {
			return nil, err
		}
		if IsExcludedASTPath(r.Path) || (!includeDocs && (IsDocPath(r.Path) || r.Kind == "doc_ref")) {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddImportRef resolves and records one import reference edge on g.
func (g *Graph) AddImportRef(r RawSymRef) {
	fromPkg := CanonicalPkgDir(r.Path)
	targetPkg, targetFile, ok := ResolveImportTarget(r.Path, r.ToPkg, g.goMods, g.pkgSet, g.fileByStem)
	if !ok || targetPkg == "" {
		return
	}
	g.recordPkgImport(fromPkg, targetPkg)
	impKey := r.Path + "\x00" + targetFile + "\x00" + targetPkg
	if !g.importSeen[impKey] {
		g.importSeen[impKey] = true
		g.ImportEdges = append(g.ImportEdges, ResolvedImportEdge{
			FromPath: r.Path, FromPkg: fromPkg, ToPath: targetFile, ToPkg: targetPkg, Line: r.Line,
		})
	}
	if r.ToName != "" && r.ToName != "<import>" {
		for _, toIdx := range g.lookupInTarget(targetPkg, targetFile, r.ToName) {
			g.Syms[toIdx].InRefs++
			g.In[toIdx] = append(g.In[toIdx], Edge{
				FromIdx: -1, ToIdx: toIdx, Kind: "import", Confidence: r.Confidence,
				QualifiedPkg: true, Weight: r.Weight, Line: r.Line, FromPath: r.Path,
			})
		}
	}
}

func (g *Graph) recordPkgImport(fromPkg, targetPkg string) {
	if fromPkg == "" || fromPkg == targetPkg {
		return
	}
	if g.pkgImports[fromPkg] == nil {
		g.pkgImports[fromPkg] = map[string]bool{}
	}
	g.pkgImports[fromPkg][targetPkg] = true
}

// AddRawRef resolves and records one non-import symbol reference edge on g.
func (g *Graph) AddRawRef(r RawSymRef) {
	fromIdxs := g.byOIDFrom[r.OID+"\x00"+r.FromSym]
	fromPkg := ""
	if len(fromIdxs) > 0 {
		fromPkg = g.Syms[fromIdxs[0]].Pkg
	} else if !IsDocPath(r.Path) {
		fromPkg = CanonicalPkgDir(r.Path)
	}
	targets, qualified := g.resolveRefTargets(r, fromPkg)
	for _, toIdx := range targets {
		g.Syms[toIdx].InRefs++
		if len(fromIdxs) == 0 {
			g.In[toIdx] = append(g.In[toIdx], Edge{FromIdx: -1, ToIdx: toIdx, Kind: r.Kind, Confidence: r.Confidence, QualifiedPkg: qualified, Weight: r.Weight, Line: r.Line, FromPath: r.Path})
			continue
		}
		for _, fromIdx := range fromIdxs {
			if fromIdx != toIdx {
				e := Edge{FromIdx: fromIdx, ToIdx: toIdx, Kind: r.Kind, Confidence: r.Confidence, QualifiedPkg: qualified, Weight: r.Weight, Line: r.Line, FromPath: r.Path}
				g.Out[fromIdx] = append(g.Out[fromIdx], e)
				g.In[toIdx] = append(g.In[toIdx], e)
			}
		}
	}
}

func (g *Graph) resolveRefTargets(r RawSymRef, fromPkg string) ([]int, bool) {
	switch r.Kind {
	case "doc_ref":
		return g.resolveDocRefTargets(r), false
	case "implements", "iface_call":
		if r.ToPkg != "" && g.pkgSet[r.ToPkg] {
			return g.lookupInTarget(r.ToPkg, "", r.ToName), false
		}
		return nil, false
	}
	if r.ToPkg != "" {
		return g.resolveQualifiedPkgRef(r, fromPkg)
	}
	return g.resolveUnqualifiedTargets(r.Path, fromPkg, r.ToName), false
}

func (g *Graph) resolveQualifiedPkgRef(r RawSymRef, fromPkg string) ([]int, bool) {
	targetPkg, targetFile, ok := ResolveImportTarget(r.Path, r.ToPkg, g.goMods, g.pkgSet, g.fileByStem)
	if ok && targetPkg != "" {
		if isStructuralCycleEdgeKind(r.Kind) {
			g.recordPkgImport(fromPkg, targetPkg)
		}
		return g.lookupInTarget(targetPkg, targetFile, r.ToName), true
	}
	if !isGoSource(r.Path) && !strings.Contains(r.ToPkg, "/") && !strings.Contains(r.ToPkg, ".") {
		if idxs := g.filterByPkg(g.byQual[r.ToPkg+"."+r.ToName], fromPkg); len(idxs) > 0 {
			return idxs, false
		}
	}
	return nil, true
}

func (g *Graph) resolveDocRefTargets(r RawSymRef) []int {
	if r.ToPkg != "" {
		if idxs := g.byQual[r.ToPkg+"."+r.ToName]; len(idxs) > 0 {
			return idxs
		}
		var suffixMatches []int
		for _, idx := range g.ByName[r.ToName] {
			pkg := g.Syms[idx].Pkg
			if pkg == r.ToPkg || strings.HasSuffix(pkg, "/"+r.ToPkg) {
				suffixMatches = append(suffixMatches, idx)
			}
		}
		return g.preferIfaceOrTopLevel(suffixMatches)
	}
	cands := g.ByName[r.ToName]
	if len(cands) == 0 || len(cands) > MaxImplicitImplementers {
		return nil
	}
	return g.preferIfaceOrTopLevel(cands)
}

func (g *Graph) lookupInTarget(targetPkg, targetFile, toName string) []int {
	if matched := g.lookupInTargetFile(targetFile, toName); len(matched) > 0 {
		return matched
	}
	if strings.Contains(toName, ".") {
		return g.filterByPkg(g.byQual[toName], targetPkg)
	}
	if idxs := g.filterByPkg(g.ByName[toName], targetPkg); len(idxs) > 0 {
		return g.preferIfaceOrTopLevel(idxs)
	}
	return nil
}

func (g *Graph) lookupInTargetFile(targetFile, toName string) []int {
	if targetFile == "" {
		return nil
	}
	var matched []int
	for _, idx := range g.ByPath[targetFile] {
		if g.Syms[idx].Name == toName || g.Syms[idx].QualName() == toName {
			matched = append(matched, idx)
		}
	}
	if len(matched) == 0 {
		return nil
	}
	return g.preferIfaceOrTopLevel(matched)
}

func (g *Graph) resolveUnqualifiedTargets(fromPath, fromPkg, toName string) []int {
	if strings.Contains(toName, ".") {
		return g.resolveUnqualifiedDotTarget(fromPath, fromPkg, toName)
	}
	cands := g.ByName[toName]
	if len(cands) == 0 {
		return nil
	}
	if same := g.samePkgMatches(cands, fromPkg); len(same) > 0 {
		return same
	}
	if isGoSource(fromPath) {
		return g.resolveCrossPkgGoMethods(fromPkg, cands)
	}
	return g.filterNonGoTargets(cands)
}

func (g *Graph) resolveUnqualifiedDotTarget(fromPath, fromPkg, toName string) []int {
	idxs := g.byQual[toName]
	if len(idxs) == 0 {
		return nil
	}
	if same := g.filterByPkg(idxs, fromPkg); len(same) > 0 {
		return same
	}
	if !isGoSource(fromPath) && len(idxs) <= MaxImplicitImplementers {
		return idxs
	}
	return nil
}

func (g *Graph) samePkgMatches(cands []int, fromPkg string) []int {
	if fromPkg == "" {
		return nil
	}
	if same := g.filterByPkg(cands, fromPkg); len(same) > 0 {
		return g.preferIfaceOrTopLevel(same)
	}
	return nil
}

func (g *Graph) filterNonGoTargets(cands []int) []int {
	var nonGo []int
	for _, idx := range cands {
		if !isGoSource(g.Syms[idx].Path) && !IsDocPath(g.Syms[idx].Path) {
			nonGo = append(nonGo, idx)
		}
	}
	if len(nonGo) == 0 || len(nonGo) > MaxImplicitImplementers {
		return nil
	}
	return g.preferIfaceOrTopLevel(nonGo)
}

func (g *Graph) resolveCrossPkgGoMethods(fromPkg string, cands []int) []int {
	allowedPkgs := g.pkgImports[fromPkg]
	if len(allowedPkgs) == 0 {
		return nil
	}
	var out []int
	for _, idx := range cands {
		s := g.Syms[idx]
		if isGoSource(s.Path) && s.Recv != "" && allowedPkgs[s.Pkg] {
			out = append(out, idx)
		}
	}
	if len(out) == 0 || len(out) > MaxImplicitImplementers {
		return nil
	}
	return out
}

func (g *Graph) preferIfaceOrTopLevel(idxs []int) []int {
	var pref []int
	for _, idx := range idxs {
		if g.Syms[idx].Recv == "" || g.Syms[idx].Kind == "interface_method" {
			pref = append(pref, idx)
		}
	}
	if len(pref) > 0 {
		return pref
	}
	return idxs
}

func (g *Graph) filterByPkg(idxs []int, pkg string) []int {
	if pkg == "" {
		return idxs
	}
	var out []int
	for _, idx := range idxs {
		if g.Syms[idx].Pkg == pkg {
			out = append(out, idx)
		}
	}
	return out
}

func (g *Graph) collectPackageEdgeWeights(includeTests bool) ([]string, map[[2]string]int) {
	pkgSet := map[string]bool{}
	weights := map[[2]string]int{}
	for _, f := range g.Files {
		if keepPkgWeightPath(f.Pkg, f.Path, includeTests) {
			pkgSet[f.Pkg] = true
		}
	}
	g.addSymbolPkgEdgeWeights(includeTests, pkgSet, weights)
	g.addImportPkgEdgeWeights(includeTests, pkgSet, weights)
	return SortedMapKeys(pkgSet), weights
}

func keepPkgWeightPath(pkg, p string, includeTests bool) bool {
	return pkg != "" && !IsDocPath(p) && (includeTests || !IsTestPath(p))
}

func (g *Graph) addSymbolPkgEdgeWeights(includeTests bool, pkgSet map[string]bool, weights map[[2]string]int) {
	for i, s := range g.Syms {
		if !keepPkgWeightPath(s.Pkg, s.Path, includeTests) {
			continue
		}
		pkgSet[s.Pkg] = true
		for _, e := range g.Out[i] {
			to := g.Syms[e.ToIdx]
			if to.Pkg != s.Pkg && keepPkgWeightPath(to.Pkg, to.Path, includeTests) {
				weights[[2]string{s.Pkg, to.Pkg}]++
			}
		}
	}
}

func (g *Graph) addImportPkgEdgeWeights(includeTests bool, pkgSet map[string]bool, weights map[[2]string]int) {
	for _, ie := range g.ImportEdges {
		if ie.FromPkg == ie.ToPkg || !keepPkgWeightPath(ie.FromPkg, ie.FromPath, includeTests) {
			continue
		}
		if pkgSet[ie.FromPkg] && pkgSet[ie.ToPkg] {
			weights[[2]string{ie.FromPkg, ie.ToPkg}]++
		}
	}
}
