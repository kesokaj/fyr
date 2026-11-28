package metrics

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	dir := t.TempDir()
	s := openT(t, filepath.Join(dir, "metrics.db"))
	for _, q := range []string{
		`INSERT INTO sessions(id, vendor, started_at) VALUES ('s1', 'claude', 1)`,
		`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('s1', 'tu1', 2, 'tool', 'Bash')`,
		`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES ('s1', 'tu1', 0, 'git status')`,
		`INSERT INTO events(session_id, ts, kind, ref, value) VALUES ('s1', 3, 'commit', 'abc', '')`,
		`INSERT INTO ingest_state(source, size, mtime, offset) VALUES ('/x.jsonl', 10, 4, 10)`,
		`INSERT INTO trials(run, label, harness, status, cause, started_at, seconds) VALUES ('s/r/1', 'eval=r', 'abc1234', 'pass', '', 5, 9)`,
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
	for _, tbl := range []string{"meta", "sessions", "steps", "step_verbs", "events", "ingest_state", "trials"} {
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

// A store at schema 2 that holds Go's zero time from before BUG-181 is
// repaired by the upgrade: the time columns become NULL and the ingest mtime
// 0, so the conversation is not re-read into a zero end time.
func TestMigrationRepairsZeroTimes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "metrics.db")
	saved := migrations
	t.Cleanup(func() { migrations = saved })
	migrations = saved[:2]
	s := openT(t, p)
	const zero = -62135596800000
	s.db.Exec(`INSERT INTO sessions(id, vendor, started_at, ended_at) VALUES ('z', 'agy', ?, ?), ('ok', 'agy', 5, 7)`, zero, zero)
	s.db.Exec(`INSERT INTO ingest_state(source, size, mtime, offset) VALUES ('z.db', 1, ?, 0)`, zero)
	s.Close()

	migrations = saved
	s2 := openT(t, p)
	var got string
	if err := s2.db.QueryRow(`SELECT (SELECT count(*) FROM sessions WHERE id = 'z' AND started_at IS NULL AND ended_at IS NULL) || ',' ||
		(SELECT ended_at FROM sessions WHERE id = 'ok') || ',' || (SELECT mtime FROM ingest_state WHERE source = 'z.db')`).Scan(&got); err != nil || got != "1,7,0" {
		t.Fatalf("after upgrade: %q, %v; want 1,7,0", got, err)
	}
}

// The export leaves out the cmd_hash salt: with the salt, anyone holding an
// export could confirm a guessed low-entropy command (security review
// 2026-10-08). The schema row stays, so an export names its schema.
func TestExportOmitsSalt(t *testing.T) {
	t.Parallel()
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

// TestTxTakesWriteLockAtBegin pins the store's transactions to BEGIN
// IMMEDIATE: the join opens with reads (the ref rewrites), and a deferred
// transaction that reads first fails at its first write, without waiting,
// when another ingest committed in between (review of 811442c..33c652a,
// finding 1). Here a second connection that never waits must find the
// store locked while the first transaction is open, before it wrote.
func TestTxTakesWriteLockAtBegin(t *testing.T) {
	t.Parallel()
	p := Path(t.TempDir())
	s := openT(t, p)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", "file:"+p+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Exec(`INSERT INTO meta(key, value) VALUES ('probe', 'x')`); err == nil {
		t.Fatal("a second connection wrote while the store's transaction was open: the transaction is deferred")
	}
}

// The first open of a new store waits for a lock another connection holds:
// switching a new file to WAL upgrades a read transaction to a write, and
// SQLite answers that upgrade SQLITE_BUSY at once, without its busy handler.
func TestFirstOpenWaitsForALock(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "metrics.db")
	holder, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	holder.SetMaxOpenConns(1)
	// The condition must hold: a file not yet in WAL mode, and a write lock
	// held on it.
	var mode string
	if err := holder.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("journal mode %q, %v", mode, err)
	}
	if _, err := holder.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(300*time.Millisecond, func() { holder.Exec(`ROLLBACK`) })
	began := time.Now()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("after %v: %v", time.Since(began), err)
	}
	s.Close()
}

