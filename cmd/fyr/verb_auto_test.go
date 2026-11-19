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
	"github.com/kesokaj/fyr/internal/trail"
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
	if err := run([]string{"auto", "on", "--conversation-idle", "7d", "--home", home}, &out); err != nil {
		t.Fatalf("on: %v, %s", err, out.String())
	}
	c, found, err := auto.Load(home)
	if err != nil || !found || c != (auto.Config{Enabled: true, ConversationIdle: "7d"}) {
		t.Fatalf("%+v, %v, %v", c, found, err)
	}
	out.Reset()
	if err := run([]string{"auto", "run", "--home", home}, &out); err != nil {
		t.Fatalf("run: %v, %s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"auto", "status", "--home", home}, &out); err != nil || !strings.Contains(out.String(), "auto       on; timer installed\n") || !strings.Contains(out.String(), "stale after 7d") {
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
		"bare":               {"auto"},
		"unknown":            {"auto", "maybe"},
		"two":                {"auto", "on", "off"},
		"conv-idle on off":   {"auto", "off", "--conversation-idle", "2h"},
		"conv-idle on run":   {"auto", "run", "--conversation-idle", "2h"},
		"conv-idle bare dry": {"auto", "--dry-run", "--conversation-idle", "2h"},
		"dry-run on status":  {"auto", "status", "--dry-run"},
		"short idle":         {"auto", "on", "--conversation-idle", "4m"},
		"long idle":          {"auto", "on", "--conversation-idle", "8d"},
		"empty idle":         {"auto", "on", "--conversation-idle="},
		"bad idle":           {"auto", "on", "--conversation-idle", "soon"},
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

// Deprecated --level / --project-idle / --user-idle flags are accepted as
// no-ops with warnings and auto on no longer requires a harness checkout.
func TestAutoVerbDeprecatedFlagsAndNoCheckoutGate(t *testing.T) {
	home, _ := autoHome(t)
	var out bytes.Buffer
	if err := run([]string{"auto", "on", "--level", "harness", "--project-idle", "6h", "--user-idle", "15m", "--conversation-idle", "2h", "--home", home}, &out); err != nil {
		t.Fatalf("deprecated flags: %v, %s", err, out.String())
	}
	for _, want := range []string{"--level", "--project-idle", "--user-idle"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing deprecation warning for %s in:\n%s", want, out.String())
		}
	}
	if c, found, err := auto.Load(home); err != nil || !found || c != (auto.Config{Enabled: true, ConversationIdle: "2h"}) {
		t.Fatalf("config after deprecated flags: %+v, %v, %v", c, found, err)
	}
	for name, args := range map[string][]string{
		"plain dir": {"--repo", t.TempDir()},
	} {
		t.Run(name, func(t *testing.T) {
			out.Reset()
			if err := run(append([]string{"auto", "on", "--home", home}, args...), &out); err != nil {
				t.Fatalf("auto on without harness checkout: %v\n%s", err, out.String())
			}
		})
	}
	t.Run("release binary", func(t *testing.T) {
		old := version
		version = "v9.9.9"
		defer func() { version = old }()
		out.Reset()
		if err := run([]string{"auto", "on", "--home", home}, &out); err != nil {
			t.Fatalf("auto on in release binary: %v\n%s", err, out.String())
		}
	})
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
	if err := run([]string{"auto", "on", "--conversation-idle", "2h", "--home", home}, &out); err != nil {
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
	if c, found, err := auto.Load(home); err != nil || !found || c.ConversationIdle != "2h" {
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
	if !strings.Contains(out.String(), "\nauto       on, stale after 1h; timer installed; never run\n") {
		t.Errorf("on:\n%s", out.String())
	}
}

// A failed stop of auto's timer or a legacy dream timer stops nothing else,
// and the exit status says so.
func TestUninstallGoesOnWhenTheAutoTimerFails(t *testing.T) {
	home, _ := autoHome(t)
	var out bytes.Buffer
	if err := run([]string{"install", "--home", home}, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"auto", "on", "--home", home}, &out); err != nil {
		t.Fatal(err)
	}
	timerFilesFor(t, home)
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

func TestUninstallJoinsTimerAndHarnessErrors(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("StrictStop timer failure applies on linux")
	}
	home, _ := autoHome(t)
	var out bytes.Buffer
	if err := run([]string{"install", "--home", home}, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"auto", "on", "--home", home}, &out); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, harness.StateDir, "state.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out.Reset()
	err := run([]string{"uninstall", "--home", home}, &out)
	if err == nil || !strings.Contains(err.Error(), "systemctl") {
		t.Fatalf("uninstall err = %v, want joined error naming both systemctl and state.json", err)
	}
}

func TestAutoDreamHarnessRepoResolution(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if got := autoDreamHarnessRepo("", home, repo); got != "" {
		t.Fatalf("uninstalled home leaked %q, want empty", got)
	}
	if opts := (&ctx{home: home}).autoRunOptions(auto.Options{Home: home}); opts.Repo != "" {
		t.Fatalf("uninstalled autoRunOptions().Repo = %q, want empty", opts.Repo)
	}
	if got := autoDreamHarnessRepo(repo, home, repo); got != repo {
		t.Fatalf("explicit --repo = %q, want %q", got, repo)
	}
	checkout := repoRoot(t)
	if opts := (&ctx{home: home, repoFlag: checkout}).autoRunOptions(auto.Options{Home: home}); opts.Repo == "" {
		t.Fatalf("explicit --repo autoRunOptions().Repo is empty")
	}
	t.Setenv("FYR_REPO", repo)
	if got := autoDreamHarnessRepo("", home, repo); got != repo {
		t.Fatalf("FYR_REPO = %q, want %q", got, repo)
	}
	t.Setenv("FYR_REPO", checkout)
	if opts := (&ctx{home: home}).autoRunOptions(auto.Options{Home: home}); opts.Repo == "" {
		t.Fatalf("FYR_REPO autoRunOptions().Repo is empty")
	}
}

func TestAutoDreamHarnessRepoIgnoresUnrelatedCWDCheckout(t *testing.T) {
	home := t.TempDir()
	legitRepo := checkoutShaped(t)
	unrelatedRepo := checkoutShaped(t)
	if err := harness.SaveState(home, harness.State{Repo: legitRepo}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	t.Setenv("FYR_REPO", "")
	t.Chdir(unrelatedRepo)

	if got := autoDreamHarnessRepo("", home, unrelatedRepo); got != "" {
		t.Fatalf("autoDreamHarnessRepo leaked unrelated CWD repo %q, want empty", got)
	}
	opts := (&ctx{home: home}).autoRunOptions(auto.Options{Home: home})
	if !trail.SameDir(opts.Repo, legitRepo) || trail.SameDir(opts.Repo, unrelatedRepo) {
		t.Fatalf("autoRunOptions().Repo = %q, want %q (not %q)", opts.Repo, legitRepo, unrelatedRepo)
	}

	// Missing/invalid st.Repo must return "" and never fall back to unrelated CWD checkout.
	if err := harness.SaveState(home, harness.State{Repo: filepath.Join(t.TempDir(), "missing")}); err != nil {
		t.Fatalf("SaveState missing: %v", err)
	}
	if opts := (&ctx{home: home}).autoRunOptions(auto.Options{Home: home}); opts.Repo != "" {
		t.Fatalf("missing st.Repo leaked CWD repo %q, want empty", opts.Repo)
	}
	for _, repoVal := range []string{"", harness.ZipPrefix + "sha256:abcd;verified v1.0.0"} {
		if err := harness.SaveState(home, harness.State{Repo: repoVal}); err != nil {
			t.Fatalf("SaveState(%q): %v", repoVal, err)
		}
		if opts := (&ctx{home: home}).autoRunOptions(auto.Options{Home: home}); opts.Repo != "" {
			t.Fatalf("origin %q leaked CWD repo %q, want empty", repoVal, opts.Repo)
		}
	}
}
