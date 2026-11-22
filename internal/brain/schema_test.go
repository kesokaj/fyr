package brain

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// The store is created under the home's ~/.config/fyr, private: the
// directory 0700, the database 0600, with the records, FTS and meta tables.
func TestSchemaAndModes(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	add(t, opts(dir, home), "learning", "first", "body")
	p := Path(home)
	if p != filepath.Join(home, harness.StateDir, "brain.db") {
		t.Fatalf("Path = %s", p)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("brain.db mode %v, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("state dir mode %v, want 0700", di.Mode().Perm())
	}
	db, err := sql.Open("sqlite", "file:"+p+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"records", "records_fts", "meta"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = ?`, name).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s: n=%d err=%v", name, n, err)
		}
	}
	var cols []string
	rows, err := db.Query(`SELECT name FROM pragma_table_info('records')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c string
		rows.Scan(&c)
		cols = append(cols, c)
	}
	rows.Close()
	for _, want := range []string{"scope", "id", "kind", "status", "date", "title", "body", "project", "updated"} {
		if !strings.Contains(" "+strings.Join(cols, " ")+" ", " "+want+" ") {
			t.Errorf("records lacks column %s: %v", want, cols)
		}
	}
}

// A store a newer binary wrote is refused, untouched.
func TestNewerSchemaRefused(t *testing.T) {
	home := t.TempDir()
	p := Path(home)
	os.MkdirAll(filepath.Dir(p), 0o700)
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, err = List(Options{Home: home, Global: true}, ListFilter{})
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("a newer schema: %v, want a refusal naming it", err)
	}
}

