package symgraph

import (
	"path"
	"regexp"
	"strings"
)

type tsBinding struct {
	spec string
	name string
}

var (
	tsImportFromRe = regexp.MustCompile(`(?s)^\s*(?:import|export)\s+(?:type\s+)?(.+?)\s+from\s+['"]([^'"]+)['"]`)
	tsSideImportRe = regexp.MustCompile(`^\s*import\s+['"]([^'"]+)['"]`)
	tsTypeIdentRe  = regexp.MustCompile(`(?::|extends|implements|<)\s*([A-Za-z_$][A-Za-z0-9_$]*)`)
)

func extractTSSymbols(filePath, text string) ([]SymbolRow, []SymRefRow) {
	if IsExcludedASTPath(filePath) {
		return nil, nil
	}
	pkg := CanonicalPkgDir(filePath)
	lines := SplitLines(text)
	impSyms, impNS, extSyms, impRefs := parseTSImports(filePath, lines)
	var syms []SymbolRow
	refs := append([]SymRefRow(nil), impRefs...)
	curSym := "<module>"
	for i, l := range lines {
		trim := strings.TrimSpace(l)
		if trim == "" || strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "import ") || strings.HasPrefix(trim, "import{") {
			continue
		}
		if m := tsDeclRe.FindStringSubmatch(l); m != nil {
			exp := boolToInt(strings.TrimSpace(m[1]) == "export")
			kind, name := m[2], m[3]
			if kind == "const" && exp == 0 {
				refs = append(refs, scanTSLineRefs(trim, curSym, i+1, impSyms, impNS, extSyms)...)
				continue
			}
			if kind == "function" {
				kind = "func"
			}
			curSym = name
			syms = append(syms, SymbolRow{
				Name: name, Kind: kind, Pkg: pkg, StartLine: i + 1, EndLine: i + 1,
				Sig: strings.TrimSpace(strings.TrimSuffix(trim, "{")), Exported: exp, BodyHash: HashString(trim),
			})
			refs = append(refs, scanTSLineRefs(trim, curSym, i+1, impSyms, impNS, extSyms)...)
			continue
		}
		refs = append(refs, scanTSLineRefs(trim, curSym, i+1, impSyms, impNS, extSyms)...)
	}
	return syms, DedupSymRefs(refs)
}

func parseTSImports(filePath string, lines []string) (map[string]tsBinding, map[string]string, map[string]bool, []SymRefRow) {
	impSyms := map[string]tsBinding{}
	impNS := map[string]string{}
	extSyms := map[string]bool{}
	var refs []SymRefRow
	for i := 0; i < len(lines); i++ {
		trim := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trim, "import") && !strings.HasPrefix(trim, "export") {
			continue
		}
		if strings.HasPrefix(trim, "export ") && !strings.Contains(trim, " from ") && !strings.HasPrefix(trim, "export {") && !strings.HasPrefix(trim, "export *") && !strings.HasPrefix(trim, "export type {") {
			continue
		}
		startLine := i + 1
		stmt := trim
		for !strings.Contains(stmt, "from ") && !tsSideImportRe.MatchString(stmt) && i+1 < len(lines) && i-startLine < 20 {
			i++
			stmt += " " + strings.TrimSpace(lines[i])
			if strings.Contains(lines[i], ";") {
				break
			}
		}
		refs = append(refs, parseSingleTSImport(filePath, stmt, startLine, impSyms, impNS, extSyms)...)
	}
	return impSyms, impNS, extSyms, refs
}

func parseSingleTSImport(filePath, stmt string, lineNum int, syms map[string]tsBinding, ns map[string]string, ext map[string]bool) []SymRefRow {
	if sm := tsSideImportRe.FindStringSubmatch(stmt); sm != nil {
		if resolved, ok := resolveTSRelativeSpec(filePath, sm[1]); ok {
			return []SymRefRow{{
				FromSym: "<import>", ToPkg: resolved, ToName: "<import>",
				Kind: "import", Confidence: ConfidenceExtracted, Weight: WeightExtracted, Line: lineNum,
			}}
		}
		return nil
	}
	m := tsImportFromRe.FindStringSubmatch(stmt)
	if m == nil {
		return nil
	}
	clause, spec := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	resolved, isRel := resolveTSRelativeSpec(filePath, spec)
	return bindTSClause(clause, resolved, isRel, lineNum, syms, ns, ext)
}

