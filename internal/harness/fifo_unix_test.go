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

func TestNonRegularFilesAreRefusedWithoutBlocking(t *testing.T) {
	runNoHang := func(name string, fn func() error) {
		t.Helper()
		ch := make(chan error, 1)
		go func() { ch <- fn() }()
		select {
		case err := <-ch:
			if err == nil {
				t.Errorf("%s: succeeded on FIFO, want refusal", name)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: blocked on FIFO", name)
		}
	}

	t.Run("Plan skill FIFO", func(t *testing.T) {
		repo := newRepo(t)
		fifo := filepath.Join(repo, "skills", "alpha", "pipe")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Skip("mkfifo:", err)
		}
		runNoHang("Plan(skill)", func() error {
			_, err := Plan(DirSource(repo), nil)
			return err
		})
	})

	for _, tc := range []struct {
		name string
		rel  string
	}{
		{"RenderContext FIFO", filepath.Join("context", "extra.md")},
		{"loadDocs rule FIFO", filepath.Join("rules", "r.md")},
		{"loadDocs command FIFO", filepath.Join("commands", "c.md")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t)
			fifo := filepath.Join(repo, tc.rel)
			if err := os.MkdirAll(filepath.Dir(fifo), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(fifo, 0o600); err != nil {
				t.Skip("mkfifo:", err)
			}
			runNoHang(tc.name, func() error {
				_, err := Plan(DirSource(repo), nil)
				return err
			})
		})
	}

	t.Run("Adopt FIFO", func(t *testing.T) {
		home := t.TempDir()
		repo := newRepo(t)
		src := filepath.Join(t.TempDir(), "ext-skill")
		writeFile(t, filepath.Join(src, "SKILL.md"), "---\nname: ext-skill\ndescription: ext\n---\nbody\n")
		if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o600); err != nil {
			t.Skip("mkfifo:", err)
		}
		runNoHang("Adopt", func() error {
			_, err := Adopt(repo, home, src, false)
			return err
		})
	})

	t.Run("readCapped FIFO", func(t *testing.T) {
		fifo := filepath.Join(t.TempDir(), "in")
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Skip("mkfifo:", err)
		}
		runNoHang("readCapped", func() error {
			_, err := readCapped(fifo, 64)
			return err
		})
	})
}
