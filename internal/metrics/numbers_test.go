package metrics

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// Numbers' keys are this package's own names and its values numbers: no
// tool, skill, agent type, label or path reaches the dream's prompt.
func TestNumbersOnly(t *testing.T) {
	sum := reportFixture(t)
	n := sum.Numbers()
	key := regexp.MustCompile(`^(sessions|subagents|[ABCD]\.[a-z_]+)(\.(claude|agy|PASS|FAIL|BLOCKED|high|medium|low|git_add_all|git_add_dot|push_force|no_verify|commit_chained_to_test))?$`)
	for k := range n {
		if !key.MatchString(k) {
			t.Errorf("key %q is not one of the package's names", k)
		}
	}
	if n["B.tool_calls.claude"] != float64(sum.B.ToolCalls["claude"]) || n["C.forbidden.git_add_dot"] != 1 {
		t.Errorf("values: tool calls %v, git add . %v", n["B.tool_calls.claude"], n["C.forbidden.git_add_dot"])
	}
	if _, ok := n["B.cost_usd.agy"]; ok {
		t.Error("a metric Antigravity cannot produce is present (must be absent, never 0)")
	}
	// The context target and the over-target count reach the dream; the
	// per-session list, which would carry session ids, does not.
	if n["B.context_target"] != 300000 || n["B.sessions_over_context_target.claude"] != float64(*sum.B.SessionsOverContextTarget["claude"]) {
		t.Errorf("context target %v, over target %v", n["B.context_target"], n["B.sessions_over_context_target.claude"])
	}
	sum.B.SessionsOverContextTarget["agy"] = nil
	if _, ok := sum.Numbers()["B.sessions_over_context_target.agy"]; ok {
		t.Error("a null over-target count is present (must be absent, never 0)")
	}
}

// A snapshot reads a copy: the store's directory gains no -wal or -shm and
// the store's bytes do not change; a store at another schema is refused.
func TestSnapshotReadOnly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "metrics.db")
	s := openT(t, p)
	s.db.Exec(`INSERT INTO sessions(id, vendor, started_at, ended_at) VALUES ('x', 'claude', 1, 2)`)
	s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	s.Close()
	for _, suffix := range []string{"-wal", "-shm"} {
		os.Remove(p + suffix)
	}
	before, _ := os.ReadFile(p)

	snap, err := Snapshot(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snap.Show(ShowOptions{Since: time.UnixMilli(0), Now: time.UnixMilli(10)}); err != nil {
		t.Fatal(err)
	}
	snap.Close()
	after, _ := os.ReadFile(p)
	entries, _ := os.ReadDir(dir)
	if !bytes.Equal(before, after) || len(entries) != 1 {
		t.Fatalf("snapshot changed the store or its directory: %d entries", len(entries))
	}

	saved := migrations
	t.Cleanup(func() { migrations = saved })
	migrations = append(append([]string{}, saved...), `SELECT 1`)
	if _, err := Snapshot(p, t.TempDir()); err == nil {
		t.Fatal("a snapshot read a store at another schema")
	}
	if _, err := Snapshot(filepath.Join(dir, "none.db"), t.TempDir()); !os.IsNotExist(err) {
		t.Fatalf("missing store: %v, want not-exist", err)
	}
}