func bindTSClause(clause, resolvedSpec string, isRel bool, lineNum int, syms map[string]tsBinding, ns map[string]string, ext map[string]bool) []SymRefRow {
	var refs []SymRefRow
	if isRel {
		refs = append(refs, SymRefRow{
			FromSym: "<import>", ToPkg: resolvedSpec, ToName: "<import>",
			Kind: "import", Confidence: ConfidenceExtracted, Weight: WeightExtracted, Line: lineNum,
		})
	}
	if braceStart := strings.IndexByte(clause, '{'); braceStart >= 0 {
		if braceEnd := strings.LastIndexByte(clause, '}'); braceEnd > braceStart {
			refs = append(refs, bindTSNamedList(clause[braceStart+1:braceEnd], resolvedSpec, isRel, lineNum, syms, ext)...)
		}
		clause = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(clause[:braceStart]), ","))
	}
	if clause == "" || clause == "*" {
		return refs
	}
	if strings.HasPrefix(clause, "* as ") {
		alias := strings.TrimSpace(strings.TrimPrefix(clause, "* as "))
		if isRel {
			ns[alias] = resolvedSpec
		} else if alias != "" {
			ext[alias] = true
		}
		return refs
	}
	if isRel {
		syms[clause] = tsBinding{spec: resolvedSpec, name: clause}
	} else {
		ext[clause] = true
	}
	return refs
}

func bindTSNamedList(body, resolvedSpec string, isRel bool, lineNum int, syms map[string]tsBinding, ext map[string]bool) []SymRefRow {
	var refs []SymRefRow
	for _, part := range strings.Split(body, ",") {
		tok := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "type "))
		if tok == "" {
			continue
		}
		orig, local := tok, tok
		if lhs, rhs, ok := strings.Cut(tok, " as "); ok {
			orig, local = strings.TrimSpace(lhs), strings.TrimSpace(rhs)
		}
		if !isRel {
			if local != "" {
				ext[local] = true
			}
			continue
		}
		if local != "" && orig != "" {
			syms[local] = tsBinding{spec: resolvedSpec, name: orig}
			refs = append(refs, SymRefRow{
				FromSym: "<import>", ToPkg: resolvedSpec, ToName: orig,
				Kind: "import", Confidence: ConfidenceExtracted, Weight: WeightExtracted, Line: lineNum,
			})
		}
	}
	return refs
}

func resolveTSRelativeSpec(filePath, spec string) (string, bool) {
	if !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") {
		return "", false
	}
	dir := CanonicalPkgDir(filePath)
	joined := spec
	if dir != "." {
		joined = dir + "/" + spec
	}
	clean := strings.TrimPrefix(path.Clean(joined), "./")
	if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return "./" + clean, true
}

func scanTSLineRefs(line, curSym string, lineNum int, syms map[string]tsBinding, ns map[string]string, ext map[string]bool) []SymRefRow {
	var out []SymRefRow
	for _, m := range callIdentRe.FindAllStringSubmatch(line, -1) {
		if row, ok := resolveTSCallMatch(m[1], m[2], curSym, lineNum, syms, ns, ext); ok {
			out = append(out, row)
		}
	}
	return append(out, scanTSTypeRefs(line, curSym, lineNum, syms, ext)...)
}

func resolveTSCallMatch(qual, name, curSym string, lineNum int, syms map[string]tsBinding, ns map[string]string, ext map[string]bool) (SymRefRow, bool) {
	if codeSkip[name] || codeSkip[qual] || name == curSym {
		return SymRefRow{}, false
	}
	if qual != "" {
		return resolveTSQualifiedCall(qual, name, curSym, lineNum, ns, ext)
	}
	if ext[name] {
		return SymRefRow{}, false
	}
	toPkg, toName := "", name
	if b, ok := syms[name]; ok {
		toPkg, toName = b.spec, b.name
	}
	return SymRefRow{FromSym: curSym, ToPkg: toPkg, ToName: toName, Kind: "call", Confidence: ConfidenceExtracted, Weight: WeightExtracted, Line: lineNum}, true
}

func resolveTSQualifiedCall(qual, name, curSym string, lineNum int, ns map[string]string, ext map[string]bool) (SymRefRow, bool) {
	if ext[qual] {
		return SymRefRow{}, false
	}
	if spec, ok := ns[qual]; ok {
		return SymRefRow{FromSym: curSym, ToPkg: spec, ToName: name, Kind: "call", Confidence: ConfidenceExtracted, Weight: WeightExtracted, Line: lineNum}, true
	}
	if qual == "this" {
		qual = ""
	}
	return SymRefRow{FromSym: curSym, ToPkg: qual, ToName: name, Kind: "call", Confidence: ConfidenceExtracted, Weight: WeightExtracted, Line: lineNum}, true
}

func scanTSTypeRefs(line, curSym string, lineNum int, syms map[string]tsBinding, ext map[string]bool) []SymRefRow {
	var out []SymRefRow
	for _, tm := range tsTypeIdentRe.FindAllStringSubmatch(line, -1) {
		tName := tm[1]
		if codeSkip[tName] || ext[tName] {
			continue
		}
		if b, ok := syms[tName]; ok {
			out = append(out, SymRefRow{FromSym: curSym, ToPkg: b.spec, ToName: b.name, Kind: "type_ref", Confidence: ConfidenceExtracted, Weight: WeightExtracted, Line: lineNum})
		}
	}
	return out
}
