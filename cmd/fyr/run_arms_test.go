package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// The arms of run that its split moves into helpers, pinned before the
// split (PLAN-2026-10-27-funclen Phase 6, D13).

// With no --home and no $HOME, a verb fails before it runs.
func TestRunWithoutAnyHome(t *testing.T) {
	t.Setenv("HOME", "")
	var out bytes.Buffer
	err := run([]string{"list", "--repo", plainCopy(t)}, &out)
	if err == nil || !strings.Contains(err.Error(), "$HOME") || out.Len() != 0 {
		t.Errorf("err = %v, output %q", err, out.String())
	}
}

// update --from-zip moves the old state directory first, and a refusal of
// that move stops it before the zip is read.
func TestUpdateFromZipMigrationRefused(t *testing.T) {
	home := t.TempDir()
	elsewhere := t.TempDir()
	old := filepath.Join(home, harness.OldStateDir)
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, old); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := run([]string{"update", "--from-zip", filepath.Join(t.TempDir(), "absent.zip"), "--home", home}, &out)
	if err == nil || !strings.Contains(err.Error(), "~/.config/sh-agent is a symlink") {
		t.Errorf("err = %v\n%s", err, out.String())
	}
}
