package search

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTarjanSCCExcludesIface(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/scc\n\ngo 1.24\n")
	writeFile(t, root, "internal/port/port.go", "package port\n\ntype Store interface { Save() error }\nfunc RunPort(s Store) error { return s.Save() }\n")
	writeFile(t, root, "internal/adapter/adapter.go", "package adapter\n\nimport \"example.com/scc/internal/port\"\n\ntype SQLStore struct{}\nfunc (SQLStore) Save() error { return nil }\nfunc Boot() error { return port.RunPort(SQLStore{}) }\n")
	commitAll(t, root, "seed clean hexagonal")

	snapClean, err := LoadGraphSnapshot(Options{Dir: root, Home: home}, GraphOptions{})
	if err != nil {
		t.Fatalf("LoadGraphSnapshot clean: %v", err)
	}
	if len(snapClean.PkgCycles) != 0 || snapClean.StructuralSignals.ProdPkgCycles != 0 {
		t.Fatalf("expected 0 cycles when only reverse iface_call/implements points back, got %+v", snapClean.PkgCycles)
	}

	writeFile(t, root, "internal/c1/c1.go", "package c1\nimport \"example.com/scc/internal/c2\"\nfunc A() { c2.B() }\n")
	writeFile(t, root, "internal/c2/c2.go", "package c2\nimport \"example.com/scc/internal/c3\"\nfunc B() { c3.C() }\n")
	writeFile(t, root, "internal/c3/c3.go", "package c3\nimport \"example.com/scc/internal/c1\"\nfunc C() { c1.A() }\n")
	commitAll(t, root, "seed 3-hop cycle")

	var mapBuf bytes.Buffer
	if err := Search(Options{Dir: root, Home: home, Map: true}, &mapBuf); err != nil {
		t.Fatalf("Search --map: %v", err)
	}
	if !strings.Contains(mapBuf.String(), "internal/c1 -> internal/c2 -> internal/c3 -> internal/c1") {
		t.Fatalf("expected 3-hop cycle in map, got:\n%s", mapBuf.String())
	}
	snapCyc, err := LoadGraphSnapshot(Options{Dir: root, Home: home}, GraphOptions{})
	if err != nil {
		t.Fatalf("LoadGraphSnapshot cycle: %v", err)
	}
	if len(snapCyc.PkgCycles) != 1 || len(snapCyc.PkgCycles[0]) != 3 || snapCyc.StructuralSignals.ProdPkgCycles != 1 {
		t.Fatalf("expected 1 3-node SCC cycle, got cycles=%+v sig=%+v", snapCyc.PkgCycles, snapCyc.StructuralSignals)
	}
}

func TestLouvainDeterminism(t *testing.T) {
	edgesA := []LouvainEdge{
		{U: 0, V: 1, Weight: 12}, {U: 1, V: 2, Weight: 10}, {U: 0, V: 2, Weight: 11},
		{U: 3, V: 4, Weight: 15}, {U: 4, V: 5, Weight: 14}, {U: 3, V: 5, Weight: 13},
		{U: 2, V: 3, Weight: 1},
	}
	edgesB := []LouvainEdge{
		{U: 2, V: 3, Weight: 1},
		{U: 5, V: 3, Weight: 13}, {U: 4, V: 5, Weight: 14}, {U: 3, V: 4, Weight: 15},
		{U: 0, V: 2, Weight: 11}, {U: 2, V: 1, Weight: 10}, {U: 1, V: 0, Weight: 12},
	}
	base := LouvainCommunities(6, edgesA)
	for run := 0; run < 20; run++ {
		got := LouvainCommunities(6, edgesB)
		if !reflect.DeepEqual(got, base) {
			t.Fatalf("run %d non-deterministic Louvain: got %v, want %v", run, got, base)
		}
	}
	if base[0] != base[1] || base[1] != base[2] || base[3] != base[4] || base[4] != base[5] || base[0] == base[3] {
		t.Fatalf("expected 2 distinct dense clusters {0,1,2} and {3,4,5}, got %v", base)
	}
	// Multi-level coarsening: pairs (0,1) and (2,3) merge in Phase 1, then merge via super-node cross weight in Phase 2.
	hier := []LouvainEdge{
		{U: 0, V: 1, Weight: 20}, {U: 2, V: 3, Weight: 20},
		{U: 0, V: 2, Weight: 8}, {U: 1, V: 3, Weight: 8},
		{U: 4, V: 5, Weight: 30}, {U: 6, V: 7, Weight: 30},
	}
	hComm := LouvainCommunities(8, hier)
	if hComm[0] != hComm[1] || hComm[1] != hComm[2] || hComm[2] != hComm[3] || hComm[0] == hComm[4] {
		t.Fatalf("expected Phase 2 super-node aggregation to merge {0,1} and {2,3}, got %v", hComm)
	}
}

