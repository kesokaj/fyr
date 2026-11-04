package auto

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestWithRunnerKeepsVerdict pins the order withRunner writes (BUG-506): a
// runner's reason longer than the whole line is what the cut at lineLimit
// takes, and the verdict and the branch before it survive.
func TestWithRunnerKeepsVerdict(t *testing.T) {
	verdict := "FAIL (no commits); branch work/x kept"
	got := short(withRunner(verdict, "host ("+strings.Repeat("r", lineLimit)+")"))
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("precondition: the line reached the %d-rune cut:\n%s", lineLimit, got)
	}
	if want := verdict + "; runner host (r"; !strings.HasPrefix(got, want) {
		t.Fatalf("the cut line lost %q:\n%s", want, got)
	}
}

// TestRunnerOnlyThroughWithRunner pins BUG-506 at every site: a worker's
// report line names the runner only through withRunner, which puts it last.
// runnerWhy in an operand of + or an argument of a fmt call, outside
// withRunner, is a line built another way, where the runner's reason can
// come first again and the cut at lineLimit take the verdict and the branch.
func TestRunnerOnlyThroughWithRunner(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	calls := map[string]int{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil && fd.Name.Name != "withRunner" {
				bad, n := runnerWhyMisuse(fset, fd.Body)
				calls[name] += n
				for _, at := range bad {
					t.Errorf("%s: runnerWhy is formatted outside withRunner (BUG-506)", at)
				}
			}
		}
	}
	for _, name := range []string{"apply.go", "eval_gate.go", "worker.go"} {
		if calls[name] == 0 {
			t.Errorf("precondition: the scan found no withRunner(…, runnerWhy) call in %s", name)
		}
	}
}

// runnerWhyMisuse returns where body uses runnerWhy in an operand of + or an
// argument of a fmt call, and how many withRunner calls end in runnerWhy.
func runnerWhyMisuse(fset *token.FileSet, body *ast.BlockStmt) (bad []string, calls int) {
	seen := map[token.Pos]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		var operands []ast.Expr
		switch e := n.(type) {
		case *ast.BinaryExpr:
			if e.Op == token.ADD {
				operands = []ast.Expr{e.X, e.Y}
			}
		case *ast.CallExpr:
			if callsFunc(e, "withRunner") && len(e.Args) == 2 && isIdent(e.Args[1], "runnerWhy") {
				calls++
			} else if sel, ok := e.Fun.(*ast.SelectorExpr); ok && isIdent(sel.X, "fmt") {
				operands = e.Args
			}
		}
		for _, op := range operands {
			for _, pos := range runnerWhyIn(op) {
				if !seen[pos] {
					seen[pos] = true
					bad = append(bad, fset.Position(pos).String())
				}
			}
		}
		return true
	})
	return bad, calls
}

// runnerWhyIn returns where e mentions runnerWhy outside a withRunner call.
func runnerWhyIn(e ast.Expr) []token.Pos {
	var at []token.Pos
	ast.Inspect(e, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && callsFunc(c, "withRunner") {
			return false
		}
		if isIdent(n, "runnerWhy") {
			at = append(at, n.Pos())
		}
		return true
	})
	return at
}

func callsFunc(c *ast.CallExpr, name string) bool { return isIdent(c.Fun, name) }

func isIdent(n ast.Node, name string) bool {
	id, ok := n.(*ast.Ident)
	return ok && id.Name == name
}
