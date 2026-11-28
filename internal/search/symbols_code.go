package search

import (
	"go/ast"
	"path"
	"regexp"
	"strings"
)

func extractSymbolsAndRefs(filePath, text string) ([]symbolRow, []symRefRow) {
	return extractSymbolsAndRefsCore(filePath, text, true)
}

func extractSymbolsAndRefsForIndex(filePath, text string) ([]symbolRow, []symRefRow) {
	return extractSymbolsAndRefsCore(filePath, text, false)
}

func extractSymbolsAndRefsCore(filePath, text string, includeImplicitGo bool) ([]symbolRow, []symRefRow) {
	if isExcludedASTPath(filePath) {
		return nil, nil
	}
	switch strings.ToLower(path.Ext(filePath)) {
	case ".go":
		return extractGoSymbolsCore(filePath, text, includeImplicitGo)
	case ".ts", ".tsx", ".js", ".jsx", ".mjs":
		return extractTSSymbols(filePath, text)
	case ".py":
		return extractPySymbols(filePath, text)
	case ".sh":
		return extractShSymbols(filePath, text)
	case ".md":
		return extractMarkdownSymbols(filePath, text)
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

func searchWordSet(words string) map[string]bool {
	fields := strings.Fields(words)
	out := make(map[string]bool, len(fields))
	for _, w := range fields {
		out[w] = true
	}
	return out
}

var goPredeclared = map[string]bool{
	"bool": true, "byte": true, "comparable": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true, "int": true, "int8": true,
	"int16": true, "int32": true, "int64": true, "rune": true, "string": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "any": true, "true": true, "false": true, "iota": true, "nil": true,
	"append": true, "cap": true, "clear": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true, "make": true,
	"max": true, "min": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true, "_": true,
}

var stdlibOnlyMethods = map[string]bool{
	"Scan": true, "QueryRow": true, "QueryRowContext": true, "Exec": true, "ExecContext": true,
	"Prepare": true, "Next": true, "Err": true, "CombinedOutput": true, "Output": true,
	"WriteString": true, "WriteByte": true, "ReadByte": true, "FindString": true,
	"FindStringSubmatch": true, "FindAllString": true, "FindAllStringSubmatch": true,
	"MatchString": true, "ReplaceAllString": true, "SubexpNames": true, "Truncate": true,
	"Unix": true, "UnixNano": true, "UnixMilli": true, "Milliseconds": true,
	"Seconds": true, "Minutes": true, "Hours": true, "AfterFunc": true, "Chdir": true,
	"SetMaxOpenConns": true, "Lstat": true, "Chmod": true, "Chtimes": true, "MkdirAll": true,
	"ReadFile": true, "WriteFile": true, "OpenFile": true, "ReadDir": true, "EvalSymlinks": true,
	"IsRegular": true, "Perm": true, "Fatal": true, "Fatalf": true, "Errorf": true,
	"Helper": true, "TempDir": true, "Cleanup": true, "Skip": true,
}

func isGoPredeclared(name string) bool    { return goPredeclared[name] }
func isStdlibOnlyMethod(name string) bool { return stdlibOnlyMethods[name] }

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
	goFuncLineRe = regexp.MustCompile(`^func\s+(?:\(\s*[A-Za-z0-9_]*\s*\*?([A-Za-z_][A-Za-z0-9_]*)(?:\[[^\]]*\])?\s*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*(?:\[[^\]]*\])?\(`)
	goTypeLineRe = regexp.MustCompile(`^type\s+([A-Za-z_][A-Za-z0-9_]*)\s+(struct|interface|[A-Za-z0-9_.*\[\]]+)`)
	callTokenRe  = regexp.MustCompile(`(?:([A-Za-z_][A-Za-z0-9_]*)\.)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	tsDeclRe     = regexp.MustCompile(`^\s*(export\s+)?(?:default\s+)?(?:async\s+)?(function|class|interface|type|const)\s+([A-Za-z_$][A-Za-z0-9_$]*)`)
	pyDeclRe     = regexp.MustCompile(`^(\s*)(?:async\s+)?(def|class)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	shDeclRe     = regexp.MustCompile(`^\s*(?:function\s+([A-Za-z_][A-Za-z0-9_-]*)\s*(?:\(\s*\))?\s*\{?|([A-Za-z_][A-Za-z0-9_-]*)\s*\(\s*\)\s*\{)`)
	codeSkip     = map[string]bool{
		"if": true, "for": true, "while": true, "switch": true, "catch": true,
		"function": true, "return": true, "typeof": true, "instanceof": true, "new": true,
		"import": true, "export": true, "super": true, "console": true, "Math": true,
		"JSON": true, "Object": true, "Array": true, "Promise": true, "String": true,
		"Number": true, "Boolean": true, "Error": true, "Map": true, "Set": true,
		"Date": true, "RegExp": true, "parseInt": true, "parseFloat": true, "print": true,
		"len": true, "range": true, "str": true, "int": true, "float": true, "bool": true,
		"list": true, "dict": true, "set": true, "tuple": true, "isinstance": true,
		"getattr": true, "setattr": true,
	}
)

func parseGoFallbackFuncLine(trim, pkgName string, lineNum int) (symbolRow, []symRefRow, string, bool) {
	m := goFuncLineRe.FindStringSubmatch(trim)
	if m == nil {
		return symbolRow{}, nil, "", false
	}
	recv, name := m[1], m[2]
	kind, curSym := "func", name
	if recv != "" {
		kind, curSym = "method", recv+"."+name
	}
	sy := symbolRow{
		name: name, recv: recv, kind: kind, pkg: pkgName,
		startLine: lineNum, endLine: lineNum, sig: strings.TrimSuffix(trim, "{"),
		exported: boolInt(ast.IsExported(name)), bodyHash: hashString(trim),
	}
	var refs []symRefRow
	if _, body, ok := strings.Cut(trim, "{"); ok && body != "" {
		refs = scanLineCalls(body, curSym, lineNum, goPredeclared)
	}
	return sy, refs, curSym, true
}

func parseGoFallbackTypeLine(trim, pkgName string, lineNum int) (symbolRow, string, bool) {
	m := goTypeLineRe.FindStringSubmatch(trim)
	if m == nil {
		return symbolRow{}, "", false
	}
	name, kind := m[1], m[2]
	if kind != "struct" && kind != "interface" {
		kind = "type"
	}
	sy := symbolRow{
		name: name, kind: kind, pkg: pkgName, startLine: lineNum, endLine: lineNum,
		sig: "type " + name + " " + kind, exported: boolInt(ast.IsExported(name)), bodyHash: hashString(trim),
	}
	return sy, name, true
}

func parseGoFallback(text, pkgName string) ([]symbolRow, []symRefRow) {
	var syms []symbolRow
	var refs []symRefRow
	curSym := ""
	for i, l := range splitLines(text) {
		trim := strings.TrimSpace(l)
		if sy, lineRefs, sym, ok := parseGoFallbackFuncLine(trim, pkgName, i+1); ok {
			curSym = sym
			syms = append(syms, sy)
			refs = append(refs, lineRefs...)
			continue
		}
		if sy, sym, ok := parseGoFallbackTypeLine(trim, pkgName, i+1); ok {
			curSym = sym
			syms = append(syms, sy)
			continue
		}
		if curSym != "" {
			refs = append(refs, scanLineCalls(trim, curSym, i+1, goPredeclared)...)
		}
	}
	return syms, refs
}

func extractPySymbols(filePath, text string) ([]symbolRow, []symRefRow) {
	if isExcludedASTPath(filePath) {
		return nil, nil
	}
	pkg := canonicalPkgDir(filePath)
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
	if isExcludedASTPath(filePath) {
		return nil, nil
	}
	pkg := canonicalPkgDir(filePath)
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