func TestDailyDigestsMigrationSchema12AndExport(t *testing.T) {
	t.Parallel()
	if len(tables) != 14 {
		t.Fatalf("len(tables) = %d, want 14 (%v)", len(tables), tables)
	}
	dir := t.TempDir()
	s := openT(t, filepath.Join(dir, "metrics.db"))
	if got := s.meta(t, "schema"); got != "14" {
		t.Fatalf("schema = %q, want 14", got)
	}
	if empty, err := LatestDailyDigest(s); err != nil || empty != nil {
		t.Fatalf("empty LatestDailyDigest = (%+v, %v), want (nil, nil)", empty, err)
	}
	d1 := DailyDigest{Day: "2026-11-19", Verdict: VerdictSteady, CreatedAt: 100}
	d2 := DailyDigest{Day: "2026-11-20", Verdict: VerdictImproving, CreatedAt: 200}
	if err := SaveDailyDigest(s, d1); err != nil {
		t.Fatalf("SaveDailyDigest d1: %v", err)
	}
	if err := SaveDailyDigest(s, d2); err != nil {
		t.Fatalf("SaveDailyDigest d2: %v", err)
	}
	// Upsert same day overwrites cleanly.
	d2.Verdict = VerdictRegress
	d2.CreatedAt = 250
	if err := SaveDailyDigest(s, d2); err != nil {
		t.Fatalf("SaveDailyDigest upsert d2: %v", err)
	}
	latest, err := LatestDailyDigest(s)
	if err != nil || latest == nil || latest.Day != "2026-11-20" || latest.Verdict != VerdictRegress || latest.CreatedAt != 250 {
		t.Fatalf("LatestDailyDigest = (%+v, %v)", latest, err)
	}
	prev, err := PreviousDailyDigest(s, "2026-11-20")
	if err != nil || prev == nil || prev.Day != "2026-11-19" || prev.Verdict != VerdictSteady {
		t.Fatalf("PreviousDailyDigest = (%+v, %v)", prev, err)
	}
	if none, err := PreviousDailyDigest(s, "2026-11-19"); err != nil || none != nil {
		t.Fatalf("PreviousDailyDigest before earliest = (%+v, %v), want (nil, nil)", none, err)
	}
	out := filepath.Join(dir, "export12.jsonl")
	if err := s.Export(out); err != nil {
		t.Fatalf("Export: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"table":"daily_digests"`)) || !bytes.Contains(raw, []byte(`"value":"14"`)) {
		t.Fatalf("export missing daily_digests or schema 14:\n%s", raw)
	}
}

func TestV910MetricsV14Migration(t *testing.T) {
	if len(tables) != 14 {
		t.Fatalf("len(tables) = %d, want 14 (%v)", len(tables), tables)
	}
	saved := migrations
	t.Cleanup(func() { migrations = saved })
	if len(saved) < 14 {
		t.Fatalf("len(migrations) = %d, want >= 14", len(saved))
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "metrics.db")
	seedV13ForV14Upgrade(t, dbPath, saved[:13])

	migrations = saved
	s14 := openT(t, dbPath)
	if got := s14.meta(t, "schema"); got != "14" {
		t.Fatalf("schema = %q, want 14", got)
	}
	assertV14SchemaAndDefaults(t, s14, dir)

	if _, err := s14.db.Exec(`UPDATE meta SET value = '15' WHERE key = 'schema'`); err != nil {
		t.Fatal(err)
	}
	s14.Close()
	if s15, err := Open(dbPath); err == nil {
		s15.Close()
		t.Fatal("Open accepted schema 15")
	} else if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("schema 15 err = %v, want ErrNewerSchema", err)
	}
}

func seedV13ForV14Upgrade(t *testing.T, dbPath string, v13Migs []string) {
	t.Helper()
	migrations = v13Migs
	s13 := openT(t, dbPath)
	if got := s13.meta(t, "schema"); got != "13" {
		t.Fatalf("v13 schema = %q, want 13", got)
	}
	for _, q := range []string{
		`INSERT INTO background_runs(ts, kind, outcome, duration_ms, projects_checked) VALUES (1700000000000, 'auto', 'pass', 420, 2)`,
		`INSERT INTO turn_vets(project_root, repo_id, conversation_id, turn_num, intent, gate, score, created_at) VALUES ('/repo/a', 'git:github.com/org/a', 'conv-v13', 1, 'TASK', 'PROCEED', 88, 1700000000000)`,
		`INSERT INTO trials(run, label, harness, status, cause, started_at, seconds, policy_model) VALUES ('r13/a/1', 'eval=a', 'v9.9.0', 'pass', '', 1700000000000, 9, 'gemini-4-neon-high')`,
	} {
		if _, err := s13.db.Exec(q); err != nil {
			t.Fatalf("seed v13 (%s): %v", q, err)
		}
	}
	for _, absent := range []string{
		`SELECT halt_reason FROM background_runs`,
		`SELECT suggested_skills FROM turn_vets`,
		`SELECT skill, arm, tokens, defects FROM trials`,
		`SELECT id FROM orchestration_runs`,
	} {
		if _, err := s13.db.Exec(absent); err == nil {
			t.Fatalf("v13 fixture unexpectedly accepted %s", absent)
		}
	}
	s13.Close()
}

func assertV14SchemaAndDefaults(t *testing.T, s *Store, dir string) {
	t.Helper()
	var hr, sk, tSkill, tArm string
	var tTok int64
	var tDef int
	if err := s.db.QueryRow(`SELECT halt_reason FROM background_runs WHERE ts = 1700000000000`).Scan(&hr); err != nil || hr != "" {
		t.Fatalf("v13 background_runs.halt_reason = (%q, %v), want empty", hr, err)
	}
	if err := s.db.QueryRow(`SELECT suggested_skills FROM turn_vets WHERE conversation_id = 'conv-v13'`).Scan(&sk); err != nil || sk != "" {
		t.Fatalf("v13 turn_vets.suggested_skills = (%q, %v), want empty", sk, err)
	}
	if err := s.db.QueryRow(`SELECT skill, arm, tokens, defects FROM trials WHERE run = 'r13/a/1'`).Scan(&tSkill, &tArm, &tTok, &tDef); err != nil || tSkill != "" || tArm != "" || tTok != 0 || tDef != 0 {
		t.Fatalf("v13 trials v14 defaults = (%q, %q, %d, %d, %v), want zero defaults", tSkill, tArm, tTok, tDef, err)
	}
	var idxCnt int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND tbl_name='orchestration_runs'`).Scan(&idxCnt); err != nil || idxCnt < 1 {
		t.Fatalf("orchestration_runs indexes = %d, err=%v; want >= 1", idxCnt, err)
	}
	if err := s.InsertOrchestrationRun(OrchestrationRunRow{
		ID: "orch-v14-1", TS: "2026-11-22T20:00:00Z", Kind: "relay", Status: "COMPLETE", Units: 2, Passed: 2, DurationMS: 1500,
	}); err != nil {
		t.Fatalf("InsertOrchestrationRun: %v", err)
	}
	out := filepath.Join(dir, "export14.jsonl")
	if err := s.Export(out); err != nil {
		t.Fatalf("Export: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"table":"orchestration_runs"`)) || !bytes.Contains(raw, []byte(`"value":"14"`)) {
		t.Fatalf("export missing orchestration_runs or schema 14:\n%s", raw)
	}
}
