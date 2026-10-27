//go:build unix

package dream

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// The dream's feedback reader neither blocks on a FIFO nor drops later
// lines in silence after an overlong one (SD59b of the 2026-10-27 re-audit).
func TestFeedbackReaderIsSafe(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, harness.StateDir)
	os.MkdirAll(dir, 0o700)
	if err := syscall.Mkfifo(filepath.Join(dir, "feedback.jsonl"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { feedback(home, "/p", time.Time{}); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the feedback reader blocked on a FIFO")
	}
	os.Remove(filepath.Join(dir, "feedback.jsonl"))
	good := `{"ts":"2026-10-01T10:00:00Z","project":"/p","original":"kept"}` + "\n"
	long := `{"ts":"2026-10-01T11:00:00Z","project":"/p","original":"` + strings.Repeat("x", 4*1024*1024) + `"}` + "\n"
	os.WriteFile(filepath.Join(dir, "feedback.jsonl"), []byte(good+long+good), 0o600)
	got, note := feedback(home, "/p", time.Time{})
	if len(got) != 1 || !strings.Contains(note, "line 2") {
		t.Errorf("entries %d, note %q; want 1 entry and a note naming line 2", len(got), note)
	}
}

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
