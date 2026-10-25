//go:build unix

package check

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO where a checked file was is refused at once by the open, not
// waited on (security review 2026-10-25, finding 5): lineCount, which
// opened with a blocking open after checkPath's Lstat, and openRegular,
// which every read now goes through after its Lstat.
func TestReadsRefuseAFIFO(t *testing.T) {
	dir := repo(t, map[string]string{"README.md": "x\n"})
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	k, err := open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer k.root.Close()
	done := make(chan string, 2)
	go func() {
		if _, ok := k.lineCount("pipe"); ok {
			done <- "lineCount counted a FIFO"
			return
		}
		done <- ""
	}()
	go func() {
		if f, err := k.openRegular("pipe"); err == nil {
			f.Close()
			done <- "openRegular opened a FIFO"
			return
		}
		done <- ""
	}()
	for range 2 {
		select {
		case msg := <-done:
			if msg != "" {
				t.Error(msg)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a read of a FIFO blocked waiting for a writer")
		}
	}
}
