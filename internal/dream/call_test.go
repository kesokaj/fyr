package dream

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A grandchild of the CLI that kept the output pipes (a language or MCP
// server it started) held cmd.Run past the deadline with dream.lock held, so
// every later run said "already running" and exited 0 (project
// review 2026-10-09, row 2, BUG-169). The CLI runs in its own process group,
// which the deadline kills whole; a grandchild that left the group is cut
// loose after the grace period.
func TestCallReturnsAtTheDeadlineDespiteAGrandchild(t *testing.T) {
	for name, script := range map[string]string{
		"in the group":   "#!/bin/sh\nsleep 60 &\nsleep 60\n",
		"left the group": "#!/bin/sh\nsetsid sleep 60 &\necho $! > \"$PIDFILE\"\nsleep 60\n",
	} {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(script, "setsid") {
				if _, err := exec.LookPath("setsid"); err != nil {
					t.Skip("no setsid on PATH")
				}
			}
			dir := t.TempDir()
			cli := filepath.Join(dir, "holder")
			if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			pidfile := filepath.Join(dir, "pid")
			t.Cleanup(func() {
				if b, err := os.ReadFile(pidfile); err == nil {
					if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
						syscall.Kill(pid, syscall.SIGKILL)
					}
				}
			})
			done := make(chan error, 1)
			go func() {
				_, _, err := callWithin([]string{cli}, "", dir, append(os.Environ(), "PIDFILE="+pidfile), time.Second, 2*time.Second)
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "timed out") {
					t.Fatalf("want a timeout error, got %v", err)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("call did not return: a grandchild holding the pipes keeps Run blocked past the deadline")
			}
		})
	}
}

// A helper that outlives a CLI which exited 0 kept the pipes open: Run
// returned exec.ErrWaitDelay after the grace, so a finished run was reported
// as failed and its answer thrown away, and the helper was never killed
// (review of the BUG-169 fix, round 1). The answer is kept, and the group is
// killed once the CLI is done.
func TestCallKeepsTheAnswerWhenAHelperOutlivesTheCLI(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "holder")
	pidfile := filepath.Join(dir, "pid")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\nsleep 60 &\necho $! > \"$PIDFILE\"\necho answer\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killPidFile(pidfile) })
	t0 := time.Now()
	out, _, err := callWithin([]string{cli}, "", dir, append(os.Environ(), "PIDFILE="+pidfile), 30*time.Second, 2*time.Second)
	if err != nil || out != "answer\n" {
		t.Fatalf("call = %q, %v; want the answer and no error after %s", out, err, time.Since(t0))
	}
	pid := readPid(t, pidfile)
	for i := 0; i < 30 && alive(pid); i++ {
		time.Sleep(100 * time.Millisecond) // a killed process is reaped a moment later
	}
	if alive(pid) {
		t.Errorf("the helper (pid %d) outlived the CLI's exit: the group was not killed", pid)
	}
}

// TestHelperCall is not a test: TestCallKillsTheGroupOnInterrupt runs this
// binary as a subprocess with DREAM_HELPER_DIR set, and interrupts it.
func TestHelperCall(t *testing.T) {
	dir := os.Getenv("DREAM_HELPER_DIR")
	if dir == "" {
		t.Skip("helper process")
	}
	callWithin([]string{filepath.Join(dir, "holder")}, "", dir, append(os.Environ(), "PIDFILE="+filepath.Join(dir, "pid")), time.Minute, time.Second)
}

// Ctrl-C on a hand-run `fyr dream` reached the CLI while it shared
// fyr's process group; in its own group it ran on as an orphan with
// the operator's credentials (review of the BUG-169 fix, round 1). An
// interrupt or a termination kills the group, then fyr dies as before.
func TestCallKillsTheGroupOnInterrupt(t *testing.T) {
	for name, sig := range map[string]os.Signal{"interrupt": os.Interrupt, "hangup": syscall.SIGHUP, "terminate": syscall.SIGTERM} {
		t.Run(name, func(t *testing.T) { callKilledBy(t, sig) })
	}
}

