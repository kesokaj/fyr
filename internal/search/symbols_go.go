package search

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"
)

const (
	maxImplicitImplementers = 10
	ConfidenceExtracted     = "EXTRACTED"
	ConfidenceInferred      = "INFERRED"
	WeightExtracted         = 1.0
	WeightInferred          = 0.7
)

type symbolRow struct {
	oid, name, recv, kind, pkg, sig, bodyHash string
	startLine, endLine, exported              int
}

type symRefRow struct {
	oid, fromSym, toPkg, toName, kind, confidence string
	weight                                        float64
	line                                          int
}

func extractGoSymbols(filePath, text string) ([]symbolRow, []symRefRow) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, text, parser.SkipObjectResolution)
	pkgName := parseGoPackage(filePath, text)
	var syms []symbolRow
	var refs []symRefRow
	if f != nil {
		if f.Name != nil && f.Name.Name != "" {
			pkgName = f.Name.Name
		}
		imports := parseGoImports(f)
		syms, refs = walkGoFile(fset, f, text, pkgName, imports)
	}
	if err != nil {
		fbSyms, fbRefs := parseGoFallback(filePath, text, pkgName)
		syms = mergeSymbols(syms, fbSyms)
		refs = append(refs, fbRefs...)
	}
	refs = append(refs, matchImplicitInterfaces(syms)...)
	return syms, dedupSymRefs(refs)
}

func parseGoImports(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range f.Imports {
		if imp.Path == nil {
			continue
		}
		p := strings.Trim(imp.Path.Value, `"`)
		local := path.Base(p)
		if imp.Name != nil && imp.Name.Name != "_" && imp.Name.Name != "." {
			local = imp.Name.Name
		}
		out[local] = p
	}
	return out
}

func isStdlibImport(impPath string) bool {
	first, _, _ := strings.Cut(impPath, "/")
	return !strings.Contains(first, ".")
}

func walkGoFile(fset *token.FileSet, f *ast.File, text, pkgName string, imports map[string]string) ([]symbolRow, []symRefRow) {
	var syms []symbolRow
	var refs []symRefRow
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if sy, ok := extractGoFuncSym(fset, d, text, pkgName); ok {
				syms = append(syms, sy)
				refs = append(refs, extractGoFuncRefs(fset, d, sy, imports)...)
			}
		case *ast.GenDecl:
			gs, gr := extractGoGenDecl(fset, d, text, pkgName, imports)
			syms = append(syms, gs...)
			refs = append(refs, gr...)
		}
	}
	return syms, refs
}

func extractGoFuncSym(fset *token.FileSet, fd *ast.FuncDecl, text, pkgName string) (symbolRow, bool) {
	if fd.Name == nil {
		return symbolRow{}, false
	}
	recv := goRecvTypeName(fd)
	kind := "func"
	if recv != "" {
		kind = "method"
	}
	start := fset.Position(fd.Pos()).Line
	end := max(start, fset.Position(fd.End()).Line)
	return symbolRow{
		name: fd.Name.Name, recv: recv, kind: kind, pkg: pkgName,
		startLine: start, endLine: end, sig: goFuncSig(fset, fd, text),
		exported: boolInt(ast.IsExported(fd.Name.Name)), bodyHash: hashNodeText(fset, fd.Pos(), fd.End(), text),
	}, true
}

