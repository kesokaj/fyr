package work

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// DefaultImage is --image's default, the tag the build line gives.
const DefaultImage = "fyr-work:local"

// BuildLine is SPEC-fyr-work § The worker image's build line, printed by the
// refusals an image can fix.
const BuildLine = `docker buildx build --load -f docker/work.Dockerfile --build-arg GO_MINOR="$(go env GOVERSION | sed -E 's/^go([0-9]+\.[0-9]+).*/\1/')" --build-arg FYR_VERSION="$(fyr version | cut -d' ' -f2)" -t fyr-work:local .`

// dockerTimeout bounds each docker call of the refusals and the sweep.
const dockerTimeout = time.Minute

// hostOS is runtime.GOOS, a variable so a test can be the Mac.
var hostOS = runtime.GOOS

// elfMachine is the ELF machine of a linux agy for each image architecture.
var elfMachine = map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}

// dockerPlan is what a docker-mode run resolved: the image by its ID, so a
// tag moved after the check is not what runs, and the linux agy.
type dockerPlan struct {
	image, agy          string
	ramGB, cpus, diskGB int
}

// DockerPreflight is the docker runner's refusals (refusals 13-20, 22, 24
// and 25, plus the login token for Mountable) outside a run: ChooseRunner's
// default probe, fyr auto's (D2, D5, D17, ADR-504). A caller outside this
// package always has o.build buildNever, so it builds no image and predicts
// no build, and a missing or stale image is refused; Run builds one
// (buildFirst) and Check predicts the build (buildPredict, ADR-504a).
func DockerPreflight(o Options) error {
	var p prepared
	return p.dockerPreflight(o)
}

// dockerPreflight is DockerPreflight keeping what it resolved: when it
// passes, p.docker is the run's plan and p.tok the login token, so Run's
// probe is the run's only docker check. A development build refuses first,
// before the Keychain and any docker call, since no image can match it
// (ADR-504); then the offline refusals 22 and 25, then the login.
func (p *prepared) dockerPreflight(o Options) error {
	if err := develBuild(o.Version); err != nil {
		return err
	}
	for _, path := range []*string{&o.Home, &o.RealHome} {
		abs, err := filepath.Abs(*path)
		if err != nil {
			return err
		}
		*path = abs
	}
	if _, err := runZone(RunnerDocker); err != nil {
		return err
	}
	if err := gitFloor(RunnerDocker); err != nil {
		return err
	}
	var err error
	if p.tok, err = sandbox.CheckLogin(o.RealHome, hostOS, true); err != nil {
		return err
	}
	return p.dockerRefusals(o)
}

// develBuild refuses the docker runner to a development build: the image is
// built at an installed fyr's version, which such a build does not have.
func develBuild(v string) error {
	if v == "" || v == "unknown" || strings.ContainsAny(v, "() ") {
		return fmt.Errorf("--runner docker: this fyr is a development build (%s); run fyr install, then build the image from the installed fyr: %s", v, BuildLine)
	}
	return nil
}

// dockerRefusals are refusals 13-20 and 24 of SPEC-fyr-work, after refusal
// 22 and the development-build refusal (develBuild), for a docker-mode run:
// docker is on PATH, the daemon is local and applies the limits, the image
// ref is one, the image exists at this fyr's version (or o.build builds or
// predicts it: resolveAndCheckImage), the linux agy runs on the image's
// architecture, and every path the container mounts can be written as a
// --mount.
func (p *prepared) dockerRefusals(o Options) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("--runner docker: docker not found on PATH")
	}
	if err := localDaemon(); err != nil {
		return err
	}
	ncpu, memGB, err := daemonLimits()
	if err != nil {
		return err
	}
	ref := o.Image
	if ref == "" {
		ref = DefaultImage
	}
	if strings.HasPrefix(ref, "-") || strings.IndexFunc(ref, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("--image %q: an image ref, not a flag", ref)
	}
	id, arch, err := resolveAndCheckImage(o, ref)
	if err != nil {
		return err
	}
	agy, err := linuxAgy(o.AgyLinux, o.RealHome, arch)
	if err != nil {
		return err
	}
	for _, path := range []string{agy, p.tok, sandbox.Root(o.Home, "work"), SandboxRoot(o.Home, o.Root)} {
		if err := sandbox.Mountable(path); err != nil {
			return fmt.Errorf("--runner docker: %v", err)
		}
	}
	if id == "" { // buildPredict: a run builds the image first (ADR-504a)
		p.toBuild = ref
		return nil
	}
	// The token is a regular file already: refusal 8 (session).
	p.docker = newDockerPlan(id, agy, o, ncpu, memGB)
	return nil
}

