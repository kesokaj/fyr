// Package metrics keeps the metrics store, ~/.config/fyr/metrics.db: the
// shape of every ingested Antigravity session, and of the Claude Code
// sessions an ingest before ADR-062 read, kept as history (R4, ADR-055, plan
// fyr D7, spec SPEC-fyr-metrics). Unlike the search index it is
// a store of record, the only copy once a transcript expires: it migrates
// forward and is never deleted or rebuilt here.
package metrics

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

// Store is one open metrics store.
type Store struct {
	db   *sql.DB
	path string
}

// ErrNewerSchema is a store written by a newer binary: refused, untouched.
var ErrNewerSchema = errors.New("newer schema")

// migrations[i] takes the store from schema i to i+1. Entries are only ever
// appended: a released entry is never edited, because a store that already
// ran it would not run it again (D5). A variable so a test can append one.
var migrations = []string{
	`CREATE TABLE sessions(id TEXT PRIMARY KEY, vendor TEXT, project_root TEXT, parent_id TEXT,
		agent_type TEXT, model TEXT, label TEXT, outcome TEXT, started_at INTEGER, ended_at INTEGER,
		cost_usd REAL, duration_ms INTEGER);
	CREATE TABLE steps(session_id TEXT, ext_id TEXT, ts INTEGER, kind TEXT, tool TEXT, skill TEXT,
		target_kind TEXT, is_error INTEGER, denied INTEGER, tok_in INTEGER, tok_out INTEGER,
		cache_read INTEGER, cache_write INTEGER, cmd_hash TEXT, PRIMARY KEY(session_id, ext_id));
	CREATE INDEX steps_ts ON steps(session_id, ts);
	CREATE TABLE step_verbs(session_id TEXT, ext_id TEXT, n INTEGER, verb TEXT,
		PRIMARY KEY(session_id, ext_id, n));
	CREATE TABLE events(session_id TEXT, ts INTEGER, kind TEXT, ref TEXT, value TEXT,
		PRIMARY KEY(session_id, kind, ref));
	CREATE TABLE ingest_state(source TEXT PRIMARY KEY, size INTEGER, mtime INTEGER, offset INTEGER);`,
	// 2: the context a model call saw, where the CLI records it (Antigravity's
	// gen_metadata, plan D3a); a historical claude row's is computed from
	// usage at read time.
	`ALTER TABLE steps ADD COLUMN context INTEGER;`,
	// 3: Go's zero time stored before BUG-181 (a conversation Antigravity
	// never dated) is no time; mtime 0 matches what ingest now reads for it.
	`UPDATE sessions SET started_at = NULL WHERE started_at < 0;
	UPDATE sessions SET ended_at = NULL WHERE ended_at < 0;
	UPDATE ingest_state SET mtime = 0 WHERE mtime < 0;`,
	// 4: the trial, one run of a suite row, as the unit every rate reads
	// (the eval-metrics plan D13): its row exists whether or not a session
	// ran, and a session names the trial it belongs to. A session stored
	// before, or by a kit's ingest, keeps run NULL and counts as one trial
	// of its own.
	`CREATE TABLE trials(run TEXT PRIMARY KEY, label TEXT NOT NULL, row_version INTEGER, kind TEXT,
		cluster TEXT, expect TEXT, harness TEXT NOT NULL, cli TEXT, status TEXT NOT NULL, cause TEXT NOT NULL,
		started_at INTEGER NOT NULL, seconds INTEGER NOT NULL, session_seconds INTEGER);
	ALTER TABLE sessions ADD COLUMN run TEXT;`,
	// 5: per-step start, end and dialog-wait times from Antigravity's
	// steps.metadata (timestamps 6, 7, 8 and status-history field 26), and
	// per-trial setup and check seconds (plan fyr-speed Item 7, § Schema).
	`ALTER TABLE steps ADD COLUMN started_ms INTEGER;
	ALTER TABLE steps ADD COLUMN ended_ms INTEGER;
	ALTER TABLE steps ADD COLUMN wait_ms INTEGER;
	ALTER TABLE trials ADD COLUMN setup_seconds INTEGER;
	ALTER TABLE trials ADD COLUMN check_seconds INTEGER;`,
	// 6: second-brain turn vetting and read-ledger savings from per-project
	// .agents/scratch/search.db, plus fyr work wave/judge telemetry from
	// ~/.config/fyr/work/<stamp>/status.json (plan v8.5.0 D1, D3).
	`CREATE TABLE turn_vets(
		project_root TEXT NOT NULL,
		conversation_id TEXT NOT NULL,
		turn_num INTEGER NOT NULL,
		intent TEXT NOT NULL,
		gate TEXT NOT NULL,
		score INTEGER NOT NULL,
		bypassed INTEGER NOT NULL DEFAULT 0,
		skills_count INTEGER NOT NULL DEFAULT 0,
		pivots_count INTEGER NOT NULL DEFAULT 0,
		ledger_saved INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		PRIMARY KEY(project_root, conversation_id, turn_num)
	);
	CREATE TABLE work_waves(
		stamp TEXT NOT NULL,
		worker TEXT NOT NULL,
		project_root TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		step_count INTEGER NOT NULL DEFAULT 0,
		commits INTEGER NOT NULL DEFAULT 0,
		base_check TEXT NOT NULL DEFAULT '',
		judge_model TEXT NOT NULL DEFAULT '',
		judge_verdict TEXT NOT NULL DEFAULT '',
		updated_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(stamp, worker)
	);`,
	// 7: per-worker stage seconds (setup_s, agy_s, check_s) in work_waves
	// from ~/.config/fyr/work/<stamp>/status.json (Phase 7, D17).
	`ALTER TABLE work_waves ADD COLUMN setup_s INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE work_waves ADD COLUMN agy_s INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE work_waves ADD COLUMN check_s INTEGER NOT NULL DEFAULT 0;`,
	// 8: stale-conversation outcome and token-efficiency ratings written by
	// dream/auto (ADR-524).
	`CREATE TABLE session_ratings(
		session_id TEXT PRIMARY KEY,
		project_root TEXT NOT NULL DEFAULT '',
		evaluated_at INTEGER NOT NULL,
		outcome_score INTEGER NOT NULL,
		token_efficiency INTEGER NOT NULL,
		plan_alignment TEXT NOT NULL,
		skill_action TEXT NOT NULL,
		target_skill TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX session_ratings_eval ON session_ratings(evaluated_at);`,
	// 9: cross-family gate/judge verdict ledger (v9.2 Phase 1B).
	`CREATE TABLE judge_verdicts(
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts INTEGER NOT NULL,
		project_root TEXT NOT NULL DEFAULT '',
		target TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL,
		fallback_reason TEXT NOT NULL DEFAULT '',
		criterion_hash TEXT NOT NULL,
		verdict TEXT NOT NULL
	);
	CREATE INDEX judge_verdicts_ts ON judge_verdicts(ts);`,
	// 10: shape-only dream/auto background runs ledger (v9.4 Phase 2, ADR-528).
	`CREATE TABLE background_runs(id INTEGER PRIMARY KEY, ts INTEGER NOT NULL, kind TEXT NOT NULL, outcome TEXT NOT NULL, duration_ms INTEGER NOT NULL, projects_checked INTEGER NOT NULL DEFAULT 0, proposals_written INTEGER NOT NULL DEFAULT 0, ratings_written INTEGER NOT NULL DEFAULT 0, worker_ran INTEGER NOT NULL DEFAULT 0, applied INTEGER NOT NULL DEFAULT 0); CREATE INDEX background_runs_ts ON background_runs(ts);`,
	// 11: canonical repo_id across sessions/verdicts/vets/ratings, worker_model
	// attribution on judge_verdicts, and extended turn_vets columns (ADR-529, ADR-531).
	`ALTER TABLE sessions ADD COLUMN repo_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE judge_verdicts ADD COLUMN repo_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE judge_verdicts ADD COLUMN worker_model TEXT NOT NULL DEFAULT '';
	ALTER TABLE judge_verdicts ADD COLUMN worker_session_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE turn_vets ADD COLUMN repo_id TEXT NOT NULL DEFAULT '';
	ALTER TABLE turn_vets ADD COLUMN directive TEXT NOT NULL DEFAULT '';
	ALTER TABLE turn_vets ADD COLUMN subtask_count INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE turn_vets ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE turn_vets ADD COLUMN timed_out INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE turn_vets ADD COLUMN injected_bytes INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE turn_vets ADD COLUMN pivots_used_count INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE turn_vets ADD COLUMN skills_loaded_count INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE session_ratings ADD COLUMN repo_id TEXT NOT NULL DEFAULT '';
	CREATE INDEX IF NOT EXISTS sessions_repo_id ON sessions(repo_id);`,
	// 12: shape-only daily health digests and post-fix recovery snapshots (D7).
	`CREATE TABLE daily_digests(day TEXT PRIMARY KEY, verdict TEXT NOT NULL, kpi_json TEXT NOT NULL, ast_json TEXT NOT NULL, preinv_json TEXT NOT NULL, actions_json TEXT NOT NULL, created_at INTEGER NOT NULL); CREATE INDEX daily_digests_created ON daily_digests(created_at);`,
}

