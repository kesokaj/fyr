package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/auto"
	"github.com/kesokaj/fyr/internal/harness"
)

// autoHome is a scratch home with a binary and recording service managers
// first on PATH: the real user manager is never reached (R6).
func autoHome(t *testing.T) (home, calls string) {
	t.Helper()
	home = t.TempDir()
	bin := t.TempDir()
	calls = filepath.Join(t.TempDir(), "calls")
	for _, m := range []string{"systemctl", "launchctl"} {
		if err := os.WriteFile(filepath.Join(bin, m), []byte("#!/bin/sh\necho \"$*\" >> \""+calls+"\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	binPath := filepath.Join(home, harness.BinPath)
	os.MkdirAll(filepath.Dir(binPath), 0o755)
	os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755)
	return home, calls
}

func TestAutoVerbOnStatusRunOff(t *testing.T) {
	home, _ := autoHome(t)
	var out bytes.Buffer
	if err := run([]string{"auto", "on", "--level", "project", "--project-idle", "7d", "--user-idle", "1m", "--home", home}, &out); err != nil {
		t.Fatalf("on: %v, %s", err, out.String())
	}
	c, found, err := auto.Load(home)
	if err != nil || !found || c != (auto.Config{Enabled: true, Level: "project", ProjectIdle: "7d", UserIdle: "1m"}) {
		t.Fatalf("%+v, %v, %v", c, found, err)
	}
	out.Reset()
	if err := run([]string{"auto", "run", "--home", home}, &out); err != nil {
		t.Fatalf("run: %v, %s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"auto", "status", "--home", home}, &out); err != nil || !strings.Contains(out.String(), "auto       on; timer installed\n") {
		t.Fatalf("status: %v, %s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"auto", "off", "--home", home}, &out); err != nil || out.String() != "auto off\n" {
		t.Fatalf("off: %v, %q", err, out.String())
	}
}

func TestAutoVerbUsage(t *testing.T) {
	home, calls := autoHome(t)
	for name, args := range map[string][]string{
		"bare":              {"auto"},
		"unknown":           {"auto", "maybe"},
		"two":               {"auto", "on", "off"},
		"level on off":      {"auto", "off", "--level", "project"},
		"level on run":      {"auto", "run", "--level", "project"},
		"dry-run on on":     {"auto", "on", "--dry-run"},
		"dry-run on off":    {"auto", "off", "--dry-run"},
		"dry-run on status": {"auto", "status", "--dry-run"},
		"bad level":         {"auto", "on", "--level", "god"},
		"empty level":       {"auto", "on", "--level", ""},
		"short idle":        {"auto", "on", "--project-idle", "59m"},
		"long idle":         {"auto", "on", "--project-idle", "8d"},
		"empty idle":        {"auto", "on", "--project-idle="},
		"short user":        {"auto", "on", "--user-idle", "59s"},
		"long user":         {"auto", "on", "--user-idle", "25h"},
		"bad user":          {"auto", "on", "--user-idle", "soon"},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(append(args, "--home", home), &out)
			if exitOf(err) != 2 || !strings.Contains(out.String(), "fyr auto") {
				t.Errorf("exit %d, %q", exitOf(err), out.String())
			}
		})
	}
	if _, found, _ := auto.Load(home); found {
		t.Error("a usage error wrote the config")
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Error("a usage error reached the service manager")
	}
}

// --level harness: a development build inside this checkout passes; a plain
// copy, a tree that is no harness and a release binary are refused.
func TestAutoVerbHarnessLevel(t *testing.T) {
	home, _ := autoHome(t)
	var out bytes.Buffer
	if err := run([]string{"auto", "on", "--level", "harness", "--home", home}, &out); err != nil {
		t.Fatalf("this checkout: %v, %s", err, out.String())
	}
	if err := run([]string{"auto", "off", "--home", home}, &out); err != nil {
		t.Fatal(err)
	}
	plain := t.TempDir()
	os.MkdirAll(filepath.Join(plain, "context"), 0o755)
	os.MkdirAll(filepath.Join(plain, "skills"), 0o755)
	os.WriteFile(filepath.Join(plain, "context", "AGENTS.md"), []byte("x"), 0o644)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(plain))
	for name, args := range map[string][]string{
		"plain copy": {"--repo", plain},
		"no harness": {"--repo", t.TempDir()},
	} {
		t.Run(name, func(t *testing.T) {
			out.Reset()
			err := run(append([]string{"auto", "on", "--level", "harness", "--home", home}, args...), &out)
			if exitOf(err) != 2 || !strings.Contains(out.String(), "level harness needs a harness checkout") {
				t.Errorf("exit %d, %q", exitOf(err), out.String())
			}
		})
	}
	t.Run("release binary", func(t *testing.T) {
		old := version
		version = "v9.9.9"
		defer func() { version = old }()
		out.Reset()
		err := run([]string{"auto", "on", "--level", "harness", "--home", home}, &out)
		if exitOf(err) != 2 || !strings.Contains(out.String(), "built into the binary") {
			t.Errorf("exit %d, %q", exitOf(err), out.String())
		}
	})
	if c, _, _ := auto.Load(home); c.Enabled {
		t.Error("a refusal left auto on")
	}
}

func TestAutoVerbCorruptConfig(t *testing.T) {
	home, _ := autoHome(t)
	os.MkdirAll(filepath.Dir(auto.ConfigPath(home)), 0o700)
	os.WriteFile(auto.ConfigPath(home), []byte("garbage"), 0o600)
	for _, sub := range []string{"run", "status"} {
		var out bytes.Buffer
		if err := run([]string{"auto", sub, "--home", home}, &out); exitOf(err) != 1 || !strings.Contains(out.String(), "; run fyr auto off or on") {
			t.Errorf("%s: exit %d, %q", sub, exitOf(err), out.String())
		}
	}
	var out bytes.Buffer
	if err := run([]string{"auto", "off", "--home", home}, &out); err != nil {
		t.Errorf("off: %v, %q", err, out.String())
	}
}

// R4: uninstall takes the timer and leaves the config and the data, and a dry
// run only says so. The dream's timer goes on its own terms.
func TestUninstallRemovesAutoTimerKeepsItsFiles(t *testing.T) {
	home, calls := autoHome(t)
	var out bytes.Buffer
	if err := run([]string{"auto", "on", "--level", "project", "--home", home}, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"auto", "run", "--home", home}, &out); err != nil {
		t.Fatal(err)
	}
	os.Remove(calls)
	out.Reset()
	run([]string{"uninstall", "--dry-run", "--home", home}, &out)
	if !strings.Contains(out.String(), "would remove the auto timer") {
		t.Errorf("dry run: %s", out.String())
	}
	if !auto.TimerInstalledHere(home, runtime.GOOS) {
		t.Error("a dry run removed the timer")
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Error("a dry run called the service manager")
	}
	out.Reset()
	run([]string{"uninstall", "--home", home}, &out)
	if !strings.Contains(out.String(), "removed the auto timer") {
		t.Errorf("uninstall: %s", out.String())
	}
	if auto.TimerInstalledHere(home, runtime.GOOS) {
		t.Error("the timer stayed")
	}
	if c, found, err := auto.Load(home); err != nil || !found || c.Level != "project" {
		t.Errorf("auto.json: %+v, %v, %v", c, found, err)
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir, "auto", "auto.log")); err != nil {
		t.Errorf("auto/ went: %v", err)
	}
}

