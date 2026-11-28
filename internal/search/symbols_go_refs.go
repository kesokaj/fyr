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
						fromSym: fromSym, toPkg: imp, toName: x.Sel.Name,
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

type exprRefCollector struct {
	fset       *token.FileSet
	fromSym    string
	imports    map[string]string
	locals     map[string]bool
	stdlibVars map[string]bool
	skipSel    map[*ast.SelectorExpr]bool
	refs       []symRefRow
}

func (c *exprRefCollector) visit(n ast.Node) bool {
	switch x := n.(type) {
	case *ast.CallExpr:
		c.visitCall(x)
	case *ast.CompositeLit:
		c.refs = append(c.refs, extractTypeExprRefs(c.fset, x.Type, c.fromSym, c.imports)...)
	case *ast.KeyValueExpr:
		c.addIdentRef(x.Value)
	case *ast.SelectorExpr:
		c.visitSelector(x)
	}
	return true
}

func (c *exprRefCollector) visitCall(x *ast.CallExpr) {
	if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
		if c.skipSel == nil {
			c.skipSel = map[*ast.SelectorExpr]bool{}
		}
		c.skipSel[sel] = true
	}
	if r, ok := callTarget(c.fset, x.Fun, c.fromSym, c.imports, c.locals, c.stdlibVars); ok {
		c.refs = append(c.refs, r)
	}
	for _, arg := range x.Args {
		c.addIdentRef(arg)
	}
}

func (c *exprRefCollector) addIdentRef(expr ast.Expr) {
	id, ok := expr.(*ast.Ident)
	if !ok || c.locals[id.Name] || isGoPredeclared(id.Name) {
		return
	}
	c.refs = append(c.refs, symRefRow{
		fromSym: c.fromSym, toName: id.Name, kind: "call",
		confidence: ConfidenceExtracted, weight: WeightExtracted,
		line: c.fset.Position(id.Pos()).Line,
	})
}

func (c *exprRefCollector) visitSelector(x *ast.SelectorExpr) {
	if c.skipSel[x] {
		return
	}
	id, ok := x.X.(*ast.Ident)
	if !ok {
		return
	}
	imp, isImp := c.imports[id.Name]
	if !isImp || isStdlibImport(imp) {
		return
	}
	c.refs = append(c.refs, symRefRow{
		fromSym: c.fromSym, toPkg: imp, toName: x.Sel.Name, kind: "call",
		confidence: ConfidenceExtracted, weight: WeightExtracted,
		line: c.fset.Position(x.Sel.Pos()).Line,
	})
}

func extractExprRefs(fset *token.FileSet, node ast.Node, fromSym string, imports map[string]string, locals, stdlibVars map[string]bool) []symRefRow {
	if node == nil {
		return nil
	}
	c := exprRefCollector{
		fset:       fset,
		fromSym:    fromSym,
		imports:    imports,
		locals:     locals,
		stdlibVars: stdlibVars,
	}
	ast.Inspect(node, c.visit)
	return c.refs
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
