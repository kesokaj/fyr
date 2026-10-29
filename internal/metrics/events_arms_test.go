package metrics

import (
	"path/filepath"
	"strings"
	"testing"
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
