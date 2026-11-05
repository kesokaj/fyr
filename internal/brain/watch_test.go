package brain

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

// TestSnapshotScopes verifies that SnapshotScopes hashes only watched project
// scopes (plus global records attributed to those scopes or unattributed),
// ignores writes to other project scopes or global meta, leaves scratchDir
// empty, and reads schema-1 stores without migrating them (BUG-504, ADR-506).
func TestSnapshotScopes(t *testing.T) {
	home := t.TempDir()
	scratch := t.TempDir()
	watched := repo(t)
	other := repo(t)

	add(t, opts(other, home), "change", "Other initial", "body")
	dbPath := Path(home)

	d0, ok := SnapshotScopes(dbPath, scratch, []string{watched})
	if !ok || d0 == "" {
		t.Fatalf("initial SnapshotScopes: digest=%q ok=%v, want ok=true", d0, ok)
	}
	if entries, err := os.ReadDir(scratch); err != nil || len(entries) != 0 {
		t.Fatalf("scratchDir left entries: %v (%v)", entries, err)
	}

	// Unwatched writes in another project's scope, global records for that
	// project, and global dream timestamps must leave the watched digest unchanged.
	add(t, opts(other, home), "bug", "Other bug", "symptom", "status", "open")
	if _, err := MemoryAdd(opts(other, home), MemoryEntry{Type: "project", Name: "other-mem", Description: "d"}, strings.NewReader("body")); err != nil {
		t.Fatal(err)
	}
	if _, err := ProposeAll(home, other, []Proposal{{Title: "Other prop", Body: "body"}}, fixed); err != nil {
		t.Fatal(err)
	}
	gOpts := Options{Home: home, Global: true, Now: func() time.Time { return fixed }}
	add(t, gOpts, "feedback", "Other feedback", "body", "project", other)
	if _, err := Export(opts(other, home)); err != nil {
		t.Fatal(err)
	}

	d1, ok := SnapshotScopes(dbPath, scratch, []string{watched})
	if !ok || d1 != d0 {
		t.Fatalf("after unwatched writes: digest=%q ok=%v, want %q", d1, ok, d0)
	}

	// Watched writes (project record, memory, global feedback for watched,
	// unattributed global record, and watched meta) must each change the digest.
	prev := d1
	steps := []struct {
		name string
		fn   func()
	}{
		{"watched record add", func() { add(t, opts(watched, home), "change", "Watched change", "body") }},
		{"watched record edit", func() {
			if err := Edit(opts(watched, home), EditInput{ID: "CHANGE-2026-10-25-1430-watched-change", Title: str("Edited title")}); err != nil {
				t.Fatal(err)
			}
		}},
		{"watched memory add", func() {
			if _, err := MemoryAdd(opts(watched, home), MemoryEntry{Type: "project", Name: "w-mem", Description: "d"}, strings.NewReader("body")); err != nil {
				t.Fatal(err)
			}
		}},
		{"global feedback for watched", func() {
			add(t, gOpts, "feedback", "Watched feedback", "body", "project", watched)
		}},
		{"unattributed global record", func() {
			add(t, gOpts, "feedback", "Unscoped feedback", "body")
		}},
		{"watched meta export_sha", func() {
			if _, err := Export(opts(watched, home)); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, s := range steps {
		s.fn()
		got, ok := SnapshotScopes(dbPath, scratch, []string{watched})
		if !ok || got == prev {
			t.Fatalf("%s: digest=%q ok=%v did not change from %q", s.name, got, ok, prev)
		}
		prev = got
	}
}

func TestSnapshotScopesFallbacksAndSchema1(t *testing.T) {
	dir := t.TempDir()
	scratch := t.TempDir()
	watched := repo(t)

	// Missing file -> ok=false, nothing created.
	missing := filepath.Join(dir, "missing", "brain.db")
	if _, ok := SnapshotScopes(missing, scratch, []string{watched}); ok {
		t.Fatal("missing file returned ok=true")
	}
	if _, err := os.Lstat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatalf("missing parent was created: %v", err)
	}

	// Non-SQLite text and empty file -> ok=false.
	junk := filepath.Join(dir, "junk.db")
	if err := os.WriteFile(junk, []byte("store"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := SnapshotScopes(junk, scratch, []string{watched}); ok {
		t.Fatal("non-SQLite file returned ok=true")
	}
	empty := filepath.Join(dir, "empty.db")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := SnapshotScopes(empty, scratch, []string{watched}); ok {
		t.Fatal("0-byte file returned ok=true")
	}

	// Symlink -> ok=false.
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(junk, link); err != nil {
		t.Fatal(err)
	}
	if _, ok := SnapshotScopes(link, scratch, []string{watched}); ok {
		t.Fatal("symlink returned ok=true")
	}

	// Empty scopes -> ok=false.
	if _, ok := SnapshotScopes(junk, scratch, nil); ok {
		t.Fatal("empty scopes returned ok=true")
	}

	// Schema 1 store (no provenance column): SnapshotScopes succeeds and leaves
	// user_version at 1 without creating -wal or -shm when none existed.
	v1Path := filepath.Join(dir, "v1", "brain.db")
	if err := os.MkdirAll(filepath.Dir(v1Path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", search.DSN(v1Path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0] + `; PRAGMA user_version = 1;`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO records(scope, id, kind, status, date, title, body, project, updated) VALUES (?, 'BUG-001', 'bug', 'open', '2026-10-25', 't', 'b', '', '2026-10-25T00:00:00Z')`, watched); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if d, ok := SnapshotScopes(v1Path, scratch, []string{watched}); !ok || d == "" {
		t.Fatalf("schema-1 SnapshotScopes: digest=%q ok=%v, want ok=true", d, ok)
	}
	db2, err := sql.Open("sqlite", search.DSN(v1Path))
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var ver int
	if err := db2.QueryRow(`PRAGMA user_version`).Scan(&ver); err != nil || ver != 1 {
		t.Fatalf("schema-1 user_version after SnapshotScopes = %d (%v), want 1", ver, err)
	}
}
