//go:build unix

package dream

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// copyPrivate refuses a FIFO at once, checked on the opened file, never
// blocking on it (SD60b of the 2026-10-27 re-audit).
func TestCopyPrivateRefusesAFIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "token")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- copyPrivate(fifo, filepath.Join(dir, "copy")) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a FIFO was copied")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copyPrivate blocked on a FIFO")
	}
}