// localDaemon refuses a daemon that is not this machine's: the mounts name
// this machine's files, and the checks after a session ask that daemon.
// DOCKER_HOST overrides the context, so both are read.
func localDaemon() error {
	if h := os.Getenv("DOCKER_HOST"); h != "" && !strings.HasPrefix(h, "unix://") {
		return fmt.Errorf("DOCKER_HOST=%s: --runner docker needs the local daemon, whose files the mounts name", h)
	}
	out, err := docker("context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
	if err != nil {
		return fmt.Errorf("--runner docker: the docker context: %v", err)
	}
	if h := strings.TrimSpace(out); !strings.HasPrefix(h, "unix://") {
		return fmt.Errorf("the docker context's endpoint %s: --runner docker needs the local daemon, whose files the mounts name", sandbox.Printable(h))
	}
	return nil
}

// daemonLimits is refusal 24: the daemon applies every limit a worker's
// container asks for. Without a memory, swap, pids or CPU cgroup controller
// dockerd drops the flag with a warning only, into the session's err.txt,
// and the container runs unlimited (the P4 security review, finding 1).
func daemonLimits() (ncpu, memGB int, err error) {
	out, err := docker("info", "--format", "{{.MemoryLimit}} {{.SwapLimit}} {{.PidsLimit}} {{.CPUCfsQuota}} {{.NCPU}} {{.MemTotal}}")
	if err != nil {
		return 0, 0, fmt.Errorf("--runner docker: docker info: %v", err)
	}
	f := strings.Fields(out)
	var missing []string
	for i, name := range []string{"memory", "swap", "pids", "cpu quota"} {
		if i >= len(f) || f[i] != "true" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 || len(f) != 6 {
		return 0, 0, fmt.Errorf("--runner docker: the daemon cannot apply the worker's limits (%s; docker info printed %q): give it the cgroup controllers, or use --runner host", strings.Join(missing, ", "), sandbox.Printable(strings.TrimSpace(out)))
	}
	ncpu, err = strconv.Atoi(f[4])
	memBytes, merr := strconv.ParseInt(f[5], 10, 64)
	if err != nil || merr != nil || ncpu < 1 || memBytes <= 0 {
		return 0, 0, fmt.Errorf("--runner docker: the daemon cannot apply the worker's limits (docker info printed %q): give it the cgroup controllers, or use --runner host", sandbox.Printable(strings.TrimSpace(out)))
	}
	memGB = max(MinRAMGB, int((memBytes+(1<<30)-1)>>30))
	return ncpu, memGB, nil
}

// imageID is the shape of an image ID, so nothing else reaches docker run's
// image position.
var imageID = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// inspectImage is the image's ID and architecture, never pulled.
func inspectImage(ref string) (id, arch string, err error) {
	out, err := docker("image", "inspect", "-f", "{{.Id}} {{.Architecture}}", ref)
	if err != nil {
		return "", "", fmt.Errorf("not on this machine (%v)", err)
	}
	id, arch, ok := strings.Cut(strings.TrimSpace(out), " ")
	if !ok || !imageID.MatchString(id) || strings.ContainsAny(arch, " \n") {
		return "", "", fmt.Errorf("docker image inspect printed %q", out)
	}
	return id, arch, nil
}

// imageVersion is the image's fyr stamp, from a container as locked down
// as a worker's and with no network: the second word of `fyr version`. A
// check that fails or hangs past dockerTimeout has its container removed by
// its label, so none is left running.
func imageVersion(id string) (string, error) {
	label := "fyr.work.check=" + strconv.Itoa(os.Getpid())
	out, err := docker("run", "--rm", "--pull", "never", "--label", label, "--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()),
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--network", "none", "--read-only", "--pids-limit", "64",
		id, "fyr", "version")
	if err != nil {
		ids, lerr := docker("ps", "-aq", "--filter", "label="+label)
		for _, c := range strings.Fields(ids) {
			if _, rerr := docker("rm", "-f", c); rerr != nil {
				lerr = errors.Join(lerr, rerr)
			}
		}
		if lerr != nil {
			err = fmt.Errorf("%v; the check's container (label %s) may be left: %v", err, label, lerr)
		}
		return "", err
	}
	f := strings.Fields(out)
	if len(f) != 2 || f[0] != "fyr" {
		return "", fmt.Errorf("fyr version printed %q", sandbox.Printable(out))
	}
	return f[1], nil
}

// linuxAgy is the agy the container mounts: --agy-linux, else
// <realHome>/.config/fyr/work/agy-linux (ADR-503), else on a linux host the
// host's own. On darwin with an arm64 image, both EM_AARCH64 and EM_X86_64
// (via Colima --vz-rosetta, D8) are accepted.
func linuxAgy(given, realHome, arch string) (string, error) {
	path := given
	if path == "" && realHome != "" {
		wp := filepath.Join(realHome, filepath.FromSlash(sandbox.WorkAgyRel))
		if st, err := os.Lstat(wp); err == nil {
			if !st.Mode().IsRegular() {
				return "", fmt.Errorf("the linux agy %s is not a regular file", wp)
			}
			path = wp
		}
	}
	if path == "" {
		if hostOS != "linux" {
			return "", fmt.Errorf("--runner docker on %s: give --agy-linux PATH, or place a linux agy at %s for the image's %s", hostOS, filepath.Join(realHome, filepath.FromSlash(sandbox.WorkAgyRel)), arch)
		}
		var err error
		if path, err = exec.LookPath("agy"); err != nil {
			return "", errors.New("--runner docker: agy not found on PATH; give --agy-linux PATH")
		}
	}
	path, err := filepath.Abs(path)
	if err == nil {
		path, err = filepath.EvalSymlinks(path)
	}
	if err != nil {
		return "", fmt.Errorf("--agy-linux %s: %v", given, err)
	}
	if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("the linux agy %s is not an executable file", path)
	}
	want, ok := elfMachine[arch]
	if !ok {
		return "", fmt.Errorf("the image's architecture %q is not one --runner docker knows (amd64, arm64)", arch)
	}
	f, err := elf.Open(path)
	if err != nil {
		return "", fmt.Errorf("the linux agy %s is not an ELF binary: %v", path, err)
	}
	defer f.Close()
	rosetta := hostOS == "darwin" && arch == "arm64" && f.Machine == elf.EM_X86_64
	if f.Machine != want && !rosetta {
		return "", fmt.Errorf("the linux agy %s is built for %s, the image for %s", path, f.Machine, arch)
	}
	return path, nil
}

// makeNetwork is a docker-mode run's network, made after the refusals and
// before the stamp line: a bridge labelled with the stamp, so every sweep
// removes it, with no traffic between its containers (enable_icc=false), so
// one worker cannot reach another (PLAN.md item 13). A host run makes none.
// The run is refused when it cannot be made: never the default bridge.
func (o Options) makeNetwork(stamp string) error {
	if o.Runner != RunnerDocker {
		return nil
	}
	name := sandbox.RunNetwork(stamp)
	if _, err := docker("network", "create", "--driver", "bridge", "--label", "fyr.work="+stamp,
		"--opt", "com.docker.network.bridge.enable_icc=false", name); err != nil {
		// A create that failed late may have left it.
		_, _, serr := sweepStamp(stamp)
		return fmt.Errorf("--runner docker: the run's network %s: %v; the run is refused, never put on docker's default bridge%s", name, err, sweptNote(serr))
	}
	return nil
}

// sweptNote is a refusal's tail when the sweep after it failed.
func sweptNote(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("; remove what is labelled fyr.work by hand: %v", err)
}

// sweepRun is a docker-mode run's last step: each worker removed its own
// container, so this catches one created after that check, or by a worker
// whose check never ran, then removes the run's network. It prints a line
// only when it removed a container or failed; a host run asks docker
// nothing.
func (o Options) sweepRun(stamp string) {
	if o.Runner != RunnerDocker {
		return
	}
	if n, _, err := sweepStamp(stamp); err != nil || n > 0 {
		fmt.Fprintln(o.Out, sandbox.Printable(fmt.Sprintf("work: the run's container sweep (label fyr.work=%s): %d removed, error %v", stamp, n, err)))
	}
}

// sweepStamp removes every container, then every network, labelled with
// the run's stamp: the run's last step, and --kill's for a run whose
// process is gone. A network is removed after its containers, since docker
// refuses one still in use. It tries every one, whatever fails, and returns
// how many of each it removed.
func sweepStamp(stamp string) (containers, networks int, errs error) {
	containers, errs = removeLabelled([]string{"ps", "-a", "--filter", "label=fyr.work=" + stamp, "--format", "{{.ID}}"}, "rm", "-f")
	networks, err := removeLabelled([]string{"network", "ls", "-q", "--filter", "label=fyr.work=" + stamp}, "network", "rm")
	return containers, networks, errors.Join(errs, err)
}

// removeLabelled lists IDs with list and removes each with rm, trying
// every one.
func removeLabelled(list []string, rm ...string) (int, error) {
	out, err := docker(list...)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs error
	for _, id := range strings.Fields(out) {
		if _, err := docker(append(rm, id)...); err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		n++
	}
	return n, errs
}

// docker is one docker call with the real environment minus
// DOCKER_DEFAULT_PLATFORM (sandbox.DockerEnv), its stderr in the error.
func docker(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = sandbox.DockerEnv()
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		err = fmt.Errorf("%v: %s", err, sandbox.Printable(strings.TrimSpace(string(ee.Stderr))))
	}
	return string(out), err
}
