package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The arms of joinEvents that its split moves into helpers, pinned before
// the split (PLAN-2026-10-27-funclen Phase 3, D13).

// refuseKind makes every insert of an event of kind fail, through the
// store's own schema: a trigger, not a seam in the code (D12).
func refuseKind(t *testing.T, s *Store, kind string) {
	t.Helper()
	if _, err := s.db.Exec(`CREATE TRIGGER refuse BEFORE INSERT ON events WHEN NEW.kind = '` + kind +
		`' BEGIN SELECT RAISE(ABORT, 'refused ` + kind + `'); END`); err != nil {
		t.Fatal(err)
	}
}

// fixtureRoot is the project root the fixture's sessions were started in.
func fixtureRoot(t *testing.T, s *Store) string {
	t.Helper()
	var root string
	if err := s.db.QueryRow(`SELECT project_root FROM sessions LIMIT 1`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	return root
}

// A failed event write fails the join, whichever kind it is, and nothing
// of the run is kept.
func TestJoinEventsPutErrors(t *testing.T) {
	for _, kind := range []string{"correction", "commit", "handoff", "verifier", "review", "bug"} {
		t.Run(kind, func(t *testing.T) {
			s, home, _ := eventsFixture(t)
			refuseKind(t, s, kind)
			if _, err := s.joinEvents(home); err == nil || !strings.Contains(err.Error(), "refused "+kind) {
				t.Fatalf("joinEvents: %v, want the refused %s write", err, kind)
			}
			if n := len(events(t, s)); n != 0 {
				t.Errorf("%d events kept after a failed join, want 0", n)
			}
		})
	}
	t.Run("record", func(t *testing.T) {
		s, home, _, _ := recordsFixture(t)
		refuseKind(t, s, "handoff")
		if _, err := s.joinEvents(home); err == nil || !strings.Contains(err.Error(), "refused handoff") {
			t.Fatalf("joinEvents: %v, want the refused handoff write", err)
		}
	})
}

// The sessions table that cannot be read, or holds a row that does not
// scan, fails the join before any event is written.
func TestJoinEventsSessionReadErrors(t *testing.T) {
	for name, stmt := range map[string]string{
		"query": `ALTER TABLE sessions RENAME TO gone`,
		"scan":  `UPDATE sessions SET started_at = 'x' WHERE id = 'S2'`,
	} {
		t.Run(name, func(t *testing.T) {
			s, home, _ := eventsFixture(t)
			if _, err := s.db.Exec(stmt); err != nil {
				t.Fatal(err)
			}
			if _, err := s.joinEvents(home); err == nil {
				t.Fatal("joinEvents: nil error, want the sessions read's")
			}
		})
	}
}

// A session's root that is gone, or is not a git checkout, gives the join a
// note naming it and no commit events (pinned after the Phase 3 review, its
// finding 2).
func TestJoinEventsRootNotes(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	plain := t.TempDir()
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	for i, root := range []string{gone, plain} {
		if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES (?, 'agy', ?, 1, 2)`,
			"S"+string(rune('1'+i)), root); err != nil {
			t.Fatal(err)
		}
	}
	notes, err := s.joinEvents(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(notes, "\n")
	for _, want := range []string{"project root gone: " + gone, "no git history: " + plain} {
		if !strings.Contains(joined, want) {
			t.Errorf("notes %q, want one %q", notes, want)
		}
	}
	if n := len(events(t, s)); n != 0 {
		t.Errorf("%d events, want 0", n)
	}
}

// The file join's quieter arms: a project with no docs root reads no trail
// files, an untracked trail file is timed by its mtime, a verifier log with
// no status line and a bug file with no BUG-N name give no event.
func TestJoinFilesArms(t *testing.T) {
	inS1 := time.Date(2026, 10, 8, 10, 16, 0, 0, time.UTC)
	put := func(t *testing.T, p, body string) {
		t.Helper()
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, inS1, inS1); err != nil {
			t.Fatal(err)
		}
	}
	has := func(s *Store, prefix string) bool {
		for _, e := range events(t, s) {
			if strings.HasPrefix(e, prefix) {
				return true
			}
		}
		return false
	}

	t.Run("no docs root", func(t *testing.T) {
		s, home, sha1 := eventsFixture(t)
		root := fixtureRoot(t, s)
		os.RemoveAll(filepath.Join(root, ".agents"))
		put(t, filepath.Join(root, "handoffs", "2026-10-08-1016-root.md"), "handoff\n")
		if _, err := s.joinEvents(home); err != nil {
			t.Fatal(err)
		}
		if !has(s, "S1 commit "+sha1) {
			t.Errorf("events %v: the commit is missing", events(t, s))
		}
		if has(s, "S1 handoff ") {
			t.Errorf("events %v: a handoff from outside any docs root", events(t, s))
		}
	})
	t.Run("untracked by mtime", func(t *testing.T) {
		s, home, _ := eventsFixture(t)
		root := fixtureRoot(t, s)
		put(t, filepath.Join(root, ".agents", "docs", "handoffs", "2026-10-08-1016-u.md"), "handoff\n")
		if _, err := s.joinEvents(home); err != nil {
			t.Fatal(err)
		}
		var ts int64
		s.db.QueryRow(`SELECT ts FROM events WHERE kind = 'handoff' AND ref = 'HANDOFF-2026-10-08-1016-u'`).Scan(&ts)
		if ts != inS1.UnixMilli() {
			t.Errorf("untracked handoff ts %d, want its mtime %d", ts, inS1.UnixMilli())
		}
	})
	t.Run("no status, no BUG name", func(t *testing.T) {
		s, home, sha1 := eventsFixture(t)
		docs := filepath.Join(fixtureRoot(t, s), ".agents", "docs")
		put(t, filepath.Join(docs, "testing", "2026-10-08-verify-y.md"), "# V\n\nno status here\n")
		put(t, filepath.Join(docs, "bugs", "BUG-x.md"), "Introduced in "+sha1[:9]+".\n")
		if _, err := s.joinEvents(home); err != nil {
			t.Fatal(err)
		}
		if has(s, "S1 verifier TEST-2026-10-08-verify-y") {
			t.Errorf("events %v: a verifier event with no status line", events(t, s))
		}
		if has(s, "S1 bug BUG-x") {
			t.Errorf("events %v: a bug event from a file with no BUG-N name", events(t, s))
		}
	})
}
