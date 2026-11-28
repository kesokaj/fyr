package graph

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func charGraphInput() Input {
	return Input{
		RepoName: "fyr \"core\" \\v1\n<script>&|demo",
		Packages: []PackageInput{
			{Path: "cmd/fyr", PageRank: 0.30, Community: 0, ChurnScore: 4},
			{Path: "internal/core", Layer: "internal", PageRank: 0.50, Community: 1, ChurnScore: 12},
			{Path: "internal/leaf", Layer: "internal", PageRank: 0.20, Community: 1, ChurnScore: 2},
			{Path: "internal/testonly", Layer: "internal", PageRank: 0.05, Community: 2},
		},
		Files: []FileInput{
			{Path: "cmd/fyr/main.go", Pkg: "cmd/fyr", PkgIdx: 0, SymbolCount: 1, LineCount: 40, PageRank: 0.25, Community: 0, ChurnScore: 4},
			{Path: "internal/core/engine.go", Pkg: "internal/core", PkgIdx: 1, SymbolCount: 2, LineCount: 120, PageRank: 0.55, Community: 1, ChurnScore: 12},
			{Path: "internal/leaf/util.go", Pkg: "internal/leaf", PkgIdx: 2, SymbolCount: 1, LineCount: 60, PageRank: 0.20, Community: 1, ChurnScore: 2},
			{Path: "internal/testonly/only_test.go", Pkg: "internal/testonly", PkgIdx: 3, IsTest: true, SymbolCount: 1, LineCount: 30, PageRank: 0.05, Community: 2},
		},
		Symbols: []SymbolInput{
			{Name: "main", QualName: "cmd/fyr.main", Kind: "func", Pkg: "cmd/fyr", PkgIdx: 0, File: "cmd/fyr/main.go", FileIdx: 0, StartLine: 5, EndLine: 15, PageRank: 0.20, Community: 0},
			{Name: "Run", QualName: "internal/core.Run", Kind: "func", Pkg: "internal/core", PkgIdx: 1, File: "internal/core/engine.go", FileIdx: 1, StartLine: 10, EndLine: 45, Exported: true, PageRank: 0.60, Community: 1},
			{Name: "Helper", QualName: "internal/leaf.Helper", Kind: "func", Pkg: "internal/leaf", PkgIdx: 2, File: "internal/leaf/util.go", FileIdx: 2, StartLine: 8, EndLine: 20, Exported: true, PageRank: 0.20, Community: 1},
			{Name: "TestOnly", QualName: "internal/testonly.TestOnly", Kind: "func", Pkg: "internal/testonly", PkgIdx: 3, File: "internal/testonly/only_test.go", FileIdx: 3, StartLine: 5, EndLine: 12, IsTest: true, PageRank: 0.10, Community: 2},
		},
		PkgEdges: []EdgeInput{
			{0, 1, 2, 0, KindImport},
			{0, 1, 1, 1, KindCall}, // merged with {0,1}
			{1, 2, 3, 0, KindCall},
			{2, 1, 1, 0, KindCall}, // forms cycle with 1<->2
			{1, 1, 5, 0, KindCall}, // self-loop dropped
			{0, 9, 1, 0, KindCall}, // out-of-bounds dropped
			{3, 2, 0, 2, KindCall}, // test-only edge
		},
		FileEdges: []EdgeInput{
			{0, 1, 2, 0, KindCall},
			{1, 2, 2, 0, KindCall},
			{3, 2, 0, 1, KindCall},
		},
		SymEdges: []EdgeInput{
			{0, 1, 1, 0, KindCall},
			{1, 2, 1, 0, KindCall},
			{3, 2, 0, 1, KindCall},
		},
		PkgCycles: [][]int{{2, 1, 2}, {0}}, // dedups node within cycle, drops 1-node cycle
		HiddenCoupling: []HiddenCouplingInput{
			{FileAIdx: 0, FileBIdx: 2, Count: 5, Kind: "co_edit"},
			{FileAIdx: 1, FileBIdx: 0, Count: 3, Kind: "co_read"},
		},
		FileLinks: map[string]RecordLinks{
			"internal/core/engine.go": {
				IDs: []string{"BUG-101", "ADR-501", "BUG-101", "MEMORY-pref"},
			},
		},
	}
}

