package metrics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
)

func gitT(t *testing.T, dir string, when time.Time, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	stamp := when.UTC().Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// eventsFixture: a migrated project with three main sessions. S1
// (10:00-10:20) commits once, and its trail records a handoff, a PASS
// verifier log and a review stamped inside it; S2 (12:00-12:10) and S3
// (12:05-12:30) overlap, and the one commit between them is ambiguous. A bug
// record cites S1's commit; one global feedback record falls in S1's window.
func eventsFixture(t *testing.T) (*Store, string, string) {
	t.Helper()
	// The IDs below carry their minute in UTC, the zone the join reads them
	// in here. No test here runs in parallel.
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(root, 0o755)
	day := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	at := func(h, m int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	gitT(t, root, at(9, 0), "init", "-q", "-b", "main")
	gitT(t, root, at(9, 0), "config", "commit.gpgsign", "false")

	os.WriteFile(filepath.Join(root, "w"), []byte("x\n"), 0o644)
	gitT(t, root, at(10, 10), "add", "w")
	gitT(t, root, at(10, 10), "commit", "-q", "-m", "s1 work")
	sha1 := gitT(t, root, at(10, 10), "rev-parse", "HEAD")

	os.WriteFile(filepath.Join(root, "f"), []byte("x\n"), 0o644)
	gitT(t, root, at(12, 7), "add", "f")
	gitT(t, root, at(12, 7), "commit", "-q", "-m", "s2 or s3")

	markRegistered(t, home, root) // the join reads a registered checkout's trail only (ADR-068)
	o := brain.Options{Home: home, Dir: root, Now: func() time.Time { return at(14, 0) }}
	for _, r := range [][4]string{
		{"handoff", "HANDOFF-2026-10-08-1015-x", "Handoff — x", "handoff\n"},
		{"testing", "TEST-2026-10-08-1016-verify-x", "Verification — x", "# V\n\n## Status: PASS\n"},
		{"review", "REVIEW-2026-10-08-1017-review-x", "Review — x", "- **[high / confidence: high]** a\n- **[low / confidence: low]** b\n- **[low / confidence: high]** c\n"},
		{"bug", "BUG-007", "broke", "Introduced in " + sha1[:9] + ".\n"},
	} {
		addRecord(t, o, brain.AddInput{Kind: r[0], ID: r[1], Title: r[2]}, r[3])
	}
	gitT(t, root, at(14, 0), "add", ".agents")
	gitT(t, root, at(14, 0), "commit", "-q", "-m", "trail") // after every window

	g := brain.Options{Home: home, Dir: t.TempDir(), Global: true, Now: func() time.Time { return at(14, 0) }}
	addRecord(t, g, brain.AddInput{Kind: "feedback", Title: "fb", Project: root}, `{"ts":"2026-10-08T10:05:00Z","rule":"x"}`)
	addRecord(t, g, brain.AddInput{Kind: "feedback", Title: "fb", Project: root}, `{"ts":"2026-10-08T15:00:00Z"}`)

	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	for _, x := range []struct {
		id         string
		start, end time.Time
	}{{"S1", at(10, 0), at(10, 20)}, {"S2", at(12, 0), at(12, 10)}, {"S3", at(12, 5), at(12, 30)}} {
		if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES (?, 'agy', ?, ?, ?)`,
			x.id, root, x.start.UnixMilli(), x.end.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	return s, home, sha1
}

func TestJoinEvents(t *testing.T) {
	s, home, sha1 := eventsFixture(t)
	if _, err := s.joinEvents(home); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	rows, _ := s.db.Query(`SELECT session_id, kind, ref, value FROM events ORDER BY 1, 2, 3`)
	for rows.Next() {
		var sid, kind, ref, value string
		rows.Scan(&sid, &kind, &ref, &value)
		if kind == "commit" && ref != sha1 {
			ref = "other"
		}
		got[sid+" "+kind+" "+ref] = value
	}
	rows.Close()
	want := map[string]string{
		"S1 commit " + sha1:                         "",
		"S1 handoff HANDOFF-2026-10-08-1015-x":      "",
		"S1 verifier TEST-2026-10-08-1016-verify-x": "PASS",
		"S1 review REVIEW-2026-10-08-1017-review-x": "high=1,medium=0,low=2",
		"S1 bug BUG-007":                            sha1,
		"S1 correction 2026-10-08T10:05:00Z":        "",
		"S2 commit other":                           "ambiguous",
		"S3 commit other":                           "ambiguous",
	}
	for k, v := range want {
		if gv, ok := got[k]; !ok || gv != v {
			t.Errorf("event %q = %q (present %v), want %q", k, gv, ok, v)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected event %q", k)
		}
	}
	n := len(got)
	if _, err := s.joinEvents(home); err != nil {
		t.Fatal(err)
	}
	var again int
	s.db.QueryRow(`SELECT count(*) FROM events`).Scan(&again)
	if again != n {
		t.Fatalf("second join: %d events, want %d", again, n)
	}
}

// events is every stored event as "session kind ref=value", sorted.
func events(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT session_id, kind, ref, value FROM events ORDER BY 1, 2, 3`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sid, kind, ref, value string
		if err := rows.Scan(&sid, &kind, &ref, &value); err != nil {
			t.Fatal(err)
		}
		out = append(out, sid+" "+kind+" "+ref+"="+value)
	}
	return out
}

// The rewrite turns an older ingest's file-name refs into ID form, drops the
// one a session already holds in ID form, leaves every other kind alone, and
// is a no-op the second time.
func TestRefRewrite(t *testing.T) {
	s, home, _ := eventsFixture(t)
	for _, e := range [][4]string{
		{"S1", "handoff", "2026-10-08-1015-x.md", ""}, // what an older ingest stored for the fixture's own handoff
		{"S2", "handoff", "Lone Handoff.md", ""},
		{"S2", "verifier", "2026-01-01-verify-y.md", "FAIL"},
		{"S2", "review", "R+1.md", "high=0,medium=1,low=0"},
		{"S3", "handoff", "dup.md", "old"},
		{"S3", "handoff", "HANDOFF-dup", "new"},
		{"S3", "handoff", "HANDOFF-notes.md", ""}, // already an ID: a file notes.md.md imports as this
		{"S3", "commit", "c.md", ""},
		{"S3", "bug", "BUG-010.md", "abc"},
		{"S3", "correction", "x.md", ""},
	} {
		if _, err := s.db.Exec(`INSERT INTO events(session_id, ts, kind, ref, value) VALUES (?, 1, ?, ?, ?)`, e[0], e[1], e[2], e[3]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.joinEvents(home); err != nil {
		t.Fatal(err)
	}
	got := events(t, s)
	has := map[string]bool{}
	for _, e := range got {
		has[e] = true
	}
	for _, want := range []string{
		"S1 handoff HANDOFF-2026-10-08-1015-x=",
		"S2 handoff HANDOFF-lone-handoff=",
		"S2 verifier TEST-2026-01-01-verify-y=FAIL",
		"S2 review REVIEW-r-1=high=0,medium=1,low=0",
		"S3 handoff HANDOFF-dup=new",
		"S3 handoff HANDOFF-notes.md=",
		"S3 commit c.md=",
		"S3 bug BUG-010.md=abc",
		"S3 correction x.md=",
	} {
		if !has[want] {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	for _, e := range got {
		for _, gone := range []string{"S1 handoff 2026-10-08-1015-x.md", "S3 handoff dup.md"} {
			if strings.HasPrefix(e, gone) {
				t.Errorf("left behind: %q", e)
			}
		}
		if f := strings.Fields(e); (f[1] == "handoff" || f[1] == "verifier" || f[1] == "review") && strings.Contains(f[2], ".md=") && f[2] != "HANDOFF-notes.md=" {
			t.Errorf("a file-name ref survived: %q", e)
		}
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM events WHERE session_id = 'S1' AND kind = 'handoff'`).Scan(&n)
	if n != 1 {
		t.Errorf("S1 holds %d handoff events for one file, want 1", n)
	}
	if _, err := s.joinEvents(home); err != nil {
		t.Fatal(err)
	}
	if again := events(t, s); strings.Join(again, "\n") != strings.Join(got, "\n") {
		t.Errorf("a second join changed the store:\n%v\nwant\n%v", again, got)
	}
}

func TestShowOutcomeFromEvents(t *testing.T) {
	s, home, _ := eventsFixture(t)
	s.joinEvents(home)
	sum, err := s.Show(ShowOptions{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if c := sum.A.DoDClosed["agy"]; c.Closed != 1 || c.Ambiguous != 0 || c.Of != 3 {
		t.Errorf("dod_closed = %+v, want closed 1 of 3", c)
	}
	if sum.A.Verifier["PASS"] != 1 || sum.A.ReviewFindings["high"] != 1 || sum.A.ReviewFindings["low"] != 2 ||
		sum.A.EscapedBugs != 1 || sum.A.Corrections != 1 {
		t.Errorf("A = %+v", sum.A)
	}
}

// A session started in a subdirectory of the project belongs to it: its
// commits join (events group by git toplevel) and --project selects it, while
// a sibling whose name only shares the prefix does not match.
func TestSubdirectorySessionsBelongToTheProject(t *testing.T) {
	s, home, sha1 := eventsFixture(t)
	var root string
	s.db.QueryRow(`SELECT project_root FROM sessions WHERE id = 'S1'`).Scan(&root)
	sub := filepath.Join(root, ".agents")
	s.db.Exec(`UPDATE sessions SET project_root = ? WHERE id = 'S1'`, sub)
	s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES ('U', 'agy', ?, 1, 2)`,
		strings.ToUpper(root)+"/sub") // another directory on a case-sensitive file system
	sibling := root + "x"
	os.MkdirAll(sibling, 0o755)
	s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES ('X', 'agy', ?, 1, 2)`, sibling)
	if _, err := s.joinEvents(home); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM events WHERE session_id = 'S1' AND kind = 'commit' AND ref = ?`, sha1).Scan(&n)
	if n != 1 {
		t.Errorf("the subdirectory session's commit did not join")
	}
	s.db.QueryRow(`SELECT count(*) FROM events WHERE session_id = 'S1' AND kind = 'handoff'`).Scan(&n)
	if n != 1 {
		t.Errorf("the subdirectory session's handoff did not join (the trail is the toplevel's)")
	}
	sum, err := s.Show(ShowOptions{Since: time.Unix(0, 0), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), Project: root})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sessions["agy"] != 3 {
		t.Errorf("--project %s selected %d sessions, want 3 (S1 in a subdirectory, S2, S3; not the sibling)", root, sum.Sessions["agy"])
	}
}