func TestDecomposeSubtasksLouvain(t *testing.T) {
	comms := map[string]int{
		"internal/a": 0, "internal/e": 0,
		"internal/b": 1, "internal/f": 1,
		"internal/c": 2, "internal/g": 2,
		"internal/d": 3,
	}
	// > 6 directories coalesces coupled packages by Louvain community.
	over6 := DecomposeSubtasks(
		"Refactor Store across internal/a/a.go, internal/b/b.go, internal/c/c.go, internal/d/d.go, internal/e/e.go, internal/f/f.go, internal/g/g.go",
		IntentTask,
		SliceResult{PkgCommunities: comms},
		nil,
		nil,
	)
	if len(over6) != 4 {
		t.Fatalf("expected 7 dirs across 4 communities to coalesce into 4 slices, got %d: %+v", len(over6), over6)
	}
	assertDisjointSubtaskFiles(t, over6)
	if !slices.Equal(over6[0].Files, []string{"internal/a/a.go", "internal/e/e.go"}) {
		t.Fatalf("expected community 0 files [internal/a/a.go internal/e/e.go], got %+v", over6[0])
	}

	// <= 6 directories preserves 1-dir-per-slice without merging.
	atMost6 := DecomposeSubtasks(
		"Refactor Store across internal/a/a.go, internal/e/e.go, internal/b/b.go",
		IntentTask,
		SliceResult{PkgCommunities: comms},
		nil,
		nil,
	)
	if len(atMost6) != 3 {
		t.Fatalf("<= 6 dirs must keep 1-dir-per-slice (got %d): %+v", len(atMost6), atMost6)
	}

	// Single-directory prompt never splits ([COORDINATOR_GUARD] safety).
	single := DecomposeSubtasks(
		"Refactor Store in internal/a/a.go and internal/a/b.go",
		IntentTask,
		SliceResult{PkgCommunities: comms},
		nil,
		nil,
	)
	if len(single) != 0 {
		t.Fatalf("single-dir prompt must return nil subtasks, got %+v", single)
	}
}

