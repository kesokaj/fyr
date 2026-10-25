package metrics

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func openT(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestStorePathAndMode(t *testing.T) {
	home := t.TempDir()
	p := Path(home)
	if want := filepath.Join(home, harness.StateDir, "metrics.db"); p != want {
		t.Fatalf("Path = %q, want %q", p, want)
	}
	openT(t, p)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(p))
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v, want 0700", di.Mode().Perm())
	}
}

func TestStoreRefusesSymlinkedDatabase(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere.db")
	os.WriteFile(target, nil, 0o600)
	p := filepath.Join(dir, "metrics.db")
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(p); err == nil {
		s.Close()
		t.Fatal("Open followed a symlinked metrics.db")
	}
	if fi, _ := os.Stat(target); fi.Size() != 0 {
		t.Fatal("the symlink's target was written")
	}
}

func TestStoreSaltIsStable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "metrics.db")
	s := openT(t, p)
	salt1 := s.meta(t, "salt")
	if len(salt1) != 32 {
		t.Fatalf("salt %q, want 32 hex", salt1)
	}
	s.Close()
	s2 := openT(t, p)
	if got := s2.meta(t, "salt"); got != salt1 {
		t.Fatalf("salt changed on reopen: %q -> %q", salt1, got)
	}
}

func (s *Store) meta(t *testing.T, key string) string {
	t.Helper()
	var v string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v); err != nil {
		t.Fatalf("meta %s: %v", key, err)
	}
	return v
}

// A store with rows opens under a schema list one entry longer and keeps
// every row: the store is the only copy (D5).
func TestMigrationKeepsRows(t *testing.T) {
	p := filepath.Join(t.TempDir(), "metrics.db")
	s := openT(t, p)
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor) VALUES ('s1', 'claude'), ('s2', 'agy')`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	saved := migrations
	t.Cleanup(func() { migrations = saved })
	migrations = append(append([]string{}, saved...), `ALTER TABLE sessions ADD COLUMN test_v2 TEXT`)

	s2 := openT(t, p)
	var n int
	s2.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&n)
	if n != 2 {
		t.Fatalf("rows after migration = %d, want 2", n)
	}
	if v, want := s2.meta(t, "schema"), strconv.Itoa(len(saved)+1); v != want {
		t.Fatalf("schema after migration = %s, want %s", v, want)
	}
	if _, err := s2.db.Exec(`UPDATE sessions SET test_v2 = 'x'`); err != nil {
		t.Fatalf("migration did not run: %v", err)
	}
}

// A store written by a newer binary is refused, and its bytes stay as they
// were.
func TestNewerSchemaRefusedUntouched(t *testing.T) {
	p := filepath.Join(t.TempDir(), "metrics.db")
	s := openT(t, p)
	s.db.Exec(`INSERT INTO sessions(id) VALUES ('keep')`)
	s.db.Exec(`UPDATE meta SET value = '99' WHERE key = 'schema'`)
	s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	s.Close()
	before, _ := os.ReadFile(p)

	s2, err := Open(p)
	if err == nil {
		s2.Close()
		t.Fatal("Open accepted a store with a newer schema")
	}
	if !errors.Is(err, ErrNewerSchema) || !strings.Contains(err.Error(), "update fyr") {
		t.Fatalf("error = %v, want ErrNewerSchema naming the fix", err)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatal("a refused store was modified")
	}
}

func TestExportRoundTripsEveryTable(t *testing.T) {
	dir := t.TempDir()
	s := openT(t, filepath.Join(dir, "metrics.db"))
	for _, q := range []string{
		`INSERT INTO sessions(id, vendor, started_at) VALUES ('s1', 'claude', 1)`,
		`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('s1', 'tu1', 2, 'tool', 'Bash')`,
		`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES ('s1', 'tu1', 0, 'git status')`,
		`INSERT INTO events(session_id, ts, kind, ref, value) VALUES ('s1', 3, 'commit', 'abc', '')`,
		`INSERT INTO ingest_state(source, size, mtime, offset) VALUES ('/x.jsonl', 10, 4, 10)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	out := filepath.Join(dir, "export.jsonl")
	if err := s.Export(out); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(out)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("export mode %v, want 0600", fi.Mode().Perm())
	}
	f, _ := os.Open(out)
	defer f.Close()
	seen := map[string]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var line struct {
			Table string         `json:"table"`
			Row   map[string]any `json:"row"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		seen[line.Table]++
	}
	for _, tbl := range []string{"meta", "sessions", "steps", "step_verbs", "events", "ingest_state"} {
		if seen[tbl] == 0 {
			t.Errorf("export has no %s rows", tbl)
		}
	}
	if err := s.Export(out); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("second export to the same file: %v, want a refusal", err)
	}
}

// A store written at schema 1 (before steps.context) upgrades in place and
// keeps its rows.
func TestMigrationOneToCurrent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "metrics.db")
	saved := migrations
	t.Cleanup(func() { migrations = saved })
	migrations = saved[:1]
	s := openT(t, p)
	s.db.Exec(`INSERT INTO steps(session_id, ext_id, kind) VALUES ('s', 'e', 'tool')`)
	s.Close()

	migrations = saved
	s2 := openT(t, p)
	var n int
	if err := s2.db.QueryRow(`SELECT count(*) FROM steps WHERE context IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("after upgrade: n=%d err=%v", n, err)
	}
}

// The export leaves out the cmd_hash salt: with the salt, anyone holding an
// export could confirm a guessed low-entropy command (security review
// 2026-10-08). The schema row stays, so an export names its schema.
func TestExportOmitsSalt(t *testing.T) {
	dir := t.TempDir()
	s := openT(t, filepath.Join(dir, "metrics.db"))
	salt := s.meta(t, "salt")
	out := filepath.Join(dir, "e.jsonl")
	if err := s.Export(out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if bytes.Contains(b, []byte(salt)) {
		t.Error("the export carries the salt")
	}
	if !bytes.Contains(b, []byte(`"schema"`)) {
		t.Error("the export lost its schema row")
	}
}
