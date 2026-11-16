package isolate

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	DefaultImage  = "fyr-work:local"
	BuildLine     = `docker buildx build --load -f docker/work.Dockerfile --build-arg GO_MINOR="$(go env GOVERSION | sed -E 's/^go([0-9]+\.[0-9]+).*/\1/')" --build-arg FYR_VERSION="$(fyr version | cut -d' ' -f2)" -t fyr-work:local .`
	dockerTimeout = time.Minute

	DefaultRAMGB  = 8
	MinRAMGB      = 2
	DefaultCPUs   = 2
	MinCPUs       = 1
	DefaultDiskGB = 10
)

var elfMachine = map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}

var imageID = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type dockerPlan struct {
	image, agy, fyr     string
	ramGB, cpus, diskGB int
}

// EvalDockerOptions configures container preparation for `--runner docker` eval trials.
type EvalDockerOptions struct {
	Home, RealHome, Root string
	Image, AgyLinux      string
	Stamp, Name, Fyr     string
}

// SandboxRoot returns `<home>/.config/fyr/sandbox/<project>`.
func SandboxRoot(home, root string) string {
	return ProjectRoot(home, "sandbox", root)
}

// DockerPreflight checks docker availability, daemon limits, image, linux agy,
// mounts, and login for `--runner docker`.
func DockerPreflight(o WorkerOptions) error {
	var p prepared
	return p.dockerPreflight(o)
}

func (p *prepared) dockerPreflight(o WorkerOptions) error {
	for _, path := range []*string{&o.Home, &o.RealHome} {
		if *path == "" {
			continue
		}
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
	if p.tok, err = CheckWorkLogin(o.Home, o.RealHome, hostOS); err != nil {
		return err
	}
	return p.dockerRefusals(o)
}

func (p *prepared) dockerRefusals(o WorkerOptions) error {
	plan, err := prepareContainerPlan(containerPlanReq{
		image: o.Image, agyLinux: o.AgyLinux, realHome: o.RealHome, fyr: o.Self,
		mountRoots: []string{p.tok, Root(o.Home, "work"), SandboxRoot(o.Home, o.Root)},
		ramGB:      o.RAMGB, cpus: o.CPUs, diskGB: o.DiskGB,
	})
	if err != nil {
		return err
	}
	p.docker = plan
	return nil
}

// PrepareEvalDocker validates docker container prerequisites for eval and
// returns the Box container configuration and synced token path.
func PrepareEvalDocker(o EvalDockerOptions) (*Docker, string, error) {
	tok, err := CheckWorkLogin(o.Home, o.RealHome, hostOS)
	if err != nil {
		return nil, "", err
	}
	plan, err := prepareContainerPlan(containerPlanReq{
		image: o.Image, agyLinux: o.AgyLinux, realHome: o.RealHome, fyr: o.Fyr,
		mountRoots: []string{tok, ProjectRoot(o.Home, "eval", o.Root)},
		ramGB:      DefaultRAMGB, cpus: DefaultCPUs, diskGB: DefaultDiskGB,
	})
	if err != nil {
		return nil, "", err
	}
	if tok, err = SyncWorkToken(o.Home, o.RealHome, hostOS); err != nil {
		return nil, "", err
	}
	if err := Mountable(tok); err != nil {
		return nil, "", fmt.Errorf("--runner docker: %v", err)
	}
	return plan.boxConfig(o.Stamp, o.Name), tok, nil
}

type containerPlanReq struct {
	image, agyLinux, realHome, fyr string
	mountRoots                     []string
	ramGB, cpus, diskGB            int
}

func prepareContainerPlan(req containerPlanReq) (*dockerPlan, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, errors.New("--runner docker: docker not found on PATH")
	}
	if err := localDaemon(); err != nil {
		return nil, err
	}
	ncpu, memGB, err := daemonLimits()
	if err != nil {
		return nil, err
	}
	ref := req.image
	if ref == "" {
		ref = DefaultImage
	}
	if strings.HasPrefix(ref, "-") || strings.IndexFunc(ref, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return nil, fmt.Errorf("--image %q: an image ref, not a flag", ref)
	}
	id, arch, err := inspectImage(ref)
	if err != nil {
		return nil, fmt.Errorf("--image %s: %v; build it: %s", ref, err, BuildLine)
	}
	agy, err := linuxAgy(req.agyLinux, req.realHome, arch, id)
	if err != nil {
		return nil, err
	}
	if err := checkOptionalELF(req.fyr, arch); err != nil {
		return nil, err
	}
	for _, path := range append([]string{agy, req.fyr}, req.mountRoots...) {
		if path == "" {
			continue
		}
		if err := Mountable(path); err != nil {
			return nil, fmt.Errorf("--runner docker: %v", err)
		}
	}
	return newDockerPlan(id, agy, req.fyr, req.ramGB, req.cpus, req.diskGB, ncpu, memGB), nil
}

