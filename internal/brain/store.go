// Package brain is the brain store, ~/.config/fyr/brain.db: every record of
// every project's trail, with its metadata, plus the global records (feedback,
// proposals) and each project's memory, behind `fyr record` and the record
// hits of `fyr search` (R4, R7, ADR-063, spec SPEC-fyr-record).
//
// Like the metrics store it is a store of record: it migrates forward and is
// never deleted or rebuilt here. A project's exported records are also
// written to its `.agents/trail.jsonl`, which git holds; when git changes that
// file the file wins and replaces the project's rows (D21), so a lost store
// comes back from the project's own checkout.
package brain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

// migrations[i] takes the store from schema i to i+1 (PRAGMA user_version).
// Entries are only ever appended, as in internal/metrics: a released entry is
// never edited, because a store that already ran it would not run it again.
//
// rid is the rowid the external-content FTS table keys on: a table whose only
// key is a composite TEXT one has an implicit rowid that VACUUM may renumber,
// which would point every FTS row at another record. (scope, id) is the
// plan's primary key, kept as UNIQUE.
var migrations = []string{
	`CREATE TABLE records(
		rid INTEGER PRIMARY KEY,
		scope TEXT NOT NULL,
		id TEXT NOT NULL,
		kind TEXT NOT NULL,
		status TEXT NOT NULL,
		date TEXT NOT NULL,
		title TEXT NOT NULL,
		body TEXT NOT NULL,
		project TEXT NOT NULL DEFAULT '',
		updated TEXT NOT NULL,
		UNIQUE(scope, id));
	CREATE INDEX records_kind ON records(scope, kind);
	CREATE VIRTUAL TABLE records_fts USING fts5(title, body, content='records', content_rowid='rid', tokenize='porter unicode61');
	CREATE TRIGGER records_ai AFTER INSERT ON records BEGIN
		INSERT INTO records_fts(rowid, title, body) VALUES (new.rid, new.title, new.body);
	END;
	CREATE TRIGGER records_ad AFTER DELETE ON records BEGIN
		INSERT INTO records_fts(records_fts, rowid, title, body) VALUES ('delete', old.rid, old.title, old.body);
	END;
	CREATE TRIGGER records_au AFTER UPDATE ON records BEGIN
		INSERT INTO records_fts(records_fts, rowid, title, body) VALUES ('delete', old.rid, old.title, old.body);
		INSERT INTO records_fts(rowid, title, body) VALUES (new.rid, new.title, new.body);
	END;
	CREATE TABLE meta(scope TEXT PRIMARY KEY, export_sha TEXT, last_dream TEXT);`,
	`ALTER TABLE records ADD COLUMN provenance TEXT NOT NULL DEFAULT '';
	UPDATE records SET provenance = 'unknown' WHERE kind IN ('memory', 'feedback');`,
}

// Path is the store's location under home.
func Path(home string) string { return filepath.Join(home, harness.StateDir, "brain.db") }

type store struct {
	db   *sql.DB
	path string
}

// openStore opens or creates the store under home and migrates it forward.
// A store a newer binary wrote is refused before anything is written to it.
func openStore(home string) (*store, error) {
	path := Path(home)
	// 0700: ~/.config/fyr holds this store's global feedback and the other stores (search.tryOpen).
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// The same refusals as the search index and the metrics store: no
	// database or companion file that is not a regular file, all three 0600.
	if err := search.PrepareDBFiles(path); err != nil {
		return nil, refuse("brain: %v", err)
	}
	// Two processes opening a new store at once race to switch it to WAL,
	// and SQLite answers that race with SQLITE_BUSY without waiting on the
	// busy timeout: the open is retried for as long as that timeout.
	deadline := time.Now().Add(openWait)
	for {
		s, err := tryOpen(path)
		if err == nil || !isBusy(err) {
			return s, err
		}
		if time.Now().After(deadline) {
			return nil, refuse("the brain store is busy (another fyr holds its write lock); retry")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// openWait is how long a busy open is retried.
var openWait = 10 * time.Second

func tryOpen(path string) (*store, error) {
	db, err := sql.Open("sqlite", search.DSN(path))
	if err != nil {
		return nil, err
	}
	// One connection: a transaction's statements must reach the same one.
	db.SetMaxOpenConns(1)
	s := &store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *store) close() error { return s.db.Close() }

func (s *store) version(q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int, error) {
	var v int
	err := q.QueryRowContext(context.Background(), `PRAGMA user_version`).Scan(&v)
	return v, err
}

func (s *store) migrate() error {
	have, err := s.version(s.db)
	if err != nil {
		return fmt.Errorf("brain: %s: %w", s.path, err)
	}
	if have > len(migrations) {
		return refuse("brain.db schema %d is newer than this binary (%d); update fyr", have, len(migrations))
	}
	for have < len(migrations) {
		err := s.tx(func(c *sql.Conn) error {
			// Another process may have migrated while this one waited for
			// the lock: look again under it.
			v, err := s.version(c)
			if err != nil || v != have {
				have = v
				return err
			}
			if _, err := c.ExecContext(context.Background(), migrations[v]); err != nil {
				return fmt.Errorf("brain: migration %d: %w", v+1, err)
			}
			if _, err := c.ExecContext(context.Background(), fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
				return err
			}
			have = v + 1
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// tx runs fn inside BEGIN IMMEDIATE on one connection: every write, and every
// load, holds SQLite's write lock from its first read to its commit, so two
// processes never allocate the same number or export over each other (D20:
// no lock file). The driver's busy timeout is the wait.
func (s *store) tx(fn func(c *sql.Conn) error) error {
	ctx := context.Background()
	c, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		if isBusy(err) {
			return refuse("the brain store is busy (another fyr holds its write lock); retry")
		}
		return err
	}
	if err := fn(c); err != nil {
		c.ExecContext(ctx, `ROLLBACK`)
		return err
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		c.ExecContext(ctx, `ROLLBACK`)
		return err
	}
	return nil
}

// sqliteCode is the primary SQLite result code of err, or 0.
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

// refusal is input or state the store will not take: the CLI prints it and
// exits 1.
type refusal struct{ msg string }

func (r *refusal) Error() string { return r.msg }

func refuse(format string, a ...any) error { return &refusal{msg: fmt.Sprintf(format, a...)} }

// IsRefusal reports whether err is a refusal (exit 1).
func IsRefusal(err error) bool {
	var r *refusal
	return errors.As(err, &r)
}

// usageError is a verb given arguments outside its grammar: exit 2.
type usageError struct{ msg string }

func (u *usageError) Error() string { return u.msg }

func usage(format string, a ...any) error { return &usageError{msg: fmt.Sprintf(format, a...)} }

// IsUsage reports whether err is a usage error (exit 2).
func IsUsage(err error) bool {
	var u *usageError
	return errors.As(err, &u)
}
