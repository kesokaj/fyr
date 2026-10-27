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

// --install-timer installs through the service manager and says so; with no
// binary to point at it is refused with exit 2.
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
	var out bytes.Buffer
	if err := run([]string{"dream", "--install-timer", "--home", home}, &out); exitOf(err) != 2 || !strings.Contains(out.String(), "no binary at") {
		t.Errorf("no binary: exit %d, %q", exitOf(err), out.String())
	}
	binPath := filepath.Join(home, ".local", "bin", "fyr")
	os.MkdirAll(filepath.Dir(binPath), 0o755)
	os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755)
	out.Reset()
	if err := run([]string{"dream", "--install-timer", "--home", home}, &out); err != nil || out.String() != "dream timer installed: fyr dream --all runs nightly at 03:00\n" {
		t.Errorf("install: %v, %q", err, out.String())
	}
	if b, err := os.ReadFile(calls); err != nil || len(b) == 0 {
		t.Errorf("the service manager was not called: %v", err)
	}
}
