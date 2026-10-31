package work

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// holderEnv makes the test binary a live run's stand-in: it takes a pid
// file under the work root the variable names, as a run does, with
// holderCount workers running, prints its stamp and waits for SIGTERM. It
// is never the go test process itself (D33).
const (
	holderEnv   = "FYR_WORK_TEST_HOLDER"
	holderCount = "FYR_WORK_TEST_HOLDER_COUNT"
	holderTERM  = 3 // the stand-in's exit status on SIGTERM
)

func holdRun(work string) int {
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	stamp := fmt.Sprintf("2026-10-29-170000-%d", os.Getpid())
	r, err := openRun(work, stamp, MaxMaxWorkers)
	if err != nil {
		fmt.Println("error", err)
		return 1
	}
	r.running, _ = strconv.Atoi(os.Getenv(holderCount))
	if err := r.write(); err != nil {
		fmt.Println("error", err)
		return 1
	}
	fmt.Println(stamp)
	select {
	case <-term:
		return holderTERM
	case <-time.After(2 * time.Minute):
		return 4
	}
}

// holder starts the stand-in under home's work root with n workers and
// returns its stamp; the test's end kills it.
func holder(t *testing.T, home string, n int) string {
	t.Helper()
	cmd, stamp := startHolder(t, home, n)
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return stamp
}

func startHolder(t *testing.T, home string, n int) (*exec.Cmd, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), holderEnv+"="+sandbox.Root(home, "work"), holderCount+"="+strconv.Itoa(n))
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || !stampRe.MatchString(strings.TrimSpace(line)) {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("the stand-in: %q %v", line, err)
	}
	return cmd, strings.TrimSpace(line)
}

// TestKill: --kill sends SIGTERM to the run whose live pid file the stamp
// names, a stand-in process holding the lock.
func TestKill(t *testing.T) {
	home := outsideTemp(t)
	cmd, stamp := startHolder(t, home, 1)
	defer cmd.Process.Kill()
	pid, err := Kill(home, stamp)
	if err != nil {
		t.Fatal(err)
	}
	if pid != cmd.Process.Pid {
		t.Errorf("signalled %d, want %d", pid, cmd.Process.Pid)
	}
	err = cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != holderTERM {
		t.Errorf("the stand-in ended %v, want exit %d on SIGTERM", err, holderTERM)
	}
}

// TestKillRefusals: a stale pid file (no lock holder), a missing run, a
// string that is not a stamp, and a pid that is not the stamp's are each
// refused, and nothing is signalled. The stale file's sweep asks the
// recording fake docker, never the machine's daemon (the v5.0.0 gate's
// security review).
func TestKillRefusals(t *testing.T) {
	r, _ := dockerRig(t)
	home := outsideTemp(t)
	work := sandbox.Root(home, "work")
	// The stale file names this process, so a Kill that skipped the lock
	// check would end the test rather than a stranger.
	stale := fmt.Sprintf("2026-10-29-170000-%d", os.Getpid())
	put(t, filepath.Join(work, stale, pidName), fmt.Sprintf("%d 1\n", os.Getpid()))
	for _, c := range []struct{ stamp, want string }{
		{stale, "is stale: no run holds it"},
		{"2026-10-29-170000-999", "no run 2026-10-29-170000-999"},
		{"../../etc", "not a stamp"},
	} {
		if _, err := Kill(home, c.stamp); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Kill(%q): %v, want %q", c.stamp, err, c.want)
		}
	}
	if calls := r.read("docker.calls"); !strings.Contains(calls, "ps -a --filter label=fyr.work="+stale) {
		t.Errorf("the stale sweep did not reach the fake docker: %q", calls)
	}
	// A live file whose pid is not its stamp's is refused, not signalled.
	cmd, stamp := startHolder(t, home, 1)
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	other := "2026-10-29-170001-" + strconv.Itoa(cmd.Process.Pid+1)
	if err := os.Rename(filepath.Join(work, stamp), filepath.Join(work, other)); err != nil {
		t.Fatal(err)
	}
	if _, err := Kill(home, other); err == nil || !strings.Contains(err.Error(), "is not the stamp's") {
		t.Errorf("a pid not the stamp's: %v", err)
	}
	if cmd.ProcessState != nil {
		t.Error("the stand-in ended")
	}
}

