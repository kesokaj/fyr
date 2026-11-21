package graph

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sampleMaliciousInput() Input {
	xssPkg := `pkg/</script><img src=x onerror=alert(1)>"q\s` + "\n" + `p`
	xssFile := xssPkg + `/a"b\c` + "\n" + `d.go`
	xssSym := `Run</script><!-- -->"quote\slash` + "\r\n\t"
	xssRec := `BUG-999-</script><svg onload=alert(2)>`

	return Input{
		RepoName: `repo-</script><img src=x onerror=alert(1)>"quote"`,
		Packages: []PackageInput{
			{Path: xssPkg, Layer: "customlayer", FileCount: 2, SymbolCount: 2, LineCount: 120, PageRank: 0.65, Community: 0, GodNode: true},
			{Path: "internal/core", Layer: "internal", FileCount: 1, SymbolCount: 1, LineCount: 80, PageRank: 0.35, Community: 1},
		},
		Files: []FileInput{
			{Path: xssFile, Pkg: xssPkg, PkgIdx: 0, Layer: "customlayer", SymbolCount: 1, LineCount: 70, PageRank: 0.45, Community: 0, ClampHits: 3, RepeatReads: 4, ChurnScore: 7},
			{Path: "internal/core/engine.go", Pkg: "internal/core", PkgIdx: 1, Layer: "internal", SymbolCount: 1, LineCount: 80, PageRank: 0.35, Community: 1, ClampHits: 1, RepeatReads: 0, ChurnScore: 1},
			{Path: xssPkg + "/peer.go", Pkg: xssPkg, PkgIdx: 0, Layer: "customlayer", SymbolCount: 1, LineCount: 50, PageRank: 0.20, Community: 0},
		},
		Symbols: []SymbolInput{
			{Name: xssSym, QualName: xssPkg + "." + xssSym, Kind: "func", Pkg: xssPkg, PkgIdx: 0, File: xssFile, FileIdx: 0, StartLine: 10, EndLine: 25, Sig: "func() // __GRAPH_JS__", Exported: true, PageRank: 0.55, Community: 0, GodNode: true},
			{Name: "Serve", QualName: "internal/core.Serve", Kind: "func", Pkg: "internal/core", PkgIdx: 1, File: "internal/core/engine.go", FileIdx: 1, StartLine: 5, EndLine: 30, Sig: "func Serve()", Exported: true, PageRank: 0.45, Community: 1},
		},
		PkgEdges:  []EdgeInput{{0, 1, 3, 1, KindCall | KindImport}, {1, 0, 2, 0, KindCall}},
		FileEdges: []EdgeInput{{0, 1, 3, 1, KindCall}},
		SymEdges:  []EdgeInput{{0, 1, 3, 0, KindCall}},
		PkgCycles: [][]int{{1, 0}},
		HiddenCoupling: []HiddenCouplingInput{
			{FileAIdx: 2, FileBIdx: 1, Count: 6, Kind: "co_modified"},
		},
		FileLinks: map[string]RecordLinks{
			xssFile: {
				Bugs:     1,
				ADRs:     1,
				Memories: 1,
				IDs:      []string{xssRec, "ADR-001", "MEMORY-note"},
			},
			xssPkg + "/peer.go": {
				Bugs: 1,
				IDs:  []string{xssRec, "ADR-002"},
			},
		},
	}
}

