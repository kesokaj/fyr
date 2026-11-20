package search

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

func extractTSSymbols(filePath, text string) ([]symbolRow, []symRefRow) {
	if isExcludedASTPath(filePath) {
		return nil, nil
	}
	pkg := canonicalPkgDir(filePath)
	lines := splitLines(text)
	impSyms, impNS, extSyms, impRefs := parseTSImports(filePath, lines)
	var syms []symbolRow
	refs := append([]symRefRow(nil), impRefs...)
	curSym := "<module>"
	for i, l := range lines {
		trim := strings.TrimSpace(l)
		if trim == "" || strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "import ") || strings.HasPrefix(trim, "import{") {
			continue
		}
		if m := tsDeclRe.FindStringSubmatch(l); m != nil {
			exp := boolInt(strings.TrimSpace(m[1]) == "export")
			kind, name := m[2], m[3]
			if kind == "const" && exp == 0 {
				refs = append(refs, scanTSLineRefs(trim, curSym, i+1, impSyms, impNS, extSyms)...)
				continue
			}
			if kind == "function" {
				kind = "func"
			}
			curSym = name
			syms = append(syms, symbolRow{
				name: name, kind: kind, pkg: pkg, startLine: i + 1, endLine: i + 1,
				sig: strings.TrimSpace(strings.TrimSuffix(trim, "{")), exported: exp, bodyHash: hashString(trim),
			})
			refs = append(refs, scanTSLineRefs(trim, curSym, i+1, impSyms, impNS, extSyms)...)
			continue
		}
		refs = append(refs, scanTSLineRefs(trim, curSym, i+1, impSyms, impNS, extSyms)...)
	}
	return syms, dedupSymRefs(refs)
}

func parseTSImports(filePath string, lines []string) (map[string]tsBinding, map[string]string, map[string]bool, []symRefRow) {
	impSyms := map[string]tsBinding{}
	impNS := map[string]string{}
	extSyms := map[string]bool{}
	var refs []symRefRow
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

func parseSingleTSImport(filePath, stmt string, lineNum int, syms map[string]tsBinding, ns map[string]string, ext map[string]bool) []symRefRow {
	if sm := tsSideImportRe.FindStringSubmatch(stmt); sm != nil {
		if resolved, ok := resolveTSRelativeSpec(filePath, sm[1]); ok {
			return []symRefRow{{
				fromSym: "<import>", toPkg: resolved, toName: "<import>",
				kind: "import", confidence: ConfidenceExtracted, weight: WeightExtracted, line: lineNum,
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

func bindTSClause(clause, resolvedSpec string, isRel bool, lineNum int, syms map[string]tsBinding, ns map[string]string, ext map[string]bool) []symRefRow {
	var refs []symRefRow
	if isRel {
		refs = append(refs, symRefRow{
			fromSym: "<import>", toPkg: resolvedSpec, toName: "<import>",
			kind: "import", confidence: ConfidenceExtracted, weight: WeightExtracted, line: lineNum,
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

func bindTSNamedList(body, resolvedSpec string, isRel bool, lineNum int, syms map[string]tsBinding, ext map[string]bool) []symRefRow {
	var refs []symRefRow
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
			refs = append(refs, symRefRow{
				fromSym: "<import>", toPkg: resolvedSpec, toName: orig,
				kind: "import", confidence: ConfidenceExtracted, weight: WeightExtracted, line: lineNum,
			})
		}
	}
	return refs
}

func resolveTSRelativeSpec(filePath, spec string) (string, bool) {
	if !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") {
		return "", false
	}
	dir := canonicalPkgDir(filePath)
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

func scanTSLineRefs(line, curSym string, lineNum int, syms map[string]tsBinding, ns map[string]string, ext map[string]bool) []symRefRow {
	var out []symRefRow
	for _, m := range callTokenRe.FindAllStringSubmatch(line, -1) {
		qual, name := m[1], m[2]
		if codeSkip[name] || codeSkip[qual] || name == curSym {
			continue
		}
		if qual != "" {
			if ext[qual] {
				continue
			}
			if spec, ok := ns[qual]; ok {
				out = append(out, symRefRow{fromSym: curSym, toPkg: spec, toName: name, kind: "call", confidence: ConfidenceExtracted, weight: WeightExtracted, line: lineNum})
				continue
			}
			if qual == "this" {
				qual = ""
			}
			out = append(out, symRefRow{fromSym: curSym, toPkg: qual, toName: name, kind: "call", confidence: ConfidenceExtracted, weight: WeightExtracted, line: lineNum})
			continue
		}
		if ext[name] {
			continue
		}
		if b, ok := syms[name]; ok {
			out = append(out, symRefRow{fromSym: curSym, toPkg: b.spec, toName: b.name, kind: "call", confidence: ConfidenceExtracted, weight: WeightExtracted, line: lineNum})
			continue
		}
		out = append(out, symRefRow{fromSym: curSym, toName: name, kind: "call", confidence: ConfidenceExtracted, weight: WeightExtracted, line: lineNum})
	}
	for _, tm := range tsTypeIdentRe.FindAllStringSubmatch(line, -1) {
		tName := tm[1]
		if codeSkip[tName] || ext[tName] {
			continue
		}
		if b, ok := syms[tName]; ok {
			out = append(out, symRefRow{fromSym: curSym, toPkg: b.spec, toName: b.name, kind: "type_ref", confidence: ConfidenceExtracted, weight: WeightExtracted, line: lineNum})
		}
	}
	return out
}
