package search

import (
	"go/ast"
	"go/token"
	"sort"
)

const maxImplicitImplementers = 10

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
	var skipSel map[*ast.SelectorExpr]bool
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				if skipSel == nil {
					skipSel = map[*ast.SelectorExpr]bool{}
				}
				skipSel[sel] = true
			}
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
			if skipSel[x] {
				return true
			}
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
