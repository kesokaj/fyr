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

// The runners of --runner: the host (the default) or a container per
// worker (PLAN-2026-10-29-workers § Interfaces § --runner docker).
const (
	RunnerHost   = "host"
	RunnerDocker = "docker"
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
	image, agy string
}

// dockerRefusals are refusals 13-20 of SPEC-fyr-work, after refusal 11, for
// a docker-mode run: this fyr is an installed build (before any docker
// call), docker is on PATH, the daemon is local, the image ref is one, the
// image exists at this fyr's version, the linux agy runs on the image's
// architecture, and every path the container mounts can be written as a
// --mount.
func (p *prepared) dockerRefusals(o Options) error {
	if v := o.Version; v == "" || v == "unknown" || strings.ContainsAny(v, "() ") {
		return fmt.Errorf("--runner docker: this fyr is a development build (%s); run fyr install, then build the image from the installed fyr: %s", v, BuildLine)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("--runner docker: docker not found on PATH")
	}
	if err := localDaemon(); err != nil {
		return err
	}
	ref := o.Image
	if ref == "" {
		ref = DefaultImage
	}
	if strings.HasPrefix(ref, "-") || strings.IndexFunc(ref, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("--image %q: an image ref, not a flag", ref)
	}
	id, arch, err := inspectImage(ref)
	if err != nil {
		return fmt.Errorf("--image %s: %v; build it: %s", ref, err, BuildLine)
	}
	if got, err := imageVersion(id); err != nil || got != o.Version {
		return fmt.Errorf("--image %s holds fyr %q, not this fyr's %s (%v); rebuild it: %s", ref, got, o.Version, err, BuildLine)
	}
	agy, err := linuxAgy(o.AgyLinux, arch)
	if err != nil {
		return err
	}
	for _, path := range []string{agy, p.tok, sandbox.Root(o.Home, "work")} {
		if err := sandbox.Mountable(path); err != nil {
			return fmt.Errorf("--runner docker: %v", err)
		}
	}
	// The token is a regular file already: refusal 8 (session).
	p.docker = &dockerPlan{image: id, agy: agy}
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

// linuxAgy is the agy the container mounts: --agy-linux, or on a linux host
// the host's own. Either must be a regular executable ELF file whose machine
// is the image's architecture, so a Mach-O or the wrong arch is refused here,
// not met as exit 126 in every worker.
func linuxAgy(given, arch string) (string, error) {
	path := given
	if path == "" {
		if hostOS != "linux" {
			return "", fmt.Errorf("--runner docker on %s: give --agy-linux PATH, a linux agy for the image's %s", hostOS, arch)
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
	if f.Machine != want {
		return "", fmt.Errorf("the linux agy %s is built for %s, the image for %s", path, f.Machine, arch)
	}
	// Mounted by this path at each session: a file the operator's own
	// process swaps in between is the operator's (D110).
	return path, nil
}

// sweepRun is a docker-mode run's last step: each worker removed its own
// container, so this catches one created after that check, or by a worker
// whose check never ran. It prints a line only when it removed one or
// failed; a host run asks docker nothing.
func (o Options) sweepRun(stamp string) {
	if o.Runner != RunnerDocker {
		return
	}
	if n, err := sweepContainers(stamp); err != nil || n > 0 {
		fmt.Fprintln(o.Out, sandbox.Printable(fmt.Sprintf("work: the run's container sweep (label fyr.work=%s): %d removed, error %v", stamp, n, err)))
	}
}

// sweepContainers removes every container labelled with the run's stamp:
// the run's last step, and --kill's for a run whose process is gone. It
// tries every one, whatever fails, and returns how many it removed.
func sweepContainers(stamp string) (int, error) {
	out, err := docker("ps", "-a", "--filter", "label=fyr.work="+stamp, "--format", "{{.ID}}")
	if err != nil {
		return 0, err
	}
	n := 0
	var errs error
	for _, id := range strings.Fields(out) {
		if _, err := docker("rm", "-f", id); err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		n++
	}
	return n, errs
}

// docker is one docker call with the real environment, its stderr in the
// error.
func docker(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = sandbox.FyrEnv()
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		err = fmt.Errorf("%v: %s", err, sandbox.Printable(strings.TrimSpace(string(ee.Stderr))))
	}
	return string(out), err
}
