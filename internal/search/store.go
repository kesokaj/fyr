// Package search keeps the derived SQLite indexes behind `fyr search`
// and `fyr index`: one per git checkout at .agents/scratch/search.db and
// the global search index at ~/.config/fyr/search.db. Both are caches of
// files git or ~/.config/fyr/ already holds (R4, ADR-045): every query
// first reconciles the index against git, so an answer never comes from
// another branch or a stale tree, and deleting a database loses nothing.
//
// This package, internal/metrics (the metrics store, ADR-055) and
// internal/brain (the brain store, ADR-063) are the three that import the
// SQLite driver; the installer in internal/harness never does.
package search

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// schemaVersion is stored in meta; a database with any other value is
// rebuilt, since it is a cache.
const schemaVersion = "1"

// busyTimeoutMS is how long a writer waits for another process's write lock
// before the command refuses. A variable so tests can shorten it.
var busyTimeoutMS = 10000

// nowFunc is the clock, replaceable in tests (garbage collection by age).
var nowFunc = time.Now

var errBusy = errors.New("search: index busy (another fyr is indexing); retry, or use grep")

const schemaSQL = `
CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE projects(root TEXT PRIMARY KEY, name TEXT, docs_root TEXT, last_seen INTEGER);
CREATE TABLE files(project TEXT, path TEXT, oid TEXT, state TEXT, PRIMARY KEY(project, path));
CREATE INDEX files_oid ON files(oid);
CREATE TABLE blobs(oid TEXT PRIMARY KEY, last_seen INTEGER, skipped INTEGER);
CREATE TABLE chunks(id INTEGER PRIMARY KEY, oid TEXT, line INTEGER, heading TEXT, body TEXT);
CREATE INDEX chunks_oid ON chunks(oid);
CREATE VIRTUAL TABLE chunks_fts USING fts5(heading, body, content='chunks', content_rowid='id', tokenize='porter unicode61');
CREATE TRIGGER chunks_ai AFTER INSERT ON chunks BEGIN
  INSERT INTO chunks_fts(rowid, heading, body) VALUES (new.id, new.heading, new.body);
END;
CREATE TRIGGER chunks_ad AFTER DELETE ON chunks BEGIN
  INSERT INTO chunks_fts(chunks_fts, rowid, heading, body) VALUES ('delete', old.id, old.heading, old.body);
END;
CREATE TABLE records(oid TEXT, id TEXT, line INTEGER, title TEXT, status TEXT, date TEXT);
CREATE INDEX records_id ON records(id);
CREATE INDEX records_oid ON records(oid);
CREATE TABLE refs(oid TEXT, line INTEGER, target TEXT);
CREATE INDEX refs_target ON refs(target);
`

// store is one open index database.
type store struct {
	db   *sql.DB
	path string
}

// dsn escapes the path: the driver splits the string at its first '?' and
// runs every _pragma value as SQL, and SQLite decodes %XX in a file: URI, so
// a raw '?', '#' or '%' in a checkout's path opened another file and could
// inject a pragma (security review 2026-09-29, finding 1).
func dsn(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path),
		RawQuery: fmt.Sprintf("_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)&_pragma=foreign_keys(0)", busyTimeoutMS)}
	return u.String()
}

// sqliteCode returns the primary SQLite result code of err, or 0.
func sqliteCode(err error) int {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code() & 0xff
	}
	return 0
}

func isBusy(err error) bool {
	c := sqliteCode(err)
	return c == sqlite3.SQLITE_BUSY || c == sqlite3.SQLITE_LOCKED
}

func isUnreadable(err error) bool {
	c := sqliteCode(err)
	return c == sqlite3.SQLITE_CORRUPT || c == sqlite3.SQLITE_NOTADB
}