func TestGraphEscapingXSS(t *testing.T) {
	payload := Build(sampleMaliciousInput())

	// Verify package brainLinks aggregation deduplicated shared BUG IDs and sorted all IDs.
	wantPkgLinks := RecordLinks{
		Bugs:     1,
		ADRs:     2,
		Memories: 1,
		IDs:      []string{"ADR-001", "ADR-002", "BUG-999-</script><svg onload=alert(2)>", "MEMORY-note"},
	}
	if !reflect.DeepEqual(payload.Packages[0].BrainLinks, wantPkgLinks) {
		t.Fatalf("pkg[0] BrainLinks = %+v, want %+v", payload.Packages[0].BrainLinks, wantPkgLinks)
	}
	// Verify dynamic FNV-1a layer color assignment for arbitrary top-level dir.
	if got := payload.Meta.LayerColors["customlayer"]; !strings.HasPrefix(got, "#") || len(got) != 7 {
		t.Fatalf("customlayer color = %q, want #RRGGBB from palette", got)
	}

	var htmlBuf bytes.Buffer
	if err := RenderHTML(&htmlBuf, payload); err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	htmlStr := htmlBuf.String()

	// Must have exactly 2 </script> tags (closing #graphData and inline script), zero injected ones.
	if n := strings.Count(strings.ToLower(htmlStr), "</script"); n != 2 {
		t.Fatalf("HTML contains %d </script occurrences, want exactly 2", n)
	}
	if strings.Contains(htmlStr, "<img src=x onerror=alert(1)>") || strings.Contains(htmlStr, "<svg onload=alert(2)>") {
		t.Fatal("raw unescaped HTML tag survived in rendered HTML")
	}
	if !strings.Contains(htmlStr, DefaultHTMLCSP) {
		t.Fatal("rendered HTML missing DefaultHTMLCSP meta tag")
	}
	for _, cdn := range []string{"http://", "https://"} {
		if strings.Contains(htmlStr, cdn) {
			t.Fatalf("rendered HTML contains external URL prefix %q", cdn)
		}
	}

	// Extract #graphData JSON from HTML and verify lossless round-trip.
	const openTag = `<script id="graphData" type="application/json">`
	_, after, ok := strings.Cut(htmlStr, openTag)
	if !ok {
		t.Fatal("missing #graphData script element")
	}
	embeddedJSON, _, ok := strings.Cut(after, "</script>")
	if !ok {
		t.Fatal("unterminated #graphData script element")
	}
	var roundTrip Payload
	if err := json.Unmarshal([]byte(embeddedJSON), &roundTrip); err != nil {
		t.Fatalf("embedded JSON failed Unmarshal: %v", err)
	}
	if roundTrip.Symbols[0].Sig != "func() // __GRAPH_JS__" {
		t.Fatalf("placeholder cross-injection corrupted sig: got %q", roundTrip.Symbols[0].Sig)
	}
	if len(roundTrip.HiddenCoupling) != 1 || roundTrip.HiddenCoupling[0] != (HiddenCouplingTuple{U: 1, V: 2, Count: 6, Kind: "co_modified"}) {
		t.Fatalf("hiddenCoupling round-trip = %+v", roundTrip.HiddenCoupling)
	}

	// Verify RenderJSON round-trip and compact [u, v, count, kind] wire format.
	var jsonBuf bytes.Buffer
	if err := RenderJSON(&jsonBuf, payload); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	if !strings.Contains(jsonBuf.String(), `[1,2,6,"co_modified"]`) {
		t.Errorf("RenderJSON hiddenCoupling not encoded as 4-tuple: %s", jsonBuf.String())
	}

	// Strict zero-XSS and <= 400 LOC check across EVERY assets/*.js file.
	assetEntries, err := os.ReadDir("assets")
	if err != nil {
		t.Fatalf("read assets dir: %v", err)
	}
	jsCount := 0
	for _, ae := range assetEntries {
		if !strings.HasSuffix(ae.Name(), ".js") {
			continue
		}
		jsCount++
		p := filepath.Join("assets", ae.Name())
		jsBytes, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Fatalf("read %s: %v", p, rerr)
		}
		jsSrc := string(jsBytes)
		lines := strings.Count(strings.TrimSuffix(jsSrc, "\n"), "\n") + 1
		if lines > 400 {
			t.Errorf("%s is %d lines, want <= 400 LOC", p, lines)
		}
		for _, banned := range []string{
			"innerHTML",
			"outerHTML",
			"insertAdjacentHTML",
			"document.write",
			"eval(",
			"new Function",
		} {
			if strings.Contains(jsSrc, banned) {
				t.Errorf("%s contains forbidden DOM/XSS sink %q", p, banned)
			}
		}
	}
	if jsCount == 0 {
		t.Fatal("no assets/*.js files found")
	}
}

