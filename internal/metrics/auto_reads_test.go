package metrics

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// ProjectVerbs, ProjectFrictions and SuiteTrials read session verbs, session
// friction counts (denials and repeated Bash command hashes) and suite trials
// from a read-only Store snapshot, scoping project queries to root and its
// subdirectories without matching sibling prefixes.
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
	s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, cmd_hash, denied) VALUES
		('s1', '1', 100, 'tool', 'Bash', 'h1', 0),
		('s1', '2', 200, 'tool', 'Bash', 'h1', 0),
		('s1', '3', 300, 'tool', 'Read', '', 1),
		('s2', '1', 400, 'tool', 'Bash', 'h2', 0),
		('s3', '1', 500, 'tool', 'Bash', 'h3', 1),
		('s-sub', '1', 600, 'tool', 'Bash', 'h4', 1)`)

	t1 := Trial{Run: "r/alpha/1", Label: "eval=alpha", Harness: "abc1234", CLI: "agy 1.4.12", Status: "pass", StartedAt: base, Seconds: 5}
	t2 := Trial{Run: "r/beta/1", Label: "kit=beta", Harness: "abc1234", CLI: "agy 1.4.12", Status: "pass", StartedAt: base.Add(time.Minute), Seconds: 5}
	if err := s.RecordTrial(t1); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTrial(t2); err != nil {
		t.Fatal(err)
	}
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

	frictions, err := snap.ProjectFrictions("/work/proj", base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ProjectFrictions: %v", err)
	}
	if !slices.Equal(frictions, []int{2, 0}) {
		t.Errorf("frictions = %v, want [2 0]", frictions)
	}

	trials, err := snap.SuiteTrials()
	if err != nil {
		t.Fatalf("SuiteTrials: %v", err)
	}
	if len(trials) != 1 || trials[0].Label != "eval=alpha" || trials[0].Harness != "abc1234" || trials[0].CLI != "agy 1.4.12" {
		t.Errorf("SuiteTrials = %+v, want 1 eval=alpha trial", trials)
	}
}
