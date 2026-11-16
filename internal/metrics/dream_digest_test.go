package metrics

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func openDreamDigestTestStore(t *testing.T) (*Store, func(string, ...any)) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return st, exec
}

func TestDreamTrajectoryDigestEmpty(t *testing.T) {
	st, _ := openDreamDigestTestStore(t)
	d, err := st.DreamTrajectoryDigest("/work/proj", time.Now().AddDate(0, 0, -7))
	if err != nil {
		t.Fatalf("DreamTrajectoryDigest: %v", err)
	}
	if d.Sessions != 0 || len(d.Retries) != 0 || len(d.RepeatedVerbs) != 0 || d.RedundantReadHotspots != 0 {
		t.Errorf("empty store digest = %+v, want zero value", d)
	}
}

func TestDreamTrajectoryDigestLabelIsolationAndSinceFilter(t *testing.T) {
	st, exec := openDreamDigestTestStore(t)
	base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	root := "/work/proj"

	// Included session: label IS NULL, started_at >= since.
	exec(`INSERT INTO sessions(id, vendor, project_root, started_at) VALUES ('s1', 'agy', ?, ?)`,
		root, base.UnixMilli())
	// Excluded: label IS NOT NULL (eval / synthetic).
	exec(`INSERT INTO sessions(id, vendor, project_root, label, started_at) VALUES ('s-eval', 'agy', ?, 'eval=row1', ?)`,
		root, base.Add(time.Minute).UnixMilli())
	// Excluded: before since.
	exec(`INSERT INTO sessions(id, vendor, project_root, started_at) VALUES ('s-old', 'agy', ?, ?)`,
		root, base.Add(-48*time.Hour).UnixMilli())
	// Excluded: different project.
	exec(`INSERT INTO sessions(id, vendor, project_root, started_at) VALUES ('s-other', 'agy', '/work/other', ?)`,
		base.UnixMilli())

	for _, sid := range []string{"s-eval", "s-old", "s-other"} {
		exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, is_error) VALUES (?, '1', 100, 'tool', 'EvalTool', 1)`, sid)
		exec(`INSERT INTO events(session_id, ts, kind, ref, value) VALUES (?, 100, 'redundant_read', 'x', '')`, sid)
	}

	d, err := st.DreamTrajectoryDigest(root, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("DreamTrajectoryDigest: %v", err)
	}
	if d.Sessions != 1 {
		t.Errorf("Sessions = %d, want 1", d.Sessions)
	}
	if len(d.Retries) != 0 || d.RedundantReadHotspots != 0 {
		t.Errorf("excluded sessions leaked into digest: %+v", d)
	}
}

func TestDreamTrajectoryDigestMultiSessionAggregationAndNoLeak(t *testing.T) {
	st, exec := openDreamDigestTestStore(t)
	base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	root := "/work/proj"

	// Two matching sessions (one at root, one in a subpath).
	exec(`INSERT INTO sessions(id, vendor, project_root, started_at) VALUES
		('s1', 'agy', ?, ?),
		('s2', 'agy', ?, ?)`,
		root, base.UnixMilli(),
		root+"/sub", base.Add(time.Hour).UnixMilli())

	// Errors across 6 tools to test top-5 truncation + retry_loop event aggregation.
	errs := []struct {
		sid, ext, tool string
		n              int
	}{
		{"s1", "e1", "Bash", 4},
		{"s2", "e2", "Read", 3},
		{"s1", "e3", "Edit", 3},
		{"s2", "e4", "Grep", 2},
		{"s1", "e5", "Write", 2},
		{"s2", "e6", "Glob", 1},
	}
	seq := 0
	for _, e := range errs {
		for i := 0; i < e.n; i++ {
			seq++
			exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, is_error, cmd_hash) VALUES (?, ?, ?, 'tool', ?, 1, '/secret/path/file.go --token=abc')`,
				e.sid, e.ext+"-"+string(rune('a'+i)), int64(seq*100), e.tool)
		}
	}
	// Extra retry_loop event for Bash -> total Bash = 5.
	exec(`INSERT INTO events(session_id, ts, kind, ref, value) VALUES
		('s1', 100, 'retry_loop', 'Bash', 'secret prose with /home/user/private.txt'),
		('s1', 200, 'redundant_read', '/secret/path/file.go', 'secret arg'),
		('s2', 300, 'REDUNDANT_READ', '/secret/other/secret.go', 'secret arg'),
		('s2', 400, 'knee_exceeded', 'turn-5', '190000')`)
	// Plus one step-level redundant read in s1 (two consecutive Reads of same hash).
	exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, cmd_hash, is_error) VALUES
		('s1', 'r1', 9001, 'tool', 'Read', 'f:deadbeef:/secret/path/main.go', 0),
		('s1', 'r2', 9002, 'tool', 'Read', 'r:deadbeef:/secret/path/main.go', 0)`)

	// Verb sequences: "git status" -> "go test" -> "git commit" across both sessions,
	// plus a single-session repeated pattern that must NOT qualify (sessions < 2).
	s1Verbs := []string{"git status", "go test", "git commit", "only s1", "only s1", "only s1"}
	for i, v := range s1Verbs {
		exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES ('s1', ?, 0, ?)`,
			"v-"+string(rune('0'+i)), v)
	}
	s2Verbs := []string{"git status", "go test", "git commit"}
	for i, v := range s2Verbs {
		exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES ('s2', ?, 0, ?)`,
			"v-"+string(rune('0'+i)), v)
	}

	d, err := st.DreamTrajectoryDigest(root, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("DreamTrajectoryDigest: %v", err)
	}
	if d.Sessions != 2 {
		t.Errorf("Sessions = %d, want 2", d.Sessions)
	}
	wantRetries := []RetryPattern{
		{Tool: "Bash", Count: 5},
		{Tool: "Edit", Count: 3},
		{Tool: "Read", Count: 3},
		{Tool: "Grep", Count: 2},
		{Tool: "Write", Count: 2},
	}
	if !reflect.DeepEqual(d.Retries, wantRetries) {
		t.Errorf("Retries = %+v, want %+v", d.Retries, wantRetries)
	}
	if d.RedundantReadHotspots != 3 {
		t.Errorf("RedundantReadHotspots = %d, want 3 (2 events + 1 step)", d.RedundantReadHotspots)
	}
	wantVerbs := []VerbLoop{
		{Verbs: "git status -> go test", Sessions: 2},
		{Verbs: "git status -> go test -> git commit", Sessions: 2},
		{Verbs: "go test -> git commit", Sessions: 2},
	}
	if !reflect.DeepEqual(d.RepeatedVerbs, wantVerbs) {
		t.Errorf("RepeatedVerbs = %+v, want %+v", d.RepeatedVerbs, wantVerbs)
	}

	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)
	for _, leak := range []string{"/secret/", "/home/user/", "private.txt", "--token=abc", "secret arg", "only s1", "Glob"} {
		if strings.Contains(js, leak) {
			t.Errorf("digest JSON leaked forbidden content %q: %s", leak, js)
		}
	}
}
