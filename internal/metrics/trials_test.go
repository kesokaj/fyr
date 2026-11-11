package metrics

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A store written at schema 3 (before trials) upgrades in place: its rows
// stay, its sessions get a NULL run, the trials table, migration-5 step
// and stage columns, migration-6 turn_vets/work_waves tables, and migration-7
// work_waves stage columns exist, the schema reads 7; a store at schema 8 is
// still refused untouched.
func TestMigrationThreeToCurrent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "metrics.db")
	saved := migrations
	t.Cleanup(func() { migrations = saved })
	migrations = saved[:3]
	s := openT(t, p)
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, label, outcome, started_at) VALUES ('old', 'agy', 'eval=x', 'pass', 5)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, kind) VALUES ('old', 'e1', 'tool')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO trials(run) VALUES ('x')`); err == nil {
		t.Fatal("the schema-3 fixture already has a trials table: the test proves nothing")
	}
	s.Close()

	migrations = saved
	s2 := openT(t, p)
	if v := s2.meta(t, "schema"); v != "7" {
		t.Fatalf("schema after upgrade = %s, want 7", v)
	}
	var got string
	if err := s2.db.QueryRow(`SELECT (SELECT count(*) FROM trials) || ',' ||
		(SELECT count(*) FROM turn_vets) || ',' ||
		(SELECT count(*) FROM work_waves WHERE setup_s = 0 AND agy_s = 0 AND check_s = 0) || ',' ||
		(SELECT count(*) FROM sessions WHERE id = 'old' AND run IS NULL AND outcome = 'pass') || ',' ||
		(SELECT count(*) FROM steps WHERE session_id = 'old' AND started_ms IS NULL AND ended_ms IS NULL AND wait_ms IS NULL)`).Scan(&got); err != nil || got != "0,0,0,1,1" {
		t.Fatalf("after upgrade: %q, %v; want 0,0,0,1,1", got, err)
	}
	s2.db.Exec(`UPDATE meta SET value = '8' WHERE key = 'schema'`)
	s2.Close()
	if s3, err := Open(p); err == nil {
		s3.Close()
		t.Fatal("a store at schema 8 was opened by a binary at 7")
	} else if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("schema 8: %v, want ErrNewerSchema", err)
	}
}

func trialT(run, status, cause string) Trial {
	return Trial{Run: run, Label: "eval=smoke", Harness: "abc1234 (dirty)", CLI: "agy 1.4.12", Status: status, Cause: cause,
		StartedAt: time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC), Seconds: 61}
}

// A trial is stored once: recording the same run again adds no row and
// changes none; the per-row fields and the stage times are NULL until
// given; the export carries the table.
func TestRecordTrialOnce(t *testing.T) {
	dir := t.TempDir()
	s := openT(t, filepath.Join(dir, "metrics.db"))
	if err := s.RecordTrial(trialT("2026-10-31-100000/smoke/1", "pass", "")); err != nil {
		t.Fatal(err)
	}
	again := trialT("2026-10-31-100000/smoke/1", "fail", "check")
	if err := s.RecordTrial(again); err != nil {
		t.Fatal(err)
	}
	setup, secs, check := int64(3), int64(40), int64(2)
	errored := trialT("2026-10-31-100000/smoke/2", "error", "setup")
	errored.SetupSeconds, errored.SessionSeconds, errored.CheckSeconds = &setup, &secs, &check
	errored.RowVersion, errored.Kind, errored.Cluster, errored.Expect = 2, "regression", "fam", "Read,fyr-record"
	if err := s.RecordTrial(errored); err != nil {
		t.Fatal(err)
	}
	noCLI := trialT("2026-10-31-100000/smoke/3", "fail", "check")
	noCLI.CLI = ""
	if err := s.RecordTrial(noCLI); err != nil {
		t.Fatal(err)
	}
	var got string
	err := s.db.QueryRow(`SELECT count(*) || ',' ||
		(SELECT status || '/' || cause || '/' || COALESCE(row_version, 'null') || '/' || COALESCE(kind, 'null') || '/' || COALESCE(cluster, 'null') || '/' || COALESCE(expect, 'null') || '/' || COALESCE(setup_seconds, 'null') || '/' || COALESCE(session_seconds, 'null') || '/' || COALESCE(check_seconds, 'null') || '/' || harness || '/' || cli || '/' || started_at || '/' || seconds
			FROM trials WHERE run = '2026-10-31-100000/smoke/1') || ',' ||
		(SELECT status || '/' || cause || '/' || row_version || '/' || kind || '/' || cluster || '/' || expect || '/' || setup_seconds || '/' || session_seconds || '/' || check_seconds
			FROM trials WHERE run = '2026-10-31-100000/smoke/2') || ',' ||
		(SELECT status || '/' || cause || '/' || COALESCE(cli, 'null') FROM trials WHERE run = '2026-10-31-100000/smoke/3') FROM trials`).Scan(&got)
	want := fmt.Sprintf("3,pass//null/null/null/null/null/null/null/abc1234 (dirty)/agy 1.4.12/%d/61,error/setup/2/regression/fam/Read,fyr-record/3/40/2,fail/check/null", again.StartedAt.UnixMilli())
	if err != nil || got != want {
		t.Fatalf("trials:\n got %q\nwant %q\n%v", got, want, err)
	}
	out := filepath.Join(dir, "e.jsonl")
	if err := s.Export(out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); !strings.Contains(string(b), `{"table":"trials","row":{`) {
		t.Error("the export has no trials rows")
	}
}

// What a trial must look like to be stored: the status set, a cause only
// on a fail or an error and identifier-shaped, a run, a harness and a
// KEY=VALUE label, printable short text, no count below 0.
func TestRecordTrialRefuses(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	bad := map[string]Trial{
		"status":          trialT("r", "PASS", ""),
		"pass with cause": trialT("r", "pass", "check"),
		"fail no cause":   trialT("r", "fail", ""),
		"error no cause":  trialT("r", "error", ""),
		"cause prose":     trialT("r", "fail", "the check failed"),
	}
	noRun := trialT("", "pass", "")
	bad["no run"] = noRun
	noHarness := trialT("r", "pass", "")
	noHarness.Harness = ""
	bad["no harness"] = noHarness
	badLabel := trialT("r", "pass", "")
	badLabel.Label = "smoke"
	bad["label"] = badLabel
	escape := trialT("r", "pass", "")
	escape.CLI = "agy \x1b[2J"
	bad["escape in cli"] = escape
	long := trialT("r", "pass", "")
	long.Harness = strings.Repeat("a", trialText+1)
	bad["long harness"] = long
	negative := trialT("r", "pass", "")
	negative.Seconds = -1
	bad["negative seconds"] = negative
	negStage := int64(-1)
	negSetup := trialT("r", "pass", "")
	negSetup.SetupSeconds = &negStage
	bad["negative setup"] = negSetup
	negCheck := trialT("r", "pass", "")
	negCheck.CheckSeconds = &negStage
	bad["negative check"] = negCheck
	noStart := trialT("r", "pass", "")
	noStart.StartedAt = time.Time{}
	bad["zero start time"] = noStart
	for name, tr := range bad {
		if err := s.RecordTrial(tr); err == nil {
			t.Errorf("%s: stored", name)
		} else if strings.Contains(err.Error(), "\x1b") {
			t.Errorf("%s: the error carries the escape byte: %q", name, err)
		}
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM trials`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d trials stored from refused inputs", n)
	}
}

