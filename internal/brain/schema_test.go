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
