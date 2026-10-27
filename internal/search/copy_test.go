//go:build unix

package search

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A conversation database that is a FIFO or a symlink is refused at once,
// never read or waited on (SD48 of the 2026-10-27 audit).
func TestCopyFileRefusesNonRegular(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo.db")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "real.db")
	os.WriteFile(target, []byte("x"), 0o600)
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, from := range []string{fifo, link} {
		done := make(chan error, 1)
		go func() { done <- copyFileImpl(from, filepath.Join(t.TempDir(), "copy")) }()
		select {
		case err := <-done:
			if !errors.Is(err, ErrNotRegular) {
				t.Errorf("%s: err %v, want ErrNotRegular", filepath.Base(from), err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the copy blocked", filepath.Base(from))
		}
	}
	if err := copyFileImpl(filepath.Join(dir, "absent.db"), filepath.Join(t.TempDir(), "copy")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file: %v, want ErrNotExist", err)
	}
	if err := copyFileImpl(target, filepath.Join(t.TempDir(), "copy")); err != nil {
		t.Errorf("a regular file: %v", err)
	}
}

// One conversation whose database is a FIFO is skipped; the others are read
// and the project's dream does not fail (plan 2026-10-27-harness-audit-fix D8).
func TestAgyConversationTextSkipsAFIFO(t *testing.T) {
	home := t.TempDir()
	type conv = struct {
		workspace string
		modified  string
		payloads  [][]byte
	}
	agyFixture(t, home, map[string]conv{
		"good": {"/p/one", "2026-09-30 16:14:07.567153506+00:00", [][]byte{blob("a prompt that stays readable here")}},
		"bad":  {"/p/one", "2026-09-30 16:00:00.000000000+00:00", [][]byte{blob("this one becomes a pipe")}},
	})
	db := filepath.Join(home, ".gemini", "antigravity-cli", "conversations", "bad.db")
	os.Remove(db)
	if err := syscall.Mkfifo(db, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var got []AgyConversation
	var err error
	go func() {
		got, err = AgyConversationText(home, "/p/one", time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("AgyConversationText blocked on a FIFO")
	}
	if err != nil {
		t.Fatalf("one FIFO failed the whole read: %v", err)
	}
	texts := map[string]string{}
	for _, c := range got {
		texts[c.ID] = c.Text
	}
	if texts["good"] == "" || texts["bad"] != "" {
		t.Errorf("texts %v, want good read and bad empty", texts)
	}
}
