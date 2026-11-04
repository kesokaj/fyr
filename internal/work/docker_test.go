package work

import (
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// fakeDocker stands in for the docker CLI in a work run. image inspect and
// the version check print what the controls say. A session `run` writes the
// cid file, points the sandbox's fyr wrapper at the installed fyr (the
// image's own, here) and runs the rig's fake agy with exactly the
// container's environment in the clone. ps prints the worker's list or the
// run's by the format asked for.
const fakeDocker = `#!/bin/bash
CTL=@CTL@
printf '%s\n' "$*" >> "$CTL/docker.calls"
on() { [ -e "$CTL/docker-$1" ]; }
case "$1 $2" in
"image inspect")
  on noimage && { echo "Error: No such image" >&2; exit 1; }
  echo "$(cat "$CTL/docker-id") $(cat "$CTL/docker-arch")" ;;
"context inspect")
  cat "$CTL/docker-endpoint" ;;
"info --format")
  if [ -e "$CTL/docker-info" ]; then cat "$CTL/docker-info"; else echo "true true true true"; fi ;;
"run --rm")
  on version-hang && { echo "timed out" >&2; exit 1; }
  echo "fyr $(cat "$CTL/docker-version")" ;;
run\ *)
  shift; envs=(); cid=; wd=
  while [ $# -gt 0 ]; do
    case "$1" in
      -e) envs+=("$2"); shift 2 ;;
      --cidfile) cid=$2; shift 2 ;;
      -w) wd=$2; shift 2 ;;
      --read-only|--init) shift ;;
      -*) shift 2 ;;
      *) break ;;
    esac
  done
  echo "$1" > "$CTL/docker-image-ran"; shift 2
  echo cid1 > "$cid"
  for kv in "${envs[@]}"; do case "$kv" in HOME=*) sb=${kv#HOME=} ;; esac; done
  sed -i "s|'/usr/local/bin/fyr'|'$sb/.local/bin/fyr'|" "$sb/bin/fyr"
  cd "$wd" && exec env -i "${envs[@]}" @AGY@ "$@" ;;
"rm -f")
  on "rm-fail-$3" && { echo "removal of $3 failed" >&2; exit 1; } ;;
"network create")
  on network-fail && { echo "Error response from daemon: could not find an available address pool" >&2; exit 1; }
  echo net1 ;;
"network ls")
  cat "$CTL/docker-net-run" 2>/dev/null ;;
"network rm")
  on "net-rm-fail-$3" && { echo "removal of network $3 failed" >&2; exit 1; } ;;
"ps -aq")
  cat "$CTL/docker-ps-check" 2>/dev/null ;;
"ps -a")
  case "$*" in
    *"{{.ID}} {{.State}}"*) cat "$CTL/docker-ps-worker" 2>/dev/null ;;
    *) cat "$CTL/docker-ps-run" 2>/dev/null ;;
  esac ;;
esac
exit 0
`

// dockerRig is a rig with the fake docker on PATH and no agy on it, an
// image at version "vtest" of this machine's architecture, and the test
// binary itself as the linux agy: an ELF of that architecture on linux.
func dockerRig(t *testing.T) (*rig, Options) {
	r := newRig(t)
	// The host's agy is off PATH: a docker-mode run must not need one.
	agy := filepath.Join(r.tmp, "agy-off-path", "agy")
	mkdir(t, filepath.Dir(agy))
	if err := os.Rename(filepath.Join(r.bin, "agy"), agy); err != nil {
		t.Fatal(err)
	}
	var path []string
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(d, "agy")); err != nil {
			path = append(path, d)
		}
	}
	t.Setenv("PATH", strings.Join(path, string(os.PathListSeparator)))
	// The operator's own daemon settings never reach the fake's run.
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	script := strings.NewReplacer("@CTL@", sandbox.ShellQuote(r.ctl), "@AGY@", sandbox.ShellQuote(agy)).Replace(fakeDocker)
	put(t, filepath.Join(r.bin, "docker"), script)
	os.Chmod(filepath.Join(r.bin, "docker"), 0o755)
	r.on("docker-arch", runtime.GOARCH)
	r.on("docker-version", "vtest")
	r.on("docker-id", imgID)
	r.on("docker-endpoint", "unix:///var/run/docker.sock\n")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	o := r.opts()
	o.Runner, o.Version, o.AgyLinux = RunnerDocker, "vtest", self
	return r, o
}

