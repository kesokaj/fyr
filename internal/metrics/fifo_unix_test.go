//go:build unix

package metrics

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func syscallMkfifo(p string) error { return syscall.Mkfifo(p, 0o600) }

// openRegular is the one way ingest opens a transcript, a trail file or a
// .meta.json: a FIFO at the path returns at once, refused (security review
// of v2.6.0, part 1, round 4: the check-then-open race, every site).
func TestOpenRegularRefusesFIFO(t *testing.T) {
	p := filepath.Join(t.TempDir(), "S1.jsonl")
	if err := syscallMkfifo(p); err != nil {
		t.Skip("no FIFOs here")
	}
	done := make(chan error)
	go func() {
		f, _, err := openRegular(p)
		if f != nil {
			f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO was opened as a regular file")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("openRegular blocked on a FIFO")
	}
}
