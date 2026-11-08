package search

import (
	"go/ast"
	"path"
	"regexp"
	"strings"
	"unicode"
)

func extractSymbolsAndRefs(filePath, text string) ([]symbolRow, []symRefRow) {
	switch strings.ToLower(path.Ext(filePath)) {
	case ".go":
		return extractGoSymbols(filePath, text)
	case ".ts", ".tsx", ".js", ".jsx", ".mjs":
		return extractTSSymbols(filePath, text)
	case ".py":
		return extractPySymbols(filePath, text)
	case ".sh":
		return extractShSymbols(filePath, text)
	case ".md":
		return nil, extractMarkdownDocRefs(text)
	}
	return nil, nil
}

type recvKey struct{ pkg, recv string }

func groupInterfaceAndRecvMethods(syms []symbolRow) (map[recvKey]symbolRow, map[recvKey][]symbolRow, map[recvKey]map[string]bool) {
	ifaces := map[recvKey]symbolRow{}
	ifaceMethods := map[recvKey][]symbolRow{}
	recvMethods := map[recvKey]map[string]bool{}
	for _, sy := range syms {
		switch sy.kind {
		case "interface":
			ifaces[recvKey{sy.pkg, sy.name}] = sy
		case "interface_method":
			k := recvKey{sy.pkg, sy.recv}
			ifaceMethods[k] = append(ifaceMethods[k], sy)
		case "method":
			if sy.recv != "" {
				k := recvKey{sy.pkg, sy.recv}
				if recvMethods[k] == nil {
					recvMethods[k] = map[string]bool{}
				}
				recvMethods[k][sy.name] = true
			}
		}
	}
	return ifaces, ifaceMethods, recvMethods
}

func findInterfaceImplementers(iface symbolRow, req []symbolRow, recvMethods map[recvKey]map[string]bool) []recvKey {
	var out []recvKey
	for rk, mset := range recvMethods {
		if rk.recv == iface.name {
			continue
		}
		ok := true
		for _, m := range req {
			if !mset[m.name] {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, rk)
		}
	}
	return out
}

var goPredeclared = map[string]bool{}
var stdlibOnlyMethods = map[string]bool{}

func init() {
	for _, w := range strings.Fields("bool byte comparable complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr any true false iota nil append cap clear close complex copy delete imag len make max min new panic print println real recover _") {
		goPredeclared[w] = true
	}
	for _, m := range strings.Fields("Scan QueryRow QueryRowContext Exec ExecContext Prepare Next Err CombinedOutput Output WriteString WriteByte ReadByte FindString FindStringSubmatch FindAllString FindAllStringSubmatch MatchString ReplaceAllString SubexpNames Truncate Unix UnixNano UnixMilli Milliseconds Seconds Minutes Hours AfterFunc Chdir SetMaxOpenConns Lstat Chmod Chtimes MkdirAll ReadFile WriteFile OpenFile ReadDir EvalSymlinks IsRegular Perm Fatal Fatalf Errorf Helper TempDir Cleanup Skip") {
		stdlibOnlyMethods[m] = true
	}
	for _, w := range strings.Fields("if for while switch catch function return typeof instanceof new import export super console Math JSON Object Array Promise String Number Boolean Error Map Set Date RegExp parseInt parseFloat print len range str int float bool list dict set tuple isinstance getattr setattr") {
		codeSkip[w] = true
	}
}

func isGoPredeclared(name string) bool    { return goPredeclared[name] }
func isStdlibOnlyMethod(name string) bool { return stdlibOnlyMethods[mName(name)] }
func mName(s string) string               { return s }

func isSQLChain(expr ast.Expr) bool {
	for expr != nil {
		switch x := expr.(type) {
		case *ast.SelectorExpr:
			if x.Sel.Name == "db" || x.Sel.Name == "chunk" || x.Sel.Name == "ref" || x.Sel.Name == "sym" || x.Sel.Name == "symRef" {
				return true
			}
			expr = x.X
		case *ast.CallExpr:
			expr = x.Fun
		default:
			return false
		}
	}
	return false
}