// imgID is the fake image's ID.
const imgID = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// linuxOnly skips where the test binary is no ELF to stand in for agy.
func linuxOnly(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the test binary is not an ELF here")
	}
}

// A docker-mode run PASSes as a host run does. It checks the image by tag,
// then runs by ID, labels the container with the stamp, and sweeps the
// stamp's containers at the end, printing a line only when it removed one.
func TestRunDockerPass(t *testing.T) {
	linuxOnly(t)
	r, o := dockerRig(t)
	r.on("change", "")
	r.on("docker-ps-run", "left1\n")
	r.on("docker-net-run", "net1\n")
	res, err := runWith(o, r.kickoff("fix-a", "src/a.txt, pkg/"))
	if err != nil {
		t.Fatal(err)
	}
	wantReasons(t, res)
	if res.Verdict != "PASS" {
		t.Fatalf("result %+v\n%s", res, r.out.String())
	}
	stamp := fmt.Sprintf("2026-10-29-180000-%d", os.Getpid())
	lines := strings.Split(r.out.String(), "\n")
	if want := "work: stamp " + stamp + "  1 workers  " + filepath.Join(sandbox.Root(r.home, "work"), stamp) + "/  runner docker"; lines[0] != want {
		t.Errorf("stamp line %q, want %q", lines[0], want)
	}
	if !strings.HasSuffix(res.Block()[0], "  runner docker") {
		t.Errorf("result line %q does not end with runner docker", res.Block()[0])
	}
	calls := r.read("docker.calls")
	for _, want := range []string{
		"image inspect -f {{.Id}} {{.Architecture}} fyr-work:local",
		"run --rm --pull never --label fyr.work.check=" + fmt.Sprint(os.Getpid()) + " --user " + fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()) + " --cap-drop ALL --security-opt no-new-privileges --network none --read-only --pids-limit 64 " + imgID + " fyr version",
		"network create --driver bridge --label fyr.work=" + stamp + " --opt com.docker.network.bridge.enable_icc=false fyr-work-" + stamp,
		"--label fyr.work=" + stamp + " --label fyr.worker=fix-a",
		"--memory 8g --memory-swap 8g --cpus 2 --pids-limit 1024 --network fyr-work-" + stamp + " ",
		"ps -a --filter label=fyr.work=" + stamp + " --format {{.ID}}",
		"rm -f left1",
		"network ls -q --filter label=fyr.work=" + stamp,
		"network rm net1",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("no %q in the docker calls:\n%s", want, calls)
		}
	}
	create, session := strings.Index(calls, "network create"), strings.Index(calls, "--name fyr-work-")
	rmC, rmN := strings.Index(calls, "rm -f left1"), strings.Index(calls, "network rm net1")
	if create < 0 || session < create || rmN < rmC {
		t.Errorf("the network is not made before the session and removed after its containers:\n%s", calls)
	}
	if got := strings.TrimSpace(r.read("docker-image-ran")); got != imgID {
		t.Errorf("the session ran %q, not the checked image's ID", got)
	}
	if !strings.Contains(r.out.String(), "work: the run's container sweep (label fyr.work="+stamp+"): 1 removed") {
		t.Errorf("no sweep line:\n%s", r.out.String())
	}
}

