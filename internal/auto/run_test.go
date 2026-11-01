package auto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

var runAt = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func runOpts(home string, out *bytes.Buffer) Options {
	o := opts(home, out)
	o.Now = func() time.Time { return runAt }
	return o
}

func logOf(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir(home), "auto.log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAutoRunOffIsOneLine(t *testing.T) {
	for name, text := range map[string]string{"missing": "", "disabled": `{"enabled": false, "level": "project", "project_idle": "6h", "user_idle": "15m"}`} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if text != "" {
				writeConfig(t, home, text)
			}
			var out bytes.Buffer
			if err := Run(runOpts(home, &out), false); err != nil {
				t.Fatal(err)
			}
			got := logOf(t, home)
			if got != "2026-11-01T12:00:00Z G1 switch: off\n" {
				t.Errorf("%q", got)
			}
			if _, err := os.Stat(filepath.Join(dir(home), "runs.json")); err == nil {
				t.Error("a stop at G1 wrote the state")
			}
		})
	}
}

func TestAutoRunCorruptFailsClosed(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "garbage\x07")
	var out bytes.Buffer
	err := Run(runOpts(home, &out), false)
	if exit(err) != 1 {
		t.Errorf("%v", err)
	}
	if !strings.HasPrefix(out.String(), "auto: config: ") || !strings.Contains(out.String(), "; run fyr auto off or on\n") {
		t.Errorf("%q", out.String())
	}
	if !strings.Contains(logOf(t, home), "auto: config: ") {
		t.Error("not logged")
	}
}

func TestAutoRunHeldLockStops(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	if err := ensureDir(home); err != nil {
		t.Fatal(err)
	}
	unlock, held, err := lock(home)
	if err != nil || held {
		t.Fatal(held, err)
	}
	defer unlock()
	var out bytes.Buffer
	if err := Run(runOpts(home, &out), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logOf(t, home), "G2 lock: held by another run") {
		t.Errorf("%q", logOf(t, home))
	}
	if _, err := os.Stat(filepath.Join(dir(home), "runs.json")); err == nil {
		t.Error("a stop at G2 wrote the state")
	}
}

func TestAutoRunPassesG1AndG2(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	var out bytes.Buffer
	if err := Run(runOpts(home, &out), false); err != nil {
		t.Fatal(err)
	}
	st, found, err := loadState(home)
	if err != nil || !found || st.Date != "2026-11-01" || !st.LastRun.Equal(runAt) || !strings.Contains(st.Verdict, "G1 and G2 passed") {
		t.Fatalf("%+v, %v, %v", st, found, err)
	}
	for _, name := range []string{"auto.log", "auto.lock", "runs.json"} {
		if info, err := os.Stat(filepath.Join(dir(home), name)); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v, %v", name, info, err)
		}
	}
	if info, _ := os.Stat(dir(home)); info.Mode().Perm() != 0o700 {
		t.Errorf("auto/ is %v", info.Mode())
	}
	if strings.Count(logOf(t, home), "\n") != 1 {
		t.Errorf("not one line: %q", logOf(t, home))
	}
	// The lock is free again: a second run passes too.
	if err := Run(runOpts(home, &out), false); err != nil {
		t.Fatal(err)
	}
	if strings.Count(logOf(t, home), "G1 and G2 passed") != 2 {
		t.Error("the second run held at the first's lock")
	}
}

// A day's model count is kept within the day and starts over on the next.
func TestAutoRunKeepsTheDaysCount(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	if err := ensureDir(home); err != nil {
		t.Fatal(err)
	}
	if err := saveState(home, State{Date: "2026-11-01", ModelRuns: 3, OpenAsks: 2}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	o := runOpts(home, &out)
	if err := Run(o, false); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := loadState(home); st.ModelRuns != 3 || st.OpenAsks != 2 {
		t.Errorf("same day: %+v", st)
	}
	o.Now = func() time.Time { return runAt.Add(24 * time.Hour) }
	if err := Run(o, false); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := loadState(home); st.ModelRuns != 0 || st.OpenAsks != 2 {
		t.Errorf("next day: %+v", st)
	}
}

// A corrupt state stops the run and stays: resetting it would hand the day's
// budget back.
func TestAutoRunCorruptStateIsKept(t *testing.T) {
	for name, text := range map[string]string{"garbage": "garbage", "negative": `{"model_runs": -1}`} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, validConfig)
			if err := ensureDir(home); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dir(home), "runs.json")
			os.WriteFile(p, []byte(text), 0o600)
			var out bytes.Buffer
			if err := Run(runOpts(home, &out), false); exit(err) != 1 {
				t.Errorf("%v", err)
			}
			if readFile(t, p) != text {
				t.Error("the state was rewritten")
			}
		})
	}
}

