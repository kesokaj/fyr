package search

import (
	"strings"
	"testing"
)

// The arms of refs and find that their splits move into helpers, pinned
// before the splits (PLAN-2026-10-27-funclen Phase 4, D13).

// refsRepo is a checkout with a trail, indexed once under the home it
// returns; a test then changes the index behind the code's back.
func refsRepo(t *testing.T) (string, string) {
	t.Helper()
	root := newRepo(t)
	writeFile(t, root, ".agents/docs/bugs/BUG-001-heron.md", "---\nid: BUG-001\nstatus: open\ndate: 2026-09-10\n---\n# BUG-001: the heron\n")
	writeFile(t, root, ".agents/docs/plans/p.md", "# the plan\n\nSee adr/egret-note.md for the egret.\n")
	commitAll(t, root, "trail")
	home := t.TempDir()
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	return root, home
}

// readmeOID is README.md's blob in root's index.
func readmeOID(t *testing.T, root string) string {
	t.Helper()
	var oid string
	if err := openDB(t, root).QueryRow(`SELECT oid FROM files WHERE path = 'README.md'`).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	return oid
}

// A directory under the docs root also matches the references written
// relative to that root.
func TestRefsDirDocsRelative(t *testing.T) {
	root, home := refsRepo(t)
	out := mustSearch(t, Options{Dir: root, Home: home, Refs: ".agents/docs/adr/"})
	if !strings.Contains(out, "referenced by 1:") || !strings.Contains(out, "plans/p.md") {
		t.Errorf("--refs of a docs directory missed its docs-relative reference:\n%s", out)
	}
}

// Every read of refs that fails, fails the query: the records table gone,
// a record, a file state or a reference that does not scan, a pattern too
// long for SQLite's LIKE.
func TestRefsReadErrors(t *testing.T) {
	long := strings.Repeat("a", 50001)
	for _, c := range []struct {
		name, sql, refs, want string
	}{
		{"records table", `DROP TABLE records`, "BUG-001", "no such table: records"},
		{"record scan", `INSERT INTO records(oid, id, line, title, status, date) VALUES (?, 'BUG-009', 1, NULL, NULL, NULL)`, "BUG-009", "NULL"},
		{"state scan", `UPDATE files SET state = NULL WHERE path = 'README.md' AND ? != ''`, "README.md", "NULL"},
		{"same-name hint", ``, long, "LIKE or GLOB pattern too complex"},
		{"reference rows", ``, long + "/", "LIKE or GLOB pattern too complex"},
		{"reference scan", `INSERT INTO refs(oid, line, target) VALUES (?, NULL, 'BUG-777')`, "BUG-777", "NULL"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, home := refsRepo(t)
			if c.sql != "" {
				args := []any{}
				if strings.Contains(c.sql, "?") {
					args = append(args, readmeOID(t, root))
				}
				if _, err := openDB(t, root).Exec(c.sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			out, err := runSearch(t, Options{Dir: root, Home: home, Refs: c.refs})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("--refs: %v, want %q\n%.300s", err, c.want, out)
			}
		})
	}
}

// A hit whose chunk no longer reads back fails the search at its snippet.
func TestFindSnippetError(t *testing.T) {
	root, home := refsRepo(t)
	if _, err := openDB(t, root).Exec(`UPDATE chunks SET body = NULL WHERE oid = ?`, readmeOID(t, root)); err != nil {
		t.Fatal(err)
	}
	_, err := runSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	if err == nil || !strings.Contains(err.Error(), "NULL") {
		t.Fatalf("search over an unreadable chunk: %v, want the scan error", err)
	}
}
