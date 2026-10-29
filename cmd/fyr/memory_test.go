package main

import (
	"bytes"
	"errors"
	"github.com/kesokaj/fyr/internal/dream"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"testing"
)

// TestMemoryVerb runs `memory add` then `memory show` the way a session
// calls them: flags after the subcommand, a description with
// spaces, the body on stdin. The memory is a record in the brain store
// under --home (D14), and nothing is written into the checkout.
func TestMemoryVerb(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	t.Chdir(root)
	home := t.TempDir()
	stdin = strings.NewReader("We deploy on Fridays.\n")
	defer func() { stdin = os.Stdin }()

	var out bytes.Buffer
	args := []string{"memory", "add", "--type", "project", "--name", "deploy-day", "--description", "we deploy on Fridays", "--home", home}
	if err := run(args, &out); err != nil {
		t.Fatalf("memory add: %v\n%s", err, out.String())
	}
	if out.String() != "saved MEMORY-deploy-day\n" {
		t.Errorf("add printed %q", out.String())
	}
	if _, err := os.Lstat(filepath.Join(root, ".agents")); !os.IsNotExist(err) {
		t.Errorf("memory add wrote into the checkout: %v", err)
	}

	out.Reset()
	if err := run([]string{"memory", "show", "--home", home}, &out); err != nil {
		t.Fatalf("memory show: %v", err)
	}
	if got := out.String(); got != "- deploy-day (project) — we deploy on Fridays\n" {
		t.Errorf("show = %q", got)
	}
}

// TestMemoryRefusalExitsTwo: a refusal is exit 2, not an error the session
// would read as a crash.
func TestMemoryRefusalExitsTwo(t *testing.T) {
	t.Chdir(t.TempDir()) // not a git checkout
	stdin = strings.NewReader("fact\n")
	defer func() { stdin = os.Stdin }()
	err := run([]string{"memory", "add", "--type", "user", "--name", "x", "--description", "d", "--home", t.TempDir()}, &bytes.Buffer{})
	var code exitCode
	if !errors.As(err, &code) || code != 2 {
		t.Fatalf("err = %v, want exit 2", err)
	}
}

