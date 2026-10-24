package metrics

import (
	"testing"
	"time"
)

// historicalRows inserts what an ingest before ADR-062 left in a store for
// one Claude Code session in root: the main session S1 with four tool calls (a
// commit chained to a test, which errs; a skill; a read of a handoff; a
// denied command), a user turn, an interrupt, a compaction and a cost; and its
// verifier subagent A1 with one Bash call. Ingest no longer writes such rows;
// the store keeps them and show reads them as the historical column (plan fyr
// D7).
func historicalRows(t *testing.T, s *Store, root string) {
	t.Helper()
	base := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC).UnixMilli()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO sessions(id, vendor, project_root, model, started_at, ended_at, cost_usd, duration_ms)
			VALUES ('S1', 'claude', ?, 'claude-opus-5-5', ?, ?, 0.5, 6000)`, []any{root, base, base + 6000}},
		{`INSERT INTO sessions(id, vendor, project_root, parent_id, agent_type, started_at, ended_at)
			VALUES ('A1', 'claude', ?, 'S1', 'verifier', ?, ?)`, []any{root, base + 2000, base + 2000}},
		{`INSERT INTO steps(session_id, ext_id, ts, kind) VALUES ('S1', 'u1', ?, 'user'), ('S1', 'u3', ?, 'interrupt'), ('S1', 'c1', ?, 'compaction')`,
			[]any{base, base + 4000, base + 6000}},
		{`INSERT INTO steps(session_id, ext_id, ts, kind, tok_in, tok_out, cache_read, cache_write)
			VALUES ('S1', 'm1', ?, 'assistant', 3, 40, 10, 5), ('A1', 'm9', ?, 'assistant', 1, 2, 10, 5)`, []any{base + 2000, base + 2000}},
		{`INSERT INTO steps(session_id, ext_id, ts, kind, tool, skill, target_kind, is_error, denied, cmd_hash) VALUES
			('S1', 't1', ?, 'tool', 'Bash', NULL, NULL, 1, 0, 'h1'),
			('S1', 't2', ?, 'tool', 'Skill', 'feature', NULL, 0, 0, NULL),
			('S1', 't3', ?, 'tool', 'Read', NULL, 'handoff', 0, 0, NULL),
			('S1', 't4', ?, 'tool', 'Bash', NULL, NULL, 1, 1, 'h2'),
			('A1', 't9', ?, 'tool', 'Bash', NULL, NULL, 0, 0, 'h3')`,
			[]any{base + 2000, base + 2000, base + 2000, base + 2000, base + 2000}},
		{`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES
			('S1', 't1', 0, 'git commit'), ('S1', 't1', 1, 'go test'), ('S1', 't4', 0, 'rm -rf'), ('A1', 't9', 0, 'go vet')`, nil},
	} {
		if _, err := s.db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
}
