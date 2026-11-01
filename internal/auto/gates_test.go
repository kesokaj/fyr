package auto

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGatePowerLinux(t *testing.T) {
	cases := []struct {
		name     string
		supplies []Supply
		err      error
		stop     string
	}{
		{name: "no supplies is AC"},
		{name: "mains online", supplies: []Supply{{Type: "Mains", Online: "1"}, {Type: "Battery"}}},
		{name: "usb online", supplies: []Supply{{Type: "USB", Online: "1"}, {Type: "Battery"}}},
		{name: "on battery", supplies: []Supply{{Type: "Mains", Online: "0"}, {Type: "Battery"}}, stop: "on battery"},
		{name: "battery alone", supplies: []Supply{{Type: "Battery"}}, stop: "on battery"},
		{name: "a mouse's battery is no battery", supplies: []Supply{{Type: "Battery", Scope: "Device"}}},
		{name: "a mouse's online is no mains", supplies: []Supply{{Type: "USB", Online: "1", Scope: "Device"}, {Type: "Battery"}}, stop: "on battery"},
		{name: "unreadable", err: errFake, stop: "unreadable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gatePower(&fakeEnv{supplies: c.supplies, suppliesErr: c.err}, "linux")
			if (c.stop == "") != (got == "") || !strings.Contains(got, c.stop) {
				t.Errorf("%q, want %q", got, c.stop)
			}
		})
	}
}

func TestGatePowerDarwin(t *testing.T) {
	cases := []struct {
		name, batt, all string
		err             error
		stop            string
	}{
		{name: "ac", batt: "Now drawing from 'AC Power'\n", all: " lowpowermode 0\n"},
		{name: "ac, no such line", batt: "Now drawing from 'AC Power'\n", all: " sleep 1\n"},
		{name: "battery", batt: "Now drawing from 'Battery Power'\n", stop: "not on AC"},
		{name: "ups", batt: "Now drawing from 'UPS Power'\n", stop: "not on AC"},
		{name: "nothing said", batt: "", stop: "not on AC"},
		{name: "low power", batt: "'AC Power'", all: " lowpowermode 1\n", stop: "low power"},
		{name: "unreadable", err: errFake, stop: "unreadable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gatePower(&fakeEnv{batt: c.batt, all: c.all, pmsetErr: c.err}, "darwin")
			if (c.stop == "") != (got == "") || !strings.Contains(got, c.stop) {
				t.Errorf("%q, want %q", got, c.stop)
			}
		})
	}
	if got := gatePower(&fakeEnv{}, "plan9"); got == "" {
		t.Error("a system with no reading passed")
	}
}