func TestAutoRunRefusesLinks(t *testing.T) {
	t.Run("the directory", func(t *testing.T) {
		home, other := t.TempDir(), t.TempDir()
		writeConfig(t, home, validConfig)
		if err := os.Symlink(other, dir(home)); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := Run(runOpts(home, &out), false); exit(err) != 1 {
			t.Errorf("%v", err)
		}
		if entries, _ := os.ReadDir(other); len(entries) != 0 {
			t.Errorf("wrote through the link: %v", entries)
		}
	})
	for _, name := range []string{"auto.lock", "runs.json"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, validConfig)
			if err := ensureDir(home); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "t")
			os.WriteFile(target, []byte("keep"), 0o600)
			if err := os.Symlink(target, filepath.Join(dir(home), name)); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := Run(runOpts(home, &out), false); exit(err) != 1 {
				t.Errorf("%v", err)
			}
			if readFile(t, target) != "keep" {
				t.Error("written through the link")
			}
		})
	}
	t.Run("auto.log", func(t *testing.T) {
		home := t.TempDir()
		if err := ensureDir(home); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "t")
		os.WriteFile(target, []byte("keep"), 0o600)
		os.Symlink(target, filepath.Join(dir(home), "auto.log"))
		var out bytes.Buffer
		if err := Run(runOpts(home, &out), false); err != nil {
			t.Fatal(err)
		}
		if readFile(t, target) != "keep" {
			t.Error("appended through the link")
		}
		if !strings.Contains(out.String(), "auto: log:") {
			t.Errorf("the failed log went unsaid: %q", out.String())
		}
	})
}

// treeOf is every path under the state directory with its mode and bytes.
func treeOf(t *testing.T, home string) string {
	t.Helper()
	var b strings.Builder
	root := filepath.Join(home, harness.StateDir)
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, _ := d.Info()
		data, _ := os.ReadFile(p)
		fmt.Fprintf(&b, "%s %v %s\n", p, info.Mode(), data)
		return nil
	})
	return b.String()
}

// A dry run changes nothing, in any of the states a real run writes in.
func TestAutoDryRunWritesNothing(t *testing.T) {
	for name, prep := range map[string]func(string){
		"no state dir": func(string) {},
		"on":           func(h string) { writeConfig(t, h, validConfig) },
		"off": func(h string) {
			writeConfig(t, h, `{"enabled": false, "level": "project", "project_idle": "6h", "user_idle": "15m"}`)
		},
		"corrupt": func(h string) { writeConfig(t, h, "garbage") },
		"ran before": func(h string) {
			writeConfig(t, h, validConfig)
			var out bytes.Buffer
			Run(runOpts(h, &out), false)
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			prep(home)
			before := treeOf(t, home)
			var out bytes.Buffer
			Run(runOpts(home, &out), true)
			if after := treeOf(t, home); after != before {
				t.Errorf("before:\n%s\nafter:\n%s", before, after)
			}
			if out.Len() == 0 {
				t.Error("a dry run said nothing")
			}
		})
	}
}

func TestAutoDryRunNamesWhatItSkipped(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	var out bytes.Buffer
	if err := Run(runOpts(home, &out), true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"G1 switch: on, level project", "G2 lock: not taken in a dry run", "budget: not read in a dry run"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %q", want, out.String())
		}
	}
}

