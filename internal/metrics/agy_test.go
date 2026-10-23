package metrics

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func pbV(n int, v uint64) []byte {
	return binary.AppendUvarint(binary.AppendUvarint(nil, uint64(n<<3)), v)
}

func pbB(n int, b []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(n<<3|2))
	out = binary.AppendUvarint(out, uint64(len(b)))
	return append(out, b...)
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// agyStepMeta is a step's metadata as the spike found it (D35): the time at
// 1→1, a tool's name at 4→2 and its arguments JSON at 4→3.
func agyStepMeta(sec uint64, tool string, args any) []byte {
	m := pbB(1, pbV(1, sec))
	if tool != "" {
		a, _ := json.Marshal(args)
		m = cat(m, pbB(4, cat(pbB(2, []byte(tool)), pbB(3, a))))
	}
	return m
}

// agyGen is one gen_metadata row (D3a): usage at 1→4, the model at 1→19, the
// context used at 1→9→10→1.
func agyGen(in, out, cached, ctx uint64) []byte {
	usage := cat(pbV(2, in), pbV(3, out))
	if cached > 0 {
		usage = cat(usage, pbV(5, cached))
	}
	return pbB(1, cat(pbB(4, usage), pbB(9, pbB(10, cat(pbV(1, ctx), pbV(4, 256000)))), pbB(19, []byte("gemini-test"))))
}

func sqliteT(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// WAL, so an open of the live file in place would have to create -shm
	// beside it: in the read-only directory below that fails, and the test
	// sees it.
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// agyFixture writes a home with one Antigravity conversation C1 in root (a
// user turn, a reply, a run_command, a failed view_file of a SKILL.md, a
// view_file of a plan) and its explorer subagent C2.
func agyFixture(t *testing.T) (home, root string) {
	t.Helper()
	home = t.TempDir()
	root = filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(filepath.Join(root, ".agents", "docs", "plans"), 0o755)
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	os.MkdirAll(filepath.Join(dir, "conversations"), 0o755)
	sqliteT(t, filepath.Join(dir, "conversation_summaries.db"),
		"CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL, `agent_name` text NOT NULL DEFAULT '', `parent_conversation_id` text NOT NULL DEFAULT '')",
		`INSERT INTO conversation_summaries VALUES ('C1', '2026-10-08 10:05:00+00:00', '["file://`+root+`"]', '', '')`,
		`INSERT INTO conversation_summaries VALUES ('C2', '2026-10-08 10:04:00+00:00', '["file://`+root+`"]', 'explorer', 'C1')`,
		`INSERT INTO conversation_summaries VALUES ('../x', '2026-10-08 10:04:00+00:00', '[]', '', '')`,
	)
	conv := func(id string, steps [][]any, gens [][]byte) {
		p := filepath.Join(dir, "conversations", id+".db")
		sqliteT(t, p,
			"CREATE TABLE steps (`idx` integer, `step_type` integer NOT NULL DEFAULT 0, `status` integer NOT NULL DEFAULT 0, `metadata` blob, `error_details` blob, `step_payload` blob)",
			"CREATE TABLE gen_metadata (`idx` integer, `data` blob, `size` integer NOT NULL DEFAULT 0)")
		db, _ := sql.Open("sqlite", p)
		defer db.Close()
		for i, s := range steps {
			db.Exec(`INSERT INTO steps VALUES (?, ?, ?, ?, ?, ?)`, i, s[0], s[1], s[2], s[3], []byte("TOOL OUTPUT "+canary))
		}
		for i, g := range gens {
			db.Exec(`INSERT INTO gen_metadata VALUES (?, ?, ?)`, i, g, len(g))
		}
	}
	const t0 = 1791367200 // 2026-10-07 10:00:00Z (the summaries table dates the conversation 10-08; the session starts at its first step)
	conv("C1", [][]any{
		{14, 3, agyStepMeta(t0, "", nil), nil},
		{15, 3, agyStepMeta(t0+5, "", nil), nil},
		{132, 3, agyStepMeta(t0+6, "run_command", map[string]string{"CommandLine": "git add . && go test -race ./... # " + canary}), nil},
		{132, 7, agyStepMeta(t0+7, "view_file", map[string]string{"AbsolutePath": "/home/u/.gemini/config/skills/golang/SKILL.md"}), []byte("err " + canary)},
		{132, 3, agyStepMeta(t0+8, "view_file", map[string]string{"AbsolutePath": filepath.Join(root, ".agents", "docs", "plans", canary+".md")}), nil},
		{132, 3, agyStepMeta(t0+9, "brand_new_tool", map[string]string{"Other": canary}), nil},
		{101, 3, agyStepMeta(t0+10, "", nil), nil},
		{15, 3, agyStepMeta(t0+11, "", nil), nil},
	}, [][]byte{agyGen(1000, 50, 0, 33000), agyGen(900, 40, 500, 42000)})
	conv("C2", [][]any{{15, 3, agyStepMeta(t0+3, "", nil), nil}}, nil)
	return home, root
}

func TestAgyIngestShape(t *testing.T) {
	home, root := agyFixture(t)
	src := filepath.Join(home, ".gemini", "antigravity-cli")
	for _, d := range []string{src, filepath.Join(src, "conversations")} {
		if err := os.Chmod(d, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(d, 0o700) })
	}
	before, _ := os.ReadDir(filepath.Join(src, "conversations"))

	dir := t.TempDir()
	s := openT(t, filepath.Join(dir, "metrics.db"))
	r := ingestT(t, s, Options{Home: home, HomeGiven: true, Vendor: "agy"})
	if r.Sessions != 2 || len(r.Failed) != 0 {
		t.Fatalf("result %+v, want 2 sessions, none failed", r)
	}
	after, _ := os.ReadDir(filepath.Join(src, "conversations"))
	if len(after) != len(before) {
		t.Fatalf("files appeared beside the live databases: %v", after)
	}

	q := func(query string) any {
		var v any
		if err := s.db.QueryRow(query).Scan(&v); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return v
	}
	checks := map[string]any{
		`SELECT vendor || project_root FROM sessions WHERE id='C1'`:                      "agy" + root,
		`SELECT parent_id || '/' || agent_type FROM sessions WHERE id='C2'`:              "C1/explorer",
		`SELECT model FROM sessions WHERE id='C1'`:                                       "gemini-test",
		`SELECT count(*) FROM steps WHERE session_id='C1' AND kind='tool'`:               int64(4),
		`SELECT count(*) FROM steps WHERE session_id='C1' AND kind='user'`:               int64(1),
		`SELECT count(*) FROM steps WHERE session_id='C1' AND kind='assistant'`:          int64(2),
		`SELECT count(*) FROM steps WHERE tool='Bash'`:                                   int64(1),
		`SELECT count(*) FROM steps WHERE tool='Read' AND is_error=1 AND skill='golang'`: int64(1),
		`SELECT count(*) FROM steps WHERE target_kind='plan'`:                            int64(1),
		`SELECT count(*) FROM steps WHERE tool='agy:brand_new_tool'`:                     int64(1),
		`SELECT sum(tok_in) FROM steps WHERE kind='generation'`:                          int64(1900),
		`SELECT sum(cache_read) FROM steps WHERE kind='generation'`:                      int64(500),
		`SELECT max(context) FROM steps WHERE kind='generation'`:                         int64(42000),
		`SELECT group_concat(verb, '|') FROM step_verbs WHERE session_id='C1'`:           "git add .|go test -race",
		`SELECT started_at FROM sessions WHERE id='C1'`:                                  int64(1791367200000),
	}
	for query, want := range checks {
		if got := q(query); got != want {
			t.Errorf("%s = %v (%T), want %v", query, got, got, want)
		}
	}

	if r2 := ingestT(t, s, Options{Home: home, HomeGiven: true, Vendor: "agy"}); r2.Sessions+r2.Steps != 0 {
		t.Fatalf("second ingest added %+v", r2)
	}
	exp := filepath.Join(dir, "export.jsonl")
	if err := s.Export(exp); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(dir, "metrics.db"), filepath.Join(dir, "metrics.db-wal"), exp} {
		if b, _ := os.ReadFile(f); bytes.Contains(b, []byte(canary)) {
			t.Errorf("%s holds the canary", filepath.Base(f))
		}
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, "metrics-agy-*")); len(entries) != 0 {
		t.Errorf("temporary copies left behind: %v", entries)
	}
}

