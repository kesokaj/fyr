package search

import (
	"database/sql"
	"strings"
)

type rawSymRef struct {
	path, oid, fromSym, toPkg, toName, kind, confidence string
	weight                                              float64
	line                                                int
}

func queryRawSymRefsDB(db *sql.DB, projects []string, includeDocs bool) ([]rawSymRef, error) {
	inSQL, args := projectsInClause(projects)
	q := `SELECT f.path, r.oid, r.from_sym, r.to_pkg, r.to_name, r.kind, r.confidence, r.weight, r.line
		FROM sym_refs r JOIN files f ON f.oid = r.oid WHERE f.project IN ` + inSQL + `
		ORDER BY f.path, r.line, r.from_sym, r.to_name`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rawSymRef
	for rows.Next() {
		var r rawSymRef
		if err := rows.Scan(&r.path, &r.oid, &r.fromSym, &r.toPkg, &r.toName, &r.kind, &r.confidence, &r.weight, &r.line); err != nil {
			return nil, err
		}
		if isExcludedASTPath(r.path) || (!includeDocs && (isDocPath(r.path) || r.kind == "doc_ref")) {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (g *symGraph) addImportRef(r rawSymRef) {
	fromPkg := canonicalPkgDir(r.path)
	targetPkg, targetFile, ok := resolveImportTarget(r.path, r.toPkg, g.goMods, g.pkgSet, g.fileByStem)
	if !ok || targetPkg == "" {
		return
	}
	if fromPkg != "" && fromPkg != targetPkg {
		if g.pkgImports[fromPkg] == nil {
			g.pkgImports[fromPkg] = map[string]bool{}
		}
		g.pkgImports[fromPkg][targetPkg] = true
	}
	impKey := r.path + "\x00" + targetFile + "\x00" + targetPkg
	if !g.importSeen[impKey] {
		g.importSeen[impKey] = true
		g.importEdges = append(g.importEdges, resolvedImportEdge{
			fromPath: r.path, fromPkg: fromPkg, toPath: targetFile, toPkg: targetPkg, line: r.line,
		})
	}
	if r.toName != "" && r.toName != "<import>" {
		for _, toIdx := range g.lookupInTarget(targetPkg, targetFile, r.toName) {
			g.syms[toIdx].InRefs++
			g.in[toIdx] = append(g.in[toIdx], graphEdge{
				fromIdx: -1, toIdx: toIdx, kind: "import", confidence: r.confidence,
				qualifiedPkg: true, weight: r.weight, line: r.line, fromPath: r.path,
			})
		}
	}
}

func (g *symGraph) addRawRef(r rawSymRef) {
	fromIdxs := g.byOIDFrom[r.oid+"\x00"+r.fromSym]
	fromPkg := ""
	if len(fromIdxs) > 0 {
		fromPkg = g.syms[fromIdxs[0]].Pkg
	} else if !isDocPath(r.path) {
		fromPkg = canonicalPkgDir(r.path)
	}
	targets, qualified := g.resolveRefTargets(r, fromPkg)
	for _, toIdx := range targets {
		g.syms[toIdx].InRefs++
		if len(fromIdxs) == 0 {
			g.in[toIdx] = append(g.in[toIdx], graphEdge{fromIdx: -1, toIdx: toIdx, kind: r.kind, confidence: r.confidence, qualifiedPkg: qualified, weight: r.weight, line: r.line, fromPath: r.path})
			continue
		}
		for _, fromIdx := range fromIdxs {
			if fromIdx != toIdx {
				e := graphEdge{fromIdx: fromIdx, toIdx: toIdx, kind: r.kind, confidence: r.confidence, qualifiedPkg: qualified, weight: r.weight, line: r.line, fromPath: r.path}
				g.out[fromIdx] = append(g.out[fromIdx], e)
				g.in[toIdx] = append(g.in[toIdx], e)
			}
		}
	}
}

func (g *symGraph) resolveRefTargets(r rawSymRef, fromPkg string) ([]int, bool) {
	if r.kind == "doc_ref" {
		return g.resolveDocRefTargets(r), false
	}
	if r.kind == "implements" || r.kind == "iface_call" {
		if r.toPkg != "" && g.pkgSet[r.toPkg] {
			return g.lookupInTarget(r.toPkg, "", r.toName), false
		}
		return nil, false
	}
	if r.toPkg != "" {
		targetPkg, targetFile, ok := resolveImportTarget(r.path, r.toPkg, g.goMods, g.pkgSet, g.fileByStem)
		if ok && targetPkg != "" {
			if fromPkg != "" && fromPkg != targetPkg && isStructuralCycleEdgeKind(r.kind) {
				if g.pkgImports[fromPkg] == nil {
					g.pkgImports[fromPkg] = map[string]bool{}
				}
				g.pkgImports[fromPkg][targetPkg] = true
			}
			return g.lookupInTarget(targetPkg, targetFile, r.toName), true
		}
		if !isGoSource(r.path) && !strings.Contains(r.toPkg, "/") && !strings.Contains(r.toPkg, ".") {
			if idxs := g.filterByPkg(g.byQual[r.toPkg+"."+r.toName], fromPkg); len(idxs) > 0 {
				return idxs, false
			}
		}
		return nil, true
	}
	return g.resolveUnqualifiedTargets(r.path, fromPkg, r.toName), false
}

func (g *symGraph) resolveDocRefTargets(r rawSymRef) []int {
	if r.toPkg != "" {
		if idxs := g.byQual[r.toPkg+"."+r.toName]; len(idxs) > 0 {
			return idxs
		}
		var suffixMatches []int
		for _, idx := range g.byName[r.toName] {
			pkg := g.syms[idx].Pkg
			if pkg == r.toPkg || strings.HasSuffix(pkg, "/"+r.toPkg) {
				suffixMatches = append(suffixMatches, idx)
			}
		}
		return g.preferIfaceOrTopLevel(suffixMatches)
	}
	cands := g.byName[r.toName]
	if len(cands) == 0 || len(cands) > maxImplicitImplementers {
		return nil
	}
	return g.preferIfaceOrTopLevel(cands)
}

func (g *symGraph) lookupInTarget(targetPkg, targetFile, toName string) []int {
	if targetFile != "" {
		if fileIdxs := g.byPath[targetFile]; len(fileIdxs) > 0 {
			var matched []int
			for _, idx := range fileIdxs {
				if g.syms[idx].Name == toName || g.syms[idx].QualName() == toName {
					matched = append(matched, idx)
				}
			}
			if len(matched) > 0 {
				return g.preferIfaceOrTopLevel(matched)
			}
		}
	}
	if strings.Contains(toName, ".") {
		return g.filterByPkg(g.byQual[toName], targetPkg)
	}
	if idxs := g.filterByPkg(g.byName[toName], targetPkg); len(idxs) > 0 {
		return g.preferIfaceOrTopLevel(idxs)
	}
	return nil
}

func (g *symGraph) resolveUnqualifiedTargets(fromPath, fromPkg, toName string) []int {
	if strings.Contains(toName, ".") {
		if idxs := g.byQual[toName]; len(idxs) > 0 {
			if same := g.filterByPkg(idxs, fromPkg); len(same) > 0 {
				return same
			}
			if !isGoSource(fromPath) && len(idxs) <= maxImplicitImplementers {
				return idxs
			}
		}
		return nil
	}
	cands := g.byName[toName]
	if len(cands) == 0 {
		return nil
	}
	if fromPkg != "" {
		if same := g.filterByPkg(cands, fromPkg); len(same) > 0 {
			return g.preferIfaceOrTopLevel(same)
		}
	}
	if isGoSource(fromPath) {
		return g.resolveCrossPkgGoMethods(fromPkg, cands)
	}
	var nonGo []int
	for _, idx := range cands {
		if !isGoSource(g.syms[idx].Path) && !isDocPath(g.syms[idx].Path) {
			nonGo = append(nonGo, idx)
		}
	}
	if len(nonGo) == 0 || len(nonGo) > maxImplicitImplementers {
		return nil
	}
	return g.preferIfaceOrTopLevel(nonGo)
}

func (g *symGraph) resolveCrossPkgGoMethods(fromPkg string, cands []int) []int {
	allowedPkgs := g.pkgImports[fromPkg]
	if len(allowedPkgs) == 0 {
		return nil
	}
	var out []int
	for _, idx := range cands {
		s := g.syms[idx]
		if isGoSource(s.Path) && s.Recv != "" && allowedPkgs[s.Pkg] {
			out = append(out, idx)
		}
	}
	if len(out) == 0 || len(out) > maxImplicitImplementers {
		return nil
	}
	return out
}

func (g *symGraph) preferIfaceOrTopLevel(idxs []int) []int {
	var pref []int
	for _, idx := range idxs {
		if g.syms[idx].Recv == "" || g.syms[idx].Kind == "interface_method" {
			pref = append(pref, idx)
		}
	}
	if len(pref) > 0 {
		return pref
	}
	return idxs
}

func (g *symGraph) filterByPkg(idxs []int, pkg string) []int {
	if pkg == "" {
		return idxs
	}
	var out []int
	for _, idx := range idxs {
		if g.syms[idx].Pkg == pkg {
			out = append(out, idx)
		}
	}
	return out
}

func (g *symGraph) collectPackageEdgeWeights(includeTests bool) ([]string, map[[2]string]int) {
	pkgSet := map[string]bool{}
	weights := map[[2]string]int{}
	for _, f := range g.files {
		if f.pkg != "" && !isDocPath(f.path) && (includeTests || !isTestPath(f.path)) {
			pkgSet[f.pkg] = true
		}
	}
	for i, s := range g.syms {
		if s.Pkg == "" || isDocPath(s.Path) || (!includeTests && isTestPath(s.Path)) {
			continue
		}
		pkgSet[s.Pkg] = true
		for _, e := range g.out[i] {
			to := g.syms[e.toIdx]
			if to.Pkg == "" || to.Pkg == s.Pkg || isDocPath(to.Path) || (!includeTests && isTestPath(to.Path)) {
				continue
			}
			weights[[2]string{s.Pkg, to.Pkg}]++
		}
	}
	for _, ie := range g.importEdges {
		if ie.fromPkg == "" || ie.toPkg == "" || ie.fromPkg == ie.toPkg || isDocPath(ie.fromPath) || (!includeTests && isTestPath(ie.fromPath)) {
			continue
		}
		if pkgSet[ie.fromPkg] && pkgSet[ie.toPkg] {
			weights[[2]string{ie.fromPkg, ie.toPkg}]++
		}
	}
	return sortedMapKeys(pkgSet), weights
}