// removeDB deletes the database at path and its two companion files, and
// nothing else: the three names are this package's own.
func removeDB(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// openStore opens or creates the index at path. An unreadable file or an
// older schema is deleted and rebuilt with a note on out: it is a cache.
func openStore(path string, out io.Writer) (*store, error) {
	s, err := tryOpen(path)
	if err == nil {
		return s, nil
	}
	var stale staleSchemaError
	if !isUnreadable(err) && !errors.As(err, &stale) {
		if isBusy(err) {
			return nil, errBusy
		}
		return nil, fmt.Errorf("search: %s: %w", path, err)
	}
	if err := removeDB(path); err != nil {
		return nil, fmt.Errorf("search: removing the unreadable index %s: %w", path, err)
	}
	fmt.Fprintln(out, printable(fmt.Sprintf("note: index at %s was unreadable (or an older schema); rebuilt", path)))
	s, err = tryOpen(path)
	if err != nil {
		if isBusy(err) {
			return nil, errBusy
		}
		return nil, fmt.Errorf("search: %s: %w", path, err)
	}
	return s, nil
}

// prepareFiles refuses a database file, or one of its two companions, that
// is anything but a regular file — SQLite writes through a symlink — and
// keeps all three private: the index holds the text of every file it read,
// whatever that file's own mode (security review of aae16af..f41e5c0). A new
// database is created 0600 here, and SQLite gives its companions the
// database's mode.
func prepareFiles(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		fi, err := os.Lstat(p)
		switch {
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			return err
		case !fi.Mode().IsRegular():
			return fmt.Errorf("%s is not a regular file; refusing to open or write through it", filepath.Base(p))
		case fi.Mode().Perm()&0o077 != 0:
			if err := os.Chmod(p, 0o600); err != nil {
				return err
			}
		}
	}
	// O_EXCL creates a missing file 0600 without following a link. A file
	// that appears between the check above and this call is opened as it
	// is: closing that window would take a lock the rest of the index does
	// not hold, and whoever can race here already writes to the directory.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	switch {
	case err == nil:
		return f.Close()
	case errors.Is(err, os.ErrExist):
		return nil
	}
	return err
}

type staleSchemaError struct{ found string }

func (e staleSchemaError) Error() string { return "schema " + e.found }

func tryOpen(path string) (*store, error) {
	// 0700: ~/.config/fyr holds the feedback log (ConfigDir).
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := prepareFiles(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	// One connection: BEGIN IMMEDIATE and COMMIT must reach the same one,
	// and a command is single-threaded anyway.
	db.SetMaxOpenConns(1)
	s := &store{db: db, path: path}
	if err := s.ensureSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *store) ensureSchema() error {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&v)
	switch {
	case err == nil && v == schemaVersion:
		return nil
	case err == nil:
		return staleSchemaError{v}
	case errors.Is(err, sql.ErrNoRows):
		return staleSchemaError{"none"}
	}
	// A new, empty database has no meta table yet; anything else (an
	// unreadable file, a lock) is the caller's to classify.
	var n int
	if qerr := s.db.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n); qerr != nil {
		return qerr
	}
	if n != 0 {
		return staleSchemaError{"unknown"}
	}
	if _, err := s.db.Exec(`BEGIN IMMEDIATE`); err != nil {
		return err
	}
	// Another process may have created the schema while this one waited
	// for the lock: look again under it.
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n); err != nil {
		s.db.Exec(`ROLLBACK`)
		return err
	}
	if n != 0 {
		if _, err := s.db.Exec(`ROLLBACK`); err != nil {
			return err
		}
		return s.ensureSchema()
	}
	if _, err := s.db.Exec(schemaSQL); err != nil {
		s.db.Exec(`ROLLBACK`)
		return err
	}
	if _, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES ('schema', ?)`, schemaVersion); err != nil {
		s.db.Exec(`ROLLBACK`)
		return err
	}
	_, err = s.db.Exec(`COMMIT`)
	return err
}

func (s *store) close() error { return s.db.Close() }

// size is the database plus its WAL, in bytes.
func (s *store) size() int64 {
	var n int64
	for _, p := range []string{s.path, s.path + "-wal"} {
		if fi, err := os.Stat(p); err == nil {
			n += fi.Size()
		}
	}
	return n
}