func goRecvTypeName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	expr := fd.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch x := expr.(type) {
	case *ast.IndexExpr:
		expr = x.X
	case *ast.IndexListExpr:
		expr = x.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func goFuncSig(fset *token.FileSet, fd *ast.FuncDecl, text string) string {
	endPos := fd.End()
	if fd.Body != nil {
		endPos = fd.Body.Pos()
	}
	return strings.TrimSpace(strings.Join(strings.Fields(sliceNodeText(fset, fd.Pos(), endPos, text)), " "))
}

func extractGoGenDecl(fset *token.FileSet, gd *ast.GenDecl, text, pkgName string, imports map[string]string) ([]symbolRow, []symRefRow) {
	var syms []symbolRow
	var refs []symRefRow
	for _, spec := range gd.Specs {
		switch sp := spec.(type) {
		case *ast.TypeSpec:
			ts, tr := extractGoTypeSpec(fset, gd, sp, text, pkgName, imports)
			syms, refs = append(syms, ts...), append(refs, tr...)
		case *ast.ValueSpec:
			vs, vr := extractGoValueSpec(fset, gd, sp, text, pkgName, imports)
			syms, refs = append(syms, vs...), append(refs, vr...)
		}
	}
	return syms, refs
}

func extractGoTypeSpec(fset *token.FileSet, gd *ast.GenDecl, ts *ast.TypeSpec, text, pkgName string, imports map[string]string) ([]symbolRow, []symRefRow) {
	if ts.Name == nil {
		return nil, nil
	}
	kind := "type"
	switch ts.Type.(type) {
	case *ast.StructType:
		kind = "struct"
	case *ast.InterfaceType:
		kind = "interface"
	}
	start, end := specLineSpan(fset, gd, ts)
	name := ts.Name.Name
	syms := []symbolRow{{
		name: name, kind: kind, pkg: pkgName, startLine: start, endLine: end,
		sig: "type " + name + " " + kind, exported: boolInt(ast.IsExported(name)),
		bodyHash: hashNodeText(fset, ts.Pos(), ts.End(), text),
	}}
	if iface, ok := ts.Type.(*ast.InterfaceType); ok {
		syms = append(syms, extractIfaceMethods(fset, name, pkgName, iface, text)...)
	}
	return syms, extractTypeExprRefs(fset, ts.Type, name, imports)
}

func extractIfaceMethods(fset *token.FileSet, ifaceName, pkgName string, iface *ast.InterfaceType, text string) []symbolRow {
	if iface.Methods == nil {
		return nil
	}
	var out []symbolRow
	for _, field := range iface.Methods.List {
		if _, ok := field.Type.(*ast.FuncType); !ok || len(field.Names) == 0 {
			continue
		}
		start := fset.Position(field.Pos()).Line
		end := fset.Position(field.End()).Line
		sig := strings.TrimSpace(strings.Join(strings.Fields(sliceNodeText(fset, field.Pos(), field.End(), text)), " "))
		hash := hashNodeText(fset, field.Pos(), field.End(), text)
		for _, id := range field.Names {
			out = append(out, symbolRow{
				name: id.Name, recv: ifaceName, kind: "interface_method", pkg: pkgName,
				startLine: start, endLine: end, sig: sig,
				exported: boolInt(ast.IsExported(id.Name)), bodyHash: hash,
			})
		}
	}
	return out
}

func extractGoValueSpec(fset *token.FileSet, gd *ast.GenDecl, vs *ast.ValueSpec, text, pkgName string, imports map[string]string) ([]symbolRow, []symRefRow) {
	valKind := "var"
	if gd.Tok == token.CONST {
		valKind = "const"
	}
	start, end := specLineSpan(fset, gd, vs)
	hash := hashNodeText(fset, vs.Pos(), vs.End(), text)
	var syms []symbolRow
	var fromSym string
	for _, id := range vs.Names {
		if id.Name == "_" || id.Name == "" {
			continue
		}
		if fromSym == "" {
			fromSym = id.Name
		}
		syms = append(syms, symbolRow{
			name: id.Name, recv: "", kind: valKind, pkg: pkgName,
			startLine: start, endLine: end, sig: valKind + " " + id.Name,
			exported: boolInt(ast.IsExported(id.Name)), bodyHash: hash,
		})
	}
	if fromSym == "" {
		return syms, nil
	}
	refs := extractTypeExprRefs(fset, vs.Type, fromSym, imports)
	for _, val := range vs.Values {
		refs = append(refs, extractExprRefs(fset, val, fromSym, imports, nil, nil)...)
	}
	return syms, refs
}

func extractGoFuncRefs(fset *token.FileSet, fd *ast.FuncDecl, sy symbolRow, imports map[string]string) []symRefRow {
	fromSym := sy.name
	if sy.recv != "" {
		fromSym = sy.recv + "." + sy.name
	}
	refs := extractTypeExprRefs(fset, fd.Type, fromSym, imports)
	if fd.Body == nil {
		return refs
	}
	locals, stdlibVars := collectFuncScope(fd, imports)
	return append(refs, extractExprRefs(fset, fd.Body, fromSym, imports, locals, stdlibVars)...)
}

func extractTypeExprRefs(fset *token.FileSet, node ast.Node, fromSym string, imports map[string]string) []symRefRow {
	if node == nil {
		return nil
	}
	var refs []symRefRow
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Field:
			refs = append(refs, extractTypeExprRefs(fset, x.Type, fromSym, imports)...)
			return false
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok {
				if imp, isImp := imports[id.Name]; isImp && !isStdlibImport(imp) {
					refs = append(refs, symRefRow{
						fromSym: fromSym, toPkg: id.Name, toName: x.Sel.Name,
						kind: "type_ref", confidence: ConfidenceExtracted, weight: WeightExtracted,
						line: fset.Position(x.Sel.Pos()).Line,
					})
				}
			}
			return false
		case *ast.Ident:
			if !isGoPredeclared(x.Name) {
				refs = append(refs, symRefRow{
					fromSym: fromSym, toPkg: "", toName: x.Name,
					kind: "type_ref", confidence: ConfidenceExtracted, weight: WeightExtracted,
					line: fset.Position(x.Pos()).Line,
				})
			}
		}
		return true
	})
	return refs
}

