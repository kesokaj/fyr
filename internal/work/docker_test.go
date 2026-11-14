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
// the version check print what the controls say, and docker version the
// daemon's architecture: docker-server-arch, else the image's. A build
// gives the image the platform buildx picks: --platform, else
// DOCKER_DEFAULT_PLATFORM, else the daemon's architecture. Either run
// refuses, as the daemon does, a platform asked for (--platform, else
// DOCKER_DEFAULT_PLATFORM) that is not the image's. A session
// `run` writes the cid file, points the sandbox's fyr wrapper at the
// installed fyr (the image's own, here) and runs the rig's fake agy with
// exactly the container's environment in the clone. ps prints the worker's
// list or the run's by the format asked for.
const fakeDocker = `#!/bin/bash
CTL=@CTL@
printf '%s\n' "$*" >> "$CTL/docker.calls"
on() { [ -e "$CTL/docker-$1" ]; }
plat_ok() {
  local want=$DOCKER_DEFAULT_PLATFORM have=linux/$(cat "$CTL/docker-arch") prev= a
  for a in "$@"; do [ "$prev" = --platform ] && want=$a; prev=$a; done
  [ -z "$want" ] || [ "$want" = "$have" ] && return 0
  echo "Error response from daemon: image was found but does not match the specified platform: wanted $want, actual: $have" >&2; return 1
}
case "$1 $2" in
"image inspect")
  on noimage && { echo "Error: No such image" >&2; exit 1; }
  echo "$(cat "$CTL/docker-id") $(cat "$CTL/docker-arch")" ;;
"buildx build")
  on build-fail && { echo "build failed" >&2; exit 1; }
  rm -f "$CTL/docker-noimage"
  plat=${DOCKER_DEFAULT_PLATFORM:-linux/$(cat "$CTL/docker-server-arch" 2>/dev/null || cat "$CTL/docker-arch")}; prev=
  for a in "$@"; do
    case "$a" in FYR_VERSION=*) echo "${a#FYR_VERSION=}" > "$CTL/docker-version" ;; esac
    [ "$prev" = --platform ] && plat=$a; prev=$a
  done
  echo "${plat#linux/}" > "$CTL/docker-arch"
  echo "$*" > "$CTL/docker-build-args" ;;
"context inspect")
  cat "$CTL/docker-endpoint" ;;
"info --format")
  if [ -e "$CTL/docker-info" ]; then cat "$CTL/docker-info"; else echo "true true true true 4 17179869184"; fi ;;
"version --format")
  on server-fail && { echo "Cannot connect to the Docker daemon" >&2; exit 1; }
  if [ -e "$CTL/docker-server-arch" ]; then cat "$CTL/docker-server-arch"; else cat "$CTL/docker-arch"; fi ;;
"run --rm")
  on version-hang && { echo "timed out" >&2; exit 1; }
  on no-rosetta && [[ "$*" == *"--mount"* ]] && { echo "rosetta exec format error" >&2; exit 126; }
  plat_ok "$@" || exit 1
  if [[ "$*" == *"--label fyr.work.basecheck="* ]]; then
    [[ "$*" == *"antigravity-oauth-token"* ]] && { echo "token mounted in base-check" >&2; exit 99; }
    shift 2; envs=(); wd=; net=
    while [ $# -gt 0 ]; do
      case "$1" in
        -e) envs+=("$2"); shift 2 ;;
        -w) wd=$2; shift 2 ;;
        --network) net=$2; shift 2 ;;
        --read-only|--init) shift ;;
        -*) shift 2 ;;
        *) break ;;
      esac
    done
    [ "$net" = none ] || { echo "base-check net=$net, want none" >&2; exit 99; }
    shift
    cd "$wd" && exec env -i "${envs[@]}" "$@"
  fi
  echo "fyr $(cat "$CTL/docker-version")" ;;
run\ *)
  plat_ok "$@" || exit 1
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
	t.Setenv("DOCKER_DEFAULT_PLATFORM", "")
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
	if want := "work: stamp " + stamp + "  1 workers  " + filepath.Join(SandboxRoot(r.home, r.lead), stamp) + "/  runner docker"; lines[0] != want {
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

// TestRunDockerBaseCheck pins that Base-Check: runs inside an isolated
// --network none container (without OAuth token or host secrets) when
// --runner docker is active.
func TestRunDockerBaseCheck(t *testing.T) {
	linuxOnly(t)
	t.Setenv("SUPER_SECRET_HOST_TOKEN", "hunter2-do-not-leak")
	r, o := dockerRig(t)
	r.on("change", "")
	k := filepath.Join(r.kick, "bc-docker.md")
	put(t, k, "Project: "+r.lead+". Do the phase.\nFiles: src/a.txt\nBase-Check: grep -q work src/a.txt && [ -z \"$SUPER_SECRET_HOST_TOKEN\" ]\nStanding approvals: commit.\n")
	res, err := runWith(o, k)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "PASS" {
		t.Fatalf("Verdict=%s Reasons=%v Notes=%v\n%s", res.Verdict, res.Reasons, res.Notes, r.out.String())
	}
	stamp := fmt.Sprintf("2026-10-29-180000-%d", os.Getpid())
	bcCalls := 0
	for _, line := range strings.Split(r.read("docker.calls"), "\n") {
		if !strings.Contains(line, "--label fyr.work.basecheck="+stamp+"-bc-docker") {
			continue
		}
		bcCalls++
		if !strings.Contains(line, "--network none") {
			t.Errorf("base-check call missing --network none:\n%s", line)
		}
		if strings.Contains(line, "antigravity-oauth-token") {
			t.Errorf("base-check call unexpectedly mounted OAuth token:\n%s", line)
		}
		if strings.Contains(line, "hunter2-do-not-leak") {
			t.Errorf("base-check call leaked host secret in args:\n%s", line)
		}
	}
	if bcCalls != 2 {
		t.Errorf("expected 2 containerized Base-Check runs (pre and post), got %d:\n%s", bcCalls, r.read("docker.calls"))
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

// The machine table covers both image architectures, and linuxAgy/checkRosetta
// reject an unmountable path (such as a path containing a comma) before any
// docker --mount call.
func TestELFMachines(t *testing.T) {
	if elfMachine["amd64"] != elf.EM_X86_64 || elfMachine["arm64"] != elf.EM_AARCH64 || len(elfMachine) != 2 {
		t.Fatalf("%v", elfMachine)
	}
	commaDir := filepath.Join(t.TempDir(), "bad,dir")
	if err := os.MkdirAll(commaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	commaAgy := filepath.Join(commaDir, "agy-linux")
	if err := os.WriteFile(commaAgy, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := linuxAgy(commaAgy, "", "arm64", "sha256:1111"); err == nil || !strings.Contains(err.Error(), "cannot hold ','") {
		t.Errorf("linuxAgy(comma path) = %v, want mount path refusal", err)
	}
	if err := checkRosetta("sha256:1111", commaAgy); err == nil || !strings.Contains(err.Error(), "cannot hold ','") {
		t.Errorf("checkRosetta(comma path) = %v, want mount path refusal", err)
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
