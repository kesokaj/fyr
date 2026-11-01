package auto

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func opts(home string, out *bytes.Buffer) Options {
	return Options{Home: home, Out: out, GOOS: "linux", Path: "/p:/q"}
}

func noHarness() error { return errors.New("no checkout here") }
func aHarness() error  { return nil }

func exit(err error) int {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	if err != nil {
		return -1
	}
	return 0
}

func TestAutoOnWritesTheConfigAndTheTimer(t *testing.T) {
	home := homeWithBinary(t)
	calls := fakeManagers(t, "0")
	var out bytes.Buffer
	if err := On(opts(home, &out), Request{}); err != nil {
		t.Fatal(err)
	}
	c, found, err := Load(home)
	if err != nil || !found || c != (Config{true, LevelObserve, "6h", "15m"}) {
		t.Fatalf("%+v, %v, %v", c, found, err)
	}
	if !unit.InstalledHere(home, "linux") || !strings.Contains(readFile(t, calls), "enable --now") {
		t.Errorf("no timer; calls: %s", readFile(t, calls))
	}
	if !strings.Contains(out.String(), "level observe") {
		t.Errorf("%q", out.String())
	}
}

// Run twice it changes what was asked and keeps the rest.
func TestAutoOnIsIdempotent(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	var out bytes.Buffer
	o := opts(home, &out)
	for _, r := range []Request{{Level: LevelProject}, {UserIdle: "30m"}, {}} {
		if err := On(o, r); err != nil {
			t.Fatal(err)
		}
	}
	if c, _, _ := Load(home); c != (Config{true, LevelProject, "6h", "30m"}) {
		t.Errorf("%+v", c)
	}
}

// D21: a raised level does not come back by itself after an off, nor from a
// config a worker could have written while off.
func TestAutoOnFromOffStartsAtTheDefaults(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	var out bytes.Buffer
	o := opts(home, &out)
	if err := On(o, Request{Level: LevelProject, ProjectIdle: "1d"}); err != nil {
		t.Fatal(err)
	}
	if err := Off(o); err != nil {
		t.Fatal(err)
	}
	if err := On(o, Request{}); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := Load(home); c != (Config{true, LevelObserve, "6h", "15m"}) {
		t.Errorf("after off: %+v", c)
	}
	writeConfig(t, home, `{"enabled": false, "level": "harness", "project_idle": "1h", "user_idle": "1m"}`)
	r := Request{CheckHarness: noHarness}
	if err := On(o, r); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := Load(home); c != (Config{true, LevelObserve, "6h", "15m"}) {
		t.Errorf("from a disabled file: %+v", c)
	}
}

func TestAutoOnReplacesACorruptConfig(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	writeConfig(t, home, "garbage")
	var out bytes.Buffer
	if err := On(opts(home, &out), Request{}); err != nil {
		t.Fatal(err)
	}
	if c, _, err := Load(home); err != nil || !c.Enabled {
		t.Errorf("%+v, %v", c, err)
	}
}

func TestAutoOnRefusesALink(t *testing.T) {
	home := homeWithBinary(t)
	calls := fakeManagers(t, "0")
	target := filepath.Join(t.TempDir(), "t.json")
	os.WriteFile(target, []byte("keep"), 0o600)
	os.MkdirAll(filepath.Dir(ConfigPath(home)), 0o700)
	if err := os.Symlink(target, ConfigPath(home)); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := On(opts(home, &out), Request{})
	if exit(err) != 2 || !strings.Contains(err.Error(), "fyr auto off") {
		t.Errorf("%v", err)
	}
	if readFile(t, target) != "keep" {
		t.Error("written through the link")
	}
	if _, serr := os.Stat(calls); !errors.Is(serr, os.ErrNotExist) {
		t.Error("the service manager was called")
	}
}

func TestAutoOnHarnessNeedsACheckout(t *testing.T) {
	home := homeWithBinary(t)
	calls := fakeManagers(t, "0")
	var out bytes.Buffer
	o := opts(home, &out)
	err := On(o, Request{Level: LevelHarness, CheckHarness: noHarness})
	if exit(err) != 2 || !strings.Contains(err.Error(), "level harness needs a harness checkout") {
		t.Errorf("%v", err)
	}
	if _, found, _ := Load(home); found {
		t.Error("a refusal wrote the config")
	}
	if _, serr := os.Stat(calls); !errors.Is(serr, os.ErrNotExist) {
		t.Error("a refusal reached the service manager")
	}
	if err := On(o, Request{Level: LevelHarness, CheckHarness: aHarness}); err != nil {
		t.Fatal(err)
	}
	// A kept level is checked too: the checkout may have gone since.
	err = On(o, Request{UserIdle: "20m", CheckHarness: noHarness})
	if exit(err) != 2 {
		t.Errorf("a kept harness level: %v", err)
	}
}

