package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/graph"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/webpreview"
	_ "modernc.org/sqlite"
)

func addGraphFilesAndCommit(t *testing.T, root string, files map[string]string, msg string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", msg}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func graphGitRepo(t *testing.T) string {
	t.Helper()
	root := recordRepo(t)
	addGraphFilesAndCommit(t, root, map[string]string{
		".gitignore":                 ".agents/scratch/*\n",
		"go.mod":                     "module example.com/demo\n\ngo 1.24\n",
		"internal/core/core.go":      "package core\n\nfunc Compute() int { return 42 }\n",
		"internal/core/core_test.go": "package core\n\nimport \"testing\"\n\nfunc TestCompute(t *testing.T) { _ = Compute() }\n",
		"cmd/app/main.go":            "package main\n\nimport \"example.com/demo/internal/core\"\n\nfunc main() { _ = core.Compute() }\n",
		"ARCHITECTURE.md":            "# Architecture Overview\n\n## Core Pipeline\n\nCalls `internal/core/core.go`.\n",
	}, "seed repo")
	return root
}

func TestVerbGraph(t *testing.T) {
	repo := graphGitRepo(t)
	emptyHome := t.TempDir()

	t.Run("default_html_refuse_overwrite_and_force_and_missing_brain_db", func(t *testing.T) {
		var out bytes.Buffer
		if err := run([]string{"graph", "-C", repo, "--home", emptyHome}, &out); err != nil {
			t.Fatalf("fyr graph: %v\n%s", err, out.String())
		}
		defaultHTML := filepath.Join(repo, ".agents", "scratch", "graph.html")
		if !strings.Contains(out.String(), "graph: written to "+defaultHTML) {
			t.Errorf("unexpected output: %q", out.String())
		}
		fi, err := os.Lstat(defaultHTML)
		if err != nil {
			t.Fatalf("stat default graph.html: %v", err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("graph.html mode = %04o, want 0600", fi.Mode().Perm())
		}
		htmlBytes, err := os.ReadFile(defaultHTML)
		if err != nil {
			t.Fatal(err)
		}
		if redact.Redact(string(htmlBytes)) != string(htmlBytes) {
			t.Fatal("rendered graph.html triggered redact.Redact")
		}
		// Missing brain.db must not create brain.db under emptyHome.
		if _, berr := os.Stat(brain.Path(emptyHome)); !os.IsNotExist(berr) {
			t.Errorf("missing brain.db created unexpectedly: err=%v", berr)
		}

		// Second run without --force fails (exit 1) and refuses to overwrite.
		out.Reset()
		err = run([]string{"graph", "-C", repo, "--home", emptyHome}, &out)
		if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
			t.Fatalf("second run without --force: err=%v, want refusing to overwrite", err)
		}

		// With --force, overwrites cleanly.
		out.Reset()
		if err := run([]string{"graph", "-C", repo, "--home", emptyHome, "--force"}, &out); err != nil {
			t.Fatalf("fyr graph --force: %v\n%s", err, out.String())
		}
	})

	t.Run("dot_md_json_stdout_and_out_plus_brain_links", func(t *testing.T) {
		home := t.TempDir()
		var recOut bytes.Buffer
		if err := runWithStdin(strings.NewReader("Regression in internal/core/core.go.\n"), &recOut,
			"record", "add", "bug", "-C", repo, "--home", home, "--id", "BUG-007", "--title", "Fix core"); err != nil {
			t.Fatalf("record add bug: %v\n%s", err, recOut.String())
		}

		var dotOut bytes.Buffer
		if err := run([]string{"graph", "-C", repo, "--home", home, "--dot"}, &dotOut); err != nil {
			t.Fatalf("graph --dot: %v", err)
		}
		if !strings.HasPrefix(dotOut.String(), "digraph ") || !strings.Contains(dotOut.String(), "BUG-007") {
			t.Errorf("DOT stdout missing digraph or BUG-007:\n%s", dotOut.String())
		}

		var mdOut bytes.Buffer
		if err := run([]string{"graph", "-C", repo, "--home", home, "--md"}, &mdOut); err != nil {
			t.Fatalf("graph --md: %v", err)
		}
		if !strings.Contains(mdOut.String(), "# Architecture Graph") || !strings.Contains(mdOut.String(), "BUG-007") {
			t.Errorf("MD stdout missing header or BUG-007:\n%s", mdOut.String())
		}

		var jsonOut bytes.Buffer
		if err := run([]string{"graph", "-C", repo, "--home", home, "--json"}, &jsonOut); err != nil {
			t.Fatalf("graph --json: %v", err)
		}
		var payload graph.Payload
		if err := json.Unmarshal(jsonOut.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal graph --json: %v", err)
		}
		if payload.BrainLinks["internal/core/core.go"].Bugs != 1 {
			t.Errorf("core.go brainLinks = %+v, want 1 bug", payload.BrainLinks["internal/core/core.go"])
		}

		// --dot / --md / --json with --out writes 0600 file and refuses overwrite without --force.
		outDir := t.TempDir()
		for _, modeFlag := range []string{"--dot", "--md", "--json"} {
			target := filepath.Join(outDir, "out"+modeFlag)
			var wout bytes.Buffer
			if err := run([]string{"graph", "-C", repo, "--home", home, modeFlag, "--out", target}, &wout); err != nil {
				t.Fatalf("graph %s --out: %v", modeFlag, err)
			}
			if !strings.Contains(wout.String(), "graph: written to "+target) {
				t.Errorf("graph %s --out output = %q", modeFlag, wout.String())
			}
			if err := run([]string{"graph", "-C", repo, "--home", home, modeFlag, "--out", target}, &wout); err == nil {
				t.Errorf("graph %s --out without --force succeeded on existing file", modeFlag)
			}
			if err := run([]string{"graph", "-C", repo, "--home", home, modeFlag, "--out", target, "--force"}, &wout); err != nil {
				t.Errorf("graph %s --out --force failed: %v", modeFlag, err)
			}
		}
	})

	t.Run("include_tests_and_include_docs_on_graph_and_search_map", func(t *testing.T) {
		var prodJSON, testDocJSON bytes.Buffer
		if err := run([]string{"graph", "-C", repo, "--home", emptyHome, "--json"}, &prodJSON); err != nil {
			t.Fatalf("graph --json: %v", err)
		}
		if err := run([]string{"graph", "-C", repo, "--home", emptyHome, "--json", "--include-tests", "--include-docs"}, &testDocJSON); err != nil {
			t.Fatalf("graph --json --include-tests --include-docs: %v", err)
		}
		var pProd, pAll graph.Payload
		if err := json.Unmarshal(prodJSON.Bytes(), &pProd); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(testDocJSON.Bytes(), &pAll); err != nil {
			t.Fatal(err)
		}
		for _, f := range pProd.Files {
			if f.IsTest || f.IsDoc {
				t.Errorf("default graph included test/doc file: %+v", f)
			}
		}
		sawTest, sawDoc := false, false
		for _, f := range pAll.Files {
			sawTest = sawTest || f.IsTest
			sawDoc = sawDoc || f.IsDoc
		}
		if !sawTest || !sawDoc {
			t.Errorf("--include-tests --include-docs: sawTest=%v sawDoc=%v, want both true", sawTest, sawDoc)
		}

		// Verify `fyr search --map` vs `fyr search --map --include-docs`.
		var mapProd, mapDocs bytes.Buffer
		if err := run([]string{"search", "-C", repo, "--home", emptyHome, "--map"}, &mapProd); err != nil {
			t.Fatalf("search --map: %v", err)
		}
		if err := run([]string{"search", "-C", repo, "--home", emptyHome, "--map", "--include-docs"}, &mapDocs); err != nil {
			t.Fatalf("search --map --include-docs: %v", err)
		}
		if strings.Contains(mapProd.String(), "ARCHITECTURE.md") {
			t.Errorf("default search --map included ARCHITECTURE.md:\n%s", mapProd.String())
		}
		if !strings.Contains(mapDocs.String(), "ARCHITECTURE.md") {
			t.Errorf("search --map --include-docs omitted ARCHITECTURE.md:\n%s", mapDocs.String())
		}
	})

	t.Run("web_preview_assets_csp_and_listen", func(t *testing.T) {
		origServe := serveWebPreview
		t.Cleanup(func() { serveWebPreview = origServe })

		var gotAssets map[string]webpreview.Asset
		var gotOpts webpreview.Options
		serveWebPreview = func(_ context.Context, assets map[string]webpreview.Asset, opts webpreview.Options) error {
			gotAssets = assets
			gotOpts = opts
			return nil
		}

		var out bytes.Buffer
		if err := run([]string{"graph", "-C", repo, "--home", emptyHome, "--web", "--listen", "127.0.0.1:0"}, &out); err != nil {
			t.Fatalf("graph --web --listen: %v\n%s", err, out.String())
		}
		if gotOpts.Listen != "127.0.0.1:0" || gotOpts.DefaultCSP != graph.DefaultHTMLCSP {
			t.Errorf("webpreview opts = %+v", gotOpts)
		}
		for _, k := range []string{"/", "/graph.html"} {
			a, ok := gotAssets[k]
			if !ok || a.CSP != graph.DefaultHTMLCSP || len(a.Body) == 0 {
				t.Errorf("asset %q missing or wrong CSP: ok=%v csp=%q len=%d", k, ok, a.CSP, len(a.Body))
			}
		}
		for _, k := range []string{"/graph.json", "/graph.dot", "/graph.md"} {
			a, ok := gotAssets[k]
			if !ok || a.CSP != "default-src 'none'" || len(a.Body) == 0 {
				t.Errorf("asset %q missing or wrong CSP: ok=%v csp=%q len=%d", k, ok, a.CSP, len(a.Body))
			}
		}
	})

	t.Run("flag_validation_and_real_home_guard_exit_2", func(t *testing.T) {
		badArgs := [][]string{
			{"graph", "--dot", "--md"},
			{"graph", "--dot", "--json"},
			{"graph", "--md", "--json"},
			{"graph", "--web", "--dot"},
			{"graph", "--web", "--md"},
			{"graph", "--web", "--json"},
			{"graph", "--web", "--out", "x.html"},
			{"graph", "--web", "--force"},
			{"graph", "--listen", "127.0.0.1:0"},
			{"graph", "unexpected-arg"},
		}
		for _, args := range badArgs {
			var out bytes.Buffer
			err := run(append(args, "-C", repo, "--home", emptyHome), &out)
			if exitOf(err) != 2 {
				t.Errorf("%v: exit=%d (err=%v), want 2; out=%q", args, exitOf(err), err, out.String())
			}
		}

		// Real-home guard: dev binary without --home must exit 2 before opening anything.
		fakeRealHome := t.TempDir()
		t.Setenv("HOME", fakeRealHome)
		var guardOut bytes.Buffer
		err := run([]string{"graph", "-C", repo}, &guardOut)
		if exitOf(err) != 2 || !strings.Contains(guardOut.String(), "BUG-507") {
			t.Errorf("unflagged real-home dev run: exit=%d out=%q, want exit 2 naming BUG-507", exitOf(err), guardOut.String())
		}
	})
}

