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
	"strings"
	"time"

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
	`ALTER TABLE records ADD COLUMN memory_kind TEXT NOT NULL DEFAULT '';
	ALTER TABLE records ADD COLUMN validity REAL NOT NULL DEFAULT 1.0;
	ALTER TABLE records ADD COLUMN verified_at TEXT NOT NULL DEFAULT '';
	ALTER TABLE records ADD COLUMN evidence_count INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE records ADD COLUMN source_sessions TEXT NOT NULL DEFAULT '';
	UPDATE records SET memory_kind = CASE WHEN kind = 'memory' AND body LIKE 'type: feedback%' THEN 'procedural' WHEN kind = 'memory' AND body LIKE 'type: procedural%' THEN 'procedural' WHEN kind = 'memory' AND body LIKE 'type: episodic%' THEN 'episodic' WHEN kind = 'memory' THEN 'semantic' ELSE '' END, validity = CASE WHEN kind = 'memory' AND provenance = 'derived' THEN 0.55 WHEN kind = 'memory' AND provenance = 'unknown' THEN 0.70 WHEN kind = 'memory' THEN 1.0 ELSE 1.0 END, verified_at = CASE WHEN kind = 'memory' THEN updated ELSE '' END;`,
	`ALTER TABLE records ADD COLUMN repo_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE meta ADD COLUMN repo_id TEXT NOT NULL DEFAULT '';
	CREATE INDEX IF NOT EXISTS records_repo_id ON records(repo_id);`,
	`CREATE TABLE sim_problems(
		pid             INTEGER PRIMARY KEY,
		id              TEXT NOT NULL UNIQUE,
		root_cause      TEXT NOT NULL,
		norm_root_cause TEXT NOT NULL,
		mechanism_class TEXT NOT NULL DEFAULT '',
		signal_id       TEXT NOT NULL DEFAULT '',
		target_paths    TEXT NOT NULL DEFAULT '',
		status          TEXT NOT NULL DEFAULT 'active',
		provenance      TEXT NOT NULL DEFAULT 'derived',
		opened_at       TEXT NOT NULL,
		updated_at      TEXT NOT NULL,
		opened_harness  TEXT NOT NULL DEFAULT '',
		opened_model    TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE sim_aliases(
		old_id    TEXT PRIMARY KEY,
		target_id TEXT NOT NULL,
		merged_at TEXT NOT NULL
	);
	CREATE TABLE sim_attempts(
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		problem_id        TEXT NOT NULL,
		source_kind       TEXT NOT NULL,
		proposal_id       TEXT NOT NULL DEFAULT '',
		commit_sha        TEXT NOT NULL DEFAULT '',
		parent_sha        TEXT NOT NULL DEFAULT '',
		harness_version   TEXT NOT NULL DEFAULT '',
		proposer_model    TEXT NOT NULL DEFAULT '',
		policy_model      TEXT NOT NULL DEFAULT '',
		project_scope     TEXT NOT NULL DEFAULT '',
		files_json        TEXT NOT NULL DEFAULT '[]',
		what              TEXT NOT NULL,
		predicted_rows    TEXT NOT NULL DEFAULT '[]',
		predicted_signals TEXT NOT NULL DEFAULT '[]',
		eval_verdict      TEXT NOT NULL DEFAULT 'none',
		fixed_rows        TEXT NOT NULL DEFAULT '[]',
		missed_rows       TEXT NOT NULL DEFAULT '[]',
		regressed_rows    TEXT NOT NULL DEFAULT '[]',
		eval_passed       INTEGER NOT NULL DEFAULT 0,
		eval_total        INTEGER NOT NULL DEFAULT 0,
		field_status      TEXT NOT NULL DEFAULT 'n/a',
		field_metric      TEXT NOT NULL DEFAULT '',
		field_baseline    REAL,
		field_observed    REAL,
		field_models_json TEXT NOT NULL DEFAULT '[]',
		provenance        TEXT NOT NULL DEFAULT 'derived',
		created_at        TEXT NOT NULL,
		updated_at        TEXT NOT NULL
	);
	CREATE INDEX sim_attempts_problem ON sim_attempts(problem_id, created_at DESC);
	CREATE INDEX sim_problems_signal  ON sim_problems(signal_id);
	CREATE VIRTUAL TABLE sim_fts USING fts5(root_cause, target_paths, content='sim_problems', content_rowid='pid', ` + "token" + `ize='porter unicode61');
	CREATE TRIGGER sim_problems_ai AFTER INSERT ON sim_problems BEGIN
		INSERT INTO sim_fts(rowid, root_cause, target_paths) VALUES (new.pid, new.root_cause, new.target_paths);
	END;
	CREATE TRIGGER sim_problems_ad AFTER DELETE ON sim_problems BEGIN
		INSERT INTO sim_fts(sim_fts, rowid, root_cause, target_paths) VALUES ('delete', old.pid, old.root_cause, old.target_paths);
	END;
	CREATE TRIGGER sim_problems_au AFTER UPDATE ON sim_problems BEGIN
		INSERT INTO sim_fts(sim_fts, rowid, root_cause, target_paths) VALUES ('delete', old.pid, old.root_cause, old.target_paths);
		INSERT INTO sim_fts(rowid, root_cause, target_paths) VALUES (new.pid, new.root_cause, new.target_paths);
	END;`,
}

