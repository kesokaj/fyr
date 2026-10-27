package dream

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

// The arms of realRun and collectIn that their splits move into helpers,
// pinned before the splits (PLAN-2026-10-27-funclen Phase 7, D13). They use
// the package's existing test hooks (writeState, propose, forDream); no new
// one is added (D12).

func dreamLog(t *testing.T, f fixture) string {
	t.Helper()
	return read(t, filepath.Join(f.home, harness.StateDir, "dream.log"))
}

// A project whose checkout is gone, and one in use, are skipped and logged;
// with nothing dreamed, the run's own state write is the one that fails it.
func TestRunSkipsAndStateWriteAtFinish(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	fakeAgy(t, `{"proposals":[],"skills":[]}`, 0)
	o := runOpts(f, &bytes.Buffer{})
	gone := filepath.Join(t.TempDir(), "gone")
	o.Projects = func(string) ([]search.Project, error) {
		return []search.Project{{Root: gone, Name: "gone"}, project(f)}, nil
	}
	o.Active = func(string, search.Project, time.Time) string { return "a session open" }
	if err := Run(o); err != nil {
		t.Fatalf("run: %v", err)
	}
	log := dreamLog(t, f)
	for _, want := range []string{gone + " skip gone", f.root + " skip in use (a session open)"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}

	old := writeState
	writeState = func(string, []byte) error { return errors.New("disk full") }
	defer func() { writeState = old }()
	var ee *ExitError
	if err := Run(o); !errors.As(err, &ee) || ee.Code != 2 || !strings.Contains(ee.Msg, "could not be saved") || !strings.Contains(ee.Msg, "disk full") {
		t.Fatalf("err = %v, want exit 2 naming the lost watermarks", err)
	}
}

// An answer the scanner cannot read fails the project with the scanner's
// reason, not "invalid output".
func TestRunUnreadableAnswer(t *testing.T) {
	if testing.Short() {
		t.Skip("a 17 MB answer")
	}
	f := newFixture(t)
	scaffold(t, f)
	agyLogin(t)
	fakeCLI(t, "agy", strings.Repeat("x", 17<<20)+"\n", 0)
	var ee *ExitError
	if err := Run(runOpts(f, &bytes.Buffer{})); !errors.As(err, &ee) || ee.Code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if log := dreamLog(t, f); !strings.Contains(log, f.root+" fail output line too long or unreadable") {
		t.Errorf("log:\n%s", log)
	}
}

// Proposals that cannot be stored fail the project, and its watermark stays.
func TestRunProposalWriteFails(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	fakeAgy(t, canned, 0)
	old := propose
	propose = func(string, string, []brain.Proposal, time.Time) ([]string, error) {
		return nil, errors.New("store refused")
	}
	defer func() { propose = old }()
	var ee *ExitError
	if err := Run(runOpts(f, &bytes.Buffer{})); !errors.As(err, &ee) || ee.Code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if st, _ := readState(f.home); !st.Projects[f.root].IsZero() {
		t.Error("watermark advanced past unstored proposals")
	}
	if log := dreamLog(t, f); !strings.Contains(log, f.root+" fail ") || !strings.Contains(log, "store refused") {
		t.Errorf("log:\n%s", log)
	}
}

// A migrated project whose brain store cannot be read is an error naming
// the store.
func TestCollectBrainReadError(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.root, ".agents", "trail.jsonl"), "", fresh)
	old := forDream
	forDream = func(string, string, time.Time, time.Time) (brain.DreamInput, error) {
		return brain.DreamInput{}, errors.New("locked")
	}
	defer func() { forDream = old }()
	_, err := collectIn(f.home, f.home, project(f), time.Time{}, now, maxPrompt)
	if err == nil || err.Error() != "reading the brain store: locked" {
		t.Errorf("err = %v", err)
	}
}
