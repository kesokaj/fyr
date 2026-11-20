package search

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strings"
)

const (
	ConfidenceExtracted = "EXTRACTED"
	ConfidenceInferred  = "INFERRED"
	WeightExtracted     = 1.0
	WeightInferred      = 0.7
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
	return extractGoSymbolsCore(filePath, text, true)
}

func extractGoSymbolsCore(filePath, text string, includeImplicit bool) ([]symbolRow, []symRefRow) {
	if isExcludedASTPath(filePath) || isGeneratedGoText(text) {
		return nil, nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, text, parser.SkipObjectResolution)
	pkgName := canonicalPkgDir(filePath)
	var syms []symbolRow
	var refs []symRefRow
	if f != nil {
		imports := parseGoImports(f)
		refs = append(refs, extractGoImportRefs(fset, f)...)
		walkSyms, walkRefs := walkGoFile(fset, f, text, pkgName, imports)
		syms = append(syms, walkSyms...)
		refs = append(refs, walkRefs...)
	}
	if err != nil {
		fbSyms, fbRefs := parseGoFallback(filePath, text, pkgName)
		syms = mergeSymbols(syms, fbSyms)
		refs = append(refs, fbRefs...)
	}
	if includeImplicit {
		refs = append(refs, matchImplicitInterfaces(syms)...)
	}
	return syms, dedupSymRefs(refs)
}

func extractGoImportRefs(fset *token.FileSet, f *ast.File) []symRefRow {
	var out []symRefRow
	for _, imp := range f.Imports {
		if imp.Path == nil {
			continue
		}
		p := strings.Trim(imp.Path.Value, `"`)
		if p == "" || isStdlibImport(p) {
			continue
		}
		out = append(out, symRefRow{
			fromSym: "<import>", toPkg: p, toName: "<import>",
			kind: "import", confidence: ConfidenceExtracted, weight: WeightExtracted,
			line: fset.Position(imp.Pos()).Line,
		})
	}
	return out
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
