package search

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// The arms of reconcileGlobal and prepare that their splits move into
// helpers, pinned before the splits (PLAN-2026-10-27-funclen Phase 4, D13).

// globalStore opens home's global search index as the code does, after a
// first global search has made it; closed at cleanup.
func globalStore(t *testing.T, home string) *store {
	t.Helper()
	mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"jackalope"}})
	st, err := tryOpen(globalDB(home))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	return st
}

// registered is a checkout with a trail, registered in home's global index
// by its marker and a search from it.
func registered(t *testing.T, home string) string {
	t.Helper()
	root := trailRepo(t)
	markRegistered(t, home, root)
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"heron"}})
	return root
}

// nonEmptyDir puts a directory holding a file where path's database goes.
func nonEmptyDir(t *testing.T, path string) {
	t.Helper()
	writeFile(t, path, "keep", "x\n")
}

// --rebuild of the global index that cannot remove the old file says so,
// and opens nothing.
func TestReconcileGlobalRebuildRemoveError(t *testing.T) {
	home := globalHome(t)
	nonEmptyDir(t, globalDB(home))
	err := Index(Options{Dir: t.TempDir(), Home: home, Global: true, Rebuild: true}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "directory not empty") {
		t.Fatalf("global rebuild over a directory: %v, want the remove error", err)
	}
}

// The projects table that cannot be read, or holds a row that does not
// scan, fails the global search.
func TestReconcileGlobalRegistrationReadErrors(t *testing.T) {
	for _, c := range []struct{ name, sql, want string }{
		{"no table", `DROP TABLE projects`, "no such table: projects"},
		{"null root", `INSERT INTO projects(root, name, docs_root, last_seen) VALUES (NULL, 'x', '', 0)`, "NULL"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := globalHome(t)
			st := globalStore(t, home)
			if _, err := st.db.Exec(c.sql); err != nil {
				t.Fatal(err)
			}
			_, err := runSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"jackalope"}})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("global search: %v, want %q", err, c.want)
			}
		})
	}
}

// A registration that is gone but cannot be dropped fails the search, and
// stays registered.
func TestReconcileGlobalDropError(t *testing.T) {
	home := globalHome(t)
	root := registered(t, home)
	st := globalStore(t, home)
	refuse(t, st, "DELETE", "projects")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	_, err := runSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"heron"}})
	if err == nil || !strings.Contains(err.Error(), "refused DELETE projects") {
		t.Fatalf("global search: %v, want the refused drop", err)
	}
	if n := countStore(t, st, `SELECT count(*) FROM projects`); n != 1 {
		t.Errorf("%d registrations after a failed drop, want 1", n)
	}
}

// index --rebuild of a checkout's own index removes the old database first.
func TestPrepareRebuildRemovesProjectDB(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	mustSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
	db := openDB(t, root)
	if _, err := db.Exec(`INSERT INTO meta(key, value) VALUES ('marker', 'x')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := Index(Options{Dir: root, Home: home, Rebuild: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if n := count(t, openDB(t, root), `SELECT count(*) FROM meta WHERE key = 'marker'`); n != 0 {
		t.Error("index --rebuild kept the old database")
	}
	other := newRepo(t)
	nonEmptyDir(t, projectDB(other))
	err := Index(Options{Dir: other, Home: home, Rebuild: true}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "directory not empty") {
		t.Fatalf("rebuild over a directory: %v, want the remove error", err)
	}
}

// A checkout this git cannot read safely fails its own search; one whose
// registration cannot be written fails it too.
func TestPrepareProjectErrors(t *testing.T) {
	t.Run("partial clone", func(t *testing.T) {
		root := newRepo(t)
		gitT(t, root, "config", "extensions.partialClone", "origin")
		saved := lazyFetchSafe
		t.Cleanup(func() { lazyFetchSafe = saved })
		lazyFetchSafe = func() bool { return false }
		if _, err := runSearch(t, Options{Dir: root, Words: []string{"demo"}}); !errors.Is(err, errUnsafePartialClone) {
			t.Fatalf("search of a partial clone on an old git: %v", err)
		}
	})
	t.Run("register fails", func(t *testing.T) {
		root := trailRepo(t)
		home := t.TempDir()
		markRegistered(t, home, root)
		if err := os.MkdirAll(globalDB(home), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := runSearch(t, Options{Dir: root, Home: home, Words: []string{"demo"}})
		if err == nil || !strings.Contains(err.Error(), "search.db") {
			t.Fatalf("search with an unopenable global index: %v, want its error", err)
		}
	})
}
