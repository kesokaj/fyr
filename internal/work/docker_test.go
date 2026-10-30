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
	res, err := runWith(o, r.kickoff("fix-a", "src/a.txt, pkg/"))
	if err != nil {
		t.Fatal(err)
	}
	wantReasons(t, res)
	if res.Verdict != "PASS" {
		t.Fatalf("result %+v\n%s", res, r.out.String())
	}
	calls := r.read("docker.calls")
	stamp := fmt.Sprintf("2026-10-29-180000-%d", os.Getpid())
	for _, want := range []string{
		"image inspect -f {{.Id}} {{.Architecture}} fyr-work:local",
		"run --rm --pull never --label fyr.work.check=" + fmt.Sprint(os.Getpid()) + " --user " + fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()) + " --cap-drop ALL --security-opt no-new-privileges --network none --read-only --pids-limit 64 " + imgID + " fyr version",
		"--label fyr.work=" + stamp + " --label fyr.worker=fix-a",
		"ps -a --filter label=fyr.work=" + stamp + " --format {{.ID}}",
		"rm -f left1",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("no %q in the docker calls:\n%s", want, calls)
		}
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
	_, err := Kill(r.home, stale)
	if err == nil || !strings.Contains(err.Error(), "is stale: no run holds it; its containers (label fyr.work="+stale+"): 2 removed") {
		t.Fatalf("err %v", err)
	}
	if calls := r.read("docker.calls"); !strings.Contains(calls, "rm -f gone1") || !strings.Contains(calls, "rm -f gone2") {
		t.Fatalf("calls %q", calls)
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

// The sweep tries every container whatever fails, and returns the error.
func TestSweepContinues(t *testing.T) {
	r, _ := dockerRig(t)
	r.on("docker-ps-run", "c1\nc2\n")
	r.on("docker-rm-fail-c1", "")
	n, err := sweepContainers("s1")
	if n != 1 || err == nil || !strings.Contains(err.Error(), "removal of c1 failed") {
		t.Fatalf("n %d err %v", n, err)
	}
	if !strings.Contains(r.read("docker.calls"), "rm -f c2") {
		t.Fatal("c2 was not tried")
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
