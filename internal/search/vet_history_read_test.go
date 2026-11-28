package search

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func openTestSQLiteDB(t *testing.T, stmts ...string) *sql.DB {
	t.Helper()
	p := filepath.Join(t.TempDir(), "search.db")
	db, err := sql.Open("sqlite", DSN(p))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	return db
}

func TestReadVetHistoryForIngestMissingAndEmpty(t *testing.T) {
	if rows, err := ReadVetHistoryForIngest(nil); err != nil || rows != nil {
		t.Fatalf("nil db = (%v, %v), want (nil, nil)", rows, err)
	}
	noTableDB := openTestSQLiteDB(t, `CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT)`)
	if rows, err := ReadVetHistoryForIngest(noTableDB); err != nil || rows != nil {
		t.Fatalf("missing table = (%v, %v), want (nil, nil)", rows, err)
	}
	emptyDB := openTestSQLiteDB(t, schemaSQL)
	if rows, err := ReadVetHistoryForIngest(emptyDB); err != nil || len(rows) != 0 {
		t.Fatalf("empty table = (%v, %v), want empty slice, nil", rows, err)
	}
	if err := emptyDB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadVetHistoryForIngest(emptyDB); err == nil {
		t.Fatal("closed db succeeded, want error")
	}
}

func TestReadVetHistoryForIngestPreV10NineCols(t *testing.T) {
	db := openTestSQLiteDB(t,
		`CREATE TABLE vet_history(conversation_id TEXT, turn_num INT, intent TEXT, gate TEXT, score INT, raw_prompt TEXT, suggested_skills TEXT, pivot_files TEXT, created_at INT)`,
		`INSERT INTO vet_history VALUES ('C1', 2, 'FIX', 'PROCEED', 85, 'fix bug', 'golang', 'a.go', 200)`,
		`INSERT INTO vet_history VALUES ('C1', 1, 'TASK', 'WARN', 60, '!bypass', NULL, NULL, 100)`,
	)
	rows, err := ReadVetHistoryForIngest(db)
	if err != nil {
		t.Fatalf("ReadVetHistoryForIngest: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	// Deterministic ORDER BY turn_num, created_at, rowid.
	if rows[0].TurnNum != 1 || rows[1].TurnNum != 2 {
		t.Fatalf("order wrong: %+v", rows)
	}
	if rows[0].RawPrompt != "!bypass" || rows[0].SuggestedSkills != "" || rows[0].PivotFiles != "" {
		t.Fatalf("row[0] NULL coalescing mismatch: %+v", rows[0])
	}
	if rows[0].HasPivotsUsedCol || rows[0].HasSkillsLoadedCol {
		t.Fatalf("pre-v10 should not report optional cols: %+v", rows[0])
	}
	if rows[1].Directive != "" || rows[1].SubtaskCount != 0 || rows[1].DurationMS != 0 || rows[1].TimedOut || rows[1].InjectedBytes != 0 {
		t.Fatalf("pre-v10 defaults mismatch: %+v", rows[1])
	}
}

func TestReadVetHistoryForIngestV10Schema(t *testing.T) {
	db := openTestSQLiteDB(t,
		`CREATE TABLE vet_history(conversation_id TEXT, turn_num INT, intent TEXT, gate TEXT, score INT, raw_prompt TEXT, improved_prompt TEXT, present_anchors TEXT, missing_anchors TEXT, suggested_skills TEXT, pivot_files TEXT, subtask_count INT DEFAULT 0, directive TEXT DEFAULT '', created_at INT)`,
		`INSERT INTO vet_history VALUES ('C10', 1, 'FEATURE', 'PROCEED', 92, 'build', '', '', '', 'fyr-feature', 'main.go', 3, 'PROCEED', 1700000100)`,
	)
	rows, err := ReadVetHistoryForIngest(db)
	if err != nil || len(rows) != 1 {
		t.Fatalf("v10 rows=%+v err=%v", rows, err)
	}
	r := rows[0]
	if r.ConversationID != "C10" || r.SubtaskCount != 3 || r.Directive != "PROCEED" || r.CreatedAt != 1700000100 {
		t.Fatalf("v10 fields = %+v", r)
	}
	if r.DurationMS != 0 || r.TimedOut || r.InjectedBytes != 0 || r.HasPivotsUsedCol || r.HasSkillsLoadedCol {
		t.Fatalf("v10 missing v11 cols should be zero/false: %+v", r)
	}
}

func TestReadVetHistoryForIngestV11V12AndOptionalCols(t *testing.T) {
	// Standard v12 schema (without pivots_used_count / skills_loaded_count columns).
	v12DB := openTestSQLiteDB(t,
		schemaSQL,
		`INSERT INTO vet_history(conversation_id, turn_num, intent, gate, score, raw_prompt, improved_prompt, present_anchors, missing_anchors, suggested_skills, pivot_files, subtask_count, directive, duration_ms, timed_out, injected_bytes, created_at)
		 VALUES ('C12', 1, 'TASK', 'PROCEED', 95, 'do it', '', '', '', 'golang,fyr-bug', 'a.go,b.go', 2, 'PROCEED', 42, 1, 512, 1700000200)`,
	)
	rows, err := ReadVetHistoryForIngest(v12DB)
	if err != nil || len(rows) != 1 {
		t.Fatalf("v12 rows=%+v err=%v", rows, err)
	}
	r := rows[0]
	if r.DurationMS != 42 || !r.TimedOut || r.InjectedBytes != 512 || r.HasPivotsUsedCol || r.HasSkillsLoadedCol {
		t.Fatalf("v12 fields = %+v", r)
	}

	// Extended schema with explicit pivots_used_count & skills_loaded_count columns.
	extDB := openTestSQLiteDB(t,
		`CREATE TABLE vet_history(conversation_id TEXT, turn_num INT, intent TEXT, gate TEXT, score INT, raw_prompt TEXT, suggested_skills TEXT, pivot_files TEXT, subtask_count INT, directive TEXT, duration_ms INT, timed_out INT, injected_bytes INT, pivots_used_count INT, skills_loaded_count INT, created_at INT)`,
		`INSERT INTO vet_history VALUES ('C11', 1, 'TASK', 'PROCEED', 88, 'run', 'golang', 'a.go', 1, 'PROCEED', 15, 0, 128, 2, 1, 1700000300)`,
	)
	extRows, err := ReadVetHistoryForIngest(extDB)
	if err != nil || len(extRows) != 1 {
		t.Fatalf("extended rows=%+v err=%v", extRows, err)
	}
	er := extRows[0]
	if !er.HasPivotsUsedCol || !er.HasSkillsLoadedCol || er.PivotsUsedCount != 2 || er.SkillsLoadedCount != 1 || er.TimedOut {
		t.Fatalf("extended fields = %+v", er)
	}
}