func runWithStdin(in *strings.Reader, out *bytes.Buffer, args ...string) error {
	prev := stdin
	stdin = in
	defer func() { stdin = prev }()
	return run(args, out)
}

func TestGraphIncludeVendored(t *testing.T) {
	repo := graphGitRepo(t)
	home := t.TempDir()
	addGraphFilesAndCommit(t, repo, map[string]string{
		"skills/demo/SKILL.md":             "# Demo Skill\n",
		"skills/demo/references/helper.go": "package references\n\nfunc HelperRef() int { return 7 }\n",
		"skills/demo/references/guide.md":  "# Ref Guide\n\nCalls HelperRef.\n",
	}, "add vendored skill refs")

	prod := runGraphJSON(t, repo, home)
	if hasGraphFile(prod, "skills/demo/references/helper.go") || hasGraphFile(prod, "skills/demo/references/guide.md") || hasGraphSymbol(prod, "HelperRef") {
		t.Fatalf("default graph included vendored refs: files=%d syms=%d", len(prod.Files), len(prod.Symbols))
	}
	withCode := runGraphJSON(t, repo, home, "--include-vendored")
	if !hasGraphFile(withCode, "skills/demo/references/helper.go") || !hasGraphSymbol(withCode, "HelperRef") {
		t.Fatalf("graph --include-vendored omitted helper.go or HelperRef")
	}
	if hasGraphFile(withCode, "skills/demo/references/guide.md") {
		t.Fatalf("graph --include-vendored without --include-docs included guide.md")
	}
	withDocs := runGraphJSON(t, repo, home, "--include-vendored", "--include-docs")
	if !hasGraphFile(withDocs, "skills/demo/references/guide.md") || !hasGraphSymbol(withDocs, "Ref Guide") {
		t.Fatalf("graph --include-vendored --include-docs omitted guide.md or Ref Guide")
	}
	assertZeroVendoredGraphDB(t, filepath.Join(repo, ".agents", "scratch", "search.db"))
}

