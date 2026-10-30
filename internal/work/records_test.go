package work

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	out, err := exec.Command("go", "list", "-deps", ".").Output()
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
	if got, err := trailAt(r.lead, "HEAD"); err != nil || len(got) != 1 {
		t.Fatalf("HEAD: %d records, %v", len(got), err)
	}
	if _, err := trailAt(r.lead, "0123456789abcdef0123456789abcdef01234567"); err == nil {
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
	if got, err := trailAt(r.lead, "HEAD"); err != nil || len(got) != 1 || got[r.guide].Title != "Old guide" {
		t.Fatalf("a byte-identical copy at HEAD: %+v, %v, want the one guide", got, err)
	}
	r.git("-C", r.lead, "rm", "-q", trailRel)
	r.git("-C", r.lead, "commit", "-qm", "no trail")
	if got, err := trailAt(r.lead, "HEAD"); err != nil || len(got) != 0 {
		t.Errorf("a rev without the file: %d records, %v", len(got), err)
	}
}