// Each docker-mode refusal is an error before anything is made: no sandbox
// root, no output.
func TestRunDockerRefusals(t *testing.T) {
	notELF := func(t *testing.T, r *rig, o *Options) { o.AgyLinux = filepath.Join(r.tmp, "agy-off-path", "agy") }
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, r *rig, o *Options)
		want  string
	}{
		{"runner", func(t *testing.T, r *rig, o *Options) { o.Runner = "podman" }, `--runner "podman"`},
		{"docker-host", func(t *testing.T, r *rig, o *Options) { t.Setenv("DOCKER_HOST", "tcp://10.0.0.1:2375") }, "needs the local daemon"},
		{"remote-context", func(t *testing.T, r *rig, o *Options) { r.on("docker-endpoint", "tcp://10.0.0.1:2376\n") }, "the docker context's endpoint tcp://"},
		{"odd-id", func(t *testing.T, r *rig, o *Options) { r.on("docker-id", "--privileged") }, "docker image inspect printed"},
		{"no-docker", func(t *testing.T, r *rig, o *Options) {
			// git alone: never the machine's own docker.
			git, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			only := filepath.Join(r.tmp, "git-only")
			mkdir(t, only)
			os.Symlink(git, filepath.Join(only, "git"))
			t.Setenv("PATH", only)
		}, "docker not found"},
		{"image-flag", func(t *testing.T, r *rig, o *Options) { o.Image = "--privileged" }, "not a flag"},
		{"devel", func(t *testing.T, r *rig, o *Options) { o.Version = "(devel)" }, "development build"},
		{"unknown", func(t *testing.T, r *rig, o *Options) { o.Version = "unknown" }, "development build"},
		{"no-image", func(t *testing.T, r *rig, o *Options) { r.on("docker-noimage", "") }, "build it: docker buildx build"},
		{"other-version", func(t *testing.T, r *rig, o *Options) { r.on("docker-version", "vother") }, `holds fyr "vother"`},
		{"not-elf", notELF, "not an ELF binary"},
		{"wrong-arch", func(t *testing.T, r *rig, o *Options) { r.on("docker-arch", otherArch()) }, "built for"},
		{"odd-arch", func(t *testing.T, r *rig, o *Options) { r.on("docker-arch", "riscv64") }, "not one --runner docker knows"},
		{"mac-no-agy", func(t *testing.T, r *rig, o *Options) { o.AgyLinux = ""; setHostOS(t, "darwin") }, "--runner docker on darwin: give --agy-linux PATH"},
		{"not-executable", func(t *testing.T, r *rig, o *Options) {
			p := filepath.Join(r.tmp, "agy-copy")
			b, _ := os.ReadFile(o.AgyLinux)
			os.WriteFile(p, b, 0o644)
			o.AgyLinux = p
		}, "not an executable file"},
		{"token-link", func(t *testing.T, r *rig, o *Options) {
			tok := filepath.Join(r.real, sandbox.TokenRel)
			os.Rename(tok, tok+".real")
			os.Symlink(tok+".real", tok)
		}, "no Antigravity login"},
		{"image-space", func(t *testing.T, r *rig, o *Options) { o.Image = "fyr work:local" }, "not a flag"},
		{"image-control", func(t *testing.T, r *rig, o *Options) { o.Image = "fyr-work:\x01" }, "not a flag"},
		{"comma-agy", func(t *testing.T, r *rig, o *Options) {
			p := filepath.Join(r.tmp, "a,b", "agy")
			mkdir(t, filepath.Dir(p))
			b, err := os.ReadFile(o.AgyLinux)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, b, 0o755); err != nil {
				t.Fatal(err)
			}
			o.AgyLinux = p
		}, "cannot hold"},
		{"comma-token", func(t *testing.T, r *rig, o *Options) {
			real := filepath.Join(r.tmp, "real,x")
			put(t, filepath.Join(real, sandbox.TokenRel), "token")
			t.Setenv("HOME", real)
			o.RealHome = real
		}, "cannot hold"},
		{"comma-home", func(t *testing.T, r *rig, o *Options) { o.Home = filepath.Join(r.tmp, "home,x") }, "cannot hold"},
		// Refusal 24: a daemon that would drop a limit with a warning only.
		{"no-swap-limit", func(t *testing.T, r *rig, o *Options) { r.on("docker-info", "true false true true\n") },
			"the daemon cannot apply the worker's limits (swap; docker info printed \"true false true true\")"},
		{"no-limits", func(t *testing.T, r *rig, o *Options) { r.on("docker-info", "\n") }, "(memory, swap, pids, cpu quota;"},
		// No run network, no run: never the default bridge (PLAN.md item 13).
		{"network-fail", func(t *testing.T, r *rig, o *Options) { r.on("docker-network-fail", "") },
			"--runner docker: the run's network fyr-work-"},
		// Refusal 22: a container has no /etc/localtime of the lead's to read.
		{"no-zone", func(t *testing.T, r *rig, o *Options) { unsetTZ(t); pointLink(t, "file", "") },
			"--runner docker: the worker needs a zone name, and TZ is unset and /etc/localtime names none; set TZ=<Area/City>"},
	} {
		t.Run(c.name, func(t *testing.T) {
			linuxOnly(t)
			r, o := dockerRig(t)
			c.setup(t, r, &o)
			_, err := runWith(o, r.kickoff("a", "src/a.txt"))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
			if _, err := os.Lstat(sandbox.Root(o.Home, "work")); !os.IsNotExist(err) {
				t.Errorf("a refusal made the sandbox root: %v", err)
			}
			if r.out.Len() != 0 {
				t.Errorf("a refusal printed %q", r.out.String())
			}
			if strings.Contains(r.read("docker.calls"), "--name fyr-work-") {
				t.Errorf("a refusal started a session:\n%s", r.read("docker.calls"))
			}
			// The development-build refusal and refusal 22 come before any
			// docker call.
			if c.name == "devel" || c.name == "unknown" || c.name == "no-zone" {
				if calls := r.read("docker.calls"); calls != "" {
					t.Errorf("docker was called: %q", calls)
				}
			}
		})
	}
}