func TestGraphBuildAndLayoutCharacterization(t *testing.T) {
	p1 := Build(charGraphInput())
	p2 := Build(charGraphInput())

	if !reflect.DeepEqual(p1, p2) {
		t.Fatalf("Build is not deterministic across identical inputs")
	}

	var j1, j2 bytes.Buffer
	if err := RenderJSON(&j1, p1); err != nil {
		t.Fatalf("RenderJSON(1): %v", err)
	}
	if err := RenderJSON(&j2, p2); err != nil {
		t.Fatalf("RenderJSON(2): %v", err)
	}
	if !bytes.Equal(j1.Bytes(), j2.Bytes()) {
		t.Fatalf("RenderJSON output drifted across identical Builds")
	}
	if !json.Valid(j1.Bytes()) {
		t.Fatalf("RenderJSON produced invalid JSON")
	}

	assertGraphBuildInvariants(t, p1)
	assertGraphLayoutInvariants(t, p1)
}

func assertGraphBuildInvariants(t *testing.T, p *Payload) {
	t.Helper()
	if p.Meta.TotalPackages != 4 || p.Meta.ProdPackages != 3 {
		t.Errorf("packages meta = (%d total, %d prod), want (4, 3)", p.Meta.TotalPackages, p.Meta.ProdPackages)
	}
	if p.Meta.TotalFiles != 4 || p.Meta.ProdFiles != 3 {
		t.Errorf("files meta = (%d total, %d prod), want (4, 3)", p.Meta.TotalFiles, p.Meta.ProdFiles)
	}
	if p.Meta.TotalSymbols != 4 || p.Meta.ProdSymbols != 3 {
		t.Errorf("symbols meta = (%d total, %d prod), want (4, 3)", p.Meta.TotalSymbols, p.Meta.ProdSymbols)
	}
	wantPkgEdges := [][5]int{
		{0, 1, 3, 1, KindImport | KindCall},
		{1, 2, 3, 0, KindCall},
		{2, 1, 1, 0, KindCall},
		{3, 2, 0, 2, KindCall},
	}
	if !reflect.DeepEqual(p.PkgEdges, wantPkgEdges) {
		t.Errorf("normalized PkgEdges = %v, want %v", p.PkgEdges, wantPkgEdges)
	}
	if len(p.Meta.PkgCycles) != 1 || !reflect.DeepEqual(p.Meta.PkgCycles[0], []int{1, 2}) {
		t.Errorf("normalized PkgCycles = %v, want [[1 2]]", p.Meta.PkgCycles)
	}
	if p.Meta.HiddenCouplingEditPairs != 1 || p.Meta.HiddenCouplingReadPairs != 1 {
		t.Errorf("hidden coupling counts = (edit=%d, read=%d), want (1, 1)",
			p.Meta.HiddenCouplingEditPairs, p.Meta.HiddenCouplingReadPairs)
	}
	if !p.Packages[1].GodNode || p.Packages[3].GodNode {
		t.Errorf("godNode flags: internal/core=%v internal/testonly=%v, want (true, false)",
			p.Packages[1].GodNode, p.Packages[3].GodNode)
	}
	if p.Packages[1].ProdInDeg != 2 || p.Packages[1].ProdOutDeg != 1 {
		t.Errorf("internal/core prod degrees = (in=%d, out=%d), want (2, 1)",
			p.Packages[1].ProdInDeg, p.Packages[1].ProdOutDeg)
	}
	wantIDs := []string{"ADR-501", "BUG-101", "MEMORY-pref"}
	if !reflect.DeepEqual(p.Packages[1].BrainLinks.IDs, wantIDs) {
		t.Errorf("internal/core BrainLinks.IDs = %v, want %v", p.Packages[1].BrainLinks.IDs, wantIDs)
	}
}