// The plan's refusal: no binary, or a failing service manager, says why,
// leaves enabled false, and exits 2.
func TestAutoOnTimerRefusalLeavesItOff(t *testing.T) {
	t.Run("no binary", func(t *testing.T) {
		home := t.TempDir()
		fakeManagers(t, "0")
		var out bytes.Buffer
		err := On(opts(home, &out), Request{Level: LevelProject})
		if exit(err) != 2 || !strings.Contains(err.Error(), "no binary at") {
			t.Errorf("%v", err)
		}
		if c, found, _ := Load(home); !found || c.Enabled {
			t.Errorf("%+v, %v", c, found)
		}
	})
	t.Run("manager fails, was on", func(t *testing.T) {
		home := homeWithBinary(t)
		fakeManagers(t, "0")
		var out bytes.Buffer
		o := opts(home, &out)
		if err := On(o, Request{}); err != nil {
			t.Fatal(err)
		}
		fakeManagers(t, "1")
		if err := On(o, Request{}); exit(err) != 2 {
			t.Errorf("%v", err)
		}
		if c, _, _ := Load(home); c.Enabled {
			t.Error("still on after the timer failed")
		}
	})
}

func TestAutoOffRemovesAndKeepsTheValues(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	var out bytes.Buffer
	o := opts(home, &out)
	if err := On(o, Request{Level: LevelProject}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Off(o); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := Load(home); c != (Config{false, LevelProject, "6h", "15m"}) {
		t.Errorf("%+v", c)
	}
	if unit.InstalledHere(home, "linux") {
		t.Error("the timer stayed")
	}
	if out.String() != "auto off\n" {
		t.Errorf("%q", out.String())
	}
}

func TestAutoOffWithNothingOnSaysSo(t *testing.T) {
	home := t.TempDir()
	fakeManagers(t, "0")
	var out bytes.Buffer
	if err := Off(opts(home, &out)); err != nil {
		t.Fatal(err)
	}
	if out.String() != "auto: already off\n" {
		t.Errorf("%q", out.String())
	}
	if _, err := os.Lstat(ConfigPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Error("off with nothing on wrote a config")
	}
}

// The kill switch: a corrupt file, and a link, are replaced, never followed;
// a held lock does not stop it.
func TestAutoOffReplacesCorruptAndLink(t *testing.T) {
	fakeManagers(t, "0")
	var out bytes.Buffer
	home := t.TempDir()
	writeConfig(t, home, "garbage")
	if err := Off(opts(home, &out)); err != nil {
		t.Fatal(err)
	}
	if c, found, err := Load(home); err != nil || !found || c.Enabled {
		t.Errorf("corrupt: %+v, %v, %v", c, found, err)
	}
	home = t.TempDir()
	target := filepath.Join(t.TempDir(), "t.json")
	os.WriteFile(target, []byte(validConfig), 0o600)
	os.MkdirAll(filepath.Dir(ConfigPath(home)), 0o700)
	if err := os.Symlink(target, ConfigPath(home)); err != nil {
		t.Fatal(err)
	}
	if err := Off(opts(home, &out)); err != nil {
		t.Fatal(err)
	}
	if readFile(t, target) != validConfig {
		t.Error("written through the link")
	}
	if fi, _ := os.Lstat(ConfigPath(home)); fi.Mode()&os.ModeSymlink != 0 {
		t.Error("the link stayed")
	}
}

func TestAutoOffNeedsNoLock(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	var out bytes.Buffer
	o := opts(home, &out)
	if err := On(o, Request{}); err != nil {
		t.Fatal(err)
	}
	if err := ensureDir(home); err != nil {
		t.Fatal(err)
	}
	unlock, held, err := lock(home)
	if err != nil || held {
		t.Fatal(held, err)
	}
	defer unlock()
	if err := Off(o); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := Load(home); c.Enabled {
		t.Error("off under a held lock left it on")
	}
}

// off cannot write: said, exit 1, and the timer still goes. Never "auto off".
func TestAutoOffFailureIsSaid(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	var out bytes.Buffer
	o := opts(home, &out)
	if err := On(o, Request{}); err != nil {
		t.Fatal(err)
	}
	os.Remove(ConfigPath(home))
	if err := os.MkdirAll(filepath.Join(ConfigPath(home), "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := Off(o)
	if exit(err) != 1 || !strings.Contains(err.Error(), "the config") {
		t.Errorf("%v", err)
	}
	if strings.Contains(out.String(), "auto off") {
		t.Errorf("reported off: %q", out.String())
	}
	if unit.InstalledHere(home, "linux") {
		t.Error("a failed write stopped the timer's removal")
	}
}

// Only the timer failed: the text names the removal, once, and exit is 1.
func TestAutoOffTimerFailureIsSaid(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	var out bytes.Buffer
	o := opts(home, &out)
	if err := On(o, Request{}); err != nil {
		t.Fatal(err)
	}
	name, _ := unit.Names(home)
	service := filepath.Join(home, ".config", "systemd", "user", name+".service")
	os.Remove(service)
	if err := os.MkdirAll(filepath.Join(service, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := Off(o)
	if exit(err) != 1 || strings.Contains(err.Error(), "could not write") || strings.Count(err.Error(), "fyr auto off") != 2 {
		t.Errorf("%v", err)
	}
	if strings.Contains(out.String(), "auto off") {
		t.Errorf("reported off: %q", out.String())
	}
	if c, _, _ := Load(home); c.Enabled {
		t.Error("the config stayed on")
	}
}

// The manager's stop failed: the files go, the config is off, and off says
// the manager may still hold the timer.
func TestAutoOffStopFailureIsSaid(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	var out bytes.Buffer
	o := opts(home, &out)
	if err := On(o, Request{}); err != nil {
		t.Fatal(err)
	}
	fakeManagers(t, "1")
	out.Reset()
	err := Off(o)
	if exit(err) != 1 || !strings.Contains(err.Error(), "may still hold the timer: run systemctl --user daemon-reload") {
		t.Errorf("%v", err)
	}
	if unit.InstalledHere(home, "linux") {
		t.Error("the files stayed")
	}
	if c, _, _ := Load(home); c.Enabled {
		t.Error("the config stayed on")
	}
}

// What cannot be read as a regular file is kept, never replaced.
func TestAutoTimerKeepsWhatItCannotRead(t *testing.T) {
	prep := map[string]func(path string){
		"dangling link": func(p string) { os.Symlink(filepath.Join(filepath.Dir(p), "gone"), p) },
		"fifo":          func(p string) { syscall.Mkfifo(p, 0o600) },
		"unreadable":    func(p string) { os.WriteFile(p, []byte("x"), 0) },
	}
	for name, prepare := range prep {
		t.Run(name, func(t *testing.T) {
			home := homeWithBinary(t)
			calls := fakeManagers(t, "0")
			n, _ := unit.Names(home)
			p := filepath.Join(home, ".config", "systemd", "user", n+".service")
			os.MkdirAll(filepath.Dir(p), 0o755)
			prepare(p)
			before, _ := os.Lstat(p)
			done := inTime(func() error { return unit.Install(home, "/p", "linux") })
			if err := done(t); err == nil || !strings.Contains(err.Error(), "kept") {
				t.Errorf("%v", err)
			}
			if after, _ := os.Lstat(p); !os.SameFile(before, after) {
				t.Error("replaced")
			}
			if _, err := os.Stat(calls); !errors.Is(err, os.ErrNotExist) {
				t.Error("the service manager was called")
			}
		})
	}
}

func inTime(f func() error) func(*testing.T) error {
	ch := make(chan error, 1)
	go func() { ch <- f() }()
	return func(t *testing.T) error {
		select {
		case err := <-ch:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("hung")
			return nil
		}
	}
}

// launchd's bootout fails on a job that is not loaded, so it fails no off.
func TestAutoOffStopFailureOnMacIsNoError(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	var out bytes.Buffer
	o := opts(home, &out)
	o.GOOS = "darwin"
	if err := On(o, Request{}); err != nil {
		t.Fatal(err)
	}
	fakeManagers(t, "1")
	if err := Off(o); err != nil {
		t.Errorf("%v", err)
	}
}
