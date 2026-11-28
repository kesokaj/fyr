package metrics

import (
	"path/filepath"
	"testing"
	"time"
)

func TestShowGrepAttributionSkipsLeadingCDExcludesPipe(t *testing.T) {
	t.Parallel()
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	ts := now.Add(-2 * time.Hour).UnixMilli()
	root := "/proj/grep-cd"

	insertVerbs := func(sid, ext, cmd string) {
		t.Helper()
		for n, v := range Verb(cmd) {
			if _, err := s.db.Exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES (?, ?, ?, ?)`, sid, ext, n, v); err != nil {
				t.Fatalf("insert step_verbs %q: %v", cmd, err)
			}
		}
	}

	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES
		('M1', 'agy', ?, ?, ?),
		('S1', 'agy', ?, ?, ?)`,
		root, ts, ts+5000, root, ts+100, ts+4000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET parent_id = 'M1', agent_type = 'reviewer' WHERE id = 'S1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES
		('M1', 'm1', ?, 'tool', 'Bash'),
		('M1', 'm2', ?, 'tool', 'Bash'),
		('M1', 'm3', ?, 'tool', 'Bash'),
		('M1', 'm4', ?, 'tool', 'Bash'),
		('M1', 'm5', ?, 'tool', 'Bash'),
		('S1', 's1', ?, 'tool', 'Bash'),
		('S1', 's2', ?, 'tool', 'Bash')`,
		ts+10, ts+20, ts+30, ts+40, ts+50, ts+110, ts+120); err != nil {
		t.Fatal(err)
	}

	// Main session:
	// - m1: fyr search --slice (1 search)
	// - m2: cd dir && rg foo (1 grep: first non-cd verb is rg)
	// - m3: go test ./... | grep FAIL (0 grep: piped filter after go test)
	// - m4: cd dir && go test ./... | grep FAIL (0 grep: piped filter after cd + go test)
	// - m5: synthetic n=1 grep without n=0 (0 grep: non-zero verb index without leading cd)
	insertVerbs("M1", "m1", "fyr search --slice foo")
	insertVerbs("M1", "m2", "cd dir && rg foo")
	insertVerbs("M1", "m3", "go test ./... | grep FAIL")
	insertVerbs("M1", "m4", "cd dir && go test ./... | grep FAIL")
	if _, err := s.db.Exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES ('M1', 'm5', 1, 'grep')`); err != nil {
		t.Fatal(err)
	}

	// Reviewer subagent:
	// - s1: cd sub && grep -rn pat (1 grep)
	// - s2: cd sub && rg foo | grep bar (1 grep, never double-counted)
	insertVerbs("S1", "s1", "cd sub && grep -rn pat")
	insertVerbs("S1", "s2", "cd sub && rg foo | grep bar")

	cutoff := now.Add(-3 * time.Hour)
	sum, err := s.Show(ShowOptions{
		Since:    cutoff,
		Now:      now,
		SinceTag: "v9.11.0",
	})
	if err != nil {
		t.Fatalf("Show: %v", err)
	}

	// Total across M1 (1 grep) + S1 (2 grep) = 3 greps, 1 search.
	if sum.C.SQLiteVerbs["agy"] != 1 || sum.C.GrepCalls["agy"] != 3 {
		t.Fatalf("Summary overall sqlite=%d grep=%d, want 1, 3", sum.C.SQLiteVerbs["agy"], sum.C.GrepCalls["agy"])
	}
	if len(sum.Daily) != 1 || sum.Daily[0].GrepCalls["agy"] != 3 {
		t.Fatalf("Daily GrepCalls = %+v, want 3", sum.Daily)
	}
	if len(sum.Projects) != 1 || sum.Projects[0].SearchGrepRatio == nil || *sum.Projects[0].SearchGrepRatio != round4(1.0/3.0) {
		t.Fatalf("Projects SearchGrepRatio = %+v, want %.4f (1/3)", sum.Projects, round4(1.0/3.0))
	}

	roleGreps := map[string]int{}
	for _, r := range sum.AgentRoles {
		roleGreps[r.Role] = r.GrepCalls
	}
	if roleGreps["main"] != 1 || roleGreps["reviewer"] != 2 {
		t.Fatalf("AgentRoles GrepCalls = %v, want main=1 reviewer=2", roleGreps)
	}
	if sum.TagCohort == nil || sum.TagCohort.Post.GrepCalls != 3 || sum.TagCohort.Post.SQLiteVerbs != 1 {
		t.Fatalf("TagCohort.Post = %+v, want GrepCalls=3 SQLiteVerbs=1", sum.TagCohort)
	}
}
