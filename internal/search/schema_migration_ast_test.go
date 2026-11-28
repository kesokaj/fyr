package search

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedLegacyV9SearchDB(t *testing.T, dbPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatalf("mkdir scratch: %v", err)
	}
	if err := prepareFiles(dbPath); err != nil {
		t.Fatalf("prepareFiles: %v", err)
	}
	db, err := sql.Open("sqlite", dsn(dbPath))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	v9Schema := strings.Replace(schemaSQL, ", line_count INT DEFAULT 0", "", 1)
	v9Schema = strings.Replace(v9Schema, ", duration_ms INT DEFAULT 0, timed_out INT DEFAULT 0, injected_bytes INT DEFAULT 0", "", 1)
	v9Schema = strings.Replace(v9Schema, ", last_invocation_num INT DEFAULT 0, last_checkpoint_offset INT DEFAULT 0", "", 1)
	if _, err := db.Exec(v9Schema); err != nil {
		t.Fatalf("exec v9 schema: %v", err)
	}
	stmts := []string{
		`INSERT INTO meta(key, value) VALUES ('schema', '9'), ('touched:/proj', '100')`,
		`INSERT INTO projects(root, name, last_seen) VALUES ('/proj', 'proj', 100)`,
		`INSERT INTO blobs(oid, last_seen, skipped) VALUES ('oid1', 100, 0)`,
		`INSERT INTO files(project, path, oid, state) VALUES ('/proj', 'a.go', 'oid1', 'tracked')`,
		`INSERT INTO chunks(oid, line, heading, body, tokens) VALUES ('oid1', 1, '', 'bodyword', 'bodyword')`,
		`INSERT INTO refs(oid, line, target) VALUES ('oid1', 1, 'ADR-001')`,
		`INSERT INTO symbols(oid, name, recv, kind, pkg, start_line, end_line, sig, exported, body_hash) VALUES ('oid1', 'F', '', 'func', 'main', 1, 2, 'func F()', 1, 'h1')`,
		`INSERT INTO sym_refs(oid, from_sym, to_pkg, to_name, kind, confidence, weight, line) VALUES ('oid1', 'F', '', 'G', 'call', 'EXTRACTED', 1.0, 1)`,
		`INSERT INTO co_activity(file_a, file_b, kind, count, updated_at) VALUES ('a.go', 'b.go', 'co_modified', 4, 100)`,
		`INSERT INTO read_ledger(conversation_id, generation_id, resolved_path, start_line, end_line, sha256, turn_num, hit_count, updated_at) VALUES ('c1', 0, '/proj/a.go', -5, -5, 'sha1', 1, 2, 100)`,
		`INSERT INTO read_ledger_stats(conversation_id, turn_num, saved_count, updated_at) VALUES ('c1', 1, 3, 100)`,
		`INSERT INTO session_meta(conversation_id, generation_id, last_transcript_bytes, turn_count, est_tokens) VALUES ('c1', 1, 500, 2, 1200)`,
		`INSERT INTO vet_history(conversation_id, turn_num, intent, gate, score, raw_prompt, improved_prompt, present_anchors, missing_anchors, suggested_skills, pivot_files, created_at) VALUES ('c1', 1, 'TASK', 'PASS_THROUGH', 80, 'p', 'p', '[]', '[]', '[]', '[]', 100)`,
		`INSERT INTO watchdog_ring(conversation_id, tool, arg_hash, is_error, ts) VALUES ('c1', 'read_file', 'h', 0, 100)`,
	}
	for _, st := range stmts {
		if _, err := db.Exec(st); err != nil {
			t.Fatalf("seed v9 (%s): %v", st, err)
		}
	}
}

