package search

import (
	"fmt"
	"strings"
	"testing"
)

func TestSplitIdentTokens(t *testing.T) {
	cases := []struct {
		name              string
		path, head, body  string
		wantSub, wantSkip []string
	}{
		{
			name:     "camelCase and acronyms and snake_case",
			path:     "internal/harness/size_test.go",
			head:     "HTTPServer setup",
			body:     "var knownTools = []string{}\nfunc RedactJSONL() {}",
			wantSub:  []string{"internal", "harness", "size", "test", "go", "http", "server", "known", "tools", "redact", "jsonl"},
			wantSkip: []string{"setup", "func", "var", "string"},
		},
		{
			name:     "deduplicates and skips 1-char noise",
			path:     "a/b/knownTools.go",
			head:     "knownTools",
			body:     "x_id knownTools sName",
			wantSub:  []string{"known", "tools", "go", "id", "name"},
			wantSkip: []string{"a", "b", "x", "s"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitIdentTokens(tc.path, tc.head, tc.body)
			toks := map[string]bool{}
			for _, f := range strings.Fields(got) {
				toks[f] = true
			}
			for _, w := range tc.wantSub {
				if !toks[w] {
					t.Errorf("missing token %q in %q", w, got)
				}
			}
			for _, skip := range tc.wantSkip {
				if toks[skip] {
					t.Errorf("unexpected token %q in %q", skip, got)
				}
			}
		})
	}
}

func TestSearchCamelCaseTokensAndMatchOffsetFallback(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "internal/harness/inventory.go", strings.Join([]string{
		"package harness",
		"// inventory header",
		"func checkTools() {}",
		"var knownTools = []string{\"view_file\", \"grep_search\"}",
		"var toolNames = knownTools",
	}, "\n"))
	writeFile(t, root, "other.md", "# Other\n\nNothing relevant here.\n")
	commitAll(t, root, "add camelCase inventory")

	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"tool", "names"}})
	if !strings.Contains(out, "internal/harness/inventory.go:") {
		t.Fatalf("expected inventory.go hit for \"tool names\":\n%s", out)
	}

	// Direct tokens-only match (no literal word in body, so snippet markers are absent).
	writeFile(t, root, "camel.go", "package camel\n\n// comment\nvar RedactJSONL = true\n")
	commitAll(t, root, "add RedactJSONL")
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"jsonl"}})
	if !strings.Contains(out, "camel.go:4  code") {
		t.Fatalf("expected camel.go:4 via matchOffset fallback when matched via tokens:\n%s", out)
	}
}

func TestDistinctTermFileRerankingAcrossChunks(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()

	// File A has 3 distinct terms spread > 40 lines apart (different chunks).
	var spread strings.Builder
	spread.WriteString("package alpha\n// nebula here\n")
	for i := 0; i < 45; i++ {
		fmt.Fprintf(&spread, "// spacer line %d\n", i)
	}
	spread.WriteString("func pulsar() {}\n")
	for i := 0; i < 45; i++ {
		fmt.Fprintf(&spread, "// trailing spacer %d\n", i)
	}
	spread.WriteString("func quasar() {}\n")
	writeFile(t, root, "spread.go", spread.String())

	// File B repeats only one term many times across multiple chunks.
	var single strings.Builder
	for i := 0; i < 3; i++ {
		single.WriteString("nebula nebula nebula nebula\n")
		for j := 0; j < 42; j++ {
			single.WriteString("filler line\n")
		}
	}
	writeFile(t, root, "single.md", single.String())
	commitAll(t, root, "add spread vs single")

	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"nebula", "pulsar", "quasar"}})
	idxSpread := strings.Index(out, "spread.go:")
	idxSingle := strings.Index(out, "single.md:")
	if idxSpread < 0 || idxSingle < 0 || idxSpread > idxSingle {
		t.Fatalf("expected spread.go (3 distinct terms across chunks) before single.md (1 term):\n%s", out)
	}
	if n := strings.Count(out, "spread.go:"); n > perFileCap {
		t.Errorf("spread.go appeared %d times, want <= %d:\n%s", n, perFileCap, out)
	}
}