func TestAutoLogPrunedAtThirtyDays(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	if err := ensureDir(home); err != nil {
		t.Fatal(err)
	}
	old := runAt.Add(-31*24*time.Hour).Format(time.RFC3339) + " old\n"
	edge := runAt.Add(-29*24*time.Hour).Format(time.RFC3339) + " young\n"
	os.WriteFile(filepath.Join(dir(home), "auto.log"), []byte(old+"undated line\n"+edge), 0o600)
	var out bytes.Buffer
	if err := Run(runOpts(home, &out), false); err != nil {
		t.Fatal(err)
	}
	got := logOf(t, home)
	if strings.Contains(got, " old") || !strings.Contains(got, "undated line") || !strings.Contains(got, " young") {
		t.Errorf("%q", got)
	}
}

func TestAutoStatus(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	var out bytes.Buffer
	o := runOpts(home, &out)
	if err := Status(o); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"auto       off; timer not installed\n", "level      observe; project idle 6h, user idle 15m\n", "last run   never\n", "today      0 of 4 model runs\n", "asks       0 open\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("off: missing %q in:\n%s", want, out.String())
		}
	}
	if err := On(o, Request{Level: LevelProject}); err != nil {
		t.Fatal(err)
	}
	if err := ensureDir(home); err != nil {
		t.Fatal(err)
	}
	st := State{Date: "2026-11-01", ModelRuns: 2, OpenAsks: 3, LastRun: runAt, Verdict: "v\nw",
		Projects: map[string]Project{"/b": {Verdict: "G7 idle"}, "/a": {Verdict: "G8 dirty"}}}
	if err := saveState(home, st); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Status(o); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"auto       on; timer installed\n", "level      project;", "last run   2026-11-01T12:00:00Z: v w\n", "project    /a: G8 dirty\nproject    /b: G7 idle\n", "today      2 of 4 model runs\n", "asks       3 open\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("on: missing %q in:\n%s", want, out.String())
		}
	}
	// Yesterday's count is not today's.
	o.Now = func() time.Time { return runAt.Add(24 * time.Hour) }
	out.Reset()
	Status(o)
	if !strings.Contains(out.String(), "today      0 of 4 model runs\n") {
		t.Errorf("next day:\n%s", out.String())
	}
}

func TestAutoStatusSaysWhatDisagrees(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	var out bytes.Buffer
	o := runOpts(home, &out)
	writeConfig(t, home, validConfig)
	Status(o)
	if !strings.Contains(out.String(), "timer not installed, so nothing runs: run fyr auto on") {
		t.Errorf("on with no timer:\n%s", out.String())
	}
	if err := unit.Install(home, "/p", "linux"); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, home, `{"enabled": false, "level": "project", "project_idle": "6h", "user_idle": "15m"}`)
	out.Reset()
	Status(o)
	if !strings.Contains(out.String(), "timer installed, and nothing runs while auto is off") {
		t.Errorf("off with a timer:\n%s", out.String())
	}
}

func TestAutoStatusCorrupt(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "garbage")
	var out bytes.Buffer
	o := runOpts(home, &out)
	if err := Status(o); exit(err) != 1 || !strings.Contains(out.String(), "; run fyr auto off or on") {
		t.Errorf("%v, %q", err, out.String())
	}
	writeConfig(t, home, validConfig)
	ensureDir(home)
	os.WriteFile(filepath.Join(dir(home), "runs.json"), []byte("garbage"), 0o600)
	out.Reset()
	if err := Status(o); exit(err) != 1 || !strings.Contains(out.String(), "state      unreadable") {
		t.Errorf("%v, %q", err, out.String())
	}
}

func TestAutoStatusLine(t *testing.T) {
	home := homeWithBinary(t)
	fakeManagers(t, "0")
	var out bytes.Buffer
	o := runOpts(home, &out)
	if got := StatusLine(o); got != "off" {
		t.Errorf("%q", got)
	}
	if err := On(o, Request{Level: LevelProject}); err != nil {
		t.Fatal(err)
	}
	if got := StatusLine(o); got != "on, level project; timer installed; never run" {
		t.Errorf("%q", got)
	}
	if err := Run(o, false); err != nil {
		t.Fatal(err)
	}
	if got := StatusLine(o); got != "on, level project; timer installed; last run 2026-11-01" {
		t.Errorf("%q", got)
	}
	os.WriteFile(filepath.Join(dir(home), "runs.json"), []byte("garbage"), 0o600)
	if got := StatusLine(o); !strings.Contains(got, "state unreadable") {
		t.Errorf("%q", got)
	}
	writeConfig(t, home, "garbage")
	if got := StatusLine(o); !strings.HasPrefix(got, "config unreadable: ") {
		t.Errorf("%q", got)
	}
}