func TestSchemaV10InPlaceUpgrade(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "pkg/svc.go", "package pkg\n\nfunc Run() error {\n\treturn nil\n}\n")
	commitAll(t, root, "seed svc")

	dbPath := projectDB(root)
	seedLegacyV9SearchDB(t, dbPath)

	var out bytes.Buffer
	if err := Search(Options{Dir: root, Home: home, Words: []string{"Run"}}, &out); err != nil {
		t.Fatalf("Search on v9 DB: %v", err)
	}
	if strings.Contains(out.String(), "rebuilt") {
		t.Fatalf("v9 -> v10 must migrate in place without rebuilding DB:\n%s", out.String())
	}
	db := openDB(t, root)
	var schema string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&schema); err != nil || schema != "12" {
		t.Fatalf("schema = %q, err = %v; want 12", schema, err)
	}
	for _, tbl := range []string{"projects", "co_activity", "read_ledger", "read_ledger_stats", "session_meta", "vet_history", "watchdog_ring"} {
		if n := count(t, db, `SELECT count(*) FROM `+tbl); n != 1 {
			t.Errorf("preserved table %s row count = %d, want 1", tbl, n)
		}
	}
	if n := count(t, db, `SELECT count(*) FROM chunks_fts WHERE chunks_fts MATCH 'bodyword'`); n != 0 {
		t.Errorf("stale v9 chunks_fts rows survived migration: %d", n)
	}
	if _, err := db.Exec(`INSERT INTO chunks_fts(chunks_fts) VALUES ('integrity-check')`); err != nil {
		t.Errorf("chunks_fts integrity-check after v9->v12 migration: %v", err)
	}
	if lc := count(t, db, `SELECT line_count FROM blobs b JOIN files f ON f.oid = b.oid WHERE f.path = 'pkg/svc.go'`); lc != 5 {
		t.Errorf("blobs.line_count for pkg/svc.go = %d, want 5", lc)
	}
	assertSchemaV10ToV11PreservesIndexes(t)
	assertSchemaBelowV9Rebuilds(t)
}

func assertSchemaV10ToV11PreservesIndexes(t *testing.T) {
	t.Helper()
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "pkg/v10.go", "package pkg\n\nfunc KeepV10() int { return 42 }\n")
	commitAll(t, root, "seed v10")
	dbPath := projectDB(root)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := prepareFiles(dbPath); err != nil {
		t.Fatal(err)
	}
	rawDB, err := sql.Open("sqlite", dsn(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	v10Schema := strings.Replace(schemaSQL, ", duration_ms INT DEFAULT 0, timed_out INT DEFAULT 0, injected_bytes INT DEFAULT 0", "", 1)
	v10Schema = strings.Replace(v10Schema, ", last_invocation_num INT DEFAULT 0, last_checkpoint_offset INT DEFAULT 0", "", 1)
	if _, err := rawDB.Exec(v10Schema); err != nil {
		t.Fatalf("exec v10 schema: %v", err)
	}
	for _, st := range []string{
		`INSERT INTO meta(key, value) VALUES ('schema', '10')`,
		`INSERT INTO blobs(oid, last_seen, skipped, line_count) VALUES ('v10oid', 200, 0, 3)`,
		`INSERT INTO chunks(oid, line, heading, body, tokens) VALUES ('v10oid', 1, '', 'v10preserved', 'v10preserved')`,
		`INSERT INTO vet_history(conversation_id, turn_num, intent, gate, score, raw_prompt, improved_prompt, present_anchors, missing_anchors, suggested_skills, pivot_files, subtask_count, directive, created_at) VALUES ('cv10', 1, 'TASK', 'ENRICH_AND_PROCEED', 75, 'fix v10', '', 'target', '', '', 'pkg/v10.go', 0, 'ENRICH_AND_PROCEED', 200)`,
	} {
		if _, err := rawDB.Exec(st); err != nil {
			t.Fatalf("seed v10: %v", err)
		}
	}
	_ = rawDB.Close()

	st, err := openStore(dbPath, io.Discard)
	if err != nil {
		t.Fatalf("openStore v10->v12: %v", err)
	}
	defer st.close()
	var schema string
	if err := st.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&schema); err != nil || schema != "12" {
		t.Fatalf("v10->v12 schema = %q, err=%v; want 12", schema, err)
	}
	if n := count(t, st.db, `SELECT count(*) FROM chunks_fts WHERE chunks_fts MATCH 'v10preserved'`); n != 1 {
		t.Errorf("v10->v12 wiped existing v10 chunks_fts index: got %d, want 1", n)
	}
	rows, err := st.listConversationVetHistory("cv10", 5)
	if err != nil || len(rows) != 1 || rows[0].DurationMS != 0 || rows[0].TimedOut || rows[0].InjectedBytes != 0 {
		t.Errorf("v10->v12 vet_history row = %+v, err=%v", rows, err)
	}
	_ = home
}

