package dream

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

func TestRunWritesProposalsDraftsAndState(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	write(t, filepath.Join(f.root, ".agents", "memory", "leak.md"), "token = FAKEVALUE\n", fresh)
	rec := fakeAgy(t, canned, 0)
	var out bytes.Buffer
	if err := Run(runOpts(f, &out)); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	name := project(f).Name
	dir := filepath.Join(f.home, harness.StateDir, "proposals", "dream")
	recs := dreamProposals(t, f.home)
	if len(recs) != 3 {
		t.Fatalf("want two proposal records and one for the draft skills, got %d: %+v", len(recs), recs)
	}
	vet, ok := titled(recs, "Dream, project — Vet before tag")
	if !ok || vet.Status != "queued" || vet.Project != f.root {
		t.Fatalf("the project proposal: %+v", vet)
	}
	for _, want := range []string{"Dream of " + f.root, "by agy", "Target: project", "Run go vet before every tag."} {
		if !strings.Contains(vet.Body, want) {
			t.Errorf("the project proposal lacks %q:\n%s", want, vet.Body)
		}
	}
	if rel, ok := titled(recs, "Dream, harness — Release skill"); !ok || !strings.Contains(rel.Body, "Target: harness") {
		t.Errorf("the harness proposal: %+v", rel)
	}
	draft := read(t, filepath.Join(dir, "drafts", "2026-09-30-"+name+"-release-steps", "SKILL.md"))
	if !strings.HasPrefix(draft, "---\nname: release-steps\ndescription: \"cut a release\"\n---\n") {
		t.Errorf("draft:\n%s", draft)
	}
	ds, ok := titled(recs, "Dream, harness — draft skills for ")
	if !ok || !strings.Contains(ds.Body, "release-steps/SKILL.md") || !strings.Contains(ds.Body, "../escape") || !strings.Contains(ds.Body, "dropped") {
		t.Errorf("the draft skills record does not name the draft and the dropped name: %+v", ds)
	}
	if strings.Contains(read(t, filepath.Join(rec, "stdin")), "FAKEVALUE") {
		t.Error("a secret reached the CLI")
	}
	args := read(t, filepath.Join(rec, "args"))
	if !strings.Contains(args, "--mode\nplan\n") || !strings.Contains(args, "--input-format\nstream-json\n") {
		t.Errorf("agy called without plan mode and stream-json input:\n%s", args)
	}
	if cwd := strings.TrimSpace(read(t, filepath.Join(rec, "cwd"))); !sameDir(cwd, filepath.Join(f.home, harness.StateDir, "dream-work")) {
		t.Errorf("cwd = %s, want dream-work", cwd)
	}
	st, _ := readState(f.home)
	if !st.Projects[f.root].Equal(now) || st.LastRun.Projects != 1 || st.LastRun.Proposals != 2 || st.LastRun.Error != "" {
		t.Errorf("state = %+v", st)
	}
	log := read(t, filepath.Join(f.home, harness.StateDir, "dream.log"))
	if !strings.Contains(log, f.root+" ok 2 proposals") || !strings.Contains(log, "- run 1 projects, 2 proposals") {
		t.Errorf("log:\n%s", log)
	}
	// A second run on the same day with no change calls nothing.
	os.Remove(filepath.Join(rec, "stdin"))
	if err := Run(runOpts(f, &out)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rec, "stdin")); !os.IsNotExist(err) {
		t.Error("an unchanged project called the CLI")
	}
	if !strings.Contains(read(t, filepath.Join(f.home, harness.StateDir, "dream.log")), f.root+" unchanged") {
		t.Error("unchanged not logged")
	}
}

func TestRunPreProjectFailureIsExit2AndRecorded(t *testing.T) {
	f := newFixture(t)
	fakeAgy(t, `{"proposals":[],"skills":[]}`, 0)
	o := runOpts(f, &bytes.Buffer{})
	o.Projects = func(string) ([]search.Project, error) { return nil, errors.New("search.db is unreadable") }
	var ee *ExitError
	if err := Run(o); !errors.As(err, &ee) || ee.Code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	st, _ := readState(f.home)
	if !strings.Contains(st.LastRun.Error, "unreadable") {
		t.Errorf("last_run.error = %q", st.LastRun.Error)
	}
	if !strings.Contains(read(t, filepath.Join(f.home, harness.StateDir, "dream.log")), "error search.db is unreadable") {
		t.Error("not logged")
	}
}