// runs.json is what the plan names, in JSON a later phase can read.
func TestAutoStateShape(t *testing.T) {
	data, err := json.Marshal(State{Projects: map[string]Project{"/a": {LastClass: "health"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"date"`, `"model_runs"`, `"last_run"`, `"projects"`, `"last_class"`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("no %s in %s", key, data)
		}
	}
}

// A state a run did not write stops the run; each of these would otherwise
// reset the day's count. A day that rolled over does not stop it.
func TestAutoStateValidated(t *testing.T) {
	for name, text := range map[string]string{
		"null":        `null`,
		"no date":     `{}`,
		"junk date":   `{"date":"junk","model_runs":99}`,
		"unknown key": `{"date":"2026-11-01","bogus":1}`,
		"ahead":       `{"date":"2026-11-03","model_runs":4}`,
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, validConfig)
			ensureDir(home)
			p := filepath.Join(dir(home), "runs.json")
			os.WriteFile(p, []byte(text), 0o600)
			var out bytes.Buffer
			if err := Run(runOpts(home, &out), false); exit(err) != 1 {
				t.Errorf("%v, %q", err, out.String())
			}
			if readFile(t, p) != text {
				t.Error("the state was rewritten")
			}
		})
	}
	home := t.TempDir()
	writeConfig(t, home, validConfig)
	ensureDir(home)
	os.WriteFile(filepath.Join(dir(home), "runs.json"), []byte(`{"date":"2026-11-02","model_runs":1}`), 0o600)
	var out bytes.Buffer
	if err := Run(runOpts(home, &out), false); err != nil {
		t.Errorf("one day of grace: %v, %q", err, out.String())
	}
	if st, _, _ := loadState(home); st.ModelRuns != 1 {
		t.Errorf("the grace handed the budget back: %+v", st)
	}
}

// A dry run refuses what the real run refuses, and writes nothing doing it.
func TestAutoDryRunResolvesTheGuards(t *testing.T) {
	t.Run("corrupt state", func(t *testing.T) {
		home := t.TempDir()
		writeConfig(t, home, validConfig)
		ensureDir(home)
		os.WriteFile(filepath.Join(dir(home), "runs.json"), []byte("garbage"), 0o600)
		before := treeOf(t, home)
		var out bytes.Buffer
		if err := Run(runOpts(home, &out), true); exit(err) != 1 || !strings.Contains(out.String(), "auto: state:") {
			t.Errorf("%v, %q", err, out.String())
		}
		if treeOf(t, home) != before {
			t.Error("the dry run wrote")
		}
	})
	t.Run("state ahead", func(t *testing.T) {
		home := t.TempDir()
		writeConfig(t, home, validConfig)
		ensureDir(home)
		os.WriteFile(filepath.Join(dir(home), "runs.json"), []byte(`{"date":"2026-11-03"}`), 0o600)
		var out bytes.Buffer
		if err := Run(runOpts(home, &out), true); exit(err) != 1 {
			t.Errorf("%v, %q", err, out.String())
		}
		out.Reset()
		if err := Status(runOpts(home, &out)); exit(err) != 1 {
			t.Errorf("status: %v, %q", err, out.String())
		}
	})
	t.Run("auto/ a link", func(t *testing.T) {
		home := t.TempDir()
		writeConfig(t, home, validConfig)
		if err := os.Symlink(t.TempDir(), dir(home)); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := Run(runOpts(home, &out), true); exit(err) != 1 {
			t.Errorf("%v, %q", err, out.String())
		}
	})
}

// A fifo at either file must not hang the timer's run.
func TestAutoRunFifoRefused(t *testing.T) {
	for _, name := range []string{"auto.log", "auto.lock"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, validConfig)
			ensureDir(home)
			if err := syscall.Mkfifo(filepath.Join(dir(home), name), 0o600); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var out bytes.Buffer
				done <- Run(runOpts(home, &out), false)
			}()
			select {
			case err := <-done:
				if name == "auto.lock" && exit(err) != 1 {
					t.Errorf("a fifo for a lock: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the run hung on a fifo")
			}
		})
	}
}
