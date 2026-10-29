package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
)

// recordsDay is the records fixtures' day; at is a time on it, in UTC.
var recordsDay = time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)

func at(h, m int) time.Time {
	return recordsDay.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
}

// stamp is the minute an ID carries for t: the machine's zone, as the
// brain stamps it and the join reads it (plan 2026-10-25-1754 D15).
func stamp(t time.Time) string { return t.In(time.Local).Format("2006-01-02-1504") }

func addRecord(t *testing.T, o brain.Options, in brain.AddInput, body string) {
	t.Helper()
	in.Body = strings.NewReader(body)
	if _, err := brain.Add(o, in); err != nil {
		t.Fatalf("add %s %s: %v", in.Kind, in.ID, err)
	}
}

// sessionsAt stores three main sessions for root: S1 10:00-10:20, S2
// 12:00:30-12:10 (it starts inside a minute), S3 12:05-12:30.
func sessionsAt(t *testing.T, root string) *Store {
	t.Helper()
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	for _, x := range []struct {
		id         string
		start, end time.Time
	}{{"S1", at(10, 0), at(10, 20)}, {"S2", at(12, 0).Add(30 * time.Second), at(12, 10)}, {"S3", at(12, 5), at(12, 30)}} {
		if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES (?, 'agy', ?, ?, ?)`,
			x.id, root, x.start.UnixMilli(), x.end.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// recordsFixture is a migrated project: its trail is records in a scratch
// home, its one commit (10:10) is S1's, and a leftover docs root holds a
// handoff file, which no join reads (D4; an unmigrated root's file is
// TestJoinUnmigratedGivesNoTrailEvents').
func recordsFixture(t *testing.T) (s *Store, home, root, sha1 string) {
	t.Helper()
	// A zone that is not UTC on every machine, so a stamp read in the wrong
	// zone is caught on a UTC box too. No test here runs in parallel.
	local := time.Local
	time.Local = time.FixedZone("T+2", 2*60*60)
	t.Cleanup(func() { time.Local = local })
	home = t.TempDir()
	root = filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(root, 0o755)
	gitT(t, root, at(9, 0), "init", "-q", "-b", "main")
	gitT(t, root, at(9, 0), "config", "commit.gpgsign", "false")
	os.WriteFile(filepath.Join(root, "f"), []byte("x\n"), 0o644)
	gitT(t, root, at(10, 10), "add", "f")
	gitT(t, root, at(10, 10), "commit", "-q", "-m", "s1 work")
	sha1 = gitT(t, root, at(10, 10), "rev-parse", "HEAD")
	markRegistered(t, home, root) // the join reads a registered checkout's trail only (ADR-068)

	o := brain.Options{Home: home, Dir: root, Now: func() time.Time { return at(14, 0) }}
	day := stamp(recordsDay)[:10]
	for _, r := range [][4]string{
		{"handoff", "HANDOFF-" + stamp(at(10, 15)) + "-x", "Handoff — x", "handoff"},
		{"handoff", "HANDOFF-" + stamp(at(10, 50)) + "-edge", "Handoff — the window's last minute", "handoff"},
		{"handoff", "HANDOFF-" + stamp(at(10, 51)) + "-after", "Handoff — past every window", "handoff"},
		{"handoff", "HANDOFF-" + stamp(at(12, 0)) + "-s2", "Handoff — the minute S2 starts inside", "handoff"},
		{"handoff", "HANDOFF-" + day + "-dateonly", "Handoff — no minute", "handoff"},
		{"handoff", "HANDOFF-" + day + "-2459-bad", "Handoff — no such minute", "handoff"},
		{"handoff", "HANDOFF-" + stamp(at(16, 0)) + "-late", "Handoff — edited inside S1", "handoff"},
		{"testing", "TEST-" + stamp(at(10, 16)) + "-verify-x", "Verification — x", "# Verification — x\n\n## Status: PASS\n"},
		{"testing", "TEST-" + stamp(at(10, 17)) + "-runs", "Behavioural runs — x", "## Status: PASS\n"},
		{"testing", "TEST-" + stamp(at(10, 18)) + "-verify-y", "Verification — y", "Status: FAIL\n"},
		{"review", "REVIEW-" + stamp(at(12, 6)) + "-r", "Review — r", "- **[high / confidence: high]** a\n- **[low / confidence: low]** b\n- **[low / confidence: high]** c\n"},
		{"bug", "BUG-007", "broke", "Introduced in " + sha1[:9] + ".\n"},
		{"change", "CHANGE-" + stamp(at(10, 15)) + "-c", "Change — c", "x"},
	} {
		addRecord(t, o, brain.AddInput{Kind: r[0], ID: r[1], Title: r[2]}, r[3])
	}
	// An edit inside S1 moves `updated`, never the record's time (D3).
	e := o
	e.Now = func() time.Time { return at(10, 12) }
	body := "edited"
	if err := brain.Edit(e, brain.EditInput{ID: "HANDOFF-" + stamp(at(16, 0)) + "-late", Body: strings.NewReader(body)}); err != nil {
		t.Fatal(err)
	}
	if err := brain.Edit(e, brain.EditInput{ID: "HANDOFF-" + day + "-dateonly", Body: strings.NewReader(body)}); err != nil {
		t.Fatal(err)
	}
	handoffs := filepath.Join(root, ".agents", "docs", "handoffs")
	os.MkdirAll(handoffs, 0o755)
	os.WriteFile(filepath.Join(handoffs, "2026-10-08-1015-file.md"), []byte("handoff\n"), 0o644)
	gitT(t, root, at(10, 11), "add", ".agents")
	gitT(t, root, at(10, 11), "commit", "-q", "-m", "trail")
	return sessionsAt(t, root), home, root, sha1
}

func TestJoinRecords(t *testing.T) {
	s, home, _, sha1 := recordsFixture(t)
	notes, err := s.joinEvents(home)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range events(t, s) {
		if !strings.Contains(e, " commit ") {
			got[e] = true
		}
	}
	review := "=high=1,medium=0,low=2"
	want := []string{
		"S1 handoff HANDOFF-" + stamp(at(10, 15)) + "-x=",
		"S1 handoff HANDOFF-" + stamp(at(10, 50)) + "-edge=",
		"S2 handoff HANDOFF-" + stamp(at(12, 0)) + "-s2=",
		"S1 verifier TEST-" + stamp(at(10, 16)) + "-verify-x=PASS",
		"S2 review REVIEW-" + stamp(at(12, 6)) + "-r" + review,
		"S3 review REVIEW-" + stamp(at(12, 6)) + "-r" + review,
		"S1 bug BUG-007=" + sha1,
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing event %q", w)
		}
		delete(got, w)
	}
	for e := range got {
		t.Errorf("unexpected event %q", e)
	}
	if len(notes) != 0 {
		t.Errorf("notes %v, want none", notes)
	}
	var ts int64
	s.db.QueryRow(`SELECT ts FROM events WHERE kind = 'handoff' AND ref LIKE '%-x'`).Scan(&ts)
	if want := at(10, 15).UnixMilli(); ts != want {
		t.Errorf("handoff ts %d, want its ID's minute %d", ts, want)
	}
	before := len(events(t, s))
	if _, err := s.joinEvents(home); err != nil {
		t.Fatal(err)
	}
	if after := len(events(t, s)); after != before {
		t.Errorf("second join: %d events, want %d", after, before)
	}
}

// A trail.jsonl that does not load is a note: the join goes on, the root
// keeps the events stored before, and ingest does not fail.
func TestJoinRecordsBrokenTrail(t *testing.T) {
	s, home, root, _ := recordsFixture(t)
	if _, err := s.joinEvents(home); err != nil {
		t.Fatal(err)
	}
	before := events(t, s)
	if err := os.WriteFile(filepath.Join(root, ".agents", "trail.jsonl"), []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes, err := s.joinEvents(home)
	if err != nil {
		t.Fatalf("join with a broken trail.jsonl: %v", err)
	}
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "records not read: "+root+": ") {
		t.Errorf("notes %q, want one naming the root", notes)
	}
	if after := events(t, s); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("events changed:\n%v\nwant\n%v", after, before)
	}
	r, err := Ingest(s, Options{Home: home}, &strings.Builder{})
	if err != nil || len(r.Failed) != 0 {
		t.Errorf("ingest: %+v %v, want no failure", r, err)
	}
}

// Corrections come from the global feedback records for every root,
// migrated, unmigrated or gone; one correction stored +02:00 by an older
// ingest from the feedback log and Z in a record is one event (D11).
func TestCorrections(t *testing.T) {
	type tc struct {
		name string
		mk   func(t *testing.T) (*Store, string, string)
	}
	for _, c := range []tc{
		{"migrated", func(t *testing.T) (*Store, string, string) {
			s, home, root, _ := recordsFixture(t)
			return s, home, root
		}},
		{"unmigrated", func(t *testing.T) (*Store, string, string) {
			root := filepath.Join(t.TempDir(), "proj")
			os.MkdirAll(root, 0o755)
			gitT(t, root, at(9, 0), "init", "-q", "-b", "main")
			return sessionsAt(t, root), t.TempDir(), root
		}},
		{"gone", func(t *testing.T) (*Store, string, string) {
			root := filepath.Join(t.TempDir(), "gone")
			return sessionsAt(t, root), t.TempDir(), root
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, home, root := c.mk(t)
			g := brain.Options{Home: home, Dir: t.TempDir(), Global: true, Now: func() time.Time { return at(14, 0) }}
			for _, body := range []string{
				`{"ts":"2026-10-08T10:05:00Z","rule":"x"}`,
				`{"ts":"2026-10-08T12:07:00.5+00:00"}`,
				`not json`,
				`{"rule":"no ts"}`,
			} {
				addRecord(t, g, brain.AddInput{Kind: "feedback", Title: "fb", Project: root}, body)
			}
			addRecord(t, g, brain.AddInput{Kind: "feedback", Title: "fb", Project: filepath.Join(t.TempDir(), "other")}, `{"ts":"2026-10-08T10:06:00Z"}`)
			// What an older ingest stored for a feedback log line about the
			// record's correction: its ts as written, +02:00.
			if _, err := s.db.Exec(`INSERT INTO events(session_id, ts, kind, ref, value) VALUES ('S1', 1, 'correction', '2026-10-08T12:05:00+02:00', '')`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.joinEvents(home); err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, e := range events(t, s) {
				if strings.Contains(e, " correction ") {
					got = append(got, e)
				}
			}
			want := []string{
				"S1 correction 2026-10-08T10:05:00Z=",
				"S2 correction 2026-10-08T12:07:00.5Z=",
				"S3 correction 2026-10-08T12:07:00.5Z=",
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("corrections\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// An unmigrated project gives commits and corrections only: a handoff file
// in its docs root, inside S1's window, is not joined, since the file join
// went with the crossing (plan 2026-10-27-ledger Phase 10, item 66).
func TestJoinUnmigratedGivesNoTrailEvents(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "proj")
	handoffs := filepath.Join(root, ".agents", "docs", "handoffs")
	os.MkdirAll(handoffs, 0o755)
	gitT(t, root, at(9, 0), "init", "-q", "-b", "main")
	gitT(t, root, at(9, 0), "config", "commit.gpgsign", "false")
	os.WriteFile(filepath.Join(handoffs, "2026-10-08-1015-file.md"), []byte("handoff\n"), 0o644)
	gitT(t, root, at(10, 10), "add", ".agents")
	gitT(t, root, at(10, 10), "commit", "-q", "-m", "s1 work")
	sha1 := gitT(t, root, at(10, 10), "rev-parse", "HEAD")
	g := brain.Options{Home: home, Dir: t.TempDir(), Global: true, Now: func() time.Time { return at(14, 0) }}
	addRecord(t, g, brain.AddInput{Kind: "feedback", Title: "fb", Project: root}, `{"ts":"2026-10-08T10:05:00Z"}`)
	s := sessionsAt(t, root)
	if _, err := s.joinEvents(home); err != nil {
		t.Fatal(err)
	}
	want := []string{"S1 commit " + sha1 + "=", "S1 correction 2026-10-08T10:05:00Z="}
	if got := events(t, s); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("events\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