func TestGraphDOTAndMarkdown(t *testing.T) {
	p := Build(sampleMaliciousInput())

	var dot1, dot2 bytes.Buffer
	if err := RenderDOT(&dot1, p); err != nil {
		t.Fatalf("RenderDOT 1: %v", err)
	}
	if err := RenderDOT(&dot2, p); err != nil {
		t.Fatalf("RenderDOT 2: %v", err)
	}
	if dot1.String() != dot2.String() {
		t.Fatal("RenderDOT is not deterministic")
	}
	dotStr := dot1.String()
	if !strings.HasPrefix(dotStr, "digraph ") || !strings.HasSuffix(dotStr, "}\n") {
		t.Fatalf("invalid DOT framing:\n%s", dotStr)
	}
	if !strings.Contains(dotStr, `a\"b\\c\nd.go`) && !strings.Contains(dotStr, `\"quote\"`) {
		t.Errorf("DOT output did not escape quotes/backslashes/newlines:\n%s", dotStr)
	}
	// Every line inside subgraph / node declarations must have balanced unescaped quotes.
	for i, line := range strings.Split(strings.TrimSpace(dotStr), "\n") {
		unescaped := 0
		for j := 0; j < len(line); j++ {
			if line[j] == '\\' {
				j++
				continue
			}
			if line[j] == '"' {
				unescaped++
			}
		}
		if unescaped%2 != 0 {
			t.Errorf("DOT line %d has unbalanced unescaped quotes (%d): %s", i+1, unescaped, line)
		}
	}

	var md1, md2 bytes.Buffer
	if err := RenderMarkdown(&md1, p); err != nil {
		t.Fatalf("RenderMarkdown 1: %v", err)
	}
	if err := RenderMarkdown(&md2, p); err != nil {
		t.Fatalf("RenderMarkdown 2: %v", err)
	}
	if md1.String() != md2.String() {
		t.Fatal("RenderMarkdown is not deterministic")
	}
	for _, want := range []string{
		"# Architecture Graph",
		"## Summary",
		"## Top 10 God Packages",
		"## Top 10 God Symbols",
		"## Package Cycles",
		"## Hidden Co-Activity Coupling",
		"| Hidden Co-Edit Pairs | 1 | 1 |",
		"| Hidden Co-Read Pairs | 0 | 0 |",
		"## High-Churn Files",
		"## Brain-Linked Hotspots",
		"co_modified",
		"ADR-001",
	} {
		if !strings.Contains(md1.String(), want) {
			t.Errorf("Markdown summary missing %q:\n%s", want, md1.String())
		}
	}
}

func TestGraphHiddenCouplingMetaCounts(t *testing.T) {
	p := Build(Input{
		RepoName: "split-hc",
		Files: []FileInput{
			{Path: "a.go", Pkg: "a", PkgIdx: 0},
			{Path: "b.go", Pkg: "b", PkgIdx: 0},
			{Path: "c.go", Pkg: "c", PkgIdx: 0},
		},
		Packages: []PackageInput{{Path: "a", FileCount: 3}},
		HiddenCoupling: []HiddenCouplingInput{
			{FileAIdx: 0, FileBIdx: 1, Count: 4, Kind: "co_modified"},
			{FileAIdx: 0, FileBIdx: 1, Count: 7, Kind: "co_read"},
			{FileAIdx: 1, FileBIdx: 2, Count: 5, Kind: "co_read"},
		},
	})
	if p.Meta.HiddenCouplingEditPairs != 1 || p.Meta.HiddenCouplingReadPairs != 2 {
		t.Fatalf("Meta coupling counts = edit:%d read:%d, want 1 and 2", p.Meta.HiddenCouplingEditPairs, p.Meta.HiddenCouplingReadPairs)
	}
	var mdBuf, htmlBuf bytes.Buffer
	if err := RenderMarkdown(&mdBuf, p); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| Hidden Co-Activity Pairs | 3 | 3 |", "| Hidden Co-Edit Pairs | 1 | 1 |", "| Hidden Co-Read Pairs | 2 | 2 |"} {
		if !strings.Contains(mdBuf.String(), want) {
			t.Fatalf("Markdown missing %q:\n%s", want, mdBuf.String())
		}
	}
	if err := RenderHTML(&htmlBuf, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(htmlBuf.String(), `"hiddenCouplingEditPairs":1,"hiddenCouplingReadPairs":2`) {
		t.Fatalf("HTML payload missing split hidden coupling metadata")
	}
}