// otherArch is an image architecture the test binary is not built for.
func otherArch() string {
	if runtime.GOARCH == "arm64" {
		return "amd64"
	}
	return "arm64"
}

func setHostOS(t *testing.T, os string) {
	old := hostOS
	hostOS = os
	t.Cleanup(func() { hostOS = old })
}

// The machine table covers both image architectures.
func TestELFMachines(t *testing.T) {
	if elfMachine["amd64"] != elf.EM_X86_64 || elfMachine["arm64"] != elf.EM_AARCH64 || len(elfMachine) != 2 {
		t.Fatalf("%v", elfMachine)
	}
}

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

// deadPid is the pid of a process that has exited and been reaped.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// fakeELF writes a minimal 64-bit ELF executable for mach so docker preflight
// and Check can be tested on any OS.
func fakeELF(t *testing.T, mach elf.Machine) string {
	t.Helper()
	var b [64]byte
	copy(b[:4], "\x7fELF")
	b[4], b[5], b[6] = byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)
	b[16] = byte(elf.ET_EXEC)
	b[18], b[19] = byte(mach), byte(mach>>8)
	b[20] = byte(elf.EV_CURRENT)
	b[52] = 64
	p := filepath.Join(t.TempDir(), "agy-linux")
	if err := os.WriteFile(p, b[:], 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRunnerLines pins the stamp line and result line for a host run with an
// explicit fallback reason, a default host run, and a docker result.
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
	wantStamp := "work: stamp " + stamp + "  1 workers  " + filepath.Join(sandbox.Root(r.home, "work"), stamp) + "/  runner host (no Antigravity login at /h/tok)"
	if lines[0] != wantStamp {
		t.Errorf("stamp line:\n%s\nwant:\n%s", lines[0], wantStamp)
	}
	if !strings.HasSuffix(lines[1], "  runner host (no Antigravity login at /h/tok)") || lines[1] != res.Block()[0] {
		t.Errorf("result line:\n%s\nblock[0]:\n%s", lines[1], res.Block()[0])
	}
}

// TestCheck pins Check and RunnerLabel across explicit host, passing docker
// preflight, and failing docker preflight, verifying nothing is made on disk
// or in docker.
func TestCheck(t *testing.T) {
	r, o := dockerRig(t)
	o.AgyLinux = fakeELF(t, elfMachine[runtime.GOARCH])

	host := o
	host.Runner = RunnerHost
	got, why := Check(host)
	if label := RunnerLabel(got, why); got != RunnerHost || label != "runner host (--runner host)" {
		t.Errorf("explicit host: %q %q -> %q", got, why, label)
	}
	if calls := r.read("docker.calls"); calls != "" {
		t.Errorf("explicit host called docker: %q", calls)
	}

	got, why = Check(o)
	if label := RunnerLabel(got, why); got != RunnerDocker || why != "" || label != "runner docker" {
		t.Errorf("passing docker: %q %q -> %q", got, why, label)
	}
	if calls := r.read("docker.calls"); strings.Contains(calls, "network create") || strings.Contains(calls, "--name fyr-work-") {
		t.Errorf("Check created a network or session:\n%s", calls)
	}

	r.on("docker-noimage", "")
	got, why = Check(o)
	want := "runner host (--image fyr-work:local: not on this machine (exit status 1: Error: No such image); build it: " + BuildLine + ")"
	if label := RunnerLabel(got, why); got != RunnerHost || label != want {
		t.Errorf("missing image:\ngot:  %q\nwant: %q", label, want)
	}
	if _, err := os.Lstat(sandbox.Root(o.Home, "work")); !os.IsNotExist(err) {
		t.Errorf("Check made the sandbox root: %v", err)
	}
}
