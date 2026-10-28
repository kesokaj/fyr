package dream

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

func session(on bool, err error) func() (bool, error) {
	return func() (bool, error) { return on, err }
}

// TestRunAllNeedsASession: with no agy session an --all run prints one line,
// exits 0 and creates nothing under a fresh home; a check that errors exits
// 2 and creates nothing. The positive control, a session, creates the
// state directory.
func TestRunAllNeedsASession(t *testing.T) {
	none := func(string) ([]search.Project, error) { return nil, nil }
	for _, c := range []struct {
		name    string
		session func() (bool, error)
		code    int
		out     string
		creates bool
	}{
		{"no session", session(false, nil), 0, "fyr dream: no agy session of this user; nothing to do\n", false},
		{"the check errors", session(false, errors.New("no /proc")), 2, "", false},
		{"a session", session(true, nil), 0, "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			var out bytes.Buffer
			err := Run(Options{Home: home, All: true, Out: &out, Now: func() time.Time { return now }, Projects: none, Scopes: noScopes, Active: idle, Session: c.session})
			var ee *ExitError
			switch {
			case c.code == 0 && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case c.code != 0 && (!errors.As(err, &ee) || ee.Code != c.code || !strings.Contains(ee.Msg, "no /proc")):
				t.Fatalf("err = %v, want exit %d naming the error", err, c.code)
			}
			if c.out != "" && out.String() != c.out {
				t.Errorf("out = %q, want %q", out.String(), c.out)
			}
			_, statErr := os.Stat(filepath.Join(home, harness.StateDir))
			if created := statErr == nil; created != c.creates {
				t.Errorf("%s created: %v, want %v", harness.StateDir, created, c.creates)
			}
		})
	}
}

// TestRunProjectNeverAsks: --project runs whatever the session check would
// say.
func TestRunProjectNeverAsks(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	rec := fakeAgy(t, canned, 0)
	o := runOpts(f, &bytes.Buffer{})
	o.All, o.Project = false, f.root
	o.Session = func() (bool, error) { t.Error("--project asked for a session"); return false, nil }
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rec, "stdin")); err != nil {
		t.Error("--project did not call the CLI")
	}
}

func TestDryRunSessionLine(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct {
		session func() (bool, error)
		want    string
	}{
		{session(true, nil), "[dry-run] agy session: yes\n"},
		{session(false, nil), "[dry-run] agy session: no (a real --all run would stop here)\n"},
	} {
		var out bytes.Buffer
		o := Options{Home: f.home, All: true, DryRun: true, Out: &out, Now: func() time.Time { return now }, Active: idle, Scopes: noScopes,
			Projects: func(string) ([]search.Project, error) { return []search.Project{project(f)}, nil }, Session: c.session}
		if err := Run(o); err != nil {
			t.Fatal(err)
		}
		// The line comes first, and the dry run goes on past it.
		if !strings.HasPrefix(out.String(), c.want) || !strings.Contains(out.String(), f.root+": would dream") {
			t.Errorf("dry run:\n%s", out.String())
		}
	}
	o := Options{Home: f.home, All: true, DryRun: true, Out: &bytes.Buffer{}, Session: session(false, errors.New("no ps"))}
	var ee *ExitError
	if err := Run(o); !errors.As(err, &ee) || ee.Code != 2 {
		t.Errorf("a dry run whose check errors: %v, want exit 2 as the real run", err)
	}
}

// TestNoSessionSweepsKilledRun: an --all run that stops for want of a
// session still removes a killed run's private home (it holds a login copy)
// once it is over an hour old, keeps a younger one, and writes no log or
// state; with the lock held by a live run it removes nothing. That it
// creates no state directory where there is none is TestRunAllNeedsASession.
func TestNoSessionSweepsKilledRun(t *testing.T) {
	none := func(string) ([]search.Project, error) { return nil, nil }
	home := t.TempDir()
	conf := filepath.Join(home, harness.StateDir)
	stale, young := filepath.Join(conf, "dream-agy-home-stale"), filepath.Join(conf, "dream-agy-home-young")
	for _, d := range []string{stale, young} {
		write(t, filepath.Join(d, ".gemini", "antigravity-cli", "antigravity-oauth-token"), "token", fresh)
	}
	ago := time.Now().Add(-2 * time.Hour)
	os.Chtimes(stale, ago, ago)
	o := Options{Home: home, All: true, Out: &bytes.Buffer{}, Projects: none, Scopes: noScopes, Active: idle, Session: session(false, nil)}

	unlock, held, err := lock(filepath.Join(conf, "dream.lock"))
	if err != nil || held {
		t.Fatalf("lock: %v %v", held, err)
	}
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Error("a run swept while another held the lock")
	}
	unlock()

	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("the stale private home was left")
	}
	if _, err := os.Stat(young); err != nil {
		t.Error("a private home under an hour old was swept")
	}
	for _, f := range []string{"dream.log", "dream-state.json"} {
		if _, err := os.Stat(filepath.Join(conf, f)); !os.IsNotExist(err) {
			t.Errorf("a run with no session wrote %s", f)
		}
	}
}