func seedStructuralSignalsRepo(t *testing.T) string {
	t.Helper()
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/sig\n\ngo 1.24\n")
	var hub strings.Builder
	hub.WriteString("package core\n\nfunc CentralHub() error {\n\treturn nil\n}\n")
	for i := 0; i < 360; i++ {
		fmt.Fprintf(&hub, "// line %d\n", i)
	}
	writeFile(t, root, "internal/core/hub.go", hub.String())
	writeFile(t, root, "internal/api/api.go", "package api\nimport \"example.com/sig/internal/core\"\nfunc Handle() error { return core.CentralHub() }\n")
	writeFile(t, root, "internal/cli/cli.go", "package cli\nimport \"example.com/sig/internal/core\"\nfunc RunCLI() error { return core.CentralHub() }\n")
	commitAll(t, root, "seed structural repo")
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"CentralHub"}})

	db := openDB(t, root)
	defer db.Close()
	hubAbs := CanonicalizePath(root, "internal/core/hub.go")
	if _, err := db.Exec(`INSERT INTO read_ledger(conversation_id, generation_id, resolved_path, start_line, end_line, sha256, turn_num, hit_count, updated_at) VALUES
		('s1', 0, ?, -5, -5, 'h1', 1, 2, 100),
		('s1', 0, ?, 1, 50, 'h1', 2, 4, 100)`, hubAbs, hubAbs); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO co_activity(file_a, file_b, kind, count, updated_at) VALUES
		('internal/api/api.go', 'internal/cli/cli.go', 'co_modified', 5, 100),
		('internal/api/api.go', 'internal/core/hub.go', 'co_modified', 4, 100)`); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestReadStructuralSignals(t *testing.T) {
	root := seedStructuralSignalsRepo(t)
	home := t.TempDir()
	dbPath := projectDB(root)

	sig, err := ReadStructuralSignals(dbPath, root)
	if err != nil {
		t.Fatalf("ReadStructuralSignals: %v", err)
	}
	if sig.TotalCodeFiles != 3 || sig.TotalProdPkgs != 3 || sig.LargeFilesOver350 != 1 {
		t.Fatalf("counts mismatch: %+v", sig)
	}
	if sig.GodNodeClampHits != 2 || sig.GodNodeRepeatReads != 3 || sig.GodNodeChurnRate <= 0 {
		t.Fatalf("god node churn mismatch: %+v", sig)
	}
	if sig.HiddenCouplingPairs != 1 || sig.HiddenCouplingEditPairs != 1 || sig.HiddenCouplingReadPairs != 0 || sig.HiddenCouplingRate != 0.5 || sig.HubConcentration <= 0 {
		t.Fatalf("coupling/hub mismatch: %+v", sig)
	}

	hints, err := ReadHiddenCouplingHints(dbPath, root, 3, 10)
	if err != nil {
		t.Fatalf("ReadHiddenCouplingHints: %v", err)
	}
	if len(hints) != 1 || hints[0].FileA != "internal/api/api.go" || hints[0].FileB != "internal/cli/cli.go" || hints[0].Count != 5 {
		t.Fatalf("unexpected hidden coupling hints: %+v", hints)
	}

	snap, err := LoadGraphSnapshot(Options{Dir: root, Home: home}, GraphOptions{})
	if err != nil {
		t.Fatalf("LoadGraphSnapshot: %v", err)
	}
	if len(snap.HiddenCoupling) != 1 || len(snap.Packages) != 3 || len(snap.Files) != 3 {
		t.Fatalf("snapshot mismatch: pkgs=%d files=%d hidden=%+v", len(snap.Packages), len(snap.Files), snap.HiddenCoupling)
	}
	for _, f := range snap.Files {
		if f.Path == "internal/core/hub.go" && (f.LineCount <= 350 || f.ClampHits != 2 || f.RepeatReads != 3) {
			t.Fatalf("hub.go file telemetry mismatch: %+v", f)
		}
	}
	assertNestedGoModSignals(t)
}

func assertNestedGoModSignals(t *testing.T) {
	t.Helper()
	root, home := newRepo(t), t.TempDir()
	writeFile(t, root, "sub/go.mod", "module example.com/nested\n\ngo 1.24\n")
	writeFile(t, root, "sub/p1/a.go", "package p1\nimport \"example.com/nested/p2\"\nfunc A() { p2.B() }\n")
	writeFile(t, root, "sub/p2/b.go", "package p2\nimport \"example.com/nested/p1\"\nfunc B() { p1.A() }\n")
	commitAll(t, root, "nested mod cycle")
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"nested"}})

	sig, err := ReadStructuralSignals(projectDB(root), root)
	if err != nil || sig.ProdPkgCycles != 1 {
		t.Fatalf("nested go.mod ReadStructuralSignals = %+v, err=%v; want ProdPkgCycles=1", sig, err)
	}
}

func TestFindPackageCyclesBoundedOnDenseClique(t *testing.T) {
	deps := map[string][]string{"p00": {"p01"}, "p01": {"p00"}}
	for i := 1; i < 20; i++ {
		u := fmt.Sprintf("p%02d", i)
		for j := 1; j < 20; j++ {
			if i != j {
				deps[u] = append(deps[u], fmt.Sprintf("p%02d", j))
			}
		}
	}
	start := time.Now()
	cycles := findPackageCycles(deps)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("findPackageCycles took %v on 20-node dense clique, want < 500ms", elapsed)
	}
	if !slices.Contains(cycles, "p00 -> p01 -> p00") {
		t.Fatalf("expected canonical cycle p00 -> p01 -> p00 in %+v", cycles)
	}
}

func TestLoadGraphSnapshotIncludeVendored(t *testing.T) {
	root, home := newRepo(t), t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/vend\n\ngo 1.24\n")
	writeFile(t, root, "internal/app/app.go", "package app\n\nfunc ServeApp() {}\n")
	writeFile(t, root, "skills/demo/SKILL.md", "# Demo Skill\n")
	writeFile(t, root, "skills/demo/references/helper.ts", "export function helperFn() { return 1 }\n")
	writeFile(t, root, "skills/demo/references/widget.ts", "import { helperFn } from './helper'\nexport function renderWidget() { return helperFn() }\n")
	writeFile(t, root, "skills/demo/references/guide.md", "# Ref Guide\n\nUses `renderWidget`.\n")
	writeFile(t, root, "skills/demo/assets/theme.js", "export function themeToken() { return 'ok' }\n")
	commitAll(t, root, "seed vendored skill")

	if err := Index(Options{Dir: root, Home: home}, io.Discard); err != nil {
		t.Fatalf("Index: %v", err)
	}
	assertZeroVendoredRowsInDB(t, root)

	snapDefault, err := LoadGraphSnapshot(Options{Dir: root, Home: home}, GraphOptions{IncludeVendored: false})
	if err != nil {
		t.Fatalf("LoadGraphSnapshot default: %v", err)
	}
	if hasSnapshotFile(snapDefault, "skills/demo/references/widget.ts") || hasSnapshotFile(snapDefault, "skills/demo/assets/theme.js") {
		t.Fatalf("IncludeVendored=false unexpectedly included vendored files: %+v", snapDefault.Files)
	}

	snapCodeOnly, err := LoadGraphSnapshot(Options{Dir: root, Home: home}, GraphOptions{IncludeVendored: true})
	if err != nil {
		t.Fatalf("LoadGraphSnapshot IncludeVendored=true: %v", err)
	}
	assertVendoredCodeSnapshot(t, snapCodeOnly)

	snapWithDocs, err := LoadGraphSnapshot(Options{Dir: root, Home: home}, GraphOptions{IncludeVendored: true, IncludeDocs: true})
	if err != nil {
		t.Fatalf("LoadGraphSnapshot IncludeVendored+IncludeDocs: %v", err)
	}
	if !hasSnapshotFile(snapWithDocs, "skills/demo/references/guide.md") || !hasSnapshotSymbol(snapWithDocs, "Ref Guide") {
		t.Fatalf("expected guide.md heading when IncludeVendored && IncludeDocs")
	}
	assertZeroVendoredRowsInDB(t, root)
}

func assertVendoredCodeSnapshot(t *testing.T, snap *GraphSnapshot) {
	t.Helper()
	for _, want := range []string{"skills/demo/references/widget.ts", "skills/demo/references/helper.ts", "skills/demo/assets/theme.js"} {
		if !hasSnapshotFile(snap, want) {
			t.Fatalf("IncludeVendored=true missing %s in files", want)
		}
	}
	if hasSnapshotFile(snap, "skills/demo/references/guide.md") {
		t.Fatalf("IncludeDocs=false unexpectedly included guide.md")
	}
	for _, sym := range []string{"renderWidget", "helperFn", "themeToken"} {
		if !hasSnapshotSymbol(snap, sym) {
			t.Fatalf("IncludeVendored=true missing symbol %s", sym)
		}
	}
	if len(snap.FileEdges) == 0 {
		t.Fatalf("expected import edge widget.ts -> helper.ts in FileEdges")
	}
}

func assertZeroVendoredRowsInDB(t *testing.T, root string) {
	t.Helper()
	db := openDB(t, root)
	defer db.Close()
	const where = `(f.path LIKE 'skills/%/references/%' OR f.path LIKE 'skills/%/assets/%')`
	if n := count(t, db, `SELECT count(*) FROM symbols sy JOIN files f ON f.oid = sy.oid WHERE `+where); n != 0 {
		t.Fatalf("SKILL_SYMBOLS in search.db = %d, want 0", n)
	}
	if n := count(t, db, `SELECT count(*) FROM sym_refs sr JOIN files f ON f.oid = sr.oid WHERE `+where); n != 0 {
		t.Fatalf("vendored sym_refs in search.db = %d, want 0", n)
	}
	if n := count(t, db, `SELECT count(*) FROM symbols sy JOIN files f ON f.oid = sy.oid WHERE f.path = 'skills/demo/SKILL.md'`); n == 0 {
		t.Fatalf("SKILL.md symbols unexpectedly 0")
	}
}

func hasSnapshotFile(snap *GraphSnapshot, p string) bool {
	for _, f := range snap.Files {
		if f.Path == p {
			return true
		}
	}
	return false
}

func hasSnapshotSymbol(snap *GraphSnapshot, name string) bool {
	for _, s := range snap.Symbols {
		if s.Name == name {
			return true
		}
	}
	return false
}

func TestHiddenCouplingSplitEditAndRead(t *testing.T) {
	root := seedStructuralSignalsRepo(t)
	home := t.TempDir()
	db := openDB(t, root)
	if _, err := db.Exec(`INSERT INTO co_activity(file_a, file_b, kind, count, updated_at) VALUES
		('internal/api/api.go', 'internal/cli/cli.go', 'co_read', 6, 100),
		('internal/cli/cli.go', 'internal/core/hub.go', 'co_read', 4, 100)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	// Add a 4th disconnected file so co_read-only pair lacks AST edge.
	writeFile(t, root, "internal/aux/aux.go", "package aux\nfunc Aux() {}\n")
	commitAll(t, root, "add aux")
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"Aux"}})
	db2 := openDB(t, root)
	if _, err := db2.Exec(`INSERT INTO co_activity(file_a, file_b, kind, count, updated_at) VALUES ('internal/aux/aux.go', 'internal/cli/cli.go', 'co_read', 7, 100)`); err != nil {
		db2.Close()
		t.Fatal(err)
	}
	db2.Close()

	sig, err := ReadStructuralSignals(projectDB(root), root)
	if err != nil {
		t.Fatalf("ReadStructuralSignals: %v", err)
	}
	if sig.HiddenCouplingPairs != 2 || sig.HiddenCouplingEditPairs != 1 || sig.HiddenCouplingReadPairs != 2 {
		t.Fatalf("split coupling signals = %+v, want total=2 edit=1 read=2", sig)
	}
	snap, err := LoadGraphSnapshot(Options{Dir: root, Home: home}, GraphOptions{})
	if err != nil {
		t.Fatalf("LoadGraphSnapshot: %v", err)
	}
	if len(snap.HiddenCoupling) != 3 {
		t.Fatalf("expected 3 per-kind GraphHiddenCoupling entries, got %+v", snap.HiddenCoupling)
	}
}