func assertGraphLayoutInvariants(t *testing.T, p *Payload) {
	t.Helper()
	pkgSeen := map[[2]float64]bool{}
	for _, pkg := range p.Packages {
		if math.IsNaN(pkg.X) || math.IsInf(pkg.X, 0) || math.IsNaN(pkg.Y) || math.IsInf(pkg.Y, 0) {
			t.Errorf("package %s has non-finite position (%v, %v)", pkg.ID, pkg.X, pkg.Y)
		}
		pkgSeen[[2]float64{pkg.X, pkg.Y}] = true
	}
	if len(pkgSeen) != len(p.Packages) {
		t.Errorf("package layout produced duplicate coordinates: %d distinct of %d", len(pkgSeen), len(p.Packages))
	}
	for _, f := range p.Files {
		if math.IsNaN(f.X) || math.IsInf(f.X, 0) || math.IsNaN(f.Y) || math.IsInf(f.Y, 0) {
			t.Errorf("file %s has non-finite position (%v, %v)", f.ID, f.X, f.Y)
		}
	}
	for _, s := range p.Symbols {
		if math.IsNaN(s.X) || math.IsInf(s.X, 0) || math.IsNaN(s.Y) || math.IsInf(s.Y, 0) {
			t.Errorf("symbol %s has non-finite position (%v, %v)", s.QualName, s.X, s.Y)
		}
	}
}

func TestGraphRenderDOTHTMLMarkdownCharacterization(t *testing.T) {
	p := Build(charGraphInput())

	t.Run("RenderDOT deterministic and escaped", func(t *testing.T) {
		var d1, d2 bytes.Buffer
		if err := RenderDOT(&d1, p); err != nil {
			t.Fatalf("RenderDOT(1): %v", err)
		}
		if err := RenderDOT(&d2, p); err != nil {
			t.Fatalf("RenderDOT(2): %v", err)
		}
		if !bytes.Equal(d1.Bytes(), d2.Bytes()) {
			t.Fatalf("RenderDOT is non-deterministic")
		}
		got := d1.String()
		wantHeader := "digraph \"fyr \\\"core\\\" \\\\v1\\n<script>&|demo\" {\n"
		if !strings.HasPrefix(got, wantHeader) || !strings.HasSuffix(got, "}\n") {
			t.Fatalf("DOT header/footer mismatch:\n%s", got)
		}
		for _, want := range []string{
			`subgraph "cluster_cmd"`,
			`subgraph "cluster_internal"`,
			`[ADR-501, BUG-101, MEMORY-pref]`,
			`p1 -> p2 [penwidth=`,
			`color="#f7768e"`,
			`p0 -> p1 [penwidth=`,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("DOT missing %q:\n%s", want, got)
			}
		}
		if idxCmd, idxInt := strings.Index(got, `cluster_cmd`), strings.Index(got, `cluster_internal`); idxCmd >= idxInt {
			t.Errorf("DOT clusters not sorted alphabetically: cmd=%d internal=%d", idxCmd, idxInt)
		}
	})

	t.Run("RenderHTML deterministic offline CSP and safe JSON", func(t *testing.T) {
		var h1, h2 bytes.Buffer
		if err := RenderHTML(&h1, p); err != nil {
			t.Fatalf("RenderHTML(1): %v", err)
		}
		if err := RenderHTML(&h2, p); err != nil {
			t.Fatalf("RenderHTML(2): %v", err)
		}
		if !bytes.Equal(h1.Bytes(), h2.Bytes()) {
			t.Fatalf("RenderHTML is non-deterministic")
		}
		html := h1.String()
		for _, want := range []string{DefaultHTMLCSP, "<html", "</html>", "internal/core", "BUG-101"} {
			if !strings.Contains(html, want) {
				t.Errorf("HTML missing %q", want)
			}
		}
		if strings.Contains(html, "<script>&|demo") {
			t.Errorf("HTML failed to escape raw <script>& repo payload")
		}
	})

	t.Run("RenderMarkdown deterministic sections and escaping", func(t *testing.T) {
		var m1, m2 bytes.Buffer
		if err := RenderMarkdown(&m1, p); err != nil {
			t.Fatalf("RenderMarkdown(1): %v", err)
		}
		if err := RenderMarkdown(&m2, p); err != nil {
			t.Fatalf("RenderMarkdown(2): %v", err)
		}
		if !bytes.Equal(m1.Bytes(), m2.Bytes()) {
			t.Fatalf("RenderMarkdown is non-deterministic")
		}
		md := m1.String()
		firstLine, _, _ := strings.Cut(md, "\n")
		if !strings.HasPrefix(firstLine, "# Architecture Graph — ") || strings.Contains(firstLine, "\r") {
			t.Errorf("unexpected Markdown title line: %q", firstLine)
		}
		for _, want := range []string{
			"## Summary",
			"| Packages | 3 | 4 |",
			"| Package Cycles | 1 | 1 |",
			"| Hidden Co-Edit Pairs | 1 | 1 |",
			"| Hidden Co-Read Pairs | 1 | 1 |",
			"## Top 10 God Packages",
			"## Top 10 God Symbols",
			"## Package Cycles",
			"`internal/core` <-> `internal/leaf`",
			"## Hidden Co-Activity Coupling",
			"## High-Churn Files",
			"BUG-101",
		} {
			if !strings.Contains(md, want) {
				t.Errorf("Markdown missing %q:\n%s", want, md)
			}
		}
	})

	t.Run("nil payload safe across renderers", func(t *testing.T) {
		var b bytes.Buffer
		if err := RenderDOT(&b, nil); err != nil || !strings.HasPrefix(b.String(), "digraph ") {
			t.Errorf("RenderDOT(nil) = %v, out=%q", err, b.String())
		}
		b.Reset()
		if err := RenderHTML(&b, nil); err != nil || !strings.Contains(b.String(), DefaultHTMLCSP) {
			t.Errorf("RenderHTML(nil) = %v", err)
		}
		b.Reset()
		if err := RenderMarkdown(&b, nil); err != nil || !strings.Contains(b.String(), "## Summary") {
			t.Errorf("RenderMarkdown(nil) = %v", err)
		}
	})
}

