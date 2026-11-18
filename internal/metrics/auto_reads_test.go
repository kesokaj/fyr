package metrics

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// ProjectVerbs reads session verbs from a read-only Store snapshot, scoping
// project queries to root and its subdirectories without matching sibling prefixes.
func TestAutoReadsFromSnapshot(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "metrics.db")
	s := openT(t, p)

	base := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES
		('s1', 'agy', '/work/proj', ?, ?),
		('s2', 'agy', '/work/proj/sub', ?, ?),
		('s3', 'agy', '/work/proj-other', ?, ?),
		('s-sub', 'agy', '/work/proj', ?, ?)`,
		base.UnixMilli(), base.Add(time.Minute).UnixMilli(),
		base.Add(2*time.Minute).UnixMilli(), base.Add(3*time.Minute).UnixMilli(),
		base.Add(4*time.Minute).UnixMilli(), base.Add(5*time.Minute).UnixMilli(),
		base.Add(6*time.Minute).UnixMilli(), base.Add(7*time.Minute).UnixMilli(),
	)
	s.db.Exec(`UPDATE sessions SET parent_id = 's1' WHERE id = 's-sub'`)
	s.db.Exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES
		('s1', '1', 0, 'fyr search'),
		('s1', '2', 0, 'fyr record'),
		('s2', '1', 0, 'go test'),
		('s3', '1', 0, 'git status')`)
	s.Close()

	snap, err := Snapshot(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()

	bySess, order, err := snap.ProjectVerbs("/work/proj", 100)
	if err != nil {
		t.Fatalf("ProjectVerbs: %v", err)
	}
	if !slices.Equal(order, []string{"s2", "s1"}) {
		t.Errorf("order = %v, want [s2 s1]", order)
	}
	if !slices.Equal(bySess["s1"], []string{"fyr search", "fyr record"}) || !slices.Equal(bySess["s2"], []string{"go test"}) {
		t.Errorf("bySess = %v", bySess)
	}
}
