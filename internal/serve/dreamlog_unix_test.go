//go:build unix

package serve

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestDreamLogNotRegular(t *testing.T) {
	f := newFix(t)
	r := f.run(t)
	log := filepath.Join(f.home, harness.StateDir, "dream.log")
	target := filepath.Join(t.TempDir(), "secret")
	write(t, target, "SECRET LINE\n")
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, log); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(log); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("precondition: dream.log is not a symlink: %v", err)
	}
	body := get(t, r, "GET", "/dream").Body.String()
	if !strings.Contains(body, "dream.log is not a regular file: not read") || strings.Contains(body, "SECRET") {
		t.Errorf("a symlinked log was followed:\n%s", body)
	}

	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(log, 0o600); err != nil {
		t.Skipf("no FIFO here: %v", err)
	}
	done := make(chan string, 1)
	go func() { done <- get(t, r, "GET", "/dream").Body.String() }()
	select {
	case body := <-done:
		if !strings.Contains(body, "dream.log is not a regular file: not read") {
			t.Errorf("a FIFO log: %s", body)
		}
	case <-time.After(time.Second):
		t.Fatal("the handler blocked on a FIFO")
	}
}