// D16: a project whose trail is still files refuses every record verb and
// names the command; a migrated project (trail.jsonl present) does not.
func TestUnmigratedProjectRefused(t *testing.T) {
	for _, docs := range []string{"docs", filepath.Join(".agents", "docs")} {
		dir, home := repo(t), t.TempDir()
		o := opts(dir, home)
		os.MkdirAll(filepath.Join(dir, docs, "plans"), 0o755)
		checks := map[string]error{}
		_, checks["add"] = Add(o, AddInput{Kind: "learning", Title: "t", Body: strings.NewReader("b")})
		_, _, checks["get"] = Get(o, []string{"BUG-001"})
		checks["edit"] = Edit(o, EditInput{ID: "BUG-001", Status: str("fixed")})
		_, checks["list"] = List(o, ListFilter{})
		for verb, err := range checks {
			if !IsRefusal(err) || !strings.Contains(err.Error(), "fyr record import") {
				t.Errorf("%s: %s in an unmigrated project: %v, want a refusal naming fyr record import", docs, verb, err)
			}
		}
		if _, err := os.Stat(trailFile(dir)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: a refused verb created trail.jsonl", docs)
		}
		// The global scope is not the project's: it still answers.
		g := o
		g.Global = true
		if _, err := List(g, ListFilter{}); err != nil {
			t.Errorf("%s: list --global in an unmigrated project: %v", docs, err)
		}
		os.MkdirAll(filepath.Join(dir, ".agents"), 0o755)
		if err := os.WriteFile(trailFile(dir), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := List(o, ListFilter{}); err != nil {
			t.Errorf("%s: with trail.jsonl present: %v", docs, err)
		}
	}
}

func TestMigrationTwoToThreePreservesAndMapsMemory(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	p := Path(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	rows := []struct{ id, kind, body, prov, upd string }{
		{"MEMORY-fb", "memory", "type: feedback\n\nfb", "derived", "2026-10-01T00:00:00Z"},
		{"MEMORY-proc", "memory", "type: procedural\n\nproc", "unknown", "2026-10-02T00:00:00Z"},
		{"MEMORY-ep", "memory", "type: episodic\n\nep", "operator", "2026-10-03T00:00:00Z"},
		{"MEMORY-user", "memory", "type: user\n\nusr", "operator", "2026-10-04T00:00:00Z"},
		{"BUG-001", "bug", "symptom", "", "2026-10-05T00:00:00Z"},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO records(scope, id, kind, status, date, title, body, project, provenance, updated)
			VALUES(?, ?, ?, 'closed', '2026-10-01', ?, ?, '', ?, ?)`,
			dir, r.id, r.kind, r.id, r.body, r.prov, r.upd); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	o := opts(dir, home)
	recs, missing, err := Get(o, []string{"MEMORY-fb", "MEMORY-proc", "MEMORY-ep", "MEMORY-user", "BUG-001"})
	if err != nil || len(missing) != 0 || len(recs) != 5 {
		t.Fatalf("Get after migration 2->3: %v %v %v", recs, missing, err)
	}
	byID := map[string]Record{}
	for _, r := range recs {
		byID[r.ID] = r
	}
	if r := byID["MEMORY-fb"]; r.MemoryKind != "procedural" || r.Validity != 0.55 || r.VerifiedAt != "2026-10-01T00:00:00Z" {
		t.Errorf("MEMORY-fb = %+v", r)
	}
	if r := byID["MEMORY-proc"]; r.MemoryKind != "procedural" || r.Validity != 0.70 || r.VerifiedAt != "2026-10-02T00:00:00Z" {
		t.Errorf("MEMORY-proc = %+v", r)
	}
	if r := byID["MEMORY-ep"]; r.MemoryKind != "episodic" || r.Validity != 1.0 || r.VerifiedAt != "2026-10-03T00:00:00Z" {
		t.Errorf("MEMORY-ep = %+v", r)
	}
	if r := byID["MEMORY-user"]; r.MemoryKind != "semantic" || r.Validity != 1.0 || r.VerifiedAt != "2026-10-04T00:00:00Z" {
		t.Errorf("MEMORY-user = %+v", r)
	}
	if r := byID["BUG-001"]; r.MemoryKind != "" || r.Validity != 1.0 || r.VerifiedAt != "" {
		t.Errorf("BUG-001 = %+v", r)
	}
}

func TestMigrationThreeToFourAndReadOnlyV3(t *testing.T) {
	dir, home := repo(t), t.TempDir()
	p := Path(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA user_version = 3`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO records(scope, id, kind, status, date, title, body, project, provenance, memory_kind, validity, verified_at, updated)
		VALUES(?, 'BUG-001', 'bug', 'open', '2026-10-25', 'v3 bug', 'Touches pkg/a.go', '', '', '', 1.0, '', '2026-10-25T14:30:00Z')`, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO meta(scope, export_sha) VALUES(?, 'abc123')`, dir); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Read-only callers must read a v3 store cleanly without migrating it.
	if recs, ok, err := ReadScope(home, dir); err != nil || !ok || len(recs) != 1 || recs[0].ID != "BUG-001" {
		t.Fatalf("ReadScope on v3 store: recs=%+v ok=%v err=%v", recs, ok, err)
	}
	if links, err := IndexRecordLinks(home, dir, []string{"pkg/a.go"}); err != nil || links["pkg/a.go"].Bugs != 1 {
		t.Fatalf("IndexRecordLinks on v3 store: links=%+v err=%v", links, err)
	}
	if exported, err := ExportStateRecords(home, StateExportOpts{}); err != nil || len(exported) != 1 || exported[0].RepoID == "" {
		t.Fatalf("ExportStateRecords on v3 store: %+v %v", exported, err)
	}
	ro, err := sql.Open("sqlite", "file:"+p+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var uv int
	if err := ro.QueryRow(`PRAGMA user_version`).Scan(&uv); err != nil || uv != 3 {
		t.Fatalf("user_version after read-only v3 reads = %d (%v), want 3", uv, err)
	}
	ro.Close()

	// Opening read-write migrates v3 -> v4, adding repo_id on records and meta plus records_repo_id index.
	o := opts(dir, home)
	recs, missing, err := Get(o, []string{"BUG-001"})
	if err != nil || len(missing) != 0 || len(recs) != 1 {
		t.Fatalf("Get after v3->v4 migration: %v %v %v", recs, missing, err)
	}
	ro2, err := sql.Open("sqlite", "file:"+p+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer ro2.Close()
	if err := ro2.QueryRow(`PRAGMA user_version`).Scan(&uv); err != nil || uv != len(migrations) {
		t.Fatalf("migrated user_version = %d (%v), want %d", uv, err, len(migrations))
	}
	var idxCount int
	if err := ro2.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'records_repo_id'`).Scan(&idxCount); err != nil || idxCount != 1 {
		t.Fatalf("records_repo_id index count = %d, err = %v", idxCount, err)
	}
	var metaRepoID string
	if err := ro2.QueryRow(`SELECT repo_id FROM meta WHERE scope = ?`, dir).Scan(&metaRepoID); err != nil || metaRepoID != "" {
		t.Fatalf("migrated meta.repo_id = %q, err = %v", metaRepoID, err)
	}
}
