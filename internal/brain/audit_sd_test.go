package brain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSD106OwnHitsChecksRowsErr(t *testing.T) {
	t.Parallel()
	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	for _, stmt := range []string{
		`DROP TRIGGER records_ai`,
		`DROP TRIGGER records_ad`,
		`DROP TRIGGER records_au`,
		`DROP TABLE records`,
		`CREATE VIEW records AS
			SELECT 1 AS rid, '' AS scope, 'BUG-001' AS id, 'bug' AS kind, 'open' AS status, '2026-10-25' AS date, 'ok' AS title, '' AS project
			UNION ALL
			SELECT abs(-9223372036854775808), '' AS scope, 'BUG-001' AS id, 'bug' AS kind, 'open' AS status, '2026-10-25' AS date, 'boom' AS title, '' AS project`,
	} {
		if _, err := st.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	hits, seen, err := ownHits(st, Options{Global: true}, "", []string{""}, "BUG-001", "", 10)
	if err == nil || hits != nil || seen != nil {
		t.Fatalf("ownHits with failing second row = (%v, %v, %v), want error and nil results", hits, seen, err)
	}
}

func TestSD107MemoryShowIncludesGlobalInProject(t *testing.T) {
	t.Parallel()
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	g := Options{Home: home, Dir: dir, Global: true, Now: o.Now}

	if _, err := MemoryAdd(g, MemoryEntry{Type: "procedural", Name: "global-pref", Description: "Global rule", Global: true}, strings.NewReader("Global body.")); err != nil {
		t.Fatal(err)
	}
	if _, err := MemoryAdd(o, MemoryEntry{Type: "semantic", Name: "proj-fact", Description: "Project fact"}, strings.NewReader("Project body.")); err != nil {
		t.Fatal(err)
	}

	showProj, err := MemoryShow(o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showProj, "global-pref (procedural, 1.00) [global]") || !strings.Contains(showProj, "proj-fact (semantic, 1.00)") {
		t.Fatalf("MemoryShow(project) = %q, want both global [global] and project cards", showProj)
	}

	rawJSON, err := MemoryShowJSONWithOptions(o, MemoryShowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var items []MemorySummary
	if err := json.Unmarshal([]byte(rawJSON), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("MemoryShowJSONWithOptions(project) len = %d, want 2: %+v", len(items), items)
	}
	seenGlobal, seenProj := false, false
	for _, it := range items {
		if it.Name == "global-pref" && it.Global {
			seenGlobal = true
		}
		if it.Name == "proj-fact" && !it.Global {
			seenProj = true
		}
	}
	if !seenGlobal || !seenProj {
		t.Errorf("MemoryShowJSONWithOptions(project) missing expected Global flags: %+v", items)
	}

	showGlobal, err := MemoryShow(g)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showGlobal, "global-pref") || strings.Contains(showGlobal, "proj-fact") {
		t.Errorf("MemoryShow(global) = %q, want only global-pref", showGlobal)
	}
}

func TestSD108ProposeAllEmptyProjectDoesNotStampCWD(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	ids, err := ProposeAll(home, "", []Proposal{{Title: "Global proposal", Body: "Proposal body"}}, fixed)
	if err != nil || len(ids) != 1 {
		t.Fatalf("ProposeAll: %v %v", ids, err)
	}
	g := Options{Home: home, Dir: dir, Global: true, Now: func() time.Time { return fixed }}
	recs, err := List(g, ListFilter{Kind: "proposal"})
	if err != nil || len(recs) != 1 {
		t.Fatalf("List proposals: %v %v", recs, err)
	}
	if recs[0].Project != "" {
		t.Errorf("ProposeAll with empty project stamped %q, want empty", recs[0].Project)
	}
}

func TestSD109MemoryStalenessTagUsesBoundedReadRegular(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	small := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(small, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(small, fixed, fixed); err != nil {
		t.Fatal(err)
	}
	huge := filepath.Join(dir, "huge.bin")
	if err := os.WriteFile(huge, bytes.Repeat([]byte("a"), maxBody+64), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(huge, fixed, fixed); err != nil {
		t.Fatal(err)
	}

	stamp := fixed.UTC().Format(time.RFC3339)
	smallRec := Record{
		ID: "MEMORY-small", Kind: "memory", Updated: stamp,
		Title: "small", Body: "small.txt sha256: 0000000",
	}
	if tag := MemoryStalenessTag(dir, smallRec); !strings.Contains(tag, "[STALE: small.txt") {
		t.Errorf("MemoryStalenessTag(small mismatch) = %q, want [STALE: ...]", tag)
	}
	hugeRec := Record{
		ID: "MEMORY-huge", Kind: "memory", Updated: stamp,
		Title: "huge", Body: "huge.bin sha256: 0000000",
	}
	if tag := MemoryStalenessTag(dir, hugeRec); tag != "" {
		t.Errorf("MemoryStalenessTag(> maxBody) = %q, want empty (bounded skip)", tag)
	}
}

func TestSD110EmptyHomeRefused(t *testing.T) {
	cwd := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if p := Path(""); p != "" {
		t.Errorf("Path(\"\") = %q, want empty", p)
	}
	if p := Path("   "); p != "" {
		t.Errorf("Path(\"   \") = %q, want empty", p)
	}
	for _, h := range []string{"", "   "} {
		if st, err := openStore(h); !IsRefusal(err) || st != nil {
			t.Errorf("openStore(%q) = (%v, %v), want refusal", h, st, err)
		}
		if st, _, err := openStoreReadOnly(h); !IsRefusal(err) || st != nil {
			t.Errorf("openStoreReadOnly(%q) = (%v, %v), want refusal", h, st, err)
		}
	}
	entries, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("empty home created entries in CWD: %v", entries)
	}
}

func TestSD111OpenRecordsAndListSortByUpdatedAndIDLess(t *testing.T) {
	t.Parallel()
	dir, home := repo(t), t.TempDir()
	o1 := Options{Home: home, Dir: dir, Now: func() time.Time { return fixed }}
	o2 := Options{Home: home, Dir: dir, Now: func() time.Time { return fixed.Add(time.Minute) }}

	if _, err := Add(o1, AddInput{Kind: "bug", ID: "BUG-999", Title: "b999", Body: strings.NewReader("b")}); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(o1, AddInput{Kind: "bug", ID: "BUG-1000", Title: "b1000", Body: strings.NewReader("b")}); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(o2, AddInput{Kind: "bug", ID: "BUG-998", Title: "b998 newer", Body: strings.NewReader("b")}); err != nil {
		t.Fatal(err)
	}

	listed, err := List(o1, ListFilter{Kind: "bug"})
	if err != nil || len(listed) != 3 {
		t.Fatalf("List: %v %v", listed, err)
	}
	if listed[0].ID != "BUG-998" || listed[1].ID != "BUG-1000" || listed[2].ID != "BUG-999" {
		t.Errorf("List order = [%s %s %s], want [BUG-998 BUG-1000 BUG-999]", listed[0].ID, listed[1].ID, listed[2].ID)
	}

	openAll, err := OpenRecords(o1, "bug", 10)
	if err != nil || len(openAll) != 3 {
		t.Fatalf("OpenRecords: %v %v", openAll, err)
	}
	if openAll[0].ID != "BUG-998" || openAll[1].ID != "BUG-1000" || openAll[2].ID != "BUG-999" {
		t.Errorf("OpenRecords order = [%s %s %s], want [BUG-998 BUG-1000 BUG-999]", openAll[0].ID, openAll[1].ID, openAll[2].ID)
	}

	openTwo, err := OpenRecords(o1, "bug", 2)
	if err != nil || len(openTwo) != 2 {
		t.Fatalf("OpenRecords(limit=2): %v %v", openTwo, err)
	}
	if openTwo[0].ID != "BUG-998" || openTwo[1].ID != "BUG-1000" {
		t.Errorf("OpenRecords(limit=2) = [%s %s], want [BUG-998 BUG-1000]", openTwo[0].ID, openTwo[1].ID)
	}
}

func TestSD112AddPopulatesMemoryFields(t *testing.T) {
	t.Parallel()
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	stamp := fixed.UTC().Format(time.RFC3339)

	idDer, err := Add(o, AddInput{
		Kind: "memory", Title: "derived proc", Provenance: "derived",
		Body: strings.NewReader("type: procedural\n\nalways verify"),
	})
	if err != nil {
		t.Fatal(err)
	}
	idUnk, err := Add(o, AddInput{
		Kind: "memory", Title: "unknown sem",
		Body: strings.NewReader("plain semantic fact"),
	})
	if err != nil {
		t.Fatal(err)
	}

	recs, _, err := Get(o, []string{idDer, idUnk})
	if err != nil || len(recs) != 2 {
		t.Fatalf("Get: %v %v", recs, err)
	}
	if recs[0].MemoryKind != "procedural" || recs[0].Validity != 0.80 || recs[0].VerifiedAt != stamp {
		t.Errorf("derived Add memory = (kind=%q val=%.2f verified=%q), want (procedural, 0.80, %q)",
			recs[0].MemoryKind, recs[0].Validity, recs[0].VerifiedAt, stamp)
	}
	if recs[1].MemoryKind != "semantic" || recs[1].Validity != 0.80 || recs[1].VerifiedAt != stamp {
		t.Errorf("unknown Add memory = (kind=%q val=%.2f verified=%q), want (semantic, 0.80, %q)",
			recs[1].MemoryKind, recs[1].Validity, recs[1].VerifiedAt, stamp)
	}
}

func TestSD113CappedTextMemorySortedByValidityAndRecency(t *testing.T) {
	t.Parallel()
	dir, home := repo(t), t.TempDir()
	oOld := Options{Home: home, Dir: dir, Now: func() time.Time { return fixed.Add(-2 * time.Hour) }}
	oMid := Options{Home: home, Dir: dir, Now: func() time.Time { return fixed.Add(-time.Hour) }}
	oNew := Options{Home: home, Dir: dir, Now: func() time.Time { return fixed }}

	if _, err := MemoryAdd(oOld, MemoryEntry{Type: "semantic", Name: "a-low", Description: "low old", Provenance: "derived"}, strings.NewReader("b")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("f-%03d", i)
		if _, err := MemoryAdd(oMid, MemoryEntry{Type: "semantic", Name: name, Description: "filler"}, strings.NewReader("b")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := MemoryAdd(oNew, MemoryEntry{Type: "semantic", Name: "z-high", Description: "high new", Provenance: "operator"}, strings.NewReader("b")); err != nil {
		t.Fatal(err)
	}

	show, err := MemoryShow(oNew)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, "z-high") || strings.Contains(show, "a-low") || !strings.Contains(show, "more:") {
		t.Errorf("MemoryShow sort/cap failed: z-high=%v a-low=%v more=%v",
			strings.Contains(show, "z-high"), strings.Contains(show, "a-low"), strings.Contains(show, "more:"))
	}

	res, err := Resume(oNew)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "z-high") || strings.Contains(res, "a-low") || !strings.Contains(res, "more:") {
		t.Errorf("Resume memory sort/cap failed: z-high=%v a-low=%v more=%v",
			strings.Contains(res, "z-high"), strings.Contains(res, "a-low"), strings.Contains(res, "more:"))
	}

	rawJSON, err := MemoryShowJSONWithOptions(oNew, MemoryShowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var items []MemorySummary
	if err := json.Unmarshal([]byte(rawJSON), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 202 {
		t.Errorf("MemoryShowJSONWithOptions len = %d, want 202 (uncapped)", len(items))
	}
}

func TestSD41ImportSummaryAndImportedText(t *testing.T) {
	t.Parallel()
	if got := ImportSummary(&ImportResult{}); got != "none" {
		t.Fatalf("ImportSummary(empty) = %q, want none", got)
	}
	got := ImportSummary(&ImportResult{
		Records: []Imported{
			{Kind: "plan"},
			{Kind: "bug"},
			{Kind: "adr"},
			{Kind: "bug"},
		},
	})
	if got != "adr 1, bug 2, plan 1" {
		t.Fatalf("ImportSummary = %q, want %q", got, "adr 1, bug 2, plan 1")
	}

	dir, home := repo(t), t.TempDir()
	docs := filepath.Join(dir, ".agents", "docs")
	for _, sub := range []string{"plans", "architecture"} {
		if err := os.MkdirAll(filepath.Join(docs, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	planText := "# Plan Alpha\n\n- [ ] step one\n"
	decText := "# Decisions\n\n## ADR-001: Use SQLite (2026-10-25) — accepted\n\nSQLite body.\n"
	if err := os.WriteFile(filepath.Join(docs, "plans", "2026-10-25-alpha.md"), []byte(planText), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "architecture", "DECISIONS.md"), []byte(decText), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "docs fixture")

	res, err := Import(opts(dir, home), "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Records) != 2 {
		t.Fatalf("Import dry-run len = %d, want 2: %+v", len(res.Records), res.Records)
	}
	byID := map[string]Imported{}
	for _, r := range res.Records {
		byID[r.ID] = r
	}
	title, body := byID["ADR-001"].Text()
	if title != "Use SQLite" || !strings.Contains(body, "SQLite body.") {
		t.Errorf("ADR-001.Text() = (%q, %q)", title, body)
	}
	if sum := ImportSummary(res); sum != "adr 1, plan 1" {
		t.Errorf("ImportSummary(dry-run) = %q, want %q", sum, "adr 1, plan 1")
	}
}

func TestSD36OpenSessionUnmigratedAndNoTrail(t *testing.T) {
	t.Parallel()
	dir, home := repo(t), t.TempDir()
	docs := filepath.Join(dir, ".agents", "docs", "plans")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "PLAN-2026-01-01-demo.md"), []byte("# Legacy plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := opts(dir, home)
	if _, err := Add(o, AddInput{Kind: "bug", Title: "x", Body: strings.NewReader("b")}); !IsRefusal(err) {
		t.Fatalf("unmigrated Add without DB err = %v, want refusal", err)
	}
	if _, err := os.Lstat(Path(home)); !os.IsNotExist(err) {
		t.Fatalf("unmigrated Add created brain.db: %v", err)
	}

	other := repo(t)
	add(t, opts(other, home), "bug", "other scope", "b")
	if _, err := Add(o, AddInput{Kind: "bug", Title: "x", Body: strings.NewReader("b")}); !IsRefusal(err) {
		t.Fatalf("unmigrated Add with unrelated DB scope err = %v, want refusal", err)
	}

	migrated, home2 := repo(t), t.TempDir()
	mo := opts(migrated, home2)
	add(t, mo, "bug", "already in db", "b")
	if err := os.MkdirAll(filepath.Join(migrated, ".agents", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(migrated, ".agents", "docs", "ADR-001.md"), []byte("# Legacy ADR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(mo, AddInput{Kind: "bug", Title: "second in db", Body: strings.NewReader("b2")}); err != nil {
		t.Fatalf("Add with DB scope records and no trail.jsonl: %v", err)
	}
	if recs, err := List(mo, ListFilter{}); err != nil || len(recs) != 2 {
		t.Fatalf("List after Add without trail.jsonl = (%d, %v), want 2", len(recs), err)
	}
}

func TestSD37ForDreamAndForMetricsSinglePass(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	dir, home := repo(t), t.TempDir()
	o := opts(dir, home)
	o.Now = func() time.Time { return base }
	if _, err := MemoryAdd(o, MemoryEntry{Type: "semantic", Name: "fact-one", Description: "Fact one"}, strings.NewReader("Body.")); err != nil {
		t.Fatal(err)
	}
	add(t, o, "bug", "dream bug", "symptom")
	g := Options{Home: home, Dir: dir, Global: true, Now: o.Now}
	add(t, g, "feedback", "dream feedback", `{"ts":"2026-11-18T12:00:00Z"}`, "project", dir)

	din, err := ForDream(home, dir, base.Add(-time.Hour), base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(din.Memory) != 1 || len(din.Trail) != 1 || len(din.Feedback) != 1 || din.Last.IsZero() {
		t.Fatalf("ForDream = %+v, want 1 memory, 1 trail, 1 feedback, non-zero Last", din)
	}

	markRegistered(t, home, dir)
	min, err := ForMetrics(home, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !min.Migrated || len(min.Trail) != 1 || len(min.Feedback) != 1 {
		t.Fatalf("ForMetrics(DB only) = %+v, want Migrated=true, 1 trail, 1 feedback", min)
	}

	if _, err := Export(o); err != nil {
		t.Fatal(err)
	}
	home2 := t.TempDir()
	markRegistered(t, home2, dir)
	min2, err := ForMetrics(home2, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !min2.Migrated || len(min2.Trail) != 1 || len(min2.Feedback) != 0 {
		t.Fatalf("ForMetrics(trail fallback) = %+v, want Migrated=true, 1 trail, 0 feedback", min2)
	}
	if _, err := os.Lstat(Path(home2)); !os.IsNotExist(err) {
		t.Fatalf("ForMetrics(trail fallback) created brain.db: %v", err)
	}
}