func newDockerPlan(id, agy, fyr string, ram, cpus, disk, ncpu, memGB int) *dockerPlan {
	if ram == 0 {
		ram = DefaultRAMGB
	}
	if cpus == 0 {
		cpus = DefaultCPUs
	}
	if disk == 0 {
		disk = DefaultDiskGB
	}
	if ncpu > 0 && cpus > ncpu {
		cpus = ncpu
	}
	if memGB >= MinRAMGB && ram > memGB {
		ram = memGB
	}
	return &dockerPlan{image: id, agy: agy, fyr: fyr, ramGB: ram, cpus: cpus, diskGB: disk}
}

func (p *dockerPlan) boxConfig(stamp, name string) *Docker {
	if p == nil {
		return nil
	}
	d := &Docker{Image: p.image, Agy: p.agy, Fyr: p.fyr, Stamp: stamp, Name: name, Network: RunNetwork(stamp)}
	if p.ramGB > 0 {
		d.Memory = fmt.Sprintf("%dg", p.ramGB)
	}
	if p.cpus > 0 {
		d.CPUs = fmt.Sprintf("%d", p.cpus)
	}
	if p.diskGB > 0 {
		d.TmpfsSize = fmt.Sprintf("%dg", max(1, min(2, p.diskGB/2)))
	}
	return d
}

func localDaemon() error {
	if h := os.Getenv("DOCKER_HOST"); h != "" && !strings.HasPrefix(h, "unix://") {
		return fmt.Errorf("DOCKER_HOST=%s: --runner docker needs the local daemon, whose files the mounts name", h)
	}
	out, err := dockerCLI("context", "inspect", "--format", "{{.Endpoints.docker.Host}}")
	if err != nil {
		return fmt.Errorf("--runner docker: the docker context: %v", err)
	}
	if h := strings.TrimSpace(out); !strings.HasPrefix(h, "unix://") {
		return fmt.Errorf("the docker context's endpoint %s: --runner docker needs the local daemon, whose files the mounts name", Printable(h))
	}
	return nil
}

func daemonLimits() (ncpu, memGB int, err error) {
	out, err := dockerCLI("info", "--format", "{{.MemoryLimit}} {{.SwapLimit}} {{.PidsLimit}} {{.CPUCfsQuota}} {{.NCPU}} {{.MemTotal}}")
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
		return 0, 0, fmt.Errorf("--runner docker: the daemon cannot apply the limits (%s; docker info printed %q): give it the cgroup controllers", strings.Join(missing, ", "), Printable(strings.TrimSpace(out)))
	}
	ncpu, err = strconv.Atoi(f[4])
	memBytes, merr := strconv.ParseInt(f[5], 10, 64)
	if err != nil || merr != nil || ncpu < 1 || memBytes <= 0 {
		return 0, 0, fmt.Errorf("--runner docker: the daemon cannot apply the limits (docker info printed %q): give it the cgroup controllers", Printable(strings.TrimSpace(out)))
	}
	memGB = max(MinRAMGB, int((memBytes+(1<<30)-1)>>30))
	return ncpu, memGB, nil
}

func inspectImage(ref string) (id, arch string, err error) {
	out, err := dockerCLI("image", "inspect", "-f", "{{.Id}} {{.Architecture}}", ref)
	if err != nil {
		return "", "", fmt.Errorf("not on this machine (%v)", err)
	}
	id, arch, ok := strings.Cut(strings.TrimSpace(out), " ")
	if !ok || !imageID.MatchString(id) || strings.ContainsAny(arch, " \n") {
		return "", "", fmt.Errorf("docker image inspect printed %q", out)
	}
	return id, arch, nil
}

func checkOptionalELF(path, arch string) error {
	if path == "" {
		return nil
	}
	want, ok := elfMachine[arch]
	if !ok {
		return fmt.Errorf("the image's architecture %q is not known (amd64, arm64)", arch)
	}
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("the container fyr %s is not an ELF binary: %v", path, err)
	}
	_ = f.Close()
	if f.Machine != want {
		return fmt.Errorf("the container fyr %s is built for %s, the image for %s", path, f.Machine, arch)
	}
	return nil
}

func linuxAgy(given, realHome, arch, id string) (string, error) {
	path, err := findLinuxAgyPath(given, realHome, arch)
	if err != nil {
		return "", err
	}
	path, err = resolveExecutableFile(given, path)
	if err != nil {
		return "", err
	}
	if err := Mountable(path); err != nil {
		return "", fmt.Errorf("the linux agy %s: %v", path, err)
	}
	return path, verifyLinuxAgyArch(path, arch, id)
}