func TestStopWordFilteringAndFallback(t *testing.T) {
	got := filterQueryWords([]string{"where", "are", "the", "x", "knownTools", "and"})
	if len(got) != 1 || got[0] != "knownTools" {
		t.Fatalf("filterQueryWords = %v, want [knownTools]", got)
	}
	fallback := filterQueryWords([]string{"the", "and", "of"})
	if len(fallback) != 3 || fallback[0] != "the" {
		t.Fatalf("all-stop-word fallback = %v, want original words", fallback)
	}

	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "doc.md", "# Doc\n\nOnly a lynx lives here.\n")
	writeFile(t, root, "noise.md", "# Noise\n\nWhere are the things.\n")
	commitAll(t, root, "add docs")

	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"where", "are", "the", "lynx"}})
	if !strings.Contains(out, ", 1 with every word") || !strings.Contains(out, "doc.md:3") {
		t.Fatalf("stop-word filtering should leave lynx as every-word hit:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"where", "the"}})
	if !strings.Contains(out, "noise.md") {
		t.Fatalf("all-stop-word query should fall back to original words:\n%s", out)
	}
}

func TestBatchedSnippetsAndSchemaV9RefsOID(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	for i := 1; i <= 5; i++ {
		writeFile(t, root, fmt.Sprintf("f%d.md", i), fmt.Sprintf("# Heading %d\n\nline two\nline three with narwhal%d token\n", i, i))
	}
	writeFile(t, root, "multi.md", "# Multi\n\nline two\ncaracal token here\n")
	commitAll(t, root, "add multi files")

	// Add second file matching caracal on line 1, third on line 5 to test batched snippet order & offsets.
	writeFile(t, root, "a_caracal.md", "caracal first line\n")
	writeFile(t, root, "z_caracal.md", "one\ntwo\nthree\nfour\ncaracal fifth line\n")
	commitAll(t, root, "add caracal files")

	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"caracal"}})
	for _, want := range []string{"a_caracal.md:1  doc", "multi.md:4  doc", "z_caracal.md:5  doc"} {
		if !strings.Contains(out, want) {
			t.Errorf("batched snippet missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "»caracal«") {
		t.Errorf("batched snippet missing highlight marks:\n%s", out)
	}

	db := openDB(t, root)
	var schema string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&schema); err != nil || schema != "9" {
		t.Fatalf("schema = %q, err = %v; want 9", schema, err)
	}
	if n := count(t, db, `SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'refs_oid'`); n != 1 {
		t.Fatalf("refs_oid count = %d, want 1", n)
	}
}

func TestPrepareQueryTermsPreservesCompositeIdentStopTokens(t *testing.T) {
	got := prepareQueryTerms([]string{"no_hooks", "to_pkg"})
	want := []string{"no", "hooks", "to", "pkg"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("prepareQueryTerms = %v, want %v", got, want)
	}

	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "hooks.go", "package h\n// hooks file without flag\nvar hooks = true\n")
	writeFile(t, root, "switch.go", "package h\nvar no_hooks = true\n")
	commitAll(t, root, "add composite ident")

	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"no_hooks"}})
	iSwitch := strings.Index(out, "switch.go:")
	iHooks := strings.Index(out, "hooks.go:")
	if !strings.Contains(out, ", 1 with every word") || iSwitch < 0 || (iHooks >= 0 && iSwitch > iHooks) {
		t.Fatalf("expected switch.go first as sole every-word hit for no_hooks:\n%s", out)
	}
}

func TestDistinctTermKindFilterBeyond400OtherChunks(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	for i := 0; i < 420; i++ {
		writeFile(t, root, fmt.Sprintf("docs/d%03d.md", i), "lynx lynx lynx\n")
	}
	writeFile(t, root, "src/code.go", "package src\n// lynx panther\nfunc f() {}\n")
	commitAll(t, root, "add 420 docs and 1 code file")

	out := mustSearch(t, Options{Dir: root, Home: home, Kind: "code", Words: []string{"lynx", "missing"}})
	if !strings.Contains(out, "src/code.go:") {
		t.Fatalf("expected src/code.go under --kind code despite >400 higher-BM25 docs:\n%s", out)
	}
}
