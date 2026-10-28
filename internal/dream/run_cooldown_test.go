package dream

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCooldown: a project whose CLI call failed is not tried again by --all
// within 24 hours (the dry run says so too), is tried by --project, and is
// tried by --all once the 24 hours are past.
func TestCooldown(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	rec := fakeAgy(t, `{"proposals":[],"skills":[]}`, 3)
	stdin := filepath.Join(rec, "stdin")
	called := func() bool {
		_, err := os.Stat(stdin)
		os.Remove(stdin)
		return err == nil
	}
	at := now
	o := runOpts(f, &bytes.Buffer{})
	o.Now = func() time.Time { return at }

	if Run(o); !called() {
		t.Fatal("the first run did not call the CLI")
	}
	st, _ := readState(f.home)
	if !st.Attempts[f.root].Equal(now) || !st.Projects[f.root].IsZero() {
		t.Fatalf("after a failed call: attempts %v, watermark %v", st.Attempts, st.Projects[f.root])
	}

	at = now.Add(23 * time.Hour)
	if Run(o); called() {
		t.Error("--all tried the project again within 24 hours")
	}
	if want := f.root + " skip attempted " + now.UTC().Format(time.RFC3339); !strings.Contains(dreamLog(t, f), want) {
		t.Errorf("log lacks %q:\n%s", want, dreamLog(t, f))
	}
	var out bytes.Buffer
	dry := o
	dry.DryRun, dry.Out = true, &out
	if err := Run(dry); err != nil || !strings.Contains(out.String(), f.root+": skip (attempted "+now.UTC().Format(time.RFC3339)+")") {
		t.Errorf("dry run within the cooldown: %v\n%s", err, out.String())
	}

	p := o
	p.All, p.Project = false, f.root
	if Run(p); !called() {
		t.Error("--project was held by the cooldown")
	}

	at = now.Add(23*time.Hour + 25*time.Hour) // past --project's own attempt too
	if Run(o); !called() {
		t.Error("--all did not try the project after 24 hours")
	}
}

// TestStateWithoutAttempts: a file an older fyr wrote has no attempts and
// reads as none, and a run adds them.
func TestStateWithoutAttempts(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	write(t, statePath(f.home), `{"projects":{},"last_run":{"at":"2026-09-29T03:00:00Z","projects":1,"proposals":0}}`, old)
	st, err := readState(f.home)
	if err != nil || st.Attempts == nil || len(st.Attempts) != 0 {
		t.Fatalf("readState = %+v, %v", st, err)
	}
	fakeAgy(t, canned, 0)
	if err := Run(runOpts(f, &bytes.Buffer{})); err != nil {
		t.Fatal(err)
	}
	if st, _ := readState(f.home); !st.Attempts[f.root].Equal(now) {
		t.Errorf("attempts after a run: %v", st.Attempts)
	}
}
