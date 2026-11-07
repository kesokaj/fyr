package work

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// --kill of a stamp whose run is gone removes the containers labelled with
// it, and says so in its refusal.
func TestKillStaleSweeps(t *testing.T) {
	r, _ := dockerRig(t)
	stale := fmt.Sprintf("2026-10-29-170000-%d", os.Getpid())
	put(t, filepath.Join(sandbox.Root(r.home, "work"), stale, pidName), fmt.Sprintf("%d 1\n", os.Getpid()))
	r.on("docker-ps-run", "gone1\ngone2\n")
	r.on("docker-net-run", "net1\n")
	_, err := Kill(r.home, stale)
	if err == nil || !strings.Contains(err.Error(), "is stale: no run holds it; its containers and network (label fyr.work="+stale+"): 2 containers and 1 network removed") {
		t.Fatalf("err %v", err)
	}
	if calls := r.read("docker.calls"); !strings.Contains(calls, "rm -f gone1") || !strings.Contains(calls, "rm -f gone2") || !strings.Contains(calls, "network rm net1") {
		t.Fatalf("calls %q", calls)
	}
	// A run killed between its network and its pid file left no pid file:
	// its network is removed, never a container, since the stamp may be a
	// live run of another home; and nothing while the stamp's pid lives.
	r.on("docker-ps-run", "other1\n")
	before := r.read("docker.calls")
	early := fmt.Sprintf("2026-10-29-160000-%d", deadPid(t))
	if _, err := Kill(r.home, early); err == nil || !strings.Contains(err.Error(), "no run "+early+": ") || !strings.Contains(err.Error(), "its network (label fyr.work="+early+"): 1 removed") {
		t.Fatalf("err %v", err)
	}
	if calls := strings.TrimPrefix(r.read("docker.calls"), before); strings.Contains(calls, "rm -f") || strings.Contains(calls, "ps -a") {
		t.Fatalf("a stamp with no pid file touched containers: %q", calls)
	}
	before = r.read("docker.calls")
	live := fmt.Sprintf("2026-10-29-160000-%d", os.Getpid())
	if _, err := Kill(r.home, live); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("its process %d is alive, so nothing was swept", os.Getpid())) {
		t.Fatalf("err %v", err)
	}
	if calls := strings.TrimPrefix(r.read("docker.calls"), before); calls != "" {
		t.Fatalf("a live stamp asked docker %q", calls)
	}
	r.on("docker-ps-run", "")
	// A killed run whose containers are gone still leaves its network.
	r.on("docker-ps-run", "")
	if _, err := Kill(r.home, stale); err == nil || !strings.Contains(err.Error(), "0 containers and 1 network removed") {
		t.Fatalf("err %v", err)
	}
}

// A version check that fails is refused, and the check's container, found
// by its label, is removed rather than left running.
func TestVersionCheckCleansUp(t *testing.T) {
	linuxOnly(t)
	r, o := dockerRig(t)
	r.on("docker-version-hang", "")
	r.on("docker-ps-check", "chk1\n")
	if _, err := runWith(o, r.kickoff("a", "src/a.txt")); err == nil || !strings.Contains(err.Error(), "rebuild it") {
		t.Fatalf("err %v", err)
	}
	calls := r.read("docker.calls")
	if !strings.Contains(calls, "ps -aq --filter label=fyr.work.check="+fmt.Sprint(os.Getpid())) || !strings.Contains(calls, "rm -f chk1") {
		t.Fatalf("calls %q", calls)
	}
}

