package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The arms of the prepare steps and of agy, pinned before their splits
// (PLAN-2026-10-27-funclen Phase 8, D13) and moved here with the steps
// (PLAN-2026-10-29-workers Phase 1). The steps run on a Box whose directory
// is made by the test, with the obstacle placed first; no hook is added
// (D12). Exempt: the clone that is not its own toplevel (git just made it),
// EvalSymlinks and OpenRoot of the directories Clone just made, the settings
// read back from the file Settings just wrote, and agy's last -1: with
// *os.File outputs Wait fails only with an exit error.

// unmade is a Box of the rig whose directory exists, made by the test
// rather than by Make, so an obstacle can sit inside it first.
func (r *rig) unmade(c Config) *Box {
	r.t.Helper()
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		r.t.Fatal(err)
	}
	return r.box(c)
}

func wantPrepare(t *testing.T, b *Box, reason string) {
	t.Helper()
	if prepare(context.Background(), b) || strings.Join(b.Reasons(), "; ") != reason {
		t.Errorf("reasons %v, want [%s]", b.Reasons(), reason)
	}
}

// A sandbox that is the real home, and one inside a git checkout, fail the
// clone step.
func TestPrepareRefusesItsPlace(t *testing.T) {
	r := newRig(t)
	c := r.config("inhome")
	c.RealHome = filepath.Join(c.Dir, "app")
	wantPrepare(t, r.unmade(c), ReasonClone)
	if !strings.Contains(r.out.String(), "is the real home; refusing") {
		t.Errorf("no real-home note:\n%s", r.out.String())
	}

	r = newRig(t)
	c = r.config("inrepo")
	gitInit(t, c.Dir)
	c.Dir = filepath.Join(c.Dir, "inner")
	wantPrepare(t, r.unmade(c), ReasonClone)
	if !strings.Contains(r.out.String(), "(BUG-106)") {
		t.Errorf("no BUG-106 note:\n%s", r.out.String())
	}
}

// Each step's own write, refused by what already sits at its path, fails
// that step.
func TestPrepareStepWritesRefused(t *testing.T) {
	for _, c := range []struct {
		name, reason string
		place        func(t *testing.T, b *Box)
	}{
		{"sandbox dir", ReasonSetup, func(t *testing.T, b *Box) { put(t, filepath.Join(b.Home(), ".gemini"), "a file", 0o644) }},
		{"install log", ReasonInstall, func(t *testing.T, b *Box) {
			if err := os.MkdirAll(filepath.Join(b.Dir(), "install.log"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrapper dir", ReasonInstall, func(t *testing.T, b *Box) { put(t, filepath.Join(b.Home(), "bin"), "a file", 0o644) }},
		{"settings", ReasonSettings, func(t *testing.T, b *Box) {
			if err := os.MkdirAll(filepath.Join(b.Home(), ".gemini", "antigravity-cli", "settings.json"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			b := r.unmade(r.config("p"))
			c.place(t, b)
			wantPrepare(t, b, c.reason)
		})
	}
}

// An error file that is not a regular file means the sandbox was replaced;
// an agy that ignores SIGTERM is killed StopGrace after the run ends.
func TestAgySessionArms(t *testing.T) {
	r := newRig(t)
	b := r.prepared(r.config("p"))
	errDir := filepath.Join(b.Dir(), "err.txt")
	if err := os.MkdirAll(errDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if rc := b.agy(context.Background(), r.agy, "p", filepath.Join(b.Dir(), "out.txt"), errDir, false); rc != -1 || !b.Has(ReasonReplaced) {
		t.Errorf("rc %d, reasons %v", rc, b.Reasons())
	}

	started := filepath.Join(r.rec, "deaf.started")
	deaf := filepath.Join(r.bin, "deaf")
	put(t, deaf, "#!/bin/bash\ntrap '' TERM\ntouch "+ShellQuote(started)+"\nexec sleep 300\n", 0o755)
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
		done <- b.agy(ctx, deaf, "p", filepath.Join(b.Dir(), "out2.txt"), filepath.Join(b.Dir(), "err2.txt"), false)
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

// Who a session's end reaches: on the run's end the whole group (SIGTERM,
// then SIGKILL after StopGrace), at the timeout agy alone, so what it started
// stays for check e.
func TestAgyProcessGroup(t *testing.T) {
	for _, c := range []struct {
		name, trap string
		timeout    bool
		childLives bool
	}{
		{"cancel, agy dies of SIGTERM", "", false, false},
		{"cancel, agy and child ignore SIGTERM", "trap '' TERM\n", false, false},
		{"timeout", "", true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			cfg := r.config("p")
			if c.timeout {
				cfg.Timeout = 300 * time.Millisecond
			}
			b := r.prepared(cfg)
			pidFile, started := filepath.Join(r.rec, "child.pid"), filepath.Join(r.rec, "group.started")
			script := filepath.Join(r.bin, "grouped")
			put(t, script, "#!/bin/bash\n"+c.trap+"sleep 300 &\necho $! > "+ShellQuote(pidFile)+"\ntouch "+ShellQuote(started)+"\nwait\n", 0o755)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if !c.timeout {
				go func() {
					for i := 0; i < 500; i++ {
						if _, err := os.Stat(started); err == nil {
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
					cancel()
				}()
			}
			done := make(chan int, 1)
			go func() {
				done <- b.agy(ctx, script, "p", filepath.Join(b.Dir(), "out.txt"), filepath.Join(b.Dir(), "err.txt"), false)
			}()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("the session never ended")
			}
			raw, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatal(err)
			}
			pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
			t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
			alive := true
			for i := 0; i < 100 && alive; i++ {
				alive = syscall.Kill(pid, 0) == nil
				if alive {
					time.Sleep(20 * time.Millisecond)
				}
			}
			if alive != c.childLives {
				t.Errorf("child alive %v, want %v", alive, c.childLives)
			}
			if b.TimedOut() != c.timeout {
				t.Errorf("TimedOut %v, want %v", b.TimedOut(), c.timeout)
			}
		})
	}
}
