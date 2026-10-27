package metrics

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The arms of add, finish, Numbers, Show and ingestAgyConversation that
// their splits move into helpers, pinned before the split
// (PLAN-2026-10-27-funclen Phase 3, D13).

// A subagent with no agent type counts as "unknown", a tool result whose
// call was never seen is no call, a Grep call counts toward the search :
// grep ratio and its day, and a Bash command repeated by hash is a retry.
func TestAddArms(t *testing.T) {
	day := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	ms := day.UnixMilli()
	s := newSummary(day.AddDate(0, 0, -1), day.AddDate(0, 0, 1))
	main := sess{id: "M", vendor: "agy", start: ms, end: ms + 60000}
	sub := sess{id: "S", vendor: "agy", parent: "M", start: ms, end: ms + 1000}
	s.add(main, []stp{
		{ext: "1", kind: "tool", tool: "Grep", ts: ms + 1000},
		{ext: "2", kind: "tool", tool: "Bash", hash: "h", ts: ms + 2000, verbs: []string{"fyr search --kind"}},
		{ext: "3", kind: "tool", tool: "Bash", hash: "h", ts: ms + 3000},
		{ext: "4", kind: "tool", ts: ms + 4000},
	})
	s.add(sub, []stp{{ext: "5", kind: "tool", tool: "Read", ts: ms + 5000}})
	s.finish([]sess{main, sub})

	for name, c := range map[string][2]int{
		"subagents unknown":           {s.Subagents["agy"]["unknown"], 1},
		"subagent tool calls unknown": {s.D.SubagentToolCalls["agy"]["unknown"], 1},
		"tool calls":                  {s.B.ToolCalls["agy"], 4},
		"grep calls":                  {s.C.GrepCalls["agy"], 1},
		"retry loops":                 {s.D.RetryLoops["agy"], 1},
	} {
		if c[0] != c[1] {
			t.Errorf("%s = %d, want %d", name, c[0], c[1])
		}
	}
	if len(s.Daily) != 1 || s.Daily[0].GrepCalls["agy"] != 1 {
		t.Errorf("daily %+v, want one day with one grep call", s.Daily)
	}
	if r := s.C.SearchGrepRatio["agy"]; r == nil || *r != 1 {
		t.Errorf("search : grep ratio %v, want 1", r)
	}
	if v, ok := s.Numbers()["C.search_grep_ratio.agy"]; !ok || v != 1 {
		t.Errorf("Numbers C.search_grep_ratio.agy = %v (present %v), want 1", v, ok)
	}
}

// The sessions table that cannot be read, or holds a row that does not
// scan, fails Show.
func TestShowSessionReadErrors(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for name, stmt := range map[string]string{
		"query": `ALTER TABLE sessions RENAME TO gone`,
		"scan":  `UPDATE sessions SET cost_usd = 'abc'`,
	} {
		t.Run(name, func(t *testing.T) {
			s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
			if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, started_at, ended_at) VALUES ('S', 'agy', ?, ?)`,
				now.Add(-time.Hour).UnixMilli(), now.Add(-time.Minute).UnixMilli()); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Show(ShowOptions{Now: now}); err != nil {
				t.Fatalf("Show before the break: %v", err)
			}
			if _, err := s.db.Exec(stmt); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Show(ShowOptions{Now: now}); err == nil {
				t.Fatal("Show: nil error, want the sessions read's")
			}
		})
	}
}

// A conversation copy whose steps cannot be read, or hold a row that does
// not scan, fails that conversation's ingest and stores nothing of it.
func TestAgyConversationStepErrors(t *testing.T) {
	for name, stmts := range map[string][]string{
		"no steps table": {"CREATE TABLE gen_metadata (`idx` integer, `data` blob)"},
		"bad step row": {
			"CREATE TABLE steps (`idx` integer, `step_type` integer, `status` integer, `metadata` blob)",
			"INSERT INTO steps VALUES ('x', 3, 3, NULL)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			src := t.TempDir()
			os.MkdirAll(filepath.Join(src, "conversations"), 0o755)
			db := filepath.Join(src, "conversations", "C9.db")
			sqliteT(t, db, stmts...)
			s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
			if err := s.ingestAgyConversation(src, agyConv{id: "C9"}, db, t.TempDir(), Options{}); err == nil {
				t.Fatal("ingestAgyConversation: nil error, want the steps read's")
			}
			var n int
			s.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&n)
			if n != 0 {
				t.Errorf("%d sessions stored, want 0", n)
			}
		})
	}
}