func findLinuxAgyPath(given, realHome, arch string) (string, error) {
	if given != "" {
		return given, nil
	}
	if realHome != "" {
		wp := filepath.Join(realHome, filepath.FromSlash(WorkAgyRel))
		if st, err := os.Lstat(wp); err == nil {
			if !st.Mode().IsRegular() {
				return "", fmt.Errorf("the linux agy %s is not a regular file", wp)
			}
			return wp, nil
		}
	}
	if hostOS != "linux" {
		return "", fmt.Errorf("--runner docker on %s: give --agy-linux PATH, or place a linux agy at %s for the image's %s", hostOS, filepath.Join(realHome, filepath.FromSlash(WorkAgyRel)), arch)
	}
	p, err := exec.LookPath("agy")
	if err != nil {
		return "", errors.New("--runner docker: agy not found on PATH; give --agy-linux PATH")
	}
	return p, nil
}

func resolveExecutableFile(given, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err == nil {
		abs, err = filepath.EvalSymlinks(abs)
	}
	if err != nil {
		return "", fmt.Errorf("--agy-linux %s: %v", given, err)
	}
	if st, err := os.Stat(abs); err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("the linux agy %s is not an executable file", abs)
	}
	return abs, nil
}

func verifyLinuxAgyArch(path, arch, id string) error {
	want, ok := elfMachine[arch]
	if !ok {
		return fmt.Errorf("the image's architecture %q is not one --runner docker knows (amd64, arm64)", arch)
	}
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("the linux agy %s is not an ELF binary: %v", path, err)
	}
	defer f.Close()
	rosetta := hostOS == "darwin" && arch == "arm64" && f.Machine == elf.EM_X86_64 && checkRosetta(id, path) == nil
	if f.Machine != want && !rosetta {
		return fmt.Errorf("the linux agy %s is built for %s, the image for %s", path, f.Machine, arch)
	}
	return nil
}

func checkRosetta(id, agyPath string) error {
	if id == "" {
		return nil
	}
	if err := Mountable(agyPath); err != nil {
		return err
	}
	_, err := dockerCLI("run", "--rm", "--pull", "never", "--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()),
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--network", "none", "--read-only", "--pids-limit", "64",
		"--mount", "type=bind,src="+agyPath+",dst="+ContainerAgy+",readonly",
		id, ContainerAgy, "--version")
	return err
}

// MakeRunNetwork creates an isolated bridge network labelled `fyr.work=<stamp>` with ICC off.
func MakeRunNetwork(stamp string) error {
	name := RunNetwork(stamp)
	if _, err := dockerCLI("network", "create", "--driver", "bridge", "--label", "fyr.work="+stamp,
		"--opt", "com.docker.network.bridge.enable_icc=false", name); err != nil {
		_, _, serr := SweepRunStamp(stamp)
		return fmt.Errorf("--runner docker: the run's network %s: %v; the run is refused, never put on docker's default bridge%s", name, err, sweptNote(serr))
	}
	return nil
}

func (o WorkerOptions) makeNetwork(stamp string) error {
	if o.Runner != RunnerDocker {
		return nil
	}
	return MakeRunNetwork(stamp)
}

func sweptNote(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("; remove what is labelled fyr.work by hand: %v", err)
}

func (o WorkerOptions) sweepRun(stamp string) {
	if o.Runner != RunnerDocker {
		return
	}
	if n, _, err := SweepRunStamp(stamp); err != nil || n > 0 {
		fmt.Fprintln(o.Out, Printable(fmt.Sprintf("isolate: the run's container sweep (label fyr.work=%s): %d removed, error %v", stamp, n, err)))
	}
}

// SweepRunStamp removes containers then networks labelled `fyr.work=<stamp>`.
func SweepRunStamp(stamp string) (containers, networks int, errs error) {
	containers, errs = removeLabelled([]string{"ps", "-a", "--filter", "label=fyr.work=" + stamp, "--format", "{{.ID}}"}, "rm", "-f")
	networks, err := removeLabelled([]string{"network", "ls", "-q", "--filter", "label=fyr.work=" + stamp}, "network", "rm")
	return containers, networks, errors.Join(errs, err)
}

func removeLabelled(list []string, rm ...string) (int, error) {
	out, err := dockerCLI(list...)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs error
	for _, id := range strings.Fields(out) {
		if _, err := dockerCLI(append(rm, id)...); err != nil {
			errs = errors.Join(errs, err)
			continue
		}
		n++
	}
	return n, errs
}

func dockerCLI(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = DockerEnv()
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		err = fmt.Errorf("%v: %s", err, Printable(strings.TrimSpace(string(ee.Stderr))))
	}
	return string(out), err
}