func TestGraphWriteFile0600(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "sub", "graph.html")

	if err := WriteFile(outPath, []byte("first"), false); err != nil {
		t.Fatalf("initial WriteFile: %v", err)
	}
	fi, err := os.Lstat(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %04o, want 0600", perm)
	}

	// Refuse overwrite without --force.
	err = WriteFile(outPath, []byte("second"), false)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("overwrite without force: got %v, want refusing to overwrite (... --force)", err)
	}
	got, err := os.ReadFile(outPath)
	if err != nil || string(got) != "first" {
		t.Fatalf("content after refused overwrite = %q, want %q", got, "first")
	}

	// Loosen mode to 0644, then overwrite with --force and verify content + 0600 restored.
	if err := os.Chmod(outPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(outPath, []byte("forced-second"), true); err != nil {
		t.Fatalf("WriteFile with force: %v", err)
	}
	got, err = os.ReadFile(outPath)
	if err != nil || string(got) != "forced-second" {
		t.Fatalf("content after forced overwrite = %q, want %q", got, "forced-second")
	}
	fi, err = os.Lstat(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode after forced overwrite = %04o, want 0600", perm)
	}

	// Overwrite with shorter content via --force; verifies Truncate(0) after SameFile leaves zero trailing bytes.
	if err := WriteFile(outPath, []byte("tiny"), true); err != nil {
		t.Fatalf("WriteFile shorter forced overwrite: %v", err)
	}
	got, err = os.ReadFile(outPath)
	if err != nil || string(got) != "tiny" {
		t.Fatalf("content after shorter forced overwrite = %q, want %q", got, "tiny")
	}

	// Refuse symlink target both without and with --force.
	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep-me"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(dir, "sym.html")
	if err := os.Symlink(victim, linkPath); err != nil {
		t.Fatal(err)
	}
	for _, force := range []bool{false, true} {
		if err := WriteFile(linkPath, []byte("clobber"), force); err == nil {
			t.Fatalf("WriteFile on symlink (force=%v) succeeded, want error", force)
		}
	}
	victimBytes, err := os.ReadFile(victim)
	if err != nil || string(victimBytes) != "keep-me" {
		t.Fatalf("symlink victim modified: %q", victimBytes)
	}

	// Refuse symlinked intermediate parent directory when creating new nested target.
	symDir := filepath.Join(dir, "symdir")
	if err := os.Symlink(t.TempDir(), symDir); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(filepath.Join(symDir, "nested", "out.html"), []byte("x"), true); err == nil {
		t.Fatal("WriteFile through symlinked intermediate parent succeeded, want error")
	}

	// Fresh write with force=true succeeds via O_EXCL.
	freshForce := filepath.Join(dir, "sub", "fresh-force.html")
	if err := WriteFile(freshForce, []byte("fresh"), true); err != nil {
		t.Fatalf("WriteFile fresh with force=true: %v", err)
	}
}

func TestGraphPackageDeps(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			first, _, _ := strings.Cut(p, "/")
			if p == "database/sql" || p == "modernc.org/sqlite" || strings.Contains(first, ".") {
				t.Errorf("%s imports forbidden package %q", name, p)
			}
		}
	}

	cmd := exec.Command("go", "list", "-deps", ".")
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if pkg == "github.com/kesokaj/fyr/internal/graph" {
			continue
		}
		first, _, _ := strings.Cut(pkg, "/")
		if pkg == "database/sql" || pkg == "modernc.org/sqlite" || strings.Contains(first, ".") {
			t.Errorf("internal/graph transitively depends on forbidden package %q", pkg)
		}
	}
}
