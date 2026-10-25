//go:build unix

package harness

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestStatusAndDiffNeverOpenAFIFO: a planned path that is a symlink to a
// FIFO, or a FIFO itself, is classed without being opened; reading it first
// blocked status forever (IN27).
func TestStatusAndDiffNeverOpenAFIFO(t *testing.T) {
	repo := newRepo(t)
	files, err := Plan(DirSource(repo), nil)
	if err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join(AgyRoot, "skills", "alpha", "SKILL.md")
	for _, linked := range []bool{true, false} {
		home := t.TempDir()
		if err := Install(DirSource(repo), home, files, false, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(home, rel)
		if err := os.Remove(dest); err != nil {
			t.Fatal(err)
		}
		fifo := dest
		if linked {
			fifo = filepath.Join(t.TempDir(), "fifo")
		}
		if err := syscall.Mkfifo(fifo, 0o644); err != nil {
			t.Skipf("no FIFO here: %v", err)
		}
		if linked {
			if err := os.Symlink(fifo, dest); err != nil {
				t.Skipf("no symlink here: %v", err)
			}
		}
		done := make(chan Report, 1)
		go func() {
			r, err := Status(home, files)
			if err != nil {
				t.Errorf("linked=%v: Status: %v", linked, err)
			}
			if err := Diff(home, files, &bytes.Buffer{}); err != nil {
				t.Errorf("linked=%v: Diff: %v", linked, err)
			}
			done <- r
		}()
		select {
		case r := <-done:
			want := "differs"
			if linked {
				want = "foreign"
			}
			for _, e := range r.Entries {
				if e.Rel == rel && e.State != want {
					t.Errorf("linked=%v: %s is %s, want %s", linked, rel, e.State, want)
				}
			}
		case <-time.After(10 * time.Second):
			// Unblock the reader so the test binary can finish.
			if f, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
				f.Close()
			}
			t.Fatalf("linked=%v: Status or Diff blocked on a FIFO", linked)
		}
	}
}
