//go:build unix

package harness

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The manifest is read as every deployed file is (openPlain): a FIFO at
// state.json is refused at once instead of blocking install, and a symlink
// is not followed (IN43 of the 2026-10-27 audit).
func TestLoadStateReadsPlainOnly(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			p := statePath(home)
			os.MkdirAll(filepath.Dir(p), 0o700)
			switch kind {
			case "fifo":
				if err := syscall.Mkfifo(p, 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "elsewhere.json")
				os.WriteFile(target, []byte(`{"files":[]}`), 0o600)
				if err := os.Symlink(target, p); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() { _, _, err := LoadState(home); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					t.Errorf("a %s state.json was read", kind)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("LoadState blocked on a %s", kind)
			}
		})
	}
}