func TestPhase1SchemaV11ToV12Migration(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	dbPath := projectDB(root)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := prepareFiles(dbPath); err != nil {
		t.Fatal(err)
	}
	rawDB, err := sql.Open("sqlite", dsn(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	v11Schema := strings.Replace(schemaSQL, ", last_invocation_num INT DEFAULT 0, last_checkpoint_offset INT DEFAULT 0", "", 1)
	if _, err := rawDB.Exec(v11Schema); err != nil {
		t.Fatalf("exec v11 schema: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO meta(key, value) VALUES ('schema', '11')`,
		`INSERT INTO blobs(oid, last_seen, skipped, line_count) VALUES ('v11oid', 300, 0, 2)`,
		`INSERT INTO chunks(oid, line, heading, body, tokens) VALUES ('v11oid', 1, '', 'v11preserved', 'v11preserved')`,
		`INSERT INTO co_activity(file_a, file_b, kind, count, updated_at) VALUES ('a.go', 'b.go', 'co_read', 3, 300)`,
		`INSERT INTO read_ledger(conversation_id, generation_id, resolved_path, start_line, end_line, sha256, turn_num, hit_count, updated_at) VALUES ('c11', 0, '/p/a.go', 1, 10, 's', 1, 1, 300)`,
		`INSERT INTO read_ledger_stats(conversation_id, turn_num, saved_count, updated_at) VALUES ('c11', 1, 2, 300)`,
		`INSERT INTO session_meta(conversation_id, generation_id, last_transcript_bytes, turn_count, est_tokens) VALUES ('c11', 2, 8000, 4, 2000)`,
		`INSERT INTO vet_history(conversation_id, turn_num, intent, gate, score, raw_prompt, improved_prompt, present_anchors, missing_anchors, suggested_skills, pivot_files, created_at) VALUES ('c11', 1, 'BUG', 'PASS_THROUGH', 90, 'p', '', '', '', '', '', 300)`,
		`INSERT INTO watchdog_ring(conversation_id, tool, arg_hash, is_error, ts) VALUES ('c11', 'read_file', 'h', 0, 300)`,
	} {
		if _, err := rawDB.Exec(stmt); err != nil {
			t.Fatalf("seed v11 (%s): %v", stmt, err)
		}
	}
	_ = rawDB.Close()

	var note bytes.Buffer
	st, err := openStore(dbPath, &note)
	if err != nil {
		t.Fatalf("openStore v11->v12: %v", err)
	}
	defer st.close()
	if strings.Contains(note.String(), "rebuilt") {
		t.Fatalf("v11->v12 unexpectedly rebuilt DB: %s", note.String())
	}
	var schema string
	if err := st.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&schema); err != nil || schema != "12" {
		t.Fatalf("v11->v12 schema = %q, err=%v; want 12", schema, err)
	}
	for _, tbl := range []string{"co_activity", "read_ledger", "read_ledger_stats", "session_meta", "vet_history", "watchdog_ring"} {
		if n := count(t, st.db, `SELECT count(*) FROM `+tbl); n != 1 {
			t.Errorf("v11->v12 lost row in %s: got %d, want 1", tbl, n)
		}
	}
	if n := count(t, st.db, `SELECT count(*) FROM chunks_fts WHERE chunks_fts MATCH 'v11preserved'`); n != 1 {
		t.Errorf("v11->v12 lost chunks_fts entry: got %d, want 1", n)
	}
	var lastInv, lastCP int
	if err := st.db.QueryRow(`SELECT last_invocation_num, last_checkpoint_offset FROM session_meta WHERE conversation_id = 'c11'`).Scan(&lastInv, &lastCP); err != nil || lastInv != 0 || lastCP != 0 {
		t.Fatalf("v11->v12 new columns = (%d, %d), err=%v; want (0, 0)", lastInv, lastCP, err)
	}
}

func assertSchemaBelowV9Rebuilds(t *testing.T) {
	t.Helper()
	root := newRepo(t)
	home := t.TempDir()
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	db := openDB(t, root)
	if _, err := db.Exec(`INSERT INTO co_activity(file_a, file_b, kind, count, updated_at) VALUES ('x.go', 'y.go', 'co_read', 5, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE meta SET value = '8' WHERE key = 'schema'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	if !strings.Contains(out, "rebuilt") {
		t.Fatalf("schema 8 must trigger staleSchemaError rebuild, got:\n%s", out)
	}
	db2 := openDB(t, root)
	if n := count(t, db2, `SELECT count(*) FROM co_activity`); n != 0 {
		t.Fatalf("expected rebuilt v8 DB to wipe co_activity, got %d", n)
	}
}

func TestClampRowSurvivesEdit(t *testing.T) {
	t.Parallel()
	dir, home := setupHookRepo(t), t.TempDir()
	var buf strings.Builder
	buf.WriteString("package big\n")
	for i := 2; i <= 450; i++ {
		fmt.Fprintf(&buf, "func Fn%03d() int { return %d }\n", i, i)
	}
	writeFile(t, dir, "big.go", buf.String())

	callHook := func(tool string, args map[string]any) HookDecisionOutput {
		t.Helper()
		req, _ := json.Marshal(map[string]any{"conversationId": "conv-clamp-edit", "workspacePaths": []string{dir}, "toolCall": map[string]any{"name": tool, "args": args}})
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(req)}, &out); err != nil {
			t.Fatalf("pre-tool-use %s: %v", tool, err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return dec
	}
	if dec := callHook("read_file", map[string]any{"Path": "big.go"}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[CLAMPED]") {
		t.Fatalf("initial unranged read: got %+v, want [CLAMPED]", dec)
	}
	if dec := callHook("read_file", map[string]any{"Path": "big.go", "StartLine": 1, "EndLine": 30}); dec.Decision != "allow" {
		t.Fatalf("ranged read 1..30: got %+v", dec)
	}
	oldLine := "func Fn010() int { return 10 }"
	newLine := "func Fn010() int { return 1000 }"
	if dec := callHook("edit_file", map[string]any{"Path": "big.go", "Target": oldLine, "Replacement": newLine}); dec.Decision != "allow" {
		t.Fatalf("pre-edit check: got %+v", dec)
	}
	writeFile(t, dir, "big.go", strings.Replace(buf.String(), oldLine, newLine, 1))
	if dec := callHook("read_file", map[string]any{"Path": "big.go", "StartLine": 40, "EndLine": 80}); dec.Decision != "allow" {
		t.Fatalf("post-edit ranged read: got %+v", dec)
	}
	db := openDB(t, dir)
	if n := count(t, db, `SELECT count(*) FROM read_ledger WHERE conversation_id = 'conv-clamp-edit' AND start_line = -5 AND end_line = -5`); n != 1 {
		t.Fatalf("expected -5 clamp row to survive post-edit read, got count=%d", n)
	}
}

func TestCanonicalPackageNoMainCollision(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/multi\n\ngo 1.24\n")
	writeFile(t, root, "cmd/a/main.go", "package main\n\ntype Runner interface { Run() error }\ntype implA struct{}\nfunc (implA) Run() error { return helperA() }\nfunc helperA() error { return nil }\nfunc main() { _ = implA{}.Run() }\n")
	writeFile(t, root, "cmd/b/main.go", "package main\n\ntype implB struct{}\nfunc (implB) Run() error { return helperB() }\nfunc helperB() error { return nil }\nfunc main() { _ = implB{}.Run() }\n")
	commitAll(t, root, "two mains")

	var mapBuf bytes.Buffer
	if err := Search(Options{Dir: root, Home: home, Map: true}, &mapBuf); err != nil {
		t.Fatalf("Search --map: %v", err)
	}
	gotMap := mapBuf.String()
	if !strings.Contains(gotMap, "cmd/a (") || !strings.Contains(gotMap, "cmd/b (") {
		t.Fatalf("expected distinct canonical packages cmd/a and cmd/b in map:\n%s", gotMap)
	}
	if strings.Contains(gotMap, "cmd/a -> cmd/b") || strings.Contains(gotMap, "cmd/b -> cmd/a") {
		t.Fatalf("unexpected cross-main collision edge in map:\n%s", gotMap)
	}
	snap, err := LoadGraphSnapshot(Options{Dir: root, Home: home}, GraphOptions{})
	if err != nil {
		t.Fatalf("LoadGraphSnapshot: %v", err)
	}
	if len(snap.Packages) != 2 || snap.Packages[0].Path != "cmd/a" || snap.Packages[1].Path != "cmd/b" {
		t.Fatalf("snapshot packages = %+v, want [cmd/a cmd/b]", snap.Packages)
	}
}

func TestGoAliasAndNoPhantomEdges(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "go.mod", "module github.com/acme/proj\n\ngo 1.24\n")
	writeFile(t, root, "errors/local_err.go", "package errors\n\nfunc New(s string) error { return nil }\n")
	writeFile(t, root, "log/local_log.go", "package log\n\nfunc Println(v ...any) {}\n")
	writeFile(t, root, "internal/beta/beta.go", "package beta\n\nfunc Connect() error { return nil }\nfunc TargetAlias() error { return nil }\n")
	writeFile(t, root, "internal/unrelated/other.go", "package unrelated\n\nfunc Open() error { return nil }\n")
	writeFile(t, root, "internal/alpha/alpha.go", `package alpha

import (
	"errors"
	"log"
	b "github.com/acme/proj/internal/beta"
	ext "vendor.example.org/driver/sqlx"
)

func UseAlpha() error {
	log.Println("x")
	_ = errors.New("e")
	_ = ext.Open()
	return b.TargetAlias()
}
`)
	commitAll(t, root, "seed alias and external")

	sc, err := prepare(Options{Dir: root, Home: home}, io.Discard)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer sc.close()
	g, err := sc.loadSymGraphOpts(false)
	if err != nil {
		t.Fatalf("loadSymGraphOpts: %v", err)
	}
	_, deps := g.summarizePackages()
	if strings.Join(deps["internal/alpha"], ",") != "internal/beta" {
		t.Fatalf("internal/alpha deps = %v, want [internal/beta] (aliased import resolved, external ignored)", deps["internal/alpha"])
	}
	for _, idx := range g.byName["Open"] {
		if len(g.in[idx]) != 0 {
			t.Fatalf("phantom fallthrough edge attached to internal/unrelated.Open: %+v", g.in[idx])
		}
	}
	assertNoStdlibOrModlessTailPhantom(t)
}

func assertNoStdlibOrModlessTailPhantom(t *testing.T) {
	t.Helper()
	pkgSet := map[string]bool{"errors": true, "log": true, "crypto/sha256": true, "internal/beta": true, "react": true}
	mods := map[string]string{"github.com/acme/proj": "."}
	for _, std := range []string{"errors", "log", "crypto/sha256"} {
		if pkg, _, ok := resolveImportTarget("internal/alpha/a.go", std, mods, pkgSet, nil); ok || pkg != "" {
			t.Fatalf("stdlib %q resolved to local dir %q", std, pkg)
		}
	}
	if pkg, _, ok := resolveImportTarget("internal/alpha/a.go", "github.com/x/y/internal/beta", nil, pkgSet, nil); ok || pkg != "" {
		t.Fatalf("external module without go.mod bound to local dir %q", pkg)
	}
	if pkg, f, ok := resolveImportTarget("web/ui/view.tsx", "react", nil, pkgSet, map[string]string{"react": "react/index.ts"}); ok || pkg != "" || f != "" {
		t.Fatalf("bare TS package 'react' resolved to local dir (%q, %q)", pkg, f)
	}
}

func TestTSImportsAndConstSuppression(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "web/lib/util.ts", "export const PUBLIC_LIMIT = 10\nconst internalLimit = 5\nexport function formatItem(v: string): string { return v }\n")
	writeFile(t, root, "web/ui/view.tsx", "import { formatItem as fmt } from '../lib/util'\nimport { useState } from 'react'\n\nexport function renderCard(label: string) {\n  const localCount = useState(0)\n  const formatted = fmt(label)\n  return formatted\n}\n")
	writeFile(t, root, "react/index.ts", "export function useState(v: number) { return v }\n")
	commitAll(t, root, "seed ts")

	snap, err := LoadGraphSnapshot(Options{Dir: root, Home: home}, GraphOptions{})
	if err != nil {
		t.Fatalf("LoadGraphSnapshot: %v", err)
	}
	symNames := map[string]bool{}
	for _, s := range snap.Symbols {
		symNames[s.Pkg+":"+s.Name] = true
		if s.Name == "useState" && s.InRefs != 0 {
			t.Fatalf("external 'react' import leaked phantom edge to web/other.useState: %+v", s)
		}
	}
	if !symNames["web/lib:PUBLIC_LIMIT"] || symNames["web/lib:internalLimit"] || symNames["web/ui:localCount"] || symNames["web/ui:formatted"] {
		t.Fatalf("const suppression mismatch in symbols: %v", symNames)
	}
	if len(snap.PkgEdges) != 1 || snap.Packages[snap.PkgEdges[0][0]].Path != "web/ui" || snap.Packages[snap.PkgEdges[0][1]].Path != "web/lib" {
		t.Fatalf("expected single TS relative package edge web/ui -> web/lib, got pkgs=%+v edges=%+v", snap.Packages, snap.PkgEdges)
	}
}

func TestIncludeDocsExclusion(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "internal/core/svc.go", "package core\n\nfunc ExecuteCore() error { return nil }\n")
	writeFile(t, root, "docs/guide.md", "# Architecture Guide\n\nCalls `ExecuteCore()`.\n")
	writeFile(t, root, "vendor/ext/v.go", "package ext\n\nfunc VendoredFunc() {}\n")
	writeFile(t, root, "node_modules/pkg/index.js", "export function NodeModFunc() {}\n")
	writeFile(t, root, "internal/core/testdata/fix.go", "package testdata\n\nfunc FixtureFunc() {}\n")
	writeFile(t, root, "ignored/.fyr-noindex", "")
	writeFile(t, root, "ignored/skip.go", "package ignored\n\nfunc NoIndexFunc() {}\n")
	commitAll(t, root, "seed exclusions")

	snapDefault, err := LoadGraphSnapshot(Options{Dir: root, Home: home}, GraphOptions{})
	if err != nil {
		t.Fatalf("LoadGraphSnapshot default: %v", err)
	}
	for _, f := range snapDefault.Files {
		if f.Path != "internal/core/svc.go" {
			t.Fatalf("unexpected file in default snapshot: %+v", f)
		}
	}
	snapDocs, err := LoadGraphSnapshot(Options{Dir: root, Home: home, IncludeDocs: true}, GraphOptions{})
	if err != nil {
		t.Fatalf("LoadGraphSnapshot IncludeDocs: %v", err)
	}
	hasDoc := false
	for _, f := range snapDocs.Files {
		if f.Path == "docs/guide.md" && f.IsDoc {
			hasDoc = true
		}
		if strings.Contains(f.Path, "vendor/") || strings.Contains(f.Path, "node_modules/") || strings.Contains(f.Path, "testdata/") || strings.Contains(f.Path, "ignored/") {
			t.Fatalf("excluded path leaked under IncludeDocs: %s", f.Path)
		}
	}
	if !hasDoc {
		t.Fatalf("expected docs/guide.md when Options.IncludeDocs=true, got %+v", snapDocs.Files)
	}
	_ = filepath.Join(root, "docs")
}

func TestLoadGoModulesIgnoresExcludedAndSymlinks(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "go.mod"), []byte("module example.com/escaped\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "go.mod", "module example.com/real\n\ngo 1.24\n")
	writeFile(t, root, "testdata/fixture/go.mod", "module example.com/real\n\ngo 1.24\n")
	writeFile(t, root, "vendor/mod/go.mod", "module example.com/real\n\ngo 1.24\n")
	writeFile(t, root, "internal/a/a.go", "package a\nfunc A() {}\n")
	commitAll(t, root, "seed excluded go.mods")
	if err := Index(Options{Dir: root, Home: home}, io.Discard); err != nil {
		t.Fatal(err)
	}

	db := openDB(t, root)
	defer db.Close()
	mods := loadGoModulesFromDB(db, []string{root})
	if mods["example.com/real"] != "." {
		t.Fatalf("mods[example.com/real] = %q, want \".\" (fixture overwrote root)", mods["example.com/real"])
	}

	if err := os.Symlink(outside, filepath.Join(root, "sublink")); err != nil {
		t.Fatal(err)
	}
	symMods := map[string]string{}
	readGoModAt(root, "sublink/go.mod", symMods)
	if len(symMods) != 0 {
		t.Fatalf("readGoModAt traversed symlinked directory: %+v", symMods)
	}
}
