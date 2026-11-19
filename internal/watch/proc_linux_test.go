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

func TestListProcsErrorsAndFallback(t *testing.T) {
	root := t.TempDir()
	mustWriteWatchFile(t, filepath.Join(root, "uptime"), "1000.00 2000.00\n", time.Time{})
	statLine := "5001 (worker) S 100 5001 100 0 -1 4194304 1 0 0 0 0 0 0 0 20 0 1 0 85000 1000 10 0 0 0 0 0 0 0 0 0 0 0 0 0 17 0 0 0 0 0 0 0 0 0 0 0 0 0 0\n"

	// Missing cmdline (exited mid-scan) must be skipped without ghost pid:<n>.
	mustWriteWatchFile(t, filepath.Join(root, "5001", "stat"), statLine, time.Time{})
	mustWriteWatchFile(t, filepath.Join(root, "5001", "wchan"), "0\n", time.Time{})

	// Empty wchan normalizes to unknownLinuxWchan instead of "".
	mustWriteWatchFile(t, filepath.Join(root, "5002", "stat"), statLine, time.Time{})
	mustWriteWatchFile(t, filepath.Join(root, "5002", "cmdline"), "sleep\x0010\x00", time.Time{})
	mustWriteWatchFile(t, filepath.Join(root, "5002", "wchan"), "", time.Time{})

	// Unreadable wchan (EACCES) falls back to unknownLinuxWchan when non-root.
	mustWriteWatchFile(t, filepath.Join(root, "5003", "stat"), statLine, time.Time{})
	mustWriteWatchFile(t, filepath.Join(root, "5003", "cmdline"), "cat\x00", time.Time{})
	wchan5003 := filepath.Join(root, "5003", "wchan")
	mustWriteWatchFile(t, wchan5003, "pipe_read\n", time.Time{})
	if os.Getuid() != 0 {
		if err := os.Chmod(wchan5003, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(wchan5003, 0o600) })
	}

	origProcDir := procDir
	procDir = root
	t.Cleanup(func() { procDir = origProcDir })

	procs, err := listProcs()
	if err != nil {
		t.Fatalf("listProcs: %v", err)
	}
	if len(procs) != 2 {
		t.Fatalf("got %d procs %+v, want 2 (vanished 5001 skipped)", len(procs), procs)
	}
	byPID := map[int]ProcInfo{}
	for _, p := range procs {
		byPID[p.PID] = p
	}
	if byPID[5002].Wchan != unknownLinuxWchan {
		t.Errorf("pid 5002 empty wchan = %q, want %q", byPID[5002].Wchan, unknownLinuxWchan)
	}
	if os.Getuid() != 0 && byPID[5003].Wchan != unknownLinuxWchan {
		t.Errorf("pid 5003 unreadable wchan = %q, want %q", byPID[5003].Wchan, unknownLinuxWchan)
	}
	if _, ok := classifyProc(ProcInfo{PID: 5002, State: "S", Wchan: "", Cmdline: "sleep 10", ElapsedSec: 200}, 90); ok {
		t.Errorf("Linux proc with empty Wchan must not fall through to classifyDarwinState")
	}
}
