package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // the named zones below load on a builder without tzdata
)

// unsetTZ unsets TZ for the test, restoring it after.
func unsetTZ(t *testing.T) {
	t.Helper()
	t.Setenv("TZ", "")
	os.Unsetenv("TZ")
}

// pointLink points localtimeLink at a path under the test's directory: a
// symlink to target, a regular file ("file"), or nothing ("").
func pointLink(t *testing.T, kind, target string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "localtime")
	switch kind {
	case "link":
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	case "file":
		put(t, p, "TZif")
	}
	old := localtimeLink
	localtimeLink = p
	t.Cleanup(func() { localtimeLink = old })
}

// TestLeadZone is § Interfaces § The worker's zone's order: a set TZ that
// is a zone name, a set-but-empty one as UTC, else the name after the last
// zoneinfo/ in /etc/localtime's link target, else no name and time.Local.
// A set TZ that is not a zone name is refused (refusal 22); a link that
// names none is no name, not a refusal.
func TestLeadZone(t *testing.T) {
	long := strings.Repeat("A", 65)
	for _, c := range []struct {
		name, tz, kind, target string // tz "-" unsets TZ
		want                   string // the zone's name; "!" is a refusal
	}{
		{"tz", "Europe/Stockholm", "link", "/usr/share/zoneinfo/America/Los_Angeles", "Europe/Stockholm"},
		{"tz-empty", "", "link", "/usr/share/zoneinfo/Europe/Stockholm", "UTC"},
		{"link-dave", "-", "link", "/usr/share/zoneinfo/Europe/Stockholm", "Europe/Stockholm"},
		{"link-mac", "-", "link", "/var/db/timezone/zoneinfo/America/Los_Angeles", "America/Los_Angeles"},
		{"link-relative", "-", "link", "../usr/share/zoneinfo/Europe/Stockholm", "Europe/Stockholm"},
		{"regular-file", "-", "file", "", ""},
		{"no-file", "-", "", "", ""},
		{"no-zoneinfo", "-", "link", "Europe/Stockholm", ""},
		{"no-zoneinfo-abs", "-", "link", "/etc/zones/Europe/Stockholm", ""},
		{"link-unknown", "-", "link", "/usr/share/zoneinfo/No/Such_Zone", ""},
		{"link-local", "-", "link", "/usr/share/zoneinfo/localtime", ""},
		{"unknown", "No/Such_Zone", "", "", "!"},
		{"path", "/etc/localtime", "", "", "!"},
		{"glibc-path", ":/etc/localtime", "", "", "!"},
		{"glibc-name", ":Europe/Stockholm", "", "", "!"},
		{"Local", "Local", "", "", "!"},
		{"localtime", "localtime", "", "", "!"},
		{"dotdot", "../x", "", "", "!"},
		{"rule", "CET-1CEST", "", "", "!"},
		{"rule-full", "CET-1CEST,M3.5.0,M10.5.0/3", "", "", "!"},
		{"65-bytes", long, "", "", "!"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.tz == "-" {
				unsetTZ(t)
			} else {
				t.Setenv("TZ", c.tz)
			}
			pointLink(t, c.kind, c.target)
			z, err := leadZone()
			if c.want == "!" {
				if err == nil || !strings.HasPrefix(err.Error(), "TZ=") || !strings.Contains(err.Error(), ": not a zone name fyr work can pass to the worker; set TZ=<Area/City>, or unset it") {
					t.Fatalf("zone %q, err %v; want refusal 22", z.name, err)
				}
				return
			}
			if err != nil || z.name != c.want {
				t.Fatalf("zone %q, err %v; want %q", z.name, err, c.want)
			}
			if c.want == "" {
				if z.loc != time.Local {
					t.Errorf("no name: loc %v, want time.Local", z.loc)
				}
			} else if z.loc == nil || z.loc.String() != c.want {
				t.Errorf("loc %v, want %s", z.loc, c.want)
			}
		})
	}
	// The value is written Go-quoted and cut, never raw.
	t.Setenv("TZ", "x\x1b[2J"+long+long)
	if _, err := leadZone(); err == nil || strings.ContainsRune(err.Error(), 0x1b) || len(err.Error()) > 300 {
		t.Errorf("refusal %q", err)
	}
}