func extractExprRefs(fset *token.FileSet, node ast.Node, fromSym string, imports map[string]string, locals, stdlibVars map[string]bool) []symRefRow {
	if node == nil {
		return nil
	}
	var refs []symRefRow
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if r, ok := callTarget(fset, x.Fun, fromSym, imports, locals, stdlibVars); ok {
				refs = append(refs, r)
			}
			for _, arg := range x.Args {
				if id, ok := arg.(*ast.Ident); ok && !locals[id.Name] && !isGoPredeclared(id.Name) {
					refs = append(refs, symRefRow{fromSym: fromSym, toName: id.Name, kind: "call", confidence: ConfidenceExtracted, weight: WeightExtracted, line: fset.Position(id.Pos()).Line})
				}
			}
		case *ast.CompositeLit:
			refs = append(refs, extractTypeExprRefs(fset, x.Type, fromSym, imports)...)
		case *ast.KeyValueExpr:
			if id, ok := x.Value.(*ast.Ident); ok && !locals[id.Name] && !isGoPredeclared(id.Name) {
				refs = append(refs, symRefRow{fromSym: fromSym, toName: id.Name, kind: "call", confidence: ConfidenceExtracted, weight: WeightExtracted, line: fset.Position(id.Pos()).Line})
			}
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok {
				if imp, isImp := imports[id.Name]; isImp && !isStdlibImport(imp) {
					refs = append(refs, symRefRow{fromSym: fromSym, toPkg: id.Name, toName: x.Sel.Name, kind: "call", confidence: ConfidenceExtracted, weight: WeightExtracted, line: fset.Position(x.Sel.Pos()).Line})
				}
			}
		}
		return true
	})
	return refs
}

func matchImplicitInterfaces(syms []symbolRow) []symRefRow {
	ifaces, ifaceMethods, recvMethods := groupInterfaceAndRecvMethods(syms)
	var out []symRefRow
	for key, iface := range ifaces {
		req := ifaceMethods[key]
		if len(req) == 0 {
			continue
		}
		impls := findInterfaceImplementers(iface, req, recvMethods)
		if len(impls) == 0 || len(impls) > maxImplicitImplementers {
			continue
		}
		for _, impl := range impls {
			out = append(out, symRefRow{
				oid: iface.oid, fromSym: iface.name, toPkg: impl.pkg, toName: impl.recv,
				kind: "implements", confidence: ConfidenceInferred, weight: WeightInferred, line: iface.startLine,
			})
			for _, m := range req {
				out = append(out, symRefRow{
					oid: iface.oid, fromSym: iface.name + "." + m.name, toPkg: impl.pkg, toName: impl.recv + "." + m.name,
					kind: "iface_call", confidence: ConfidenceInferred, weight: WeightInferred, line: m.startLine,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].fromSym != out[j].fromSym {
			return out[i].fromSym < out[j].fromSym
		}
		return out[i].toName < out[j].toName
	})
	return out
}