// Options.Run reaches every session of the source at insert and stays: a
// second ingest of the same conversations, changed in between, with another
// run keeps the first; an ingest with no run leaves NULL.
func TestIngestRun(t *testing.T) {
	home, _ := agyFixture(t)
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, s, Options{Home: "/nonexistent", Source: home, Label: "eval=x", Outcome: "pass", Run: "s/x/1"})
	runs := func() string {
		var got string
		if err := s.db.QueryRow(`SELECT group_concat(id || '=' || COALESCE(run, 'null'), ' ') FROM (SELECT id, run FROM sessions ORDER BY id)`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := runs(); got != "C1=s/x/1 C2=s/x/1" {
		t.Fatalf("after the first ingest: %q", got)
	}
	// The conversations change, so the second ingest re-reads them.
	sqliteT(t, filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db"),
		`UPDATE conversation_summaries SET last_modified_time = '2026-10-08 11:00:00+00:00'`)
	r := ingestT(t, s, Options{Home: "/nonexistent", Source: home, Label: "eval=x", Outcome: "pass", Run: "s/x/2"})
	if len(r.Failed) > 0 {
		t.Fatalf("second ingest failed: %v", r.Failed)
	}
	if got := runs(); got != "C1=s/x/1 C2=s/x/1" {
		t.Fatalf("after the second ingest: %q, want the first run kept", got)
	}
	plain := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, plain, Options{Home: "/nonexistent", Source: home, Label: "eval=x", Outcome: "pass"})
	var n int
	plain.db.QueryRow(`SELECT count(*) FROM sessions WHERE run IS NULL`).Scan(&n)
	if n != 2 {
		t.Fatalf("an ingest with no run left %d NULL runs, want 2", n)
	}
	// A run without a source would stamp the real home's sessions; one that
	// is not printable or too long is not a trial's run: Validate and Ingest
	// both refuse, and Ingest before it reads anything.
	for name, o := range map[string]Options{
		"no source": {Home: "/nonexistent", Run: "s/x/1"},
		"escape":    {Home: "/nonexistent", Source: home, Label: "eval=x", Run: "s/x\x1b[2J/1"},
		"long":      {Home: "/nonexistent", Source: home, Label: "eval=x", Run: strings.Repeat("r", trialText+1)},
	} {
		if err := o.Validate(); err == nil {
			t.Errorf("%s: Validate accepted", name)
		}
		fresh := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
		if _, err := Ingest(fresh, o, io.Discard); err == nil || strings.Contains(err.Error(), "\x1b") {
			t.Errorf("%s: Ingest returned %v, want a refusal without the escape byte", name, err)
		}
		fresh.db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&n)
		if n != 0 {
			t.Errorf("%s: Ingest stored %d sessions after refusing", name, n)
		}
	}
}

