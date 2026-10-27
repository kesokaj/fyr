package fyr

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"testing"
)

// funcLimit is rules/code-modularity.md item 1's ceiling.
const funcLimit = 50

// longFuncs is the allowlist: key "path:Name" or "path:Recv.Name" (path from
// the module root, slash-separated, Recv without '*'), value the function's
// length when it was listed. Each phase deletes its rows; empty at Done.
var longFuncs = map[string]int{
	"internal/check/check.go:checker.docsFiles": 52,
	"internal/check/docs.go:verbFindings":       68,
	"internal/check/scan.go:scan":               57,
}

// TestFunctionLength is the regrowth guard of PLAN-2026-10-27-funclen: no
// non-test function in a tracked .go file may pass funcLimit unless
// longFuncs lists it, a listed one may not grow, and a row whose function is
// gone or back under the limit is stale and fails, so the list only empties.
// Untracked files are not read; the plan's lister covers them (D16).
func TestFunctionLength(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := os.Stat(".git"); err != nil {
		t.Skip("not a git checkout: an unpacked copy has nothing to compare against")
	}
	files := guardedGoFiles(t)
	if len(files) == 0 {
		t.Fatal("git tracks no non-test .go files; the test ran in the wrong directory")
	}
	long := map[string]int{}
	for _, f := range files {
		for k, n := range funcLengths(t, f) {
			if n > funcLimit {
				long[k] = n
			}
		}
	}
	for _, msg := range funcLengthFindings(long, longFuncs) {
		t.Error(msg)
	}
}

// guardedGoFiles lists the tracked non-test .go files the guard reads: no
// generated file, nothing under skills/, and no tracked file deleted on disk.
func guardedGoFiles(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("git", "ls-files", "-z", "--", "*.go").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	files, err := filterGoFiles(strings.Split(string(out), "\x00"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// filterGoFiles keeps the paths the guard reads. Only a file gone from disk
// is skipped; any other stat error is returned, so an unreadable directory
// cannot hide its functions from the guard.
func filterGoFiles(paths []string) ([]string, error) {
	var files []string
	for _, p := range paths {
		base := path.Base(p)
		if p == "" || strings.HasSuffix(p, "_test.go") || strings.HasPrefix(p, "skills/") ||
			strings.Contains(base, ".pb.") || strings.Contains(base, "_gen.") {
			continue
		}
		if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("%s: %v", p, err)
		}
		files = append(files, p)
	}
	return files, nil
}

// funcLengths parses one file and returns each function's length in physical
// lines, from the func keyword through the closing brace, keyed as longFuncs
// is; a name repeated in the file (only init can be) takes #2, #3 in order.
func funcLengths(t *testing.T, file string) map[string]int {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("%s does not parse: %v", file, err)
	}
	lengths := map[string]int{}
	seen := map[string]int{}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		key := file + ":" + funcKeyName(fd)
		if seen[key]++; seen[key] > 1 {
			key = fmt.Sprintf("%s#%d", key, seen[key])
		}
		lengths[key] = fset.Position(fd.End()).Line - fset.Position(fd.Pos()).Line + 1
	}
	return lengths
}

// funcKeyName is Name, or Recv.Name with the receiver's '*' and type
// parameters stripped (*T[K] becomes T).
func funcKeyName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	typ := fd.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	switch x := typ.(type) {
	case *ast.IndexExpr:
		typ = x.X
	case *ast.IndexListExpr:
		typ = x.X
	}
	if id, ok := typ.(*ast.Ident); ok {
		return id.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// funcLengthFindings compares the functions over the limit with the
// allowlist and returns one message per offender, sorted: too long and not
// listed, grown past its listed length, or listed but no longer over.
func funcLengthFindings(long, allow map[string]int) []string {
	var msgs []string
	for k, n := range long {
		switch want, ok := allow[k]; {
		case !ok:
			msgs = append(msgs, fmt.Sprintf("%s is %d lines, over %d (rules/code-modularity.md item 1): split it", k, n, funcLimit))
		case n > want:
			msgs = append(msgs, fmt.Sprintf("%s grew to %d lines, listed at %d: a listed function may shrink, never grow", k, n, want))
		}
	}
	for k := range allow {
		if _, ok := long[k]; !ok {
			msgs = append(msgs, fmt.Sprintf("%s is listed in longFuncs but no function by that key is over %d lines: stale, delete its row", k, funcLimit))
		}
	}
	sort.Strings(msgs)
	return msgs
}

// TestFunctionLengthKeys pins the key rules the tree does not exercise
// today: a generic receiver is keyed by its bare type name, and a repeated
// init takes #2 (plan D16).
func TestFunctionLengthKeys(t *testing.T) {
	src := "package p\n\ntype T[K any] struct{}\ntype U[K, V any] struct{}\n\n" +
		"func init() {}\nfunc init() {\n}\n" +
		"func (t *T[K]) A() {}\nfunc (u U[K, V]) B() {}\nfunc F() {}\n"
	file := t.TempDir() + "/p.go"
	if err := os.WriteFile(file, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	got := funcLengths(t, file)
	want := map[string]int{"init": 1, "init#2": 2, "T.A": 1, "U.B": 1, "F": 1}
	if len(got) != len(want) {
		t.Errorf("got %d keys %v, want %d", len(got), got, len(want))
	}
	for k, n := range want {
		if got[file+":"+k] != n {
			t.Errorf("%s:%s = %d, want %d (all: %v)", file, k, got[file+":"+k], n, got)
		}
	}
}

// TestFunctionLengthFiles: a tracked file deleted on disk is skipped, but a
// file the guard cannot stat for another reason fails it (D16), and the
// filters drop test, generated and skills/ files.
func TestFunctionLengthFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 directory")
	}
	dir := t.TempDir()
	keep, locked := dir+"/keep.go", dir+"/locked/a.go"
	for _, p := range []string{keep, locked} {
		if err := os.MkdirAll(path.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package p\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := filterGoFiles([]string{"", keep, dir + "/gone.go", "x_test.go", "a.pb.go", "b_gen.go", "skills/s.go"})
	if err != nil || len(got) != 1 || got[0] != keep {
		t.Errorf("filterGoFiles = %v, %v; want [%s], nil", got, err, keep)
	}
	if err := os.Chmod(path.Dir(locked), 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path.Dir(locked), 0o700)
	if got, err := filterGoFiles([]string{keep, locked}); err == nil {
		t.Errorf("filterGoFiles over an unreadable directory = %v, nil; want an error naming %s", got, locked)
	}
}