var (
	goPkgRe      = regexp.MustCompile(`^\s*package\s+([A-Za-z_][A-Za-z0-9_]*)`)
	goFuncLineRe = regexp.MustCompile(`^func\s+(?:\(\s*[A-Za-z0-9_]*\s*\*?([A-Za-z_][A-Za-z0-9_]*)(?:\[[^\]]*\])?\s*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*(?:\[[^\]]*\])?\(`)
	goTypeLineRe = regexp.MustCompile(`^type\s+([A-Za-z_][A-Za-z0-9_]*)\s+(struct|interface|[A-Za-z0-9_.*\[\]]+)`)
	callTokenRe  = regexp.MustCompile(`(?:([A-Za-z_][A-Za-z0-9_]*)\.)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	tsDeclRe     = regexp.MustCompile(`^\s*(export\s+)?(?:default\s+)?(?:async\s+)?(function|class|interface|type|const)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	pyDeclRe     = regexp.MustCompile(`^(\s*)(?:async\s+)?(def|class)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	shDeclRe     = regexp.MustCompile(`^\s*(?:function\s+([A-Za-z_][A-Za-z0-9_-]*)\s*(?:\(\s*\))?\s*\{?|([A-Za-z_][A-Za-z0-9_-]*)\s*\(\s*\)\s*\{)`)
	codeSkip     = map[string]bool{}
)

func parseGoPackage(filePath, text string) string {
	for _, l := range splitLines(text) {
		if m := goPkgRe.FindStringSubmatch(l); m != nil {
			return m[1]
		}
	}
	return path.Base(path.Dir(filePath))
}

func parseGoFallback(filePath, text, pkgName string) ([]symbolRow, []symRefRow) {
	lines := splitLines(text)
	var syms []symbolRow
	var refs []symRefRow
	curSym := ""
	for i, l := range lines {
		trim := strings.TrimSpace(l)
		if m := goFuncLineRe.FindStringSubmatch(trim); m != nil {
			recv, name := m[1], m[2]
			kind := "func"
			curSym = name
			if recv != "" {
				kind, curSym = "method", recv+"."+name
			}
			syms = append(syms, symbolRow{
				name: name, recv: recv, kind: kind, pkg: pkgName,
				startLine: i + 1, endLine: i + 1, sig: strings.TrimSuffix(trim, "{"),
				exported: boolInt(ast.IsExported(name)), bodyHash: hashString(trim),
			})
			if _, body, ok := strings.Cut(trim, "{"); ok && body != "" {
				refs = append(refs, scanLineCalls(body, curSym, i+1, goPredeclared)...)
			}
			continue
		}
		if m := goTypeLineRe.FindStringSubmatch(trim); m != nil {
			name, kind := m[1], m[2]
			if kind != "struct" && kind != "interface" {
				kind = "type"
			}
			curSym = name
			syms = append(syms, symbolRow{
				name: name, kind: kind, pkg: pkgName, startLine: i + 1, endLine: i + 1,
				sig: "type " + name + " " + kind, exported: boolInt(ast.IsExported(name)), bodyHash: hashString(trim),
			})
			continue
		}
		if curSym != "" {
			refs = append(refs, scanLineCalls(trim, curSym, i+1, goPredeclared)...)
		}
	}
	return syms, refs
}

func extractTSSymbols(filePath, text string) ([]symbolRow, []symRefRow) {
	pkg := path.Base(path.Dir(filePath))
	lines := splitLines(text)
	var syms []symbolRow
	var refs []symRefRow
	curSym := "<module>"
	for i, l := range lines {
		trim := strings.TrimSpace(l)
		if m := tsDeclRe.FindStringSubmatch(l); m != nil {
			exp := boolInt(strings.TrimSpace(m[1]) == "export")
			kind, name := m[2], m[3]
			if kind == "function" {
				kind = "func"
			}
			curSym = name
			syms = append(syms, symbolRow{
				name: name, kind: kind, pkg: pkg, startLine: i + 1, endLine: i + 1,
				sig: strings.TrimSpace(strings.TrimSuffix(trim, "{")), exported: exp, bodyHash: hashString(trim),
			})
			continue
		}
		refs = append(refs, scanLineCalls(trim, curSym, i+1, codeSkip)...)
	}
	return syms, dedupSymRefs(refs)
}

func extractPySymbols(filePath, text string) ([]symbolRow, []symRefRow) {
	pkg := path.Base(path.Dir(filePath))
	lines := splitLines(text)
	var syms []symbolRow
	var refs []symRefRow
	curClass, curSym := "", "<module>"
	for i, l := range lines {
		if m := pyDeclRe.FindStringSubmatch(l); m != nil {
			indent, kw, name := m[1], m[2], m[3]
			kind, recv := "func", ""
			if kw == "class" {
				kind, curClass, curSym = "class", name, name
			} else if len(indent) > 0 && curClass != "" {
				kind, recv, curSym = "method", curClass, curClass+"."+name
			} else {
				curClass, curSym = "", name
			}
			syms = append(syms, symbolRow{
				name: name, recv: recv, kind: kind, pkg: pkg, startLine: i + 1, endLine: i + 1,
				sig: strings.TrimSpace(l), exported: boolInt(!strings.HasPrefix(name, "_")), bodyHash: hashString(l),
			})
			continue
		}
		refs = append(refs, scanLineCalls(strings.TrimSpace(l), curSym, i+1, codeSkip)...)
	}
	return syms, dedupSymRefs(refs)
}

func extractShSymbols(filePath, text string) ([]symbolRow, []symRefRow) {
	pkg := path.Base(path.Dir(filePath))
	lines := splitLines(text)
	var syms []symbolRow
	var refs []symRefRow
	curSym := "<script>"
	for i, l := range lines {
		trim := strings.TrimSpace(l)
		if strings.HasPrefix(trim, "#") {
			continue
		}
		if m := shDeclRe.FindStringSubmatch(l); m != nil {
			name := m[1]
			if name == "" {
				name = m[2]
			}
			curSym = name
			syms = append(syms, symbolRow{
				name: name, kind: "func", pkg: pkg, startLine: i + 1, endLine: i + 1,
				sig: name + "()", exported: 1, bodyHash: hashString(trim),
			})
			continue
		}
		refs = append(refs, scanLineCalls(trim, curSym, i+1, codeSkip)...)
	}
	return syms, dedupSymRefs(refs)
}

func scanLineCalls(line, fromSym string, lineNum int, skip map[string]bool) []symRefRow {
	var out []symRefRow
	for _, m := range callTokenRe.FindAllStringSubmatch(line, -1) {
		pkg, name := m[1], m[2]
		if skip[name] || skip[pkg] {
			continue
		}
		out = append(out, symRefRow{
			fromSym: fromSym, toPkg: pkg, toName: name, kind: "call",
			confidence: ConfidenceExtracted, weight: WeightExtracted, line: lineNum,
		})
	}
	return out
}

var (
	mdTickRe  = regexp.MustCompile("`([^`\\n]{2,120})`")
	docIdent  = regexp.MustCompile(`^(?:\(\*?([A-Za-z_][A-Za-z0-9_]*)\)\.|([A-Za-z_][A-Za-z0-9_]*)\.)?([A-Za-z_][A-Za-z0-9_]*)(?:\(.*\))?$`)
	docStop   = map[string]bool{"true": true, "false": true, "null": true, "none": true, "main": true, "bash": true, "text": true, "json": true, "yaml": true, "html": true, "todo": true, "pass": true, "fail": true}
	docExtSet = map[string]bool{}
)

func init() {
	for _, e := range refExtensions {
		docExtSet[e] = true
	}
	for _, e := range []string{"db", "txt", "log", "lock", "jsonl", "html", "css", "svg", "png"} {
		docExtSet[e] = true
	}
}

func extractMarkdownDocRefs(text string) []symRefRow {
	var out []symRefRow
	var f fence
	for i, l := range splitLines(text) {
		if f.step(l) || f.inside() || !strings.Contains(l, "`") {
			continue
		}
		for _, m := range mdTickRe.FindAllStringSubmatch(l, -1) {
			if pkg, name, ok := parseDocSymbolSpan(strings.TrimSpace(m[1])); ok {
				out = append(out, symRefRow{
					fromSym: "<doc>", toPkg: pkg, toName: name, kind: "doc_ref",
					confidence: ConfidenceInferred, weight: WeightInferred, line: i + 1,
				})
			}
		}
	}
	return dedupSymRefs(out)
}

func parseDocSymbolSpan(span string) (string, string, bool) {
	if idx := strings.LastIndexByte(span, ':'); idx > 0 && strings.Contains(span[:idx], "/") {
		span = span[idx+1:]
	}
	m := docIdent.FindStringSubmatch(span)
	if m == nil {
		return "", "", false
	}
	qual := m[1]
	if qual == "" {
		qual = m[2]
	}
	name := m[3]
	if qual != "" {
		if docExtSet[strings.ToLower(name)] {
			return "", "", false
		}
		return qual, name, true
	}
	hasCall := strings.HasSuffix(span, ")")
	if docStop[strings.ToLower(name)] || (!hasCall && !looksLikeCodeIdent(name)) {
		return "", "", false
	}
	return "", name, true
}

func looksLikeCodeIdent(s string) bool {
	if strings.Contains(s, "_") || len(s) >= 6 {
		return true
	}
	hasUpper, hasLower := false, false
	for _, r := range s {
		hasUpper = hasUpper || unicode.IsUpper(r)
		hasLower = hasLower || unicode.IsLower(r)
	}
	return hasUpper && hasLower
}