func runGraphJSON(t *testing.T, repo, home string, extra ...string) graph.Payload {
	t.Helper()
	args := append([]string{"graph", "-C", repo, "--home", home, "--json"}, extra...)
	var out bytes.Buffer
	if err := run(args, &out); err != nil {
		t.Fatalf("run(%v): %v\n%s", args, err, out.String())
	}
	var payload graph.Payload
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal graph --json (%v): %v", extra, err)
	}
	return payload
}

func hasGraphFile(p graph.Payload, filePath string) bool {
	for _, f := range p.Files {
		if f.ID == filePath {
			return true
		}
	}
	return false
}

func hasGraphSymbol(p graph.Payload, name string) bool {
	for _, s := range p.Symbols {
		if s.Name == name {
			return true
		}
	}
	return false
}

func assertZeroVendoredGraphDB(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", search.DSN(dbPath))
	if err != nil {
		t.Fatalf("open search.db: %v", err)
	}
	defer db.Close()
	const where = `(f.path LIKE 'skills/%/references/%' OR f.path LIKE 'skills/%/assets/%')`
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM symbols sy JOIN files f ON f.oid = sy.oid WHERE ` + where).Scan(&n); err != nil {
		t.Fatalf("query vendored symbols: %v", err)
	}
	if n != 0 {
		t.Fatalf("vendored symbols in search.db = %d, want 0", n)
	}
	var refs int
	if err := db.QueryRow(`SELECT count(*) FROM sym_refs sr JOIN files f ON f.oid = sr.oid WHERE ` + where).Scan(&refs); err != nil {
		t.Fatalf("query vendored sym_refs: %v", err)
	}
	if refs != 0 {
		t.Fatalf("vendored sym_refs in search.db = %d, want 0", refs)
	}
}
