package fyr

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"os/exec"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const fyrModulePath = "github.com/kesokaj/fyr"

var (
	leafPackages = map[string]bool{
		".":                      true,
		"internal/trail":         true,
		"internal/redact":        true,
		"internal/graph":         true,
		"internal/webpreview":    true,
		"internal/readledger":    true,
		"internal/isolate/acp":   true,
		"internal/isolate/creds": true,
	}
	sqliteAllowlist      = map[string]bool{"internal/sqliteutil": true}
	orchestratorPackages = map[string]bool{"internal/auto": true, "internal/dream": true, "internal/gate": true}
)

// initCeilingTable pins current production func init() counts per repo-relative
// file. Locked empty (0 production func init() repo-wide).
const initCeilingTable = ""

// dupHelperCeilingTable pins current cross-package duplicate unexported top-level
// production helper names (distinct package count). Ceiling ratchet: new
// duplicates or growth fail; shrinking or removal passes.
const dupHelperCeilingTable = `
	allowed=2 boolInt=2 brainDBExists=2 checkScratchNoSymlinks=2 classify=2
	exists=2 firstOf=2 firstString=2 isPathWordByte=2 lock=2
	oneLine=3 open=2 openStore=2 parseLine=2 printable=2
	projectKey=2 recordTime=3 runGroup=2 saveState=2 short=2
	sortFindings=3 sortedKeys=2 statePath=2 tickRun=2 tryOpen=2
	within=3
`

// TestPackageLayeringAndSQLiteConfinement enforces package architecture
// boundaries across all production Go files:
//   - zero-outbound module imports on leaf packages;
//   - internal/harness imports only root module package (.);
//   - modernc.org/sqlite* direct imports confined to the SQLite allowlist;
//   - zero downward fan-in from orchestrators into domain packages.
func TestPackageLayeringAndSQLiteConfinement(t *testing.T) {
	requireGitCheckout(t)
	files := guardedGoFiles(t)
	if len(files) == 0 {
		t.Fatal("git lists no non-test .go files; test ran in wrong directory")
	}
	pkgImports := collectProdPackageImports(t, files)
	for _, msg := range layeringFindings(fyrModulePath, pkgImports) {
		t.Error(msg)
	}
}

// TestNoNewInitOrDuplicateHelpers enforces ceiling ratchets on production
// func init() declarations and cross-package duplicate top-level prod helper
// names (failing on new/grown offenders while allowing shrinking/removal).
func TestNoNewInitOrDuplicateHelpers(t *testing.T) {
	requireGitCheckout(t)
	files := guardedGoFiles(t)
	if len(files) == 0 {
		t.Fatal("git lists no non-test .go files; test ran in wrong directory")
	}
	inits, dups := collectInitAndDupHelpers(t, files)
	for _, msg := range initCeilingFindings(inits, parseCeilingTable(t, initCeilingTable)) {
		t.Error(msg)
	}
	for _, msg := range dupHelperCeilingFindings(dups, parseCeilingTable(t, dupHelperCeilingTable)) {
		t.Error(msg)
	}
}

// TestTrack1DocsSTESentenceRatchet lives in arch_fitness_docs_test.go
// (package fyr_test) to avoid an import cycle with internal/check.

func requireGitCheckout(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := os.Stat(".git"); err != nil {
		t.Skip("not a git checkout")
	}
}

func collectProdPackageImports(t *testing.T, files []string) map[string][]string {
	t.Helper()
	seen := map[string]map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		parsed, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s does not parse: %v", f, err)
		}
		pkg := path.Dir(f)
		if seen[pkg] == nil {
			seen[pkg] = map[string]bool{}
		}
		for _, imp := range parsed.Imports {
			if p, err := strconv.Unquote(imp.Path.Value); err == nil {
				seen[pkg][p] = true
			}
		}
	}
	out := make(map[string][]string, len(seen))
	for pkg, m := range seen {
		out[pkg] = slices.Sorted(maps.Keys(m))
	}
	return out
}

func layeringFindings(mod string, pkgImports map[string][]string) []string {
	var msgs []string
	for pkg, imports := range pkgImports {
		for _, imp := range imports {
			msgs = append(msgs, checkOneImportLayer(mod, pkg, imp)...)
		}
	}
	sort.Strings(msgs)
	return msgs
}