func callKilledBy(t *testing.T, sig os.Signal) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "pid")
	if err := os.WriteFile(filepath.Join(dir, "holder"), []byte("#!/bin/sh\nsleep 60 &\necho $! > \"$PIDFILE\"\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { killPidFile(pidfile) })
	c := exec.Command(os.Args[0], "-test.run=^TestHelperCall$")
	c.Env = append(os.Environ(), "DREAM_HELPER_DIR="+dir)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	var pid int
	for i := 0; i < 50 && pid == 0; i++ {
		if b, err := os.ReadFile(pidfile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pid == 0 {
		c.Process.Kill()
		t.Fatal("the holder never started")
	}
	if err := c.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	c.Wait() // dies by the signal, as before the group existed
	for i := 0; i < 30 && alive(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if alive(pid) {
		t.Errorf("the helper (pid %d) survived %v: the group was not killed", pid, sig)
	}
}

// `nohup fyr dream --all &` ignores the hangup, and a background job
// of a non-interactive shell the interrupt. Notify took the ignored signal
// over, killed the group on it, and the re-raise after Reset found it
// ignored again: the project in flight failed and fyr ran on (the gate
// security review of v2.6.2..66f3412, finding 1). An ignored signal now
// leaves the CLI alone.
func TestCallLeavesTheGroupOnAnIgnoredSignal(t *testing.T) {
	for name, s := range map[string]struct {
		trap string
		sig  os.Signal
	}{"hangup": {"HUP", syscall.SIGHUP}, "interrupt": {"INT", os.Interrupt}} {
		t.Run(name, func(t *testing.T) { callIgnoring(t, s.trap, s.sig) })
	}
}

func callIgnoring(t *testing.T, trap string, sig os.Signal) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "pid")
	if err := os.WriteFile(filepath.Join(dir, "holder"), []byte("#!/bin/sh\nsleep 60 &\necho $! > \"$PIDFILE\"\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The shell's trap sets the ignore and exec keeps it, as nohup does.
	c := exec.Command("sh", "-c", "trap '' "+trap+"; exec \"$0\" -test.run='^TestHelperCall$'", os.Args[0])
	c.Env = append(os.Environ(), "DREAM_HELPER_DIR="+dir)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { c.Wait(); close(exited) }()
	var pid int
	t.Cleanup(func() {
		c.Process.Kill()
		<-exited
		if pid != 0 {
			if pgid, err := syscall.Getpgid(pid); err == nil {
				syscall.Kill(-pgid, syscall.SIGKILL) // the holder's group outlives the helper's kill
			}
		}
	})
	for i := 0; i < 50 && pid == 0; i++ {
		if b, err := os.ReadFile(pidfile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the holder never started")
	}
	if err := c.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20 && alive(pid); i++ {
		time.Sleep(100 * time.Millisecond) // a group killed by the signal is gone well inside this
	}
	if !alive(pid) {
		t.Errorf("the ignored %v killed the CLI's group (helper pid %d)", sig, pid)
	}
	select {
	case <-exited:
		t.Errorf("fyr exited on the ignored %v", sig)
	default:
	}
}

func readPid(t *testing.T, pidfile string) int {
	b, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func killPidFile(pidfile string) {
	if b, err := os.ReadFile(pidfile); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

// A signal that reached the channel just before the CLI ended was dropped
// half the time: select picks at random between two ready cases, and on the
// end case `dream --all` went on to the next project (the release gate's
// review of v2.6.2..13c5bde). Run often enough that a coin flip cannot pass.
func TestRelayKeepsASignalQueuedAtTheEnd(t *testing.T) {
	for i := 0; i < 200; i++ {
		sigs := make(chan os.Signal, 1)
		sigs <- syscall.SIGTERM
		waited := make(chan struct{})
		close(waited)
		got := false
		relay(sigs, waited, func(os.Signal) { got = true })
		if !got {
			t.Fatalf("run %d: a signal queued when the CLI ended was dropped", i)
		}
	}
	waited := make(chan struct{})
	close(waited)
	relay(make(chan os.Signal, 1), waited, func(os.Signal) { t.Error("no signal came, yet one was handled") })
}