func TestGraphWriteFileCharacterization(t *testing.T) {
	t.Run("regular file create force and refusal without force", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "nested", "out", "graph.dot")
		if err := WriteFile(dst, []byte("v1"), false); err != nil {
			t.Fatalf("WriteFile initial: %v", err)
		}
		fi, err := os.Stat(dst)
		if err != nil {
			t.Fatalf("Stat(%s): %v", dst, err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %o, want 0600", fi.Mode().Perm())
		}
		if err := WriteFile(dst, []byte("v2"), false); err == nil || !strings.Contains(err.Error(), "pass --force") {
			t.Fatalf("WriteFile overwrite without force = %v, want pass --force error", err)
		}
		got, err := os.ReadFile(dst)
		if err != nil || string(got) != "v1" {
			t.Fatalf("file modified despite force=false: %q, %v", got, err)
		}
		if err := WriteFile(dst, []byte("v2-forced"), true); err != nil {
			t.Fatalf("WriteFile with force=true: %v", err)
		}
		got, err = os.ReadFile(dst)
		if err != nil || string(got) != "v2-forced" {
			t.Fatalf("file after force=true = %q, %v", got, err)
		}
	})

	t.Run("must-fail empty symlink target symlink parent and directory target", func(t *testing.T) {
		if err := WriteFile("   ", []byte("x"), false); err == nil {
			t.Fatalf("expected error on whitespace path")
		}

		dir := t.TempDir()
		victim := filepath.Join(dir, "victim.txt")
		if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		symTarget := filepath.Join(dir, "link.dot")
		if err := os.Symlink(victim, symTarget); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		for _, force := range []bool{false, true} {
			if err := WriteFile(symTarget, []byte("pwn"), force); err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Errorf("WriteFile(symlink target, force=%v) = %v, want symlink refusal", force, err)
			}
		}
		if got, _ := os.ReadFile(victim); string(got) != "keep" {
			t.Fatalf("symlink target victim modified: %q", got)
		}

		extDir := t.TempDir()
		symParent := filepath.Join(dir, "linked-dir")
		if err := os.Symlink(extDir, symParent); err != nil {
			t.Fatal(err)
		}
		if err := WriteFile(filepath.Join(symParent, "child", "out.html"), []byte("x"), true); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Errorf("WriteFile(symlink parent) = %v, want symlink refusal", err)
		}
		if _, err := os.Stat(filepath.Join(extDir, "child")); !os.IsNotExist(err) {
			t.Errorf("symlink parent directory was traversed: err=%v", err)
		}

		existingDir := filepath.Join(dir, "adir")
		if err := os.Mkdir(existingDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := WriteFile(existingDir, []byte("x"), true); err == nil || !strings.Contains(err.Error(), "non-regular") {
			t.Errorf("WriteFile(existing dir) = %v, want non-regular refusal", err)
		}
	})
}