// TestDreamVerb: the verb reaches the package with its flags, --project taking
// a value (reorder reads that from the FlagSet), and refuses neither-nor with the usage line.
func TestDreamVerb(t *testing.T) {
	home := t.TempDir()
	var code exitCode
	if err := run([]string{"dream", "--home", home}, &bytes.Buffer{}); !errors.As(err, &code) || code != 2 {
		t.Fatalf("bare dream: %v, want exit 2", err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("a usage error wrote %v", entries)
	}
	// The dry run checks for agy and a login as the real run does (SD27 of
	// the 2026-10-27 audit): give it both, outside the real home.
	dreamAgy(t)
	var out bytes.Buffer
	if err := run([]string{"dream", "--all", "--dry-run", "--home", home}, &out); err != nil {
		t.Fatalf("--all --dry-run on an empty home: %v", err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("a dry run on an empty home wrote %v", entries)
	}
	if err := run([]string{"dream", "--project", t.TempDir(), "--dry-run", "--home", home}, &out); !errors.As(err, &code) || code != 2 {
		t.Errorf("--project outside a checkout: %v, want exit 2", err)
	}
	// Usage refusals exit 2 and say why on the writer the caller gave, not
	// on a stream the caller cannot read (audit of 2026-09-30, IN12, IN13).
	out.Reset()
	if err := run([]string{"dream", "--install-timer", "--remove-timer", "--home", home}, &out); !errors.As(err, &code) || code != 2 {
		t.Errorf("both timer flags: %v, want exit 2", err)
	}
	if !strings.Contains(out.String(), "not both") {
		t.Errorf("the refusal is not on the writer: %q", out.String())
	}
	out.Reset()
	run([]string{"dream", "--home", home}, &out)
	if !strings.Contains(out.String(), "fyr dream") {
		t.Errorf("a bare dream's usage message is not on the writer: %q", out.String())
	}
}

// TestUninstallDryRunLeavesTheTimer: a dry run names the timer and runs no
// systemctl; the real uninstall removes it, with no manifest left.
func TestUninstallDryRunLeavesTheTimer(t *testing.T) {
	home := t.TempDir()
	unitName, _ := dream.UnitFor(home) // a scratch home gets its own unit name (R6)
	unit := filepath.Join(home, ".config", "systemd", "user", unitName+".timer")
	os.MkdirAll(filepath.Dir(unit), 0o755)
	os.WriteFile(unit, []byte("x"), 0o644)
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\necho \"$*\" >> \""+calls+"\"\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "launchctl"), []byte("#!/bin/sh\necho \"$*\" >> \""+calls+"\"\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out bytes.Buffer
	run([]string{"uninstall", "--dry-run", "--home", home}, &out)
	if !strings.Contains(out.String(), "would remove the dream timer") {
		t.Errorf("dry run output:\n%s", out.String())
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Error("a dry run called the service manager")
	}
	if _, err := os.Stat(unit); err != nil {
		t.Error("a dry run removed the unit")
	}
	out.Reset()
	run([]string{"uninstall", "--home", home}, &out)
	if runtime.GOOS == "linux" {
		if _, err := os.Stat(unit); !os.IsNotExist(err) {
			t.Errorf("uninstall left the timer (no manifest):\n%s", out.String())
		}
	}
}

// TestDreamTimerDryRun: --dry-run with --install-timer or --remove-timer
// writes no unit and calls no service manager.
func TestDreamTimerDryRun(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	for _, m := range []string{"systemctl", "launchctl"} {
		os.WriteFile(filepath.Join(bin, m), []byte("#!/bin/sh\necho \"$*\" >> \""+calls+"\"\n"), 0o755)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// With no binary to point the unit at, the dry run refuses as the real
	// run does (SD26 of the 2026-10-27 audit, plan D17).
	var refused bytes.Buffer
	err := run([]string{"dream", "--install-timer", "--dry-run", "--home", home}, &refused)
	var code exitCode
	if !errors.As(err, &code) || code != 2 || !strings.Contains(refused.String(), "no binary at") {
		t.Errorf("--install-timer --dry-run with no binary: %v, %q; want exit 2 naming the binary", err, refused.String())
	}
	binPath := filepath.Join(home, ".local", "bin", "fyr")
	os.MkdirAll(filepath.Dir(binPath), 0o755)
	os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755)
	for _, flag := range []string{"--install-timer", "--remove-timer"} {
		var out bytes.Buffer
		if err := run([]string{"dream", flag, "--dry-run", "--home", home}, &out); err != nil || !strings.Contains(out.String(), "[dry-run] would") {
			t.Errorf("%s --dry-run: %v, %q", flag, err, out.String())
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 1 || entries[0].Name() != ".local" {
		t.Errorf("a dry run wrote %v", entries)
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Error("a dry run called the service manager")
	}
}

// TestDreamTimerTakesNoScope: the timer flags with --all or --project are a
// usage error, not silently ignored (SD33 of the 2026-10-27 audit, D18), and
// so are --all with --project.
func TestDreamTimerTakesNoScope(t *testing.T) {
	home := t.TempDir()
	for _, args := range [][]string{
		{"--install-timer", "--all"},
		{"--remove-timer", "--project", "."},
		{"--install-timer", "--dry-run", "--all"},
	} {
		var out bytes.Buffer
		err := run(append(append([]string{"dream"}, args...), "--home", home), &out)
		var code exitCode
		if !errors.As(err, &code) || code != 2 || !strings.Contains(out.String(), "take no --all or --project") {
			t.Errorf("%v: %v, %q; want exit 2", args, err, out.String())
		}
	}
	var out bytes.Buffer
	err := run([]string{"dream", "--all", "--project", ".", "--dry-run", "--home", home}, &out)
	var code exitCode
	if !errors.As(err, &code) || code != 2 || !strings.Contains(out.String(), "not both") {
		t.Errorf("--all with --project: %v, %q; want exit 2", err, out.String())
	}
}

// TestDreamTimerSaysWhatItFound: --remove-timer says when it found nothing,
// and never claims a removal then (the v2.1.0 gate's L1); with a unit there,
// --remove-timer and uninstall each say they removed it.
func TestDreamTimerSaysWhatItFound(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	for _, m := range []string{"systemctl", "launchctl"} {
		os.WriteFile(filepath.Join(bin, m), []byte("#!/bin/sh\necho \"$*\" >> \""+calls+"\"\n"), 0o755)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out bytes.Buffer
	if err := run([]string{"dream", "--remove-timer", "--home", home}, &out); err != nil {
		t.Fatalf("--remove-timer with nothing installed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "no dream timer found for "+home+" on this system") || strings.Contains(got, "dream timer removed") {
		t.Errorf("nothing installed: %q", got)
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		data, _ := os.ReadFile(calls)
		t.Errorf("the service manager was called with nothing installed:\n%s", data)
	}
	// With this home's unit on disk, each verb says it removed it, and did.
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"dream", "--remove-timer", "--home", home}, "dream timer removed\n"},
		{[]string{"uninstall", "--home", home}, "removed the dream timer\n"},
	} {
		files := timerFilesFor(t, home)
		out.Reset()
		if err := run(c.args, &out); err != nil || !strings.Contains(out.String(), c.want) {
			t.Errorf("%v: %v, %q; want %q", c.args, err, out.String(), c.want)
		}
		for _, f := range files {
			if _, err := os.Lstat(f); !os.IsNotExist(err) {
				t.Errorf("%v left %s", c.args, f)
			}
		}
	}
}

// timerFilesFor writes this home's dream unit files for the running system
// and returns their paths.
func timerFilesFor(t *testing.T, home string) []string {
	t.Helper()
	unit, plist := dream.UnitFor(home)
	files := []string{filepath.Join(home, "Library", "LaunchAgents", plist+".plist")}
	if runtime.GOOS != "darwin" {
		dir := filepath.Join(home, ".config", "systemd", "user")
		files = []string{filepath.Join(dir, unit+".service"), filepath.Join(dir, unit+".timer")}
	}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// TestTimerOfAnotherSystemIsNamed: a home copied between a Mac and a Linux
// box holds the other system's unit file. status reads "timer installed" for
// it, so uninstall and --remove-timer must not pass over it in silence: they
// name the file and say it stays (the v2.2.0 gate's L1).
func TestTimerOfAnotherSystemIsNamed(t *testing.T) {
	home := t.TempDir()
	unit, plist := dream.UnitFor(home)
	other := filepath.Join(home, "Library", "LaunchAgents", plist+".plist")
	if runtime.GOOS == "darwin" {
		other = filepath.Join(home, ".config", "systemd", "user", unit+".timer")
	}
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"uninstall", "--home", home},
		{"uninstall", "--dry-run", "--home", home},
		{"dream", "--remove-timer", "--home", home},
		{"dream", "--remove-timer", "--dry-run", "--home", home},
	} {
		var out bytes.Buffer
		if err := run(args, &out); err != nil {
			t.Errorf("%v: %v", args, err)
		}
		got := out.String()
		if !strings.Contains(got, other) || !strings.Contains(got, "for another system") {
			t.Errorf("%v does not name the other system's unit file:\n%s", args, got)
		}
		// uninstall has nothing of this system to remove here, so its dry run
		// must not announce a removal either.
		if args[0] == "dream" && strings.Contains(got, "installed now: true") {
			t.Errorf("%v says a timer of this system is installed:\n%s", args, got)
		}
		if args[0] == "uninstall" && strings.Contains(got, "would remove the dream timer") {
			t.Errorf("%v announces a removal it would not make:\n%s", args, got)
		}
		if strings.Contains(got, "removed the dream timer") || strings.Contains(got, "dream timer removed") {
			t.Errorf("%v claims a removal:\n%s", args, got)
		}
		if _, err := os.Stat(other); err != nil {
			t.Fatalf("%v removed the other system's unit file: %v", args, err)
		}
	}
}

// A memory usage error exits 2 like every other usage error of this binary;
// the usage line goes to the writer.
func TestMemoryUsageErrorExits2(t *testing.T) {
	for _, args := range [][]string{{}, {"forget"}, {"add", "extra"}} {
		var out bytes.Buffer
		err := memoryCmd(args, brain.MemoryEntry{}, t.TempDir(), &out)
		var code exitCode
		if !errors.As(err, &code) || code != 2 {
			t.Errorf("memory %v: want exit 2, got %v", args, err)
		}
		if !strings.Contains(out.String(), "usage: fyr memory") {
			t.Errorf("memory %v: usage line missing from the writer: %q", args, out.String())
		}
	}
}

// dreamAgy puts an `agy` on PATH and an Antigravity login under a temporary
// HOME, for a dream dry run that must pass its agy checks on any box.
func dreamAgy(t *testing.T) {
	t.Helper()
	home, bin := t.TempDir(), t.TempDir()
	token := filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
	os.MkdirAll(filepath.Dir(token), 0o700)
	os.WriteFile(token, []byte("token"), 0o600)
	os.WriteFile(filepath.Join(bin, "agy"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}