// An ingest killed mid-copy leaves metrics-agy-*; the next ingest sweeps
// copies older than an hour and keeps a fresh one (another ingest's).
func TestIngestSweepsStaleCopies(t *testing.T) {
	home, _ := agyFixture(t)
	dir := t.TempDir()
	s := openT(t, filepath.Join(dir, "metrics.db"))
	old, fresh := filepath.Join(dir, "metrics-agy-old"), filepath.Join(dir, "metrics-agy-fresh")
	for _, d := range []string{old, fresh} {
		os.MkdirAll(d, 0o700)
		os.WriteFile(filepath.Join(d, "C1.db"), []byte("copy"), 0o600)
	}
	past := time.Now().Add(-2 * time.Hour)
	os.Chtimes(old, past, past)
	ingestT(t, s, Options{Home: home, HomeGiven: true, Vendor: "agy"})
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("a stale copy was kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a fresh copy (another ingest's) was removed")
	}
}

// A conversation ingested while a tool step was still running is read again
// when it changes: the step's later status reaches the store (review
// 2026-10-08: rows were inserted once and never updated).
func TestAgyStepUpdatedOnReingest(t *testing.T) {
	home, _ := agyFixture(t)
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	o := Options{Home: home, HomeGiven: true, Vendor: "agy"}
	ingestT(t, s, o)
	src := filepath.Join(home, ".gemini", "antigravity-cli")
	db, _ := sql.Open("sqlite", filepath.Join(src, "conversations", "C1.db"))
	db.Exec(`UPDATE steps SET status = 7 WHERE idx = 2`) // the run_command step fails late
	db.Close()
	sum, _ := sql.Open("sqlite", filepath.Join(src, "conversation_summaries.db"))
	sum.Exec(`UPDATE conversation_summaries SET last_modified_time = '2026-10-08 10:06:00+00:00' WHERE conversation_id = 'C1'`)
	sum.Close()
	ingestT(t, s, o)
	var e int
	s.db.QueryRow(`SELECT is_error FROM steps WHERE ext_id = 'C1:2'`).Scan(&e)
	if e != 1 {
		t.Fatalf("is_error = %d after the step failed, want 1", e)
	}
}