func TestStatusAutoLine(t *testing.T) {
	home, _ := autoHome(t)
	var out bytes.Buffer
	if err := run([]string{"install", "--home", home}, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	run([]string{"status", "--home", home}, &out)
	if !strings.Contains(out.String(), "\nauto       off\n") {
		t.Errorf("off:\n%s", out.String())
	}
	if err := run([]string{"auto", "on", "--home", home}, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	run([]string{"status", "--home", home}, &out)
	if !strings.Contains(out.String(), "\nauto       on, level observe; timer installed; never run\n") {
		t.Errorf("on:\n%s", out.String())
	}
}

// A failed stop of the timer stops nothing else, and the exit status says so.
func TestUninstallGoesOnWhenTheAutoTimerFails(t *testing.T) {
	home, _ := autoHome(t)
	var out bytes.Buffer
	if err := run([]string{"install", "--home", home}, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"auto", "on", "--home", home}, &out); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, m := range []string{"systemctl", "launchctl"} {
		os.WriteFile(filepath.Join(bin, m), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out.Reset()
	err := run([]string{"uninstall", "--home", home}, &out)
	if runtime.GOOS == "linux" && err == nil {
		t.Error("a failed stop went unsaid")
	}
	if _, serr := os.Stat(filepath.Join(home, harness.BinPath)); !os.IsNotExist(serr) {
		t.Error("the rest of the uninstall did not run")
	}
}