// --outcome error is the third value: accepted, and stamped on the label's
// sessions like pass and fail.
func TestIngestOutcomeError(t *testing.T) {
	home, _ := agyFixture(t)
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	o := Options{Home: "/nonexistent", Source: home, Label: "eval=x", Outcome: "error"}
	if err := o.Validate(); err != nil {
		t.Fatal(err)
	}
	ingestT(t, s, o)
	var n int
	s.db.QueryRow(`SELECT count(*) FROM sessions WHERE outcome = 'error'`).Scan(&n)
	if n != 2 {
		t.Fatalf("sessions with outcome error = %d, want 2", n)
	}
	if err := (Options{Source: home, Label: "a=b", Outcome: "errored"}).Validate(); err == nil || !strings.Contains(err.Error(), "pass, fail or error") {
		t.Errorf("Validate(errored) = %v, want the three values named", err)
	}
}

// TestRunModels: the distinct models of the sessions of the named runs,
// sorted, a session with no model or of another run left out; no run is
// no model.
func TestRunModels(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	for _, x := range [][3]any{
		{"A", "r/x/1", "gemini-b"}, {"B", "r/x/1", "gemini-a"}, {"C", "r/x/2", "gemini-b"},
		{"D", "r/x/2", nil}, {"E", "other/x/1", "gemini-z"}, {"F", nil, "gemini-y"},
	} {
		if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, model, run) VALUES (?, 'agy', ?, ?)`, x[0], x[2], x[1]); err != nil {
			t.Fatal(err)
		}
	}
	for name, c := range map[string]struct {
		runs []string
		want string
	}{
		"two runs":      {[]string{"r/x/1", "r/x/2"}, "gemini-a gemini-b"},
		"one model":     {[]string{"r/x/2"}, "gemini-b"},
		"no session":    {[]string{"r/x/3"}, ""},
		"no run at all": {nil, ""},
	} {
		got, err := s.RunModels(c.runs)
		if err != nil || strings.Join(got, " ") != c.want {
			t.Errorf("%s: %q, %v; want %q", name, got, err, c.want)
		}
	}
}