// A host run with docker on PATH asks docker nothing and prints no sweep
// line; a docker run whose sweep finds nothing prints none either.
func TestRunSweepQuiet(t *testing.T) {
	linuxOnly(t)
	r, o := dockerRig(t)
	host := o
	host.Runner, host.AgyLinux = RunnerHost, ""
	// The host runner needs the host's agy back on PATH.
	if err := os.Rename(filepath.Join(r.tmp, "agy-off-path", "agy"), filepath.Join(r.bin, "agy")); err != nil {
		t.Fatal(err)
	}
	if res, err := runWith(host, r.kickoff("host-a", "src/a.txt")); err != nil || res.Verdict != "PASS" {
		t.Fatalf("host: %v %+v", err, res)
	}
	if calls := r.read("docker.calls"); calls != "" || strings.Contains(r.out.String(), "container sweep") {
		t.Fatalf("a host run asked docker %q or printed a sweep:\n%s", calls, r.out.String())
	}
	r2, o2 := dockerRig(t)
	if res, err := runWith(o2, r2.kickoff("fix-a", "src/a.txt")); err != nil || res.Verdict != "PASS" {
		t.Fatalf("docker: %v %+v\n%s", err, res, r2.out.String())
	}
	if strings.Contains(r2.out.String(), "container sweep") {
		t.Fatalf("a sweep that removed nothing printed:\n%s", r2.out.String())
	}
}

// The sweep tries every container and network whatever fails, and returns
// the error.
func TestSweepContinues(t *testing.T) {
	r, _ := dockerRig(t)
	r.on("docker-ps-run", "c1\nc2\n")
	r.on("docker-rm-fail-c1", "")
	r.on("docker-net-run", "n1\nn2\n")
	r.on("docker-net-rm-fail-n1", "")
	n, nets, err := sweepStamp("s1")
	if n != 1 || nets != 1 || err == nil || !strings.Contains(err.Error(), "removal of c1 failed") || !strings.Contains(err.Error(), "removal of network n1 failed") {
		t.Fatalf("n %d nets %d err %v", n, nets, err)
	}
	if calls := r.read("docker.calls"); !strings.Contains(calls, "rm -f c2") || !strings.Contains(calls, "network rm n2") {
		t.Fatalf("c2 or n2 was not tried:\n%s", calls)
	}
}

// A run's network that cannot be removed is named on the sweep line, so it
// is never left unseen.
func TestRunNetworkLeftIsNamed(t *testing.T) {
	linuxOnly(t)
	r, o := dockerRig(t)
	r.on("docker-net-run", "net1\n")
	r.on("docker-net-rm-fail-net1", "")
	if res, err := runWith(o, r.kickoff("fix-a", "src/a.txt")); err != nil || res.Verdict != "PASS" {
		t.Fatalf("%v %+v\n%s", err, res, r.out.String())
	}
	if !strings.Contains(r.out.String(), "removal of network net1 failed") {
		t.Fatalf("no sweep line names the network:\n%s", r.out.String())
	}
}

// --kill of a stale stamp never asks a daemon that is not this machine's to
// remove anything, and says it did not sweep.
func TestKillStaleRemoteDaemon(t *testing.T) {
	r, _ := dockerRig(t)
	r.on("docker-endpoint", "tcp://10.0.0.1:2376\n")
	r.on("docker-ps-run", "gone1\n")
	stale := fmt.Sprintf("2026-10-29-170000-%d", os.Getpid())
	put(t, filepath.Join(sandbox.Root(r.home, "work"), stale, pidName), fmt.Sprintf("%d 1\n", os.Getpid()))
	_, err := Kill(r.home, stale)
	if err == nil || !strings.Contains(err.Error(), "is stale: no run holds it; its containers were not swept") {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(r.read("docker.calls"), "rm -f") || strings.Contains(r.read("docker.calls"), "ps -a") {
		t.Fatalf("calls %q", r.read("docker.calls"))
	}
}

// BuildLine is the Dockerfile's own build line, so the refusals print the
// line that builds the image the verb checks.
func TestBuildLinePinned(t *testing.T) {
	b, err := os.ReadFile("../../docker/work.Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\n# Build: "+BuildLine+"\n") {
		t.Fatalf("docker/work.Dockerfile has no line # Build: %s", BuildLine)
	}
}

// A run whose pid file cannot be made is refused, and the network it made
// first is swept before the refusal.
func TestRunPidFileFailSweepsNetwork(t *testing.T) {
	linuxOnly(t)
	r, o := dockerRig(t)
	// A work root the run cannot write: the limit's count reads it empty,
	// and the stamp directory's Mkdir fails.
	work := sandbox.Root(o.Home, "work")
	mkdir(t, work)
	if err := os.Chmod(work, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(work, 0o700) })
	r.on("docker-net-run", "net1\n")
	if _, err := runWith(o, r.kickoff("fix-a", "src/a.txt")); err == nil || !strings.Contains(err.Error(), "the run's pid file") {
		t.Fatalf("err %v", err)
	}
	calls := r.read("docker.calls")
	if !strings.Contains(calls, "network create") || !strings.Contains(calls, "network rm net1") || strings.Contains(calls, "--name fyr-work-") {
		t.Fatalf("calls:\n%s", calls)
	}
}

