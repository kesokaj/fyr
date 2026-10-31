package work

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // the named zones load on a builder without tzdata
)

// TestParseTrail: the seven keys, one object per line; an unknown key, a
// second value, a line with no id or a second, different line of one ID is
// refused, and a byte-identical one read once, as fyr record reads them
// (BUG-197).
func TestParseTrail(t *testing.T) {
	updated := "2026-10-29T17:30:23Z"
	line := func(id, extra string) string {
		return `{"date":"2026-10-29","id":"` + id + `","kind":"handoff","status":"","title":"T","updated":"` + updated + `","body":"b"` + extra + `}`
	}
	got, err := parseTrail([]byte(line("HANDOFF-1", "") + "\n\n" + line("CHANGE-1", "") + "\n"))
	if err != nil || len(got) != 2 || got["HANDOFF-1"] != (Record{ID: "HANDOFF-1", Kind: "handoff", Date: "2026-10-29", Title: "T", Updated: "2026-10-29T17:30:23Z", Body: "b"}) {
		t.Fatalf("parsed %+v, %v", got, err)
	}
	if got, err := parseTrail([]byte(line("A-1", "") + "\r\n" + line("B-1", "") + "\n" + line("A-1", "") + "\n")); err != nil || len(got) != 2 {
		t.Errorf("a byte-identical pair: %+v, %v, want two records", got, err)
	}
	for _, c := range []struct{ name, in, want string }{
		{"unknown key", line("A-1", `,"x":1`), "unknown field"},
		{"two values", line("A-1", "") + line("A-2", ""), "more than one JSON value"},
		{"no id", line("", ""), "no id"},
		{"duplicate", line("A-1", "") + "\n" + strings.Replace(line("A-1", ""), `"b"`, `"c"`, 1), "duplicate ID A-1"},
		{"conflict marker", "<<<<<<< HEAD", "line 1"},
		{"missing key", `{"date":"2026-10-29","id":"A-1","kind":"guide","title":"T","updated":"2026-10-29T00:00:00Z","body":"b"}`, `no "status" key`},
		{"bad date", strings.Replace(line("A-1", ""), "2026-10-29", "2026-13-40", 1), "date"},
		{"bad updated", strings.Replace(line("A-1", ""), updated, "u", 1), "updated"},
		{"long line", line("A-1", "") + strings.Repeat(" ", maxLine), "over"},
	} {
		if _, err := parseTrail([]byte(c.in)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

// TestNoSQLiteDriver: the worker decodes the branch's trail with
// encoding/json and never opens a store (ADR-063), so it links no driver.
func TestNoSQLiteDriver(t *testing.T) {
	// GOPROXY=off: a cold module cache skips rather than fetches.
	cmd := exec.Command("go", "list", "-deps", ".")
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("go list: %v", err)
	}
	if strings.Contains(string(out), "modernc.org/sqlite") {
		t.Error("internal/work depends on modernc.org/sqlite (ADR-063)")
	}
}

// TestTrailAtFailsClosed: a rev git cannot read is an error, never a trail
// with no records, so a damaged base cannot pass the record scope open; a
// rev without the file has none.
func TestTrailAtFailsClosed(t *testing.T) {
	r := newRig(t)
	if got, err := trailAt(r.lead, "HEAD", RunnerHost); err != nil || len(got) != 1 {
		t.Fatalf("HEAD: %d records, %v", len(got), err)
	}
	if _, err := trailAt(r.lead, "0123456789abcdef0123456789abcdef01234567", RunnerHost); err == nil {
		t.Error("an unreadable rev read as an empty trail")
	}
	// A base that holds a merge=union squash carries an unchanged line twice
	// (BUG-197): one record, so the scope check sees nothing changed.
	trail, err := os.ReadFile(filepath.Join(r.lead, trailRel))
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(r.lead, trailRel), string(trail)+string(trail))
	r.git("-C", r.lead, "commit", "-qam", "union copy")
	if got, err := trailAt(r.lead, "HEAD", RunnerHost); err != nil || len(got) != 1 || got[r.guide].Title != "Old guide" {
		t.Fatalf("a byte-identical copy at HEAD: %+v, %v, want the one guide", got, err)
	}
	r.git("-C", r.lead, "rm", "-q", trailRel)
	r.git("-C", r.lead, "commit", "-qm", "no trail")
	if got, err := trailAt(r.lead, "HEAD", RunnerHost); err != nil || len(got) != 0 {
		t.Errorf("a rev without the file: %d records, %v", len(got), err)
	}
}

// TestWindowOutside is D140's bounds with workers D144's zone: updated
// within a minute of the window either side, in whole seconds, and only in
// the UTC form fyr record writes, since the store sorts it as text (an
// offset or a fraction can outrank the lead's same-day stamps); a date held
// only when it is a day, in the lead's zone, of an instant of the window.
// Each window names its zone, so no row depends on the test machine's. The
// window crosses UTC midnight: west of UTC that is still the lead's evening,
// so the UTC day after is refused; east of it the lead's day is the next
// one, so the UTC day before is refused; a window with no zone refuses
// every date rather than panic.
func TestWindowOutside(t *testing.T) {
	from, to := time.Date(2026, 10, 30, 23, 50, 0, 500, time.UTC), time.Date(2026, 10, 31, 0, 10, 0, 0, time.UTC)
	zone := func(name string) *time.Location {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatal(err)
		}
		return loc
	}
	utc, la, sthlm := time.UTC, zone("America/Los_Angeles"), zone("Europe/Stockholm")
	for _, c := range []struct {
		loc                 *time.Location
		date, updated, want string
	}{
		{utc, "2026-10-30", "2026-10-30T23:49:00Z", ""},
		{utc, "2026-10-31", "2026-10-31T00:11:00Z", ""},
		{utc, "2026-10-31", "2026-10-31T02:00:00+02:00", "updated"},
		{utc, "2026-10-31", "2026-10-31T00:05:00.5Z", "updated"},
		{utc, "2026-10-30", "2026-10-30T23:48:59Z", "updated"},
		{utc, "2026-10-31", "2026-10-31T00:11:01Z", "updated"},
		{utc, "2026-10-31", "2026-10-31T00:11:00-01:00", "updated"},
		{utc, "2026-10-29", "2026-10-30T23:55:00Z", "date"},
		{utc, "2026-11-01", "2026-10-30T23:55:00Z", "date"},
		{utc, "2099-12-31", "2026-10-30T23:55:00Z", "date"},
		// 16:49 to 17:11 PDT: the lead's day only, never UTC's next one.
		{la, "2026-10-30", "2026-10-30T23:55:00Z", ""},
		{la, "2026-10-31", "2026-10-30T23:55:00Z", "date"},
		// 00:49 to 01:11 CET: the lead's day, not UTC's day before.
		{sthlm, "2026-10-31", "2026-10-30T23:55:00Z", ""},
		{sthlm, "2026-10-30", "2026-10-30T23:55:00Z", "date"},
		{nil, "2026-10-30", "2026-10-30T23:55:00Z", "date"},
		{nil, "2026-10-31", "2026-10-30T23:55:00Z", "date"},
		{nil, "2026-10-31", "2026-10-31T00:11:01Z", "updated"},
	} {
		got := window{from, to, c.loc}.outside(Record{Date: c.date, Updated: c.updated})
		if c.want == "" && got != "" || c.want != "" && !strings.HasPrefix(got, c.want+" ") {
			t.Errorf("%v %s %s: %q, want %q", c.loc, c.date, c.updated, got, c.want)
		}
	}
	for loc, want := range map[*time.Location]string{
		utc:   `date "2099-12-31" not a day of 2026-10-30T23:49:00Z..2026-10-31T00:11:00Z in UTC`,
		sthlm: `date "2099-12-31" not a day of 2026-10-30T23:49:00Z..2026-10-31T00:11:00Z in Europe/Stockholm`,
	} {
		if got := (window{from, to, loc}).outside(Record{Date: "2099-12-31", Updated: "2026-10-30T23:55:00Z"}); got != want {
			t.Errorf("note %q, want %q", got, want)
		}
	}
}
