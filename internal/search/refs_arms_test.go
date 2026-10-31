package search

import (
	"strings"
	"testing"
)

// The arms of refs and find that their splits move into helpers, pinned
// before the splits (PLAN-2026-10-27-funclen Phase 4, D13).

// refsRepo is a checkout with a reference, indexed once under the home it
// returns; a test then changes the index behind the code's back.
func refsRepo(t *testing.T) (string, string) {
	t.Helper()
	root := newRepo(t)
	writeFile(t, root, "notes/p.md", "# the plan\n\nSee adr/egret-note.md for the egret, and BUG-001.\n")
	commitAll(t, root, "notes")
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

// Every read of refs that fails, fails the query: a file state or a
// reference that does not scan, a pattern too
// long for SQLite's LIKE.
func TestRefsReadErrors(t *testing.T) {
	long := strings.Repeat("a", 50001)
	for _, c := range []struct {
		name, sql, refs, want string
	}{
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
