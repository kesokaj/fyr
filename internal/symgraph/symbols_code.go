package symgraph

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/token"
	"path"
	"regexp"
	"strings"
)

func ExtractSymbolsAndRefs(filePath, text string) ([]SymbolRow, []SymRefRow) {
	return extractSymbolsAndRefsCore(filePath, text, true)
}

func ExtractSymbolsAndRefsForIndex(filePath, text string) ([]SymbolRow, []SymRefRow) {
	return extractSymbolsAndRefsCore(filePath, text, false)
}

func extractSymbolsAndRefsCore(filePath, text string, includeImplicitGo bool) ([]SymbolRow, []SymRefRow) {
	if IsExcludedASTPath(filePath) {
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
		return ExtractMarkdownSymbols(filePath, text)
	}
	return nil, nil
}

type recvKey struct{ pkg, recv string }

func groupInterfaceAndRecvMethods(syms []SymbolRow) (map[recvKey]SymbolRow, map[recvKey][]SymbolRow, map[recvKey]map[string]bool) {
	ifaces := map[recvKey]SymbolRow{}
	ifaceMethods := map[recvKey][]SymbolRow{}
	recvMethods := map[recvKey]map[string]bool{}
	for _, sy := range syms {
		switch sy.Kind {
		case "interface":
			ifaces[recvKey{sy.Pkg, sy.Name}] = sy
		case "interface_method":
			k := recvKey{sy.Pkg, sy.Recv}
			ifaceMethods[k] = append(ifaceMethods[k], sy)
		case "method":
			if sy.Recv != "" {
				k := recvKey{sy.Pkg, sy.Recv}
				if recvMethods[k] == nil {
					recvMethods[k] = map[string]bool{}
				}
				recvMethods[k][sy.Name] = true
			}
		}
	}
	return ifaces, ifaceMethods, recvMethods
}