func checkOneImportLayer(mod, pkg, imp string) []string {
	var msgs []string
	isMod := imp == mod || strings.HasPrefix(imp, mod+"/")
	relImp := strings.TrimPrefix(imp, mod+"/")
	if imp == mod {
		relImp = "."
	}
	if leafPackages[pkg] && isMod {
		msgs = append(msgs, fmt.Sprintf("leaf package %s must have zero outbound module imports, found %s", pkg, imp))
	}
	if pkg == "internal/harness" && isMod && relImp != "." {
		msgs = append(msgs, fmt.Sprintf("internal/harness may only import root module %s, found %s", mod, imp))
	}
	if (imp == "modernc.org/sqlite" || strings.HasPrefix(imp, "modernc.org/sqlite/")) && !sqliteAllowlist[pkg] {
		msgs = append(msgs, fmt.Sprintf("package %s directly imports %s outside sqlite allowlist", pkg, imp))
	}
	if isMod && !isOrchestratorOrCLI(pkg) && isOrchestratorImport(relImp) {
		msgs = append(msgs, fmt.Sprintf("domain package %s must not import orchestrator %s", pkg, relImp))
	}
	return msgs
}

func isOrchestratorOrCLI(pkg string) bool {
	if pkg == "cmd/fyr" || strings.HasPrefix(pkg, "cmd/") {
		return true
	}
	return orchestratorPackages[pkg] || isOrchestratorImport(pkg)
}

func isOrchestratorImport(rel string) bool {
	if orchestratorPackages[rel] {
		return true
	}
	for orch := range orchestratorPackages {
		if strings.HasPrefix(rel, orch+"/") {
			return true
		}
	}
	return false
}

func collectInitAndDupHelpers(t *testing.T, files []string) (map[string]int, map[string][]string) {
	t.Helper()
	inits := map[string]int{}
	helperPkgs := map[string]map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		parsed, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s does not parse: %v", f, err)
		}
		pkg := path.Dir(f)
		for _, d := range parsed.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Body == nil {
				continue
			}
			name := fd.Name.Name
			if name == "init" {
				inits[f]++
				continue
			}
			if name == "main" || ast.IsExported(name) {
				continue
			}
			if helperPkgs[name] == nil {
				helperPkgs[name] = map[string]bool{}
			}
			helperPkgs[name][pkg] = true
		}
	}
	dups := map[string][]string{}
	for name, pkgs := range helperPkgs {
		if len(pkgs) > 1 {
			dups[name] = slices.Sorted(maps.Keys(pkgs))
		}
	}
	return inits, dups
}

func initCeilingFindings(got, ceiling map[string]int) []string {
	var msgs []string
	for file, n := range got {
		maxN, ok := ceiling[file]
		switch {
		case !ok:
			msgs = append(msgs, fmt.Sprintf("unlisted prod init() in %s (%d): avoid new init() functions", file, n))
		case n > maxN:
			msgs = append(msgs, fmt.Sprintf("prod init() count in %s grew to %d (ceiling %d)", file, n, maxN))
		}
	}
	sort.Strings(msgs)
	return msgs
}

func dupHelperCeilingFindings(got map[string][]string, ceiling map[string]int) []string {
	var msgs []string
	for name, pkgs := range got {
		maxN, ok := ceiling[name]
		switch {
		case !ok:
			msgs = append(msgs, fmt.Sprintf("new cross-package duplicate prod helper %q in %d packages (%s): share or rename", name, len(pkgs), strings.Join(pkgs, ", ")))
		case len(pkgs) > maxN:
			msgs = append(msgs, fmt.Sprintf("duplicate prod helper %q grew to %d packages (ceiling %d: %s)", name, len(pkgs), maxN, strings.Join(pkgs, ", ")))
		}
	}
	sort.Strings(msgs)
	return msgs
}

