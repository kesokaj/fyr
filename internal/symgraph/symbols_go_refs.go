package symgraph

import (
	"go/ast"
	"go/token"
	"sort"
)

// MaxImplicitImplementers caps inferred interface implementers per interface.
const MaxImplicitImplementers = 10

func extractGoFuncRefs(fset *token.FileSet, fd *ast.FuncDecl, sy SymbolRow, imports map[string]string) []SymRefRow {
	fromSym := sy.Name
	if sy.Recv != "" {
		fromSym = sy.Recv + "." + sy.Name
	}
	refs := extractTypeExprRefs(fset, fd.Type, fromSym, imports)
	if fd.Body == nil {
		return refs
	}
	locals, stdlibVars := collectFuncScope(fd, imports)
	return append(refs, extractExprRefs(fset, fd.Body, fromSym, imports, locals, stdlibVars)...)
}

func extractTypeExprRefs(fset *token.FileSet, node ast.Node, fromSym string, imports map[string]string) []SymRefRow {
	if node == nil {
		return nil
	}
	var refs []SymRefRow
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Field:
			refs = append(refs, extractTypeExprRefs(fset, x.Type, fromSym, imports)...)
			return false
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok {
				if imp, isImp := imports[id.Name]; isImp && !isStdlibImport(imp) {
					refs = append(refs, SymRefRow{
						FromSym: fromSym, ToPkg: imp, ToName: x.Sel.Name,
						Kind: "type_ref", Confidence: ConfidenceExtracted, Weight: WeightExtracted,
						Line: fset.Position(x.Sel.Pos()).Line,
					})
				}
			}
			return false
		case *ast.Ident:
			if !isGoPredeclared(x.Name) {
				refs = append(refs, SymRefRow{
					FromSym: fromSym, ToPkg: "", ToName: x.Name,
					Kind: "type_ref", Confidence: ConfidenceExtracted, Weight: WeightExtracted,
					Line: fset.Position(x.Pos()).Line,
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
	refs       []SymRefRow
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
	c.refs = append(c.refs, SymRefRow{
		FromSym: c.fromSym, ToName: id.Name, Kind: "call",
		Confidence: ConfidenceExtracted, Weight: WeightExtracted,
		Line: c.fset.Position(id.Pos()).Line,
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
	c.refs = append(c.refs, SymRefRow{
		FromSym: c.fromSym, ToPkg: imp, ToName: x.Sel.Name, Kind: "call",
		Confidence: ConfidenceExtracted, Weight: WeightExtracted,
		Line: c.fset.Position(x.Sel.Pos()).Line,
	})
}

func extractExprRefs(fset *token.FileSet, node ast.Node, fromSym string, imports map[string]string, locals, stdlibVars map[string]bool) []SymRefRow {
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

func MatchImplicitInterfaces(syms []SymbolRow) []SymRefRow {
	ifaces, ifaceMethods, recvMethods := groupInterfaceAndRecvMethods(syms)
	var out []SymRefRow
	for key, iface := range ifaces {
		req := ifaceMethods[key]
		if len(req) == 0 {
			continue
		}
		impls := findInterfaceImplementers(iface, req, recvMethods)
		if len(impls) == 0 || len(impls) > MaxImplicitImplementers {
			continue
		}
		for _, impl := range impls {
			out = append(out, SymRefRow{
				OID: iface.OID, FromSym: iface.Name, ToPkg: impl.pkg, ToName: impl.recv,
				Kind: "implements", Confidence: ConfidenceInferred, Weight: WeightInferred, Line: iface.StartLine,
			})
			for _, m := range req {
				out = append(out, SymRefRow{
					OID: iface.OID, FromSym: iface.Name + "." + m.Name, ToPkg: impl.pkg, ToName: impl.recv + "." + m.Name,
					Kind: "iface_call", Confidence: ConfidenceInferred, Weight: WeightInferred, Line: m.StartLine,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromSym != out[j].FromSym {
			return out[i].FromSym < out[j].FromSym
		}
		return out[i].ToName < out[j].ToName
	})
	return out
}