func findInterfaceImplementers(iface SymbolRow, req []SymbolRow, recvMethods map[recvKey]map[string]bool) []recvKey {
	var out []recvKey
	for rk, mset := range recvMethods {
		if rk.recv == iface.Name {
			continue
		}
		ok := true
		for _, m := range req {
			if !mset[m.Name] {
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
	callIdentRe  = regexp.MustCompile(`(?:([A-Za-z_][A-Za-z0-9_]*)\.)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
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

func parseGoFallbackFuncLine(trim, pkgName string, lineNum int) (SymbolRow, []SymRefRow, string, bool) {
	m := goFuncLineRe.FindStringSubmatch(trim)
	if m == nil {
		return SymbolRow{}, nil, "", false
	}
	recv, name := m[1], m[2]
	kind, curSym := "func", name
	if recv != "" {
		kind, curSym = "method", recv+"."+name
	}
	sy := SymbolRow{
		Name: name, Recv: recv, Kind: kind, Pkg: pkgName,
		StartLine: lineNum, EndLine: lineNum, Sig: strings.TrimSuffix(trim, "{"),
		Exported: boolToInt(ast.IsExported(name)), BodyHash: HashString(trim),
	}
	var refs []SymRefRow
	if _, body, ok := strings.Cut(trim, "{"); ok && body != "" {
		refs = scanLineCalls(body, curSym, lineNum, goPredeclared)
	}
	return sy, refs, curSym, true
}

func parseGoFallbackTypeLine(trim, pkgName string, lineNum int) (SymbolRow, string, bool) {
	m := goTypeLineRe.FindStringSubmatch(trim)
	if m == nil {
		return SymbolRow{}, "", false
	}
	name, kind := m[1], m[2]
	if kind != "struct" && kind != "interface" {
		kind = "type"
	}
	sy := SymbolRow{
		Name: name, Kind: kind, Pkg: pkgName, StartLine: lineNum, EndLine: lineNum,
		Sig: "type " + name + " " + kind, Exported: boolToInt(ast.IsExported(name)), BodyHash: HashString(trim),
	}
	return sy, name, true
}

func parseGoFallback(text, pkgName string) ([]SymbolRow, []SymRefRow) {
	var syms []SymbolRow
	var refs []SymRefRow
	curSym := ""
	for i, l := range SplitLines(text) {
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

func extractPySymbols(filePath, text string) ([]SymbolRow, []SymRefRow) {
	if IsExcludedASTPath(filePath) {
		return nil, nil
	}
	pkg := CanonicalPkgDir(filePath)
	lines := SplitLines(text)
	var syms []SymbolRow
	var refs []SymRefRow
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
			syms = append(syms, SymbolRow{
				Name: name, Recv: recv, Kind: kind, Pkg: pkg, StartLine: i + 1, EndLine: i + 1,
				Sig: strings.TrimSpace(l), Exported: boolToInt(!strings.HasPrefix(name, "_")), BodyHash: HashString(l),
			})
			continue
		}
		refs = append(refs, scanLineCalls(strings.TrimSpace(l), curSym, i+1, codeSkip)...)
	}
	return syms, DedupSymRefs(refs)
}

func extractShSymbols(filePath, text string) ([]SymbolRow, []SymRefRow) {
	if IsExcludedASTPath(filePath) {
		return nil, nil
	}
	pkg := CanonicalPkgDir(filePath)
	lines := SplitLines(text)
	var syms []SymbolRow
	var refs []SymRefRow
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
			syms = append(syms, SymbolRow{
				Name: name, Kind: "func", Pkg: pkg, StartLine: i + 1, EndLine: i + 1,
				Sig: name + "()", Exported: 1, BodyHash: HashString(trim),
			})
			continue
		}
		refs = append(refs, scanLineCalls(trim, curSym, i+1, codeSkip)...)
	}
	return syms, DedupSymRefs(refs)
}

func scanLineCalls(line, fromSym string, lineNum int, skip map[string]bool) []SymRefRow {
	var out []SymRefRow
	for _, m := range callIdentRe.FindAllStringSubmatch(line, -1) {
		pkg, name := m[1], m[2]
		if skip[name] || skip[pkg] {
			continue
		}
		out = append(out, SymRefRow{
			FromSym: fromSym, ToPkg: pkg, ToName: name, Kind: "call",
			Confidence: ConfidenceExtracted, Weight: WeightExtracted, Line: lineNum,
		})
	}
	return out
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// DedupSymRefs removes duplicate symbol reference rows while preserving order.
func DedupSymRefs(refs []SymRefRow) []SymRefRow {
	type refDedup struct {
		from, pkg, name, kind string
		line                  int
	}
	seen := map[refDedup]bool{}
	var out []SymRefRow
	for _, r := range refs {
		if r.ToName == "" {
			continue
		}
		k := refDedup{r.FromSym, r.ToPkg, r.ToName, r.Kind, r.Line}
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	return out
}

// MergeSymbols appends symbols from extra not already present in base.
func MergeSymbols(base, extra []SymbolRow) []SymbolRow {
	seen := map[string]bool{}
	for _, s := range base {
		seen[s.Recv+"."+s.Name] = true
	}
	for _, s := range extra {
		if !seen[s.Recv+"."+s.Name] {
			seen[s.Recv+"."+s.Name] = true
			base = append(base, s)
		}
	}
	return base
}

// SpecLineSpan returns the 1-based start and end lines for a Go declaration spec.
func SpecLineSpan(fset *token.FileSet, gd *ast.GenDecl, sp ast.Spec) (int, int) {
	if len(gd.Specs) == 1 {
		return fset.Position(gd.Pos()).Line, fset.Position(gd.End()).Line
	}
	return fset.Position(sp.Pos()).Line, fset.Position(sp.End()).Line
}

// SliceNodeText slices the exact source substring covered by [start, end).
func SliceNodeText(fset *token.FileSet, start, end token.Pos, text string) string {
	s, e := fset.Position(start).Offset, fset.Position(end).Offset
	if s < 0 || e < s || e > len(text) {
		return ""
	}
	return text[s:e]
}

// HashNodeText returns the 16-hex SHA-256 prefix of the AST node's source text.
func HashNodeText(fset *token.FileSet, start, end token.Pos, text string) string {
	return HashString(SliceNodeText(fset, start, end, text))
}

// HashString returns the 16-hex SHA-256 prefix of s.
func HashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