// The checks before time.LoadLocation hold alone: with a loader that takes
// every name, a path, glibc's ':' form, '..', a rule string, Local,
// localtime and 65 bytes are still refused, and 64 bytes pass.
func TestLeadZoneNameChecks(t *testing.T) {
	old := loadLocation
	loadLocation = func(string) (*time.Location, error) { return time.UTC, nil }
	t.Cleanup(func() { loadLocation = old })
	pointLink(t, "", "")
	for _, tz := range []string{"/etc/localtime", ":/etc/localtime", ":Europe/Stockholm", "Local", "localtime",
		"../x", "a/../b", "CET-1CEST,M3.5.0,M10.5.0/3", "Europe/Stock holm", "A=B", strings.Repeat("A", 65)} {
		t.Setenv("TZ", tz)
		if _, err := leadZone(); err == nil {
			t.Errorf("TZ=%q: no refusal", tz)
		}
	}
	t.Setenv("TZ", strings.Repeat("A", 64))
	if z, err := leadZone(); err != nil || z.name != strings.Repeat("A", 64) {
		t.Errorf("64 bytes: %q, %v", z.name, err)
	}
	// A link candidate takes the same checks: no name, not a refusal.
	unsetTZ(t)
	pointLink(t, "link", "/usr/share/zoneinfo/Local")
	if z, err := leadZone(); err != nil || z.name != "" {
		t.Errorf("link to Local: %q, %v", z.name, err)
	}
}

// TestRunZone: a host worker's agy gets the lead's TZ; with no name it gets
// none and reads the machine's /etc/localtime, as the lead does.
func TestRunZone(t *testing.T) {
	r := newRig(t)
	t.Setenv("TZ", "Europe/Stockholm")
	if res := r.run(r.kickoff("fix-a", "src/a.txt")); res.Verdict != "PASS" {
		t.Fatalf("result %+v\n%s", res, r.out.String())
	}
	if tz := envLines(r.read("env"), "TZ="); len(tz) != 1 || tz[0] != "TZ=Europe/Stockholm" {
		t.Errorf("agy's TZ lines %q, want [TZ=Europe/Stockholm]", tz)
	}
	r2 := newRig(t)
	unsetTZ(t)
	pointLink(t, "file", "")
	machineLocal(t)
	if res := r2.run(r2.kickoff("fix-a", "src/a.txt")); res.Verdict != "PASS" {
		t.Fatalf("no name: result %+v\n%s", res, r2.out.String())
	}
	if tz := envLines(r2.read("env"), "TZ="); len(tz) != 0 {
		t.Errorf("no name: agy's TZ lines %q, want none", tz)
	}
}

// machineLocal makes time.Local what a process started now would read: Go
// fixed it at its first use, under whatever TZ the process or an earlier test
// had, and the fake agy's date reads /etc/localtime, so near midnight the two
// days differ and the record falls outside the window.
func machineLocal(t *testing.T) {
	t.Helper()
	b, err := os.ReadFile("/etc/localtime")
	if err != nil {
		t.Skipf("no /etc/localtime: %v", err)
	}
	loc, err := time.LoadLocationFromTZData("Local", b)
	if err != nil {
		t.Skipf("/etc/localtime: %v", err)
	}
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
}

// envLines is env's output lines that start with prefix.
func envLines(env, prefix string) []string {
	var out []string
	for _, l := range strings.Split(env, "\n") {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

// TestRunWindowZone: the scope check reads the window in the lead's zone,
// the one its worker is given. Pacific/Kiritimati (UTC+14) and Etc/GMT+12
// (UTC-12) are 26 hours apart, so their days always differ, and at any
// hour one case fails a window read in UTC or the machine's zone: a
// Kiritimati lead holds its worker's own date (UTC's day is the day before
// from 10:00 UTC), and a GMT+12 lead refuses a Kiritimati date (UTC's day
// until 10:00 UTC).
func TestRunWindowZone(t *testing.T) {
	r := newRig(t)
	t.Setenv("TZ", "Pacific/Kiritimati")
	if res := r.run(r.kickoff("fix-a", "src/a.txt")); res.Verdict != "PASS" {
		t.Fatalf("a Kiritimati lead: %+v\n%s", res, r.out.String())
	}
	// Within minutes of 12:00 UTC a GMT+12 window can reach the Kiritimati day.
	if now := time.Now().UTC(); now.Hour() == 11 && now.Minute() >= 50 || now.Hour() == 12 && now.Minute() < 10 {
		t.Skip("within ten minutes of 12:00 UTC, GMT+12's midnight")
	}
	kiri, err := time.LoadLocation("Pacific/Kiritimati")
	if err != nil {
		t.Fatal(err)
	}
	r2 := newRig(t)
	t.Setenv("TZ", "Etc/GMT+12")
	r2.on("nohandoff", "")
	r2.on("raw-kind", "handoff")
	r2.on("raw-id", "HANDOFF-2026-10-30-0000-raw")
	r2.on("raw-date", time.Now().In(kiri).Format(time.DateOnly))
	res := r2.run(r2.kickoff("dated", "src/a.txt"))
	wantReasons(t, res, ReasonOutside)
	if notes := strings.Join(res.Notes, "\n"); !strings.Contains(notes, " in Etc/GMT+12)") {
		t.Errorf("no note naming the lead's zone: %q", res.Notes)
	}
}