func TestGateReadSupplies(t *testing.T) {
	root := t.TempDir()
	if got, err := readSupplies(filepath.Join(root, "absent")); got != nil || err != nil {
		t.Errorf("%v, %v", got, err)
	}
	writeTree(t, root, "AC/type", "Mains\n")
	writeTree(t, root, "AC/online", "1\n")
	writeTree(t, root, "hid/type", "Battery\n")
	writeTree(t, root, "hid/scope", "Device\n")
	got, err := readSupplies(root)
	if err != nil || len(got) != 2 || got[0] != (Supply{"AC", "Mains", "1", ""}) || got[1] != (Supply{"hid", "Battery", "", "Device"}) {
		t.Errorf("%+v, %v", got, err)
	}
	if err := os.Mkdir(filepath.Join(root, "untyped"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := readSupplies(root); err == nil {
		t.Error("a supply with no type passed")
	}
}

func TestGatePresenceLinux(t *testing.T) {
	now := runAt
	since := func(d time.Duration) string { return strconv.FormatInt(now.Add(-d).UnixMicro(), 10) }
	session := func(kind, active, locked, idle, hint string) map[string]string {
		return map[string]string{"Type": kind, "Active": active, "LockedHint": locked, "IdleHint": idle, "IdleSinceHint": hint}
	}
	cases := []struct {
		name     string
		sessions []map[string]string
		ptys     []time.Time
		stop     bool
		unknown  bool
	}{
		{name: "no signal", unknown: true},
		{name: "only a manager session", sessions: []map[string]string{session("unspecified", "yes", "no", "no", "0")}, unknown: true},
		{name: "in use", sessions: []map[string]string{session("wayland", "yes", "no", "no", "0")}, stop: true},
		{name: "x11 in use", sessions: []map[string]string{session("x11", "yes", "no", "no", "0")}, stop: true},
		{name: "idle briefly", sessions: []map[string]string{session("x11", "yes", "no", "yes", since(time.Minute))}, stop: true},
		{name: "idle long", sessions: []map[string]string{session("x11", "yes", "no", "yes", since(time.Hour))}},
		{name: "idle unparsed counts as present", sessions: []map[string]string{session("x11", "yes", "no", "yes", "soon")}, stop: true},
		{name: "locked", sessions: []map[string]string{session("x11", "yes", "yes", "no", "0")}},
		{name: "not active", sessions: []map[string]string{session("x11", "no", "no", "no", "0")}},
		{name: "terminal in use", ptys: []time.Time{now.Add(-time.Minute)}, stop: true},
		{name: "terminal ahead of the clock", ptys: []time.Time{now.Add(time.Hour)}, stop: true},
		{name: "terminal old", ptys: []time.Time{now.Add(-time.Hour)}},
		{name: "exactly the limit is away", ptys: []time.Time{now.Add(-15 * time.Minute)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stop, unknown := gatePresence(&fakeEnv{sessions: c.sessions, ptys: c.ptys}, "linux", 1000, 15*time.Minute, now)
			if (stop != "") != c.stop || unknown != c.unknown {
				t.Errorf("stop %q, unknown %v", stop, unknown)
			}
		})
	}
}

func TestGatePresenceDarwin(t *testing.T) {
	minute := strconv.FormatInt(int64(time.Minute), 10)
	hour := strconv.FormatInt(int64(time.Hour), 10)
	cases := []struct {
		name, hid, root string
		err             error
		stop, unknown   bool
	}{
		{name: "input", hid: `"HIDIdleTime" = ` + minute, stop: true},
		{name: "the newest of two", hid: `"HIDIdleTime" = ` + hour + "\n" + `"HIDIdleTime" = ` + minute, stop: true},
		{name: "input, locked", hid: `"HIDIdleTime" = ` + minute, root: `"CGSSessionScreenIsLocked"=Yes`},
		{name: "away", hid: `"HIDIdleTime" = ` + hour},
		{name: "a value that overflows counts as present", hid: `"HIDIdleTime" = 99999999999999999999`, stop: true},
		{name: "no line", hid: "nothing", unknown: true},
		{name: "unreadable", err: errFake, unknown: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stop, unknown := gatePresence(&fakeEnv{hid: c.hid, root: c.root, ioregErr: c.err}, "darwin", 501, 15*time.Minute, runAt)
			if (stop != "") != c.stop || unknown != c.unknown {
				t.Errorf("stop %q, unknown %v", stop, unknown)
			}
		})
	}
}

func TestGateParseSession(t *testing.T) {
	got := parseSession("Id=2\nType=wayland\nIdleHint=no\n\nnoise\n")
	if got["Type"] != "wayland" || got["IdleHint"] != "no" || len(got) != 3 {
		t.Errorf("%v", got)
	}
}

func TestGateReadPTYs(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "4", "")
	writeTree(t, root, "ptmx", "")
	at := runAt.Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(root, "4"), at, at); err != nil {
		t.Fatal(err)
	}
	got, err := readPTYs(root, os.Getuid())
	if err != nil || len(got) != 1 || !got[0].Equal(at) {
		t.Errorf("%v, %v", got, err)
	}
	if got, _ := readPTYs(root, os.Getuid()+1); len(got) != 0 {
		t.Errorf("another uid's terminal counted: %v", got)
	}
}

func TestGateSession(t *testing.T) {
	cases := []struct {
		found bool
		err   error
		stop  string
	}{{}, {found: true, stop: "interactive"}, {err: errFake, stop: "unreadable"}}
	for _, c := range cases {
		e := &fakeEnv{agy: func() (bool, error) { return c.found, c.err }}
		got := gateSession(e, "linux", 1)
		if (c.stop == "") != (got == "") || !strings.Contains(got, c.stop) {
			t.Errorf("%q, want %q", got, c.stop)
		}
	}
}

func TestGateMachineStopsARun(t *testing.T) {
	root := idleProject(t)
	used := []time.Time{runAt.Add(-time.Minute)}
	envs := map[string]*fakeEnv{
		"G3 power":    {supplies: []Supply{{Type: "Battery"}}},
		"G4 operator": {ptys: used},
		"G5 agy":      {ptys: []time.Time{runAt.Add(-time.Hour)}, agy: func() (bool, error) { return true, nil }},
	}
	for gate, e := range envs {
		t.Run(gate, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, validConfig)
			e.targets = targetsOf(root)
			var out bytes.Buffer
			o := runOpts(home, &out)
			o.Env = e
			if err := Run(o, false); err != nil {
				t.Fatal(err)
			}
			log := logOf(t, home)
			if !strings.Contains(log, gate) || strings.Contains(log, root) || strings.Count(log, "\n") != 1 {
				t.Errorf("%q", log)
			}
		})
	}
}

func TestGateUnknownPresenceIsLogged(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	var out bytes.Buffer
	if err := Run(runOpts(home, &out), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logOf(t, home), "user idle: unknown") {
		t.Errorf("%q", logOf(t, home))
	}
}
