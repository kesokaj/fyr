package auto

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/dream"
)

// legacyDreamFiles writes legacy fyr-dream unit files for goos under home and
// returns their paths.
func legacyDreamFiles(t *testing.T, home, goos string) []string {
	t.Helper()
	u, p := dream.UnitFor(home)
	var files []string
	switch goos {
	case "linux":
		dir := filepath.Join(home, ".config", "systemd", "user")
		files = []string{filepath.Join(dir, u+".service"), filepath.Join(dir, u+".timer")}
	case "darwin":
		files = []string{filepath.Join(home, "Library", "LaunchAgents", p+".plist")}
	}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("legacy\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func TestAutoRunIngestsMetricsFirstAndDreamsPlanMode(t *testing.T) {
	t.Parallel()
	t.Run("ingest then dream then sweep", func(t *testing.T) {
		home := t.TempDir()
		writeConfig(t, home, validConfig)
		var order []string
		var out bytes.Buffer
		o := runOpts(home, &out)
		o.Env = &fakeEnv{onTargets: func() { order = append(order, "sweep") }}
		o.Ingest = func() (string, error) {
			order = append(order, "ingest")
			return "metrics: 2 sessions, 4 steps added", nil
		}
		o.Dream = func() error {
			order = append(order, "dream")
			return nil
		}
		if err := Run(o, false); err != nil {
			t.Fatalf("Run: %v\n%s", err, out.String())
		}
		if got := strings.Join(order, ","); got != "ingest,dream,sweep" {
			t.Fatalf("call order = %q, want ingest,dream,sweep", got)
		}
		if log := logOf(t, home); !strings.Contains(log, "metrics: 2 sessions, 4 steps added") {
			t.Errorf("auto.log missing ingest summary:\n%s", log)
		}
	})

	t.Run("ingest error does not stop dream or sweep", func(t *testing.T) {
		home := t.TempDir()
		writeConfig(t, home, validConfig)
		var dreamed, swept bool
		var out bytes.Buffer
		o := runOpts(home, &out)
		o.Env = &fakeEnv{onTargets: func() { swept = true }}
		o.Ingest = func() (string, error) { return "", errors.New("disk full") }
		o.Dream = func() error { dreamed = true; return nil }
		if err := Run(o, false); err != nil {
			t.Fatalf("Run: %v\n%s", err, out.String())
		}
		if !dreamed || !swept {
			t.Fatalf("dreamed=%v swept=%v, want both true", dreamed, swept)
		}
		if log := logOf(t, home); !strings.Contains(log, "metrics ingest failed: disk full") {
			t.Errorf("auto.log missing ingest failure:\n%s", log)
		}
	})

	t.Run("dream error marks run failed and still runs sweep", func(t *testing.T) {
		home := t.TempDir()
		writeConfig(t, home, validConfig)
		swept := false
		var out bytes.Buffer
		o := runOpts(home, &out)
		o.Env = &fakeEnv{onTargets: func() { swept = true }}
		o.Ingest = func() (string, error) { return "metrics: 0 sessions", nil }
		o.Dream = func() error { return errors.New("1 project(s) failed") }
		err := Run(o, false)
		if exit(err) != 1 {
			t.Fatalf("exit = %d (%v), want 1", exit(err), err)
		}
		if !swept {
			t.Fatal("sweep did not run after dream failure")
		}
		if log := logOf(t, home); !strings.Contains(log, "dream: 1 project(s) failed") {
			t.Errorf("auto.log missing dream failure:\n%s", log)
		}
	})

	t.Run("auto turned off during dream stops before sweep", func(t *testing.T) {
		home := t.TempDir()
		writeConfig(t, home, validConfig)
		swept := false
		var out bytes.Buffer
		o := runOpts(home, &out)
		o.Env = &fakeEnv{onTargets: func() { swept = true }}
		o.Dream = func() error {
			writeConfig(t, home, `{"enabled": false, "conversation_idle": "1h"}`)
			return nil
		}
		if err := Run(o, false); err != nil {
			t.Fatalf("Run: %v\n%s", err, out.String())
		}
		if swept {
			t.Fatal("sweep ran even though auto was turned off during dream")
		}
		if log := logOf(t, home); !strings.Contains(log, "G1 switch: off, stopped before the sweep") {
			t.Errorf("auto.log missing post-dream G1 stop:\n%s", log)
		}
	})
}

func TestAutoRetiresLegacyDreamTimerAndDryRun(t *testing.T) {
	t.Run("dry-run on refusals and preview", func(t *testing.T) {
		noBin := t.TempDir()
		fakeManagers(t, "0")
		var out bytes.Buffer
		o := opts(noBin, &out)
		o.DryRun = true
		if err := On(o, Request{}); exit(err) != 2 || !strings.Contains(err.Error(), "no binary at") {
			t.Fatalf("missing binary dry-run: %v", err)
		}

		home := homeWithBinary(t)
		calls := fakeManagers(t, "0")
		target := filepath.Join(t.TempDir(), "t.json")
		os.WriteFile(target, []byte("keep"), 0o600)
		os.MkdirAll(filepath.Dir(ConfigPath(home)), 0o700)
		os.Symlink(target, ConfigPath(home))
		o = opts(home, &out)
		o.DryRun = true
		if err := On(o, Request{}); exit(err) != 2 {
			t.Fatalf("symlink dry-run: %v", err)
		}
		os.Remove(ConfigPath(home))

		if err := On(o, Request{ConversationIdle: "1m"}); exit(err) != 2 {
			t.Fatalf("invalid ConversationIdle dry-run: %v", err)
		}

		legacy := legacyDreamFiles(t, home, "linux")
		out.Reset()
		if err := On(o, Request{ConversationIdle: "2h"}); err != nil {
			t.Fatalf("dry-run On: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "[dry-run] would turn auto on") || !strings.Contains(got, "[dry-run] would remove the legacy dream timer") {
			t.Errorf("dry-run On output:\n%s", got)
		}
		if _, found, _ := Load(home); found {
			t.Error("dry-run On wrote auto.json")
		}
		if unit.InstalledHere(home, "linux") {
			t.Error("dry-run On wrote auto timer")
		}
		for _, f := range legacy {
			if _, err := os.Stat(f); err != nil {
				t.Errorf("dry-run On removed %s: %v", f, err)
			}
		}
		if _, err := os.Stat(calls); !os.IsNotExist(err) {
			t.Error("dry-run On called service manager")
		}
	})

	for _, goos := range []string{"linux", "darwin"} {
		t.Run("real On retires legacy dream timer on "+goos, func(t *testing.T) {
			home := homeWithBinary(t)
			fakeManagers(t, "0")
			legacyDreamFiles(t, home, goos)
			var out bytes.Buffer
			o := Options{Home: home, Out: &out, GOOS: goos, Path: "/p:/q", Now: func() time.Time { return runAt }}
			if err := On(o, Request{}); err != nil {
				t.Fatalf("On: %v", err)
			}
			if !unit.InstalledHere(home, goos) {
				t.Error("auto timer not installed")
			}
			if dream.TimerInstalledHere(home, goos) {
				t.Error("legacy dream timer still present after auto on")
			}
		})
	}

	t.Run("dry-run and real Off retire legacy dream timer", func(t *testing.T) {
		home := homeWithBinary(t)
		calls := fakeManagers(t, "0")
		legacy := legacyDreamFiles(t, home, "linux")
		var out bytes.Buffer
		o := opts(home, &out)
		o.DryRun = true
		if err := Off(o); err != nil {
			t.Fatalf("dry-run Off: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "[dry-run] would turn auto off") || !strings.Contains(got, "[dry-run] would remove the dream timer") {
			t.Errorf("dry-run Off output:\n%s", got)
		}
		if _, err := os.Stat(calls); !os.IsNotExist(err) {
			t.Error("dry-run Off called service manager")
		}
		for _, f := range legacy {
			if _, err := os.Stat(f); err != nil {
				t.Errorf("dry-run Off removed %s", f)
			}
		}

		o.DryRun = false
		out.Reset()
		if err := Off(o); err != nil {
			t.Fatalf("real Off with legacy dream timer: %v", err)
		}
		if strings.Contains(out.String(), "already off") {
			t.Errorf("Off claimed already off when legacy dream timer was present:\n%s", out.String())
		}
		if dream.TimerInstalledHere(home, "linux") {
			t.Error("real Off left legacy dream timer behind")
		}
	})
}
