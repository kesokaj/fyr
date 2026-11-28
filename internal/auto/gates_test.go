package auto

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatePowerLinux(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

func TestGateMachineStopsARun(t *testing.T) {
	t.Parallel()

	root := idleProject(t)
	t.Run("G3 power stops run", func(t *testing.T) {
		home := t.TempDir()
		writeConfig(t, home, validConfig)
		e := &fakeEnv{supplies: []Supply{{Type: "Battery"}}, targets: targetsOf(root)}
		var out bytes.Buffer
		o := runOpts(home, &out)
		o.Env = e
		if err := Run(o, false); err != nil {
			t.Fatal(err)
		}
		log := logOf(t, home)
		if !strings.Contains(log, "G3 power") || strings.Contains(log, root) || strings.Count(log, "\n") != 1 {
			t.Errorf("%q", log)
		}
	})
}