// TestRunnerLines pins the stamp line and the result line of a host run
// with a fallback reason: both end in it, on one line, without the
// "--runner docker: " prefix.
func TestRunnerLines(t *testing.T) {
	r := newRig(t)
	o := r.opts()
	o.Runner, o.RunnerReason = RunnerHost, "--runner docker: no Antigravity login\nat /h/tok"
	res, err := first(Run(t.Context(), o, r.kickoff("fix-a", "src/a.txt")))
	if err != nil || res.Verdict != "PASS" {
		t.Fatalf("err=%v res=%+v\n%s", err, res, r.out.String())
	}
	stamp := fmt.Sprintf("2026-10-29-180000-%d", os.Getpid())
	lines := strings.Split(r.out.String(), "\n")
	wantStamp := "work: stamp " + stamp + "  1 workers  " + filepath.Join(SandboxRoot(r.home, r.lead), stamp) + "/  runner host (no Antigravity login at /h/tok)"
	if lines[0] != wantStamp {
		t.Errorf("stamp line:\n%s\nwant:\n%s", lines[0], wantStamp)
	}
	if !strings.HasSuffix(lines[1], "  runner host (no Antigravity login at /h/tok)") || lines[1] != res.Block()[0] {
		t.Errorf("result line:\n%s\nblock[0]:\n%s", lines[1], res.Block()[0])
	}
}

// TestCheck pins Check and RunnerLabel (ADR-504): an explicit host never
// calls docker; auto and an explicit docker are docker when the preflight
// passes, and make nothing on disk or in docker; when it fails, auto is the
// host with the refusal as its reason, and an explicit docker is the
// refusal itself, as its run would print. The rig has no Repo, so its
// missing image is one no run can build (TestCheckPredictsBuild has those).
func TestCheck(t *testing.T) {
	r, o := dockerRig(t)
	o.AgyLinux = fakeELF(t, elfMachine[runtime.GOARCH])
	auto := o
	auto.Runner = RunnerAuto

	host := o
	host.Runner = RunnerHost
	got, why, err := Check(host)
	if label := RunnerLabel(got, why); err != nil || got != RunnerHost || label != "runner host (--runner host)" {
		t.Errorf("explicit host: %q %q %v -> %q", got, why, err, label)
	}
	if calls := r.read("docker.calls"); calls != "" {
		t.Errorf("explicit host called docker: %q", calls)
	}

	for _, c := range []Options{auto, o} {
		got, why, err = Check(c)
		if label := RunnerLabel(got, why); err != nil || got != RunnerDocker || why != "" || label != "runner docker" {
			t.Errorf("--runner %s, passing docker: %q %q %v -> %q", c.Runner, got, why, err, label)
		}
	}
	if calls := r.read("docker.calls"); strings.Contains(calls, "network create") || strings.Contains(calls, "--name fyr-work-") {
		t.Errorf("Check created a network or session:\n%s", calls)
	}

	r.on("docker-noimage", "")
	refusal := "--image fyr-work:local: not on this machine (exit status 1: Error: No such image); build it: " + BuildLine
	got, why, err = Check(auto)
	if label := RunnerLabel(got, why); err != nil || got != RunnerHost || label != "runner host ("+refusal+")" {
		t.Errorf("auto, missing image:\ngot:  %q %v\nwant: %q", label, err, "runner host ("+refusal+")")
	}
	got, why, err = Check(o)
	if err == nil || strings.Join(strings.Fields(err.Error()), " ") != refusal || got != "" || why != "" {
		t.Errorf("explicit docker, missing image: %q %q %v, want the refusal %q", got, why, err, refusal)
	}
	if _, err := os.Lstat(sandbox.Root(o.Home, "work")); !os.IsNotExist(err) {
		t.Errorf("Check made the sandbox root: %v", err)
	}
}