// tables is every table export writes, in dependency order.
var tables = []string{"meta", "sessions", "steps", "step_verbs", "events", "ingest_state", "trials", "turn_vets", "work_waves", "session_ratings", "judge_verdicts", "background_runs", "daily_digests"}

// Path is the store's location under home.
func Path(home string) string { return filepath.Join(home, harness.StateDir, "metrics.db") }

// Open opens or creates the store at path and migrates it forward. A store
// whose schema is newer than this binary's is refused before anything is
// written to it.
func Open(path string) (*Store, error) {
	// 0700: ~/.config/fyr holds the brain store's global feedback (search.tryOpen).
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := search.PrepareDBFiles(path); err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	// Two processes opening a new store at once race to switch it to WAL,
	// and SQLite answers that race with SQLITE_BUSY without waiting on the
	// busy timeout: the open is retried for as long as that timeout, as the
	// brain store and the search index do (BUG-187).
	deadline := time.Now().Add(openWait)
	for {
		s, err := open(path)
		if err == nil || !search.IsBusy(err) || time.Now().After(deadline) {
			return s, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// openWait is how long a busy open is retried.
var openWait = 10 * time.Second

// open is one attempt of Open.
func open(path string) (*Store, error) {
	// _txlock=immediate: every transaction here writes, and the join's opens
	// with reads; a deferred one would fail at its first write, without
	// waiting, when another ingest committed in between.
	db, err := sql.Open("sqlite", search.DSN(path)+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	// One connection: a transaction's statements must reach the same one.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenMemory opens a transient in-memory schema-migrated Store for diskless dry-run previews.
func OpenMemory() (*Store, error) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(0)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: ":memory:"}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	have, hasMeta, err := readSchemaVersion(s.db, s.path)
	if err != nil {
		return err
	}
	if have > len(migrations) {
		return fmt.Errorf("metrics.db schema %d is newer than this binary (%d); update fyr: %w", have, len(migrations), ErrNewerSchema)
	}
	if hasMeta && have == len(migrations) && s.hasSalt() {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.applyMigrationsTx(tx); err != nil {
		return err
	}
	return tx.Commit()
}

type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

func readSchemaVersion(q rowQuerier, path string) (int, bool, error) {
	var v string
	err := q.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, true, nil
	case err != nil && strings.Contains(err.Error(), "no such table"):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("metrics: %s: %w", path, err)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false, fmt.Errorf("metrics: %s: unreadable schema %q", path, v)
	}
	return n, true, nil
}

func (s *Store) applyMigrationsTx(tx *sql.Tx) error {
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		return fmt.Errorf("metrics: %s: %w", s.path, err)
	}
	have, _, err := readSchemaVersion(tx, s.path)
	if err != nil {
		return err
	}
	if have > len(migrations) {
		return fmt.Errorf("metrics.db schema %d is newer than this binary (%d); update fyr: %w", have, len(migrations), ErrNewerSchema)
	}
	for i := have; i < len(migrations); i++ {
		if _, err := tx.Exec(migrations[i]); err != nil {
			return fmt.Errorf("metrics: migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO meta(key, value) VALUES ('schema', ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, strconv.Itoa(i+1)); err != nil {
			return err
		}
	}
	return ensureSaltTx(tx)
}

func (s *Store) hasSalt() bool {
	var v string
	return s.db.QueryRow(`SELECT value FROM meta WHERE key = 'salt'`).Scan(&v) == nil && v != ""
}

// ensureSalt gives the store its cmd_hash salt once; it never changes, or
// retry loops across two ingests would not compare (D6).
func ensureSaltTx(tx *sql.Tx) error {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO meta(key, value) VALUES ('salt', ?) ON CONFLICT(key) DO NOTHING`, hex.EncodeToString(b))
	return err
}

// Close closes the store.
func (s *Store) Close() error { return s.db.Close() }

// Export writes every row of every table but the salt to file as JSONL, one
// {"table": …, "row": {…}} per line, mode 0600. An existing file is refused:
// the export is the store's only backup and is never overwritten by mistake.
func (s *Store) Export(file string) error {
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("refusing to overwrite %s", file)
	}
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, t := range tables {
		if err := s.exportTable(enc, t); err != nil {
			f.Close()
			_ = os.Remove(file)
			return err
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(file)
		return err
	}
	return nil
}

func (s *Store) exportTable(enc *json.Encoder, table string) error {
	q := `SELECT * FROM ` + table + ` ORDER BY rowid`
	if table == "meta" {
		// The salt stays in the store: with it, an export would let anyone
		// confirm a guessed command against cmd_hash (security review
		// 2026-10-08).
		q = `SELECT * FROM meta WHERE key <> 'salt' ORDER BY rowid`
	}
	rows, err := s.db.Query(q)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			row[c] = vals[i]
		}
		if err := enc.Encode(struct {
			Table string         `json:"table"`
			Row   map[string]any `json:"row"`
		}{table, row}); err != nil {
			return err
		}
	}
	return rows.Err()
}