func TestPackageLayeringBites(t *testing.T) {
	good := map[string][]string{
		".":                   {"embed"},
		"internal/trail":      {"os", "strings"},
		"internal/harness":    {fyrModulePath, "os"},
		"internal/search":     {fyrModulePath + "/internal/trail", fyrModulePath + "/internal/sqliteutil"},
		"internal/sqliteutil": {"database/sql", "modernc.org/sqlite", "modernc.org/sqlite/lib"},
		"internal/auto":       {fyrModulePath + "/internal/dream", fyrModulePath + "/internal/brain"},
		"cmd/fyr":             {fyrModulePath + "/internal/auto", fyrModulePath + "/internal/gate"},
	}
	if msgs := layeringFindings(fyrModulePath, good); len(msgs) != 0 {
		t.Fatalf("clean layering got findings: %v", msgs)
	}
	bad := map[string][]string{
		"internal/trail":   {fyrModulePath + "/internal/redact"},
		"internal/harness": {fyrModulePath, fyrModulePath + "/internal/trail"},
		"internal/brain":   {"modernc.org/sqlite", fyrModulePath + "/internal/auto"},
		"internal/search":  {"modernc.org/sqlite/lib"},
		"internal/metrics": {"modernc.org/sqlite"},
		"internal/check":   {fyrModulePath + "/internal/gate"},
	}
	got := layeringFindings(fyrModulePath, bad)
	wantSubstrings := []string{
		"leaf package internal/trail",
		"internal/harness may only import root module",
		"package internal/brain directly imports modernc.org/sqlite",
		"package internal/search directly imports modernc.org/sqlite/lib",
		"package internal/metrics directly imports modernc.org/sqlite",
		"domain package internal/brain must not import orchestrator internal/auto",
		"domain package internal/check must not import orchestrator internal/gate",
	}
	if len(got) != len(wantSubstrings) {
		t.Fatalf("bad layering findings (%d): %v, want %d", len(got), got, len(wantSubstrings))
	}
	for _, sub := range wantSubstrings {
		if !slices.ContainsFunc(got, func(m string) bool { return strings.Contains(m, sub) }) {
			t.Errorf("missing expected finding containing %q in %v", sub, got)
		}
	}
}

func TestNoNewInitOrDuplicateHelpersBites(t *testing.T) {
	// Ceiling ratchet: shrinking or removing allowlisted entries must NOT fail.
	if msgs := initCeilingFindings(map[string]int{"a.go": 1}, map[string]int{"a.go": 2, "gone.go": 1}); len(msgs) != 0 {
		t.Fatalf("shrunk init ceiling failed: %v", msgs)
	}
	if msgs := dupHelperCeilingFindings(map[string][]string{"h": {"p1", "p2"}}, map[string]int{"h": 3, "gone": 2}); len(msgs) != 0 {
		t.Fatalf("shrunk dup helper ceiling failed: %v", msgs)
	}
	// Must-fail: unlisted or grown init() and duplicate helpers must fail.
	initBad := initCeilingFindings(map[string]int{"new.go": 1, "a.go": 2}, map[string]int{"a.go": 1})
	if len(initBad) != 2 || !strings.Contains(initBad[0], "a.go") || !strings.Contains(initBad[1], "new.go") {
		t.Fatalf("initCeilingFindings bad = %v, want 2 findings", initBad)
	}
	dupBad := dupHelperCeilingFindings(
		map[string][]string{"newDup": {"p1", "p2"}, "grew": {"p1", "p2", "p3"}},
		map[string]int{"grew": 2},
	)
	if len(dupBad) != 2 || !strings.Contains(dupBad[0], "grew") || !strings.Contains(dupBad[1], "newDup") {
		t.Fatalf("dupHelperCeilingFindings bad = %v, want 2 findings", dupBad)
	}
	// Same unexported helper name in two files of the SAME package is NOT a cross-package duplicate.
	dir := t.TempDir()
	f1, f2 := dir+"/open_unix.go", dir+"/open_other.go"
	for _, p := range []string{f1, f2} {
		if err := os.WriteFile(p, []byte("package p\nfunc openHelper() {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inits, dups := collectInitAndDupHelpers(t, []string{f1, f2})
	if len(inits) != 0 || len(dups) != 0 {
		t.Fatalf("same-package helpers flagged: inits=%v dups=%v", inits, dups)
	}
}