// Path is the store's location under home, or empty when home is empty.
func Path(home string) string {
	if strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, harness.StateDir, "brain.db")
}

type store struct {
	db         *sql.DB
	path       string
	cleanupDir string
}

// openStore opens or creates the store under home and migrates it forward.
// A store a newer binary wrote is refused before anything is written to it.
func openStore(home string) (*store, error) {
	if strings.TrimSpace(home) == "" {
		return nil, refuse("brain: home directory is required")
	}
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
	var s *store
	if err := openDBBusyRetry(func() (*sql.DB, error) {
		opened, err := tryOpen(path)
		if err != nil {
			return nil, err
		}
		s = opened
		return opened.db, nil
	}); err != nil {
		return nil, err
	}
	return s, nil
}

// openWait is how long a busy open is retried.
var openWait = 10 * time.Second

func openDBBusyRetry(fn func() (*sql.DB, error)) error {
	_, err := search.OpenDBWithRetry(openWait, fn)
	if err != nil && search.IsBusy(err) {
		return refuse("the brain store is busy (another fyr holds its write lock); retry")
	}
	return err
}

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
	if err := s.ensureTurnRecordsIndex(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// ensureTurnRecordsIndex idempotently creates the composite index used by
// ListTurnRecordsCtx without bumping user_version, matching search.ensureRefsOIDIndex
// so existing v4 stores and read-only readers stay compatible (ADR-532).
func (s *store) ensureTurnRecordsIndex() error {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'records_scope_kind_status_date'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	return s.tx(func(c *sql.Conn) error {
		_, err := c.ExecContext(context.Background(), `CREATE INDEX IF NOT EXISTS records_scope_kind_status_date ON records(scope, kind, status, date)`)
		return err
	})
}

// openStoreReadOnly opens an existing store under home read-only without
// running migrations, returning its PRAGMA user_version. A store written by a
// newer binary is refused.
func openStoreReadOnly(home string) (*store, int, error) {
	if strings.TrimSpace(home) == "" {
		return nil, 0, refuse("brain: home directory is required")
	}
	path := Path(home)
	if err := checkRegularReadOnly(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, err
		}
		return nil, 0, refuse("brain: %v", err)
	}
	var s *store
	var ver int
	if err := openDBBusyRetry(func() (*sql.DB, error) {
		opened, v, err := tryOpenReadOnly(path)
		if err != nil {
			return nil, err
		}
		s, ver = opened, v
		return opened.db, nil
	}); err != nil {
		return nil, 0, err
	}
	return s, ver, nil
}

func checkRegularReadOnly(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		fi, err := os.Lstat(p)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if p == path {
				return err
			}
		case err != nil:
			return err
		case !fi.Mode().IsRegular():
			return fmt.Errorf("%s is not a regular file; refusing to open", filepath.Base(p))
		}
	}
	return nil
}

func tryOpenReadOnly(path string) (*store, int, error) {
	tmp, err := os.MkdirTemp(filepath.Dir(path), ".brain-ro-")
	if err != nil {
		return nil, 0, err
	}
	db, err := search.OpenCopy(filepath.Dir(path), filepath.Base(path), tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return nil, 0, err
	}
	db.SetMaxOpenConns(1)
	s := &store{db: db, path: path, cleanupDir: tmp}
	ver, err := s.version(s.db)
	if err != nil {
		s.close()
		return nil, 0, fmt.Errorf("brain: %s: %w", s.path, err)
	}
	if ver > len(migrations) {
		s.close()
		return nil, 0, refuse("brain.db schema %d is newer than this binary (%d); update fyr", ver, len(migrations))
	}
	return s, ver, nil
}

func (s *store) close() error {
	err := s.db.Close()
	if s.cleanupDir != "" {
		_ = os.RemoveAll(s.cleanupDir)
	}
	return err
}

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
		if search.IsBusy(err) {
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