// TestPidBounds: a pid over maxPid is refused, in the line and in the
// stamp, since kill(2) would wrap it, at worst to -1 (the Phase 4 security
// review, finding 1).
func TestPidBounds(t *testing.T) {
	dir := outsideTemp(t)
	for _, pid := range []string{"4194305", "4294967295"} {
		p := filepath.Join(dir, pid)
		put(t, p, pid+" 1\n")
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := readPid(f, "2026-10-29-170000-"+pid); err == nil {
			t.Errorf("pid %s read", pid)
		}
		f.Close()
	}
	if _, err := Kill(dir, "2026-10-29-170000-4294967295"); err == nil || !strings.Contains(err.Error(), "not a stamp") {
		t.Errorf("Kill on a ten-digit pid: %v", err)
	}
}

// TestLiveRunsFailClosed: a pid file that cannot be opened (a link) counts
// as the whole limit, so the machine reads as full (the Phase 4 security
// review, finding 2).
func TestLiveRunsFailClosed(t *testing.T) {
	work := filepath.Join(outsideTemp(t), "work")
	mkdir(t, filepath.Join(work, "2026-10-29-170000-4242"))
	if err := os.Symlink("/dev/null", filepath.Join(work, "2026-10-29-170000-4242", pidName)); err != nil {
		t.Fatal(err)
	}
	if err := atLimit(work, MaxMaxWorkers); err == nil || !strings.Contains(err.Error(), "unreadable: "+filepath.Join(work, "2026-10-29-170000-4242", pidName)) {
		t.Errorf("an unopenable pid file: %v", err)
	}
}

// TestRunSlotsLockBusy: another process holding slots.lock never blocks a
// run: no slot is free while it does, and the run's end still reaches the
// waiting worker (the Phase 4 security review, finding 3). The holder is
// this process on its own open file, which flock treats as another.
func TestRunSlotsLockBusy(t *testing.T) {
	r := newRig(t)
	fastPoll(t)
	work := sandbox.Root(r.home, "work")
	mkdir(t, work)
	f, err := os.OpenFile(filepath.Join(work, slotsLock), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	got := make(chan []Result, 1)
	go func() {
		res, _ := Run(ctx, r.opts(), r.kickoff("one", "src/"))
		got <- res
	}()
	select {
	case res := <-got:
		if len(res) != 1 || strings.Join(res[0].Reasons, ";") != ReasonKilled || res[0].Head != "" {
			t.Errorf("results %+v", res)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the run hung on slots.lock")
	}
}

// TestHeldErrorFailsClosed: a lock test that errors (ENOLCK, as on some
// network file systems) counts the run as the whole limit, names the file,
// and --kill refuses it rather than calling it stale.
func TestHeldErrorFailsClosed(t *testing.T) {
	home := outsideTemp(t)
	work := sandbox.Root(home, "work")
	stamp := fmt.Sprintf("2026-10-29-170000-%d", os.Getpid())
	put(t, filepath.Join(work, stamp, pidName), fmt.Sprintf("%d 1\n", os.Getpid()))
	old := flock
	flock = func(int, int) error { return syscall.ENOLCK }
	t.Cleanup(func() { flock = old })
	if err := atLimit(work, MaxMaxWorkers); err == nil || !strings.Contains(err.Error(), "unreadable: "+filepath.Join(work, stamp, pidName)) {
		t.Errorf("atLimit: %v", err)
	}
	if _, err := Kill(home, stamp); err == nil || strings.Contains(err.Error(), "stale") {
		t.Errorf("Kill: %v", err)
	}
}

// TestLiveRunsGlobChars: a home whose path holds glob characters still
// counts its live runs (it is read, not matched).
func TestLiveRunsGlobChars(t *testing.T) {
	home := filepath.Join(outsideTemp(t), "h[1]*")
	stamp := holder(t, home, 2)
	if err := atLimit(sandbox.Root(home, "work"), 2); err == nil || !strings.Contains(err.Error(), stamp) {
		t.Errorf("atLimit: %v", err)
	}
}

// TestRunRootBeforeLimit: a --home inside the checkout is refused by the
// root check before anything under it is read, the limit's count included.
func TestRunRootBeforeLimit(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	o.Home = filepath.Join(r.lead, ".agents", "scratch", "home")
	holder(t, o.Home, MaxMaxWorkers)
	_, err := Run(context.Background(), o, r.kickoff("one", "src/"))
	if err == nil || !strings.Contains(err.Error(), "inside a git checkout") {
		t.Errorf("err %v, want the root refusal", err)
	}
}
