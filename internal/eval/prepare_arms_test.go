package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The arms of prepare and agy that their splits move into helpers, pinned
// before the splits (PLAN-2026-10-27-funclen Phase 8, D13). prepare is
// called on a row built as Run builds one, with the obstacle placed first;
// no hook is added (D12). Exempt: the clone that is not its own toplevel
// (git just made it), EvalSymlinks and OpenRoot of the directories prepare
// just made, the settings read back from the file it just wrote, and agy's
// last -1: with *os.File outputs Wait fails only with an exit error.

// prepRow is a row of the rig in a fresh directory under its eval home, not
// yet prepared.
func (r *rig) prepRow(name string) *row {
	r.t.Helper()
	dir := filepath.Join(r.home, "eval", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		r.t.Fatal(err)
	}
	o := r.opts()
	o.Fixture = r.root
	w := &row{o: o, r: named(name, "say ok", "true"), dir: dir, app: filepath.Join(dir, "app"), sb: filepath.Join(dir, "home"), out: &r.out, stamp: "2026-10-26-0800"}
	r.t.Cleanup(func() {
		if w.root != nil {
			w.root.Close()
		}
	})
	return w
}

func wantPrepare(t *testing.T, w *row, reason string) {
	t.Helper()
	if w.prepare(context.Background()) || strings.Join(w.reasons, "; ") != reason {
		t.Errorf("reasons %v, want [%s]", w.reasons, reason)
	}
}

// A sandbox that is the real home, and one inside a git checkout, fail the
// clone step.
func TestPrepareRefusesItsPlace(t *testing.T) {
	r := newRig(t)
	w := r.prepRow("inhome")
	w.o.RealHome = w.app
	wantPrepare(t, w, ReasonClone)
	if !strings.Contains(r.out.String(), "is the real home; refusing") {
		t.Errorf("no real-home note:\n%s", r.out.String())
	}

	r = newRig(t)
	w = r.prepRow("inrepo")
	gitInit(t, w.dir)
	w.dir = filepath.Join(w.dir, "inner")
	w.app, w.sb = filepath.Join(w.dir, "app"), filepath.Join(w.dir, "home")
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	wantPrepare(t, w, ReasonClone)
	if !strings.Contains(r.out.String(), "(BUG-106)") {
		t.Errorf("no BUG-106 note:\n%s", r.out.String())
	}
}

// Each step's own write, refused by what already sits at its path, fails
// that step.
func TestPrepareStepWritesRefused(t *testing.T) {
	for _, c := range []struct {
		name, reason string
		place        func(t *testing.T, w *row)
	}{
		{"sandbox dir", ReasonSetup, func(t *testing.T, w *row) { put(t, filepath.Join(w.sb, ".gemini"), "a file", 0o644) }},
		{"install log", ReasonInstall, func(t *testing.T, w *row) {
			if err := os.MkdirAll(filepath.Join(w.dir, "install.log"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrapper dir", ReasonInstall, func(t *testing.T, w *row) { put(t, filepath.Join(w.sb, "bin"), "a file", 0o644) }},
		{"settings", ReasonSettings, func(t *testing.T, w *row) {
			if err := os.MkdirAll(filepath.Join(w.sb, ".gemini", "antigravity-cli", "settings.json"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			w := r.prepRow("p")
			c.place(t, w)
			wantPrepare(t, w, c.reason)
		})
	}
}

// The records baseline that cannot be written fails the records step.
func TestPrepareRecordsBaselineRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	r := newRig(t)
	w := r.prepRow("p")
	if err := os.MkdirAll(filepath.Join(w.sb, ".gemini", "antigravity-cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(w.sb, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(w.sb, 0o755) })
	wantPrepare(t, w, ReasonRecords)
}

// An error file that is not a regular file means the sandbox was replaced;
// an agy that ignores SIGTERM is killed stopGrace after the run ends.
func TestAgySessionArms(t *testing.T) {
	r := newRig(t)
	w := r.prepRow("p")
	if !w.prepare(context.Background()) {
		t.Fatalf("prepare: %v", w.reasons)
	}
	errDir := filepath.Join(w.dir, "err.txt")
	if err := os.MkdirAll(errDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if rc := w.agy(context.Background(), filepath.Join(r.bin, "agy"), "p", filepath.Join(w.dir, "out.txt"), errDir, false); rc != -1 || !w.has(ReasonReplaced) {
		t.Errorf("rc %d, reasons %v", rc, w.reasons)
	}

	old := stopGrace
	stopGrace = 100 * time.Millisecond
	t.Cleanup(func() { stopGrace = old })
	started := filepath.Join(r.rec, "deaf.started")
	deaf := filepath.Join(r.bin, "deaf")
	put(t, deaf, "#!/bin/bash\ntrap '' TERM\ntouch "+shellQuote(started)+"\nexec sleep 300\n", 0o755)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for i := 0; i < 500; i++ {
			if _, err := os.Stat(started); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	done := make(chan int, 1)
	go func() {
		done <- w.agy(ctx, deaf, "p", filepath.Join(w.dir, "out2.txt"), filepath.Join(w.dir, "err2.txt"), false)
	}()
	select {
	case rc := <-done:
		if rc != -1 {
			t.Errorf("rc %d, want -1 for a killed agy", rc)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("an agy that ignores SIGTERM was never killed")
	}
}
