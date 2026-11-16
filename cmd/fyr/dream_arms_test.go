package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The arms of runDream that its split moves into a helper, pinned before
// the split (PLAN-2026-10-27-funclen Phase 6, D13).

// Legacy dream timer flags are removed (merged into fyr auto), and fyr auto on
// installs the single hourly timer through the service manager.
func TestDreamInstallTimer(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no timer on " + runtime.GOOS)
	}
	home := t.TempDir()
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	for _, m := range []string{"systemctl", "launchctl"} {
		os.WriteFile(filepath.Join(bin, m), []byte("#!/bin/sh\necho \"$*\" >> \""+calls+"\"\n"), 0o755)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, flag := range []string{"--install-timer", "--remove-timer"} {
		var out bytes.Buffer
		if err := run([]string{"dream", flag, "--home", home}, &out); exitOf(err) != 2 {
			t.Errorf("dream %s: exit %d, %q; want 2", flag, exitOf(err), out.String())
		}
	}
	var out bytes.Buffer
	if err := run([]string{"auto", "on", "--home", home}, &out); exitOf(err) != 2 || !strings.Contains(out.String(), "no binary at") {
		t.Errorf("no binary: exit %d, %q", exitOf(err), out.String())
	}
	binPath := filepath.Join(home, ".local", "bin", "fyr")
	os.MkdirAll(filepath.Dir(binPath), 0o755)
	os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755)
	out.Reset()
	if err := run([]string{"auto", "on", "--home", home}, &out); err != nil || !strings.Contains(out.String(), "fyr auto run runs hourly") {
		t.Errorf("auto on: %v, %q", err, out.String())
	}
	if b, err := os.ReadFile(calls); err != nil || len(b) == 0 {
		t.Errorf("the service manager was not called: %v", err)
	}
}
