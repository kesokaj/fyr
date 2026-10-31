package work

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// TestRunBoundedKillsTheGroup is PLAN.md item 45, finding S3: a fetch that
// never ends is killed at its deadline with what it started, since a plain
// kill leaves the upload-pack grandchild holding the output pipe.
func TestRunBoundedKillsTheGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	cmd := exec.Command("bash", "-c", `sleep 300 & echo $! > "$0"; wait`, pidFile)
	start := time.Now()
	_, err := runBounded(cmd, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err %v", err)
	}
	if d := time.Since(start); d > 3*time.Second { // under WaitDelay: the group died, nothing held the pipe
		t.Errorf("took %s", d)
	}
	b, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatal(rerr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	for i := 0; i < 50 && syscall.Kill(pid, 0) == nil; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Error("the grandchild outlived the deadline")
	}
}

func TestRunBoundedOutput(t *testing.T) {
	out, err := runBounded(exec.Command("bash", "-c", "echo out; echo err >&2"), time.Minute)
	if err != nil || !strings.Contains(string(out), "out") || !strings.Contains(string(out), "err") {
		t.Errorf("%q, %v", out, err)
	}
	if _, err := runBounded(exec.Command("false"), time.Minute); err == nil {
		t.Error("a failure passed")
	}
}

// TestDockerNeedsLazyFetchSafe is finding S2: below git's GIT_NO_LAZY_FETCH
// the host's upload-pack would run a promisor the container configured, so
// a docker run is refused, and a host run gains nothing and is not.
func TestDockerNeedsLazyFetchSafe(t *testing.T) {
	old := lazyFetchSafe
	t.Cleanup(func() { lazyFetchSafe = old })
	lazyFetchSafe = func() bool { return false }
	if err := gitFloor(RunnerDocker); err == nil || !strings.Contains(err.Error(), "GIT_NO_LAZY_FETCH") {
		t.Errorf("docker: %v", err)
	}
	if err := gitFloor(RunnerHost); err != nil {
		t.Errorf("host: %v", err)
	}
	if err := gitFloor(""); err != nil {
		t.Errorf("the default runner: %v", err)
	}
	lazyFetchSafe = func() bool { return true }
	if err := gitFloor(RunnerDocker); err != nil {
		t.Errorf("a safe git: %v", err)
	}
}

// TestSafetyReasonsHoldIntegrity: work's list is the shared one plus its
// own (PLAN.md item 51).
func TestSafetyReasonsHoldIntegrity(t *testing.T) {
	for _, r := range sandbox.IntegrityReasons {
		if !slices.Contains(safetyReasons, r) {
			t.Errorf("%s is not a safety reason", r)
		}
	}
	for _, r := range []string{sandbox.ReasonCredentials, sandbox.ReasonRealConfig, ReasonLeadChanged} {
		if !slices.Contains(safetyReasons, r) {
			t.Errorf("%s is not a safety reason", r)
		}
	}
}

// TestRunNoFallbackToTheTopLevel is the checkpoint's security finding S1:
// with .git no valid repository, upload-pack's lookup serves another one
// GitIntact did not judge as a repository: the clone's top level when the
// fetch names the clone, a .git/.git without --strict when it names .git.
func TestRunNoFallbackToTheTopLevel(t *testing.T) {
	for _, ctl := range []string{"toplevel-repo", "nested-repo"} {
		t.Run(ctl, func(t *testing.T) {
			r := newRig(t)
			r.on(ctl, "")
			res := r.run(r.kickoff("top", "src/a.txt"))
			wantReasons(t, res, ReasonNoCommits)
			if res.Head != "" || r.git("-C", r.lead, "branch", "--list", "work/*") != "" {
				t.Errorf("a repository beside the clone's .git was fetched: %+v", res)
			}
		})
	}
}

// TestCappedOutput: a fetch's output is kept to a bound.
func TestCappedOutput(t *testing.T) {
	out, err := runBounded(exec.Command("bash", "-c", "head -c 1000000 /dev/zero"), time.Minute)
	if err != nil || len(out) != maxFetchOutput {
		t.Errorf("%d bytes, %v", len(out), err)
	}
}
