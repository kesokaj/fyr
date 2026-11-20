package search

import (
	"go/ast"
	"go/token"
)

func collectFuncScope(fd *ast.FuncDecl, imports map[string]string) (map[string]bool, map[string]bool) {
	locals := map[string]bool{}
	stdlibVars := map[string]bool{}
	addFields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			isStd := isStdlibTypeExpr(f.Type, imports)
			for _, n := range f.Names {
				locals[n.Name] = true
				if isStd {
					stdlibVars[n.Name] = true
				}
			}
		}
	}
	addFields(fd.Recv)
	if fd.Type != nil {
		addFields(fd.Type.Params)
		addFields(fd.Type.Results)
	}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		collectStmtLocals(n, imports, locals, stdlibVars)
		return true
	})
	return locals, stdlibVars
}

func collectStmtLocals(n ast.Node, imports map[string]string, locals, stdlibVars map[string]bool) {
	switch st := n.(type) {
	case *ast.AssignStmt:
		if st.Tok == token.DEFINE {
			stdRHS := len(st.Rhs) > 0 && isStdlibExpr(st.Rhs[0], imports)
			for _, lhs := range st.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					locals[id.Name] = true
					if stdRHS {
						stdlibVars[id.Name] = true
					}
				}
			}
		}
	case *ast.RangeStmt:
		if st.Tok == token.DEFINE {
			for _, e := range []ast.Expr{st.Key, st.Value} {
				if id, ok := e.(*ast.Ident); ok {
					locals[id.Name] = true
				}
			}
		}
	case *ast.ValueSpec:
		isStd := isStdlibTypeExpr(st.Type, imports)
		for _, id := range st.Names {
			locals[id.Name] = true
			if isStd {
				stdlibVars[id.Name] = true
			}
		}
	}
}

func isStdlibExpr(expr ast.Expr, imports map[string]string) bool {
	if isSQLChain(expr) {
		return true
	}
	if root := rootIdent(expr); root != "" {
		if imp, ok := imports[root]; ok && isStdlibImport(imp) {
			return true
		}
	}
	return false
}

func isStdlibTypeExpr(expr ast.Expr, imports map[string]string) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok {
			return isStdlibImport(imports[id.Name]) && imports[id.Name] != ""
		}
	}
	return false
}

func callTarget(fset *token.FileSet, fun ast.Expr, fromSym string, imports map[string]string, locals, stdlibVars map[string]bool) (symRefRow, bool) {
	switch idx := fun.(type) {
	case *ast.IndexExpr:
		fun = idx.X
	case *ast.IndexListExpr:
		fun = idx.X
	}
	switch fn := fun.(type) {
	case *ast.Ident:
		if isGoPredeclared(fn.Name) || locals[fn.Name] {
			return symRefRow{}, false
		}
		return symRefRow{fromSym: fromSym, toName: fn.Name, kind: "call", confidence: ConfidenceExtracted, weight: WeightExtracted, line: fset.Position(fn.Pos()).Line}, true
	case *ast.SelectorExpr:
		return selectorCallTarget(fset, fn, fromSym, imports, stdlibVars)
	}
	return symRefRow{}, false
}

func selectorCallTarget(fset *token.FileSet, sel *ast.SelectorExpr, fromSym string, imports map[string]string, stdlibVars map[string]bool) (symRefRow, bool) {
	if root := rootIdent(sel.X); root != "" {
		if imp, isImp := imports[root]; isImp {
			if isStdlibImport(imp) {
				return symRefRow{}, false
			}
			if _, direct := sel.X.(*ast.Ident); direct {
				return symRefRow{fromSym: fromSym, toPkg: imp, toName: sel.Sel.Name, kind: "call", confidence: ConfidenceExtracted, weight: WeightExtracted, line: fset.Position(sel.Sel.Pos()).Line}, true
			}
		}
		if stdlibVars[root] {
			return symRefRow{}, false
		}
	}
	if isStdlibOnlyMethod(sel.Sel.Name) || isSQLChain(sel.X) {
		return symRefRow{}, false
	}
	return symRefRow{fromSym: fromSym, toName: sel.Sel.Name, kind: "call", confidence: ConfidenceExtracted, weight: WeightExtracted, line: fset.Position(sel.Sel.Pos()).Line}, true
}

func rootIdent(expr ast.Expr) string {
	for expr != nil {
		switch x := expr.(type) {
		case *ast.Ident:
			return x.Name
		case *ast.SelectorExpr:
			expr = x.X
		case *ast.CallExpr:
			expr = x.Fun
		case *ast.StarExpr:
			expr = x.X
		case *ast.ParenExpr:
			expr = x.X
		default:
			return ""
		}
	}
	return ""
}
