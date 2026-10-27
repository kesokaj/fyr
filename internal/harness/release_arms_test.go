package harness

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// The arms of UpdateFromRelease's replace-and-install tail, which its split
// moves into a helper, pinned before the split (PLAN-2026-10-27-funclen
// Phase 5, D13).

// A ~/.local that is a file stops the update before the binary is written.
func TestUpdateFromReleaseBinDirBlocked(t *testing.T) {
	r := newRelease(t, "v9.9.9")
	r.env(t, true)
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".local"), "a file where ~/.local belongs\n")
	marker := filepath.Join(home, "installed.txt")
	t.Setenv("FYR_FAKE_MARKER", marker)
	err := UpdateFromRelease(home, "v1.0.0", false, nil, &bytes.Buffer{})
	if !errors.Is(err, syscall.ENOTDIR) || strings.Contains(err.Error(), "replacing") {
		t.Errorf("err = %v, want the bare mkdir error", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("an install ran after the failed mkdir")
	}
}

// A binary that cannot be replaced is named, and nothing is installed.
func TestUpdateFromReleaseCannotReplace(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	r := newRelease(t, "v9.9.9")
	r.env(t, true)
	home := t.TempDir()
	dest := installedBinary(t, home)
	bin := filepath.Dir(dest)
	if err := os.Chmod(bin, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(bin, 0o755) })
	var out bytes.Buffer
	err := UpdateFromRelease(home, "v1.0.0", false, nil, &out)
	if err == nil || !strings.HasPrefix(err.Error(), "replacing "+dest+": ") {
		t.Errorf("err = %v, want the replace error", err)
	}
	if strings.Contains(out.String(), "verified and installed") {
		t.Errorf("a failed replace claimed success:\n%s", out.String())
	}
}

// A new binary whose install fails leaves it in place and says how to finish.
func TestUpdateFromReleaseNewInstallFails(t *testing.T) {
	r := newRelease(t, "v9.9.9")
	asset := assetName(runtime.GOOS, runtime.GOARCH)
	writeFile(t, filepath.Join(r.assets, asset), "#!/bin/sh\nexit 3\n")
	writeSums(t, r.assets, asset, r.tag)
	r.env(t, true)
	home := t.TempDir()
	dest := installedBinary(t, home)
	err := UpdateFromRelease(home, "v1.0.0", false, nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "the harness it carries was not installed; run `fyr install` to finish") {
		t.Errorf("err = %v, want the failed install named", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "#!/bin/sh\nexit 3\n" {
		t.Errorf("the new binary is not in place: %q", got)
	}
}
