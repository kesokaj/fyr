//go:build linux

package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListProcsFromLinuxFixture(t *testing.T) {
	root := t.TempDir()
	mustWriteWatchFile(t, filepath.Join(root, "uptime"), "1000.50 2000.00\n", time.Time{})
	pidDir := filepath.Join(root, "4242")
	statLine := "4242 (sudo apt) S 100 4242 100 0 -1 4194304 1 0 0 0 0 0 0 0 20 0 1 0 85000 1000 10 0 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n"
	mustWriteWatchFile(t, filepath.Join(pidDir, "stat"), statLine, time.Time{})
	mustWriteWatchFile(t, filepath.Join(pidDir, "cmdline"), "sudo\x00apt\x00update\x00", time.Time{})
	mustWriteWatchFile(t, filepath.Join(pidDir, "wchan"), "n_tty_read\n", time.Time{})

	procs, err := listProcsFrom(root, os.Getuid())
	if err != nil {
		t.Fatalf("listProcsFrom: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("got %d procs %+v, want 1", len(procs), procs)
	}
	p := procs[0]
	if p.PID != 4242 || p.PPID != 100 || p.State != "S" || p.Wchan != "n_tty_read" || p.Cmdline != "sudo apt update" || p.ElapsedSec != 150 {
		t.Errorf("got %+v, want PID=4242 PPID=100 State=S Wchan=n_tty_read Cmdline='sudo apt update' ElapsedSec=150", p)
	}
}