func TestRunCorruptStateIsExit2AndKept(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	rec := fakeAgy(t, `{"proposals":[],"skills":[]}`, 0)
	write(t, statePath(f.home), "{corrupt", fresh)
	var ee *ExitError
	if err := Run(runOpts(f, &bytes.Buffer{})); !errors.As(err, &ee) || ee.Code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	if got := read(t, statePath(f.home)); got != "{corrupt" {
		t.Errorf("the corrupt state was rewritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(rec, "stdin")); !os.IsNotExist(err) {
		t.Error("a project ran on a corrupt state")
	}
}

func TestRunRemovesLeftoverCopies(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	fakeAgy(t, `{"proposals":[],"skills":[]}`, 0)
	conf := filepath.Join(f.home, harness.StateDir)
	write(t, filepath.Join(conf, "dream-agy-home-123", ".gemini", "antigravity-cli", "antigravity-oauth-token"), "token", old)
	write(t, filepath.Join(conf, "dream-projects-9", "search.db"), "db", old)
	// A left-over macOS private HOME links the real Keychains folder: the
	// sweep removes the link, never what it points at (BUG-166).
	keychains := filepath.Join(t.TempDir(), "Keychains")
	write(t, filepath.Join(keychains, "login.keychain-db"), "keychain", old)
	if err := os.MkdirAll(filepath.Join(conf, "dream-agy-home-123", "Library"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(keychains, filepath.Join(conf, "dream-agy-home-123", "Library", "Keychains")); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"dream-agy-home-123", "dream-projects-9"} {
		os.Chtimes(filepath.Join(conf, d), old, old)
	}
	if err := Run(runOpts(f, &bytes.Buffer{})); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"dream-agy-home-123", "dream-projects-9"} {
		if _, err := os.Stat(filepath.Join(conf, d)); !os.IsNotExist(err) {
			t.Errorf("%s left behind", d)
		}
	}
	if read(t, filepath.Join(keychains, "login.keychain-db")) != "keychain" {
		t.Error("the sweep reached through the link into the Keychains folder")
	}
}

func TestRunInvalidOutputKeepsRawAndWatermark(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	agyLogin(t)
	fakeCLI(t, "agy", "no json here\ntoken = FAKEVALUE\n", 0)
	var out bytes.Buffer
	err := Run(runOpts(f, &out))
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	st, _ := readState(f.home)
	if !st.Projects[f.root].IsZero() {
		t.Error("watermark advanced on a failed project")
	}
	if !strings.Contains(st.LastRun.Error, "failed") {
		t.Errorf("last_run.error = %q after a failed project: status would read the run as clean", st.LastRun.Error)
	}
	failed := filepath.Join(f.home, harness.StateDir, "dream-failed")
	entries, _ := os.ReadDir(failed)
	if len(entries) == 0 {
		t.Fatal("raw output not kept")
	}
	raw := read(t, filepath.Join(failed, entries[0].Name()))
	if !strings.Contains(raw, "no json here") || strings.Contains(raw, "FAKEVALUE") {
		t.Errorf("kept raw output unredacted or missing:\n%s", raw)
	}
	if !strings.Contains(read(t, filepath.Join(f.home, harness.StateDir, "dream.log")), "fail invalid output") {
		t.Error("failure not logged")
	}
}

func TestRunCLIExitFails(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	fakeAgy(t, `{"proposals":[],"skills":[]}`, 3)
	err := Run(runOpts(f, &bytes.Buffer{}))
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if st, _ := readState(f.home); !st.Projects[f.root].IsZero() {
		t.Error("watermark advanced on a failing CLI")
	}
}

func TestRunNoCLIFailsBeforeProjects(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	t.Setenv("PATH", t.TempDir())
	err := Run(runOpts(f, &bytes.Buffer{}))
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
	st, _ := readState(f.home)
	if !strings.Contains(st.LastRun.Error, "no agy on PATH") {
		t.Errorf("last_run.error = %q", st.LastRun.Error)
	}
	if !strings.Contains(read(t, filepath.Join(f.home, harness.StateDir, "dream.log")), "error") {
		t.Error("pre-project failure not logged")
	}
}

func TestRunLock(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	rec := fakeAgy(t, `{"proposals":[],"skills":[]}`, 0)
	path := filepath.Join(f.home, harness.StateDir, "dream.lock")
	write(t, path, "", fresh)
	held, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(runOpts(f, &out)); err != nil || !strings.Contains(out.String(), "already running") {
		t.Fatalf("held lock: %v, %q", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(rec, "stdin")); !os.IsNotExist(err) {
		t.Error("ran under a held lock")
	}
	held.Close() // released, as when a run dies
	if err := Run(runOpts(f, &out)); err != nil {
		t.Fatalf("after release: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rec, "stdin")); err != nil {
		t.Error("a released lock blocked the run")
	}
}

func TestRunPrunesOldFailures(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	fakeAgy(t, `{"proposals":[],"skills":[]}`, 0)
	failed := filepath.Join(f.home, harness.StateDir, "dream-failed")
	write(t, filepath.Join(failed, "2026-08-01-x.txt"), "old", now.Add(-40*24*time.Hour))
	write(t, filepath.Join(failed, "2026-09-29-y.txt"), "recent", now.Add(-24*time.Hour))
	if err := Run(runOpts(f, &bytes.Buffer{})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(failed, "2026-08-01-x.txt")); !os.IsNotExist(err) {
		t.Error("a failure older than 30 days was kept")
	}
	if _, err := os.Stat(filepath.Join(failed, "2026-09-29-y.txt")); err != nil {
		t.Error("a recent failure was pruned")
	}
}

// TestRunAgyReadErrorKeepsWatermark: an Antigravity database that cannot be
// read fails the project, so its conversations are read again next time.
func TestRunAgyReadErrorKeepsWatermark(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	write(t, filepath.Join(f.home, ".gemini", "antigravity-cli", "conversation_summaries.db"), "not a database at all", fresh)
	fakeAgy(t, `{"proposals":[],"skills":[]}`, 0)
	var ee *ExitError
	if err := Run(runOpts(f, &bytes.Buffer{})); !errors.As(err, &ee) || ee.Code != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if st, _ := readState(f.home); !st.Projects[f.root].IsZero() {
		t.Error("watermark advanced past unread conversations")
	}
	if !strings.Contains(read(t, filepath.Join(f.home, harness.StateDir, "dream.log")), "fail reading Antigravity") {
		t.Error("the read error is not logged")
	}
}

func TestRunStateWriteFailureIsExit2(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	fakeAgy(t, `{"proposals":[],"skills":[]}`, 0)
	old := writeState
	writeState = func(string, []byte) error { return errors.New("disk full") }
	defer func() { writeState = old }()
	var ee *ExitError
	if err := Run(runOpts(f, &bytes.Buffer{})); !errors.As(err, &ee) || ee.Code != 2 || !strings.Contains(ee.Msg, "could not be saved") {
		t.Fatalf("err = %v, want exit 2 naming the lost watermarks", err)
	}
}
