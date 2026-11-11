package sandbox

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// Docker is a docker-mode box (PLAN-2026-10-29-workers § Interfaces
// § --runner docker): the session runs in a container of Image, every other
// step on the host. A nil Config.Docker is the host path, unchanged.
type Docker struct {
	Image     string // the image REF the verb checked
	Agy       string // a linux agy on the host, mounted read-only at ContainerAgy
	Stamp     string // the run's stamp: the label fyr.work
	Name      string // the worker's name: the label fyr.worker
	Network   string // the run's own network, RunNetwork(Stamp), which work makes
	Memory    string // --memory and --memory-swap ("" is memoryLimit, 8g)
	CPUs      string // --cpus ("" is cpuLimit, 2)
	TmpfsSize string // /tmp tmpfs size ("" is 2g)
}

// RunNetwork is the network a run's containers share: a bridge with no
// traffic between its containers, made per run and labelled fyr.work, so
// the run's sweep removes it (PLAN.md item 13).
func RunNetwork(stamp string) string { return "fyr-work-" + stamp }

// runNetworkRe is a network RunNetwork names: never docker's bridge, host,
// none or container:<id>, so a missing network is never the default bridge.
var runNetworkRe = regexp.MustCompile(`^fyr-work-[0-9A-Za-z][0-9A-Za-z_.-]*$`)

// The container's limits (PLAN.md item 13): what one worker's go test -race
// needs, so one worker cannot take the machine.
const (
	memoryLimit = "8g" // --memory, and --memory-swap at the same value: no swap
	cpuLimit    = "2"
	pidsLimit   = "1024"
)

// ContainerAgy and ContainerFyr are where the session finds agy (mounted)
// and fyr (the image's own, which the wrapper execs).
const (
	ContainerAgy = "/usr/local/bin/agy"
	ContainerFyr = "/usr/local/bin/fyr"
)

// helperTimeout bounds each docker call the box makes besides the session:
// they run after the run's context may have ended, so never under it.
const helperTimeout = 30 * time.Second

// container is the session's container name, unique per run and worker.
func (d *Docker) container() string { return "fyr-work-" + d.Stamp + "-" + d.Name }

// cidFile is where docker writes the container's ID: the worker
// directory's parent, outside every mount, so the session cannot remove it.
// A Kickoff name holds no '.', so no other worker's directory is named so.
func (b *Box) cidFile() string {
	return filepath.Join(filepath.Dir(b.dir), b.c.Docker.Name+".cid")
}

func (d *Docker) limits() (mem, cpus, tmpfs string) {
	mem, cpus, tmpfs = d.Memory, d.CPUs, d.TmpfsSize
	if mem == "" {
		mem = memoryLimit
	}
	if cpus == "" {
		cpus = cpuLimit
	}
	if tmpfs == "" {
		tmpfs = "2g"
	}
	return mem, cpus, tmpfs
}

// dockerArgs is the docker run argv of one session: the hardening, the
// limits, the run's network, the four mounts and the container's
// environment, then agy and its args. Only
// the clone and the sandbox home are mounted, never the worker directory
// that holds them: its out.txt and err.txt are the host's evidence of the
// session. It refuses a mount path the --mount syntax cannot carry as
// written, an image ref docker would read as a flag, a network that is not
// a run's own, and an environment entry without '=', which docker would
// fill from the host's own value.
func dockerArgs(d *Docker, app, sb, tok, cid string, uid, gid int, env, agyArgs []string) ([]string, error) {
	for _, p := range []string{app, sb, tok, d.Agy} {
		if err := Mountable(p); err != nil {
			return nil, err
		}
	}
	if d.Image == "" || strings.HasPrefix(d.Image, "-") || strings.IndexFunc(d.Image, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return nil, fmt.Errorf("the image %q is not an image ref", d.Image)
	}
	if !runNetworkRe.MatchString(d.Network) {
		return nil, fmt.Errorf("the network %q is not a run's own; a worker never runs on docker's default bridge", d.Network)
	}
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); !ok || k == "" {
			return nil, fmt.Errorf("the environment entry %q has no name=value", kv)
		}
	}
	mem, cpus, tmpfs := d.limits()
	args := []string{"run",
		"--name", d.container(), "--label", "fyr.work=" + d.Stamp, "--label", "fyr.worker=" + d.Name,
		"--cidfile", cid, "--pull", "never",
		"--user", strconv.Itoa(uid) + ":" + strconv.Itoa(gid),
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--init",
		"--memory", mem, "--memory-swap", mem, "--cpus", cpus, "--pids-limit", pidsLimit,
		"--network", d.Network,
		"--read-only", "--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=" + tmpfs + ",mode=1777",
		"--mount", "type=bind,src=" + app + ",dst=" + app,
		"--mount", "type=bind,src=" + sb + ",dst=" + sb,
		"--mount", "type=bind,src=" + tok + ",dst=" + tok,
		"--mount", "type=bind,src=" + d.Agy + ",dst=" + ContainerAgy + ",readonly",
		"-w", app,
	}
	for _, kv := range env {
		args = append(args, "-e", kv)
	}
	args = append(args, d.Image, ContainerAgy)
	return append(args, agyArgs...), nil
}

// Mountable refuses what --mount's comma-separated, quote-aware syntax
// would read as another field: a comma, a quote, a control character.
func Mountable(p string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%s: a mount path must be absolute", p)
	}
	for _, r := range p {
		if r == ',' || r == '"' || unicode.IsControl(r) {
			return fmt.Errorf("%q: a mount path cannot hold %q", p, r)
		}
	}
	return nil
}

// containerEnv is agyEnv's allowlist for inside the container: PATH is the
// sandbox's bin before the image's own, so the wrapper and its --home pin
// come first, and Go's module cache lands in the mounted home.
func (b *Box) containerEnv() []string {
	var env []string
	for _, kv := range b.agyEnv() {
		if !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	return append([]string{"PATH=" + filepath.Join(b.sb, "bin") + ":" + ImagePath},
		append(env, "GOPATH="+filepath.Join(b.sb, "go"), "GOTOOLCHAIN=local")...)
}

// dockerAgy is steps 9-10 in a container: the docker client in its own
// process group with the real environment (its context and socket) minus
// DOCKER_DEFAULT_PLATFORM (DockerEnv), the container's environment passed
// as -e pairs only.
func (b *Box) dockerAgy(ctx context.Context, prompt, outPath, errPath string, appendErr bool) int {
	if b.sessions > 1 {
		b.Note("a docker-mode box runs one session")
		return -1
	}
	cid := b.cidFile()
	cmd := b.dockerCmd(prompt, cid)
	if cmd == nil {
		return -1
	}
	out, errf, err := agyOutputs(outPath, errPath, appendErr)
	if err != nil {
		b.fileFailed(err, "%v", err)
		return -1
	}
	defer out.Close()
	defer errf.Close()
	cmd.Stdout, cmd.Stderr = out, errf
	if err := cmd.Start(); err != nil {
		b.Note("docker: %v", err)
		return -1
	}
	// Not in b.pgids: Wait reaps the client, so step 14's sweep would aim at
	// a group whose id may be another's by then.
	stopWatch := b.watchTranscript(ctx)
	rc := agyExit(b.waitDocker(ctx, cmd, cmd.Process.Pid))
	stopWatch()
	_, cerr := os.Stat(cid)
	if cerr == nil {
		b.waitContainerExited()
	}
	// Check e's container half, now: the unlink and step 12's reads come
	// after it, never while the container could still run.
	b.containersLeft()
	os.Remove(cid) // read once; check e finds the container by its labels
	if cerr != nil || (rc >= 125 && rc <= 127) {
		b.Note("docker exit %d: agy did not start in the container (see err.txt)", rc)
		return -1
	}
	b.started = true
	return rc
}

// dockerCmd is the session's docker client, or nil when the token or the
// argv is refused (the box failed); it asks for no platform (DockerEnv).
func (b *Box) dockerCmd(prompt, cid string) *exec.Cmd {
	// The token is mounted read-write (D106): only the regular file the
	// login wrote, never a link or a directory in its place.
	if st, err := os.Lstat(b.tok); err != nil || !st.Mode().IsRegular() {
		b.Note("docker: the token %s is not a regular file", b.tok)
		b.Fail(ReasonToken)
		return nil
	}
	args, err := dockerArgs(b.c.Docker, b.app, b.sb, b.tok, cid, os.Getuid(), os.Getgid(), b.containerEnv(), b.agyArgs(prompt))
	if err != nil {
		b.Note("docker: %v", err)
		b.Fail(ReasonSetup)
		return nil
	}
	cmd := exec.Command("docker", args...)
	cmd.Env = DockerEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// waitDocker waits for the client: at Timeout the container is killed, at
// ctx's end stopped; either way the client's group gets SIGKILL when it has
// not exited StopGrace later, so a docker call that fails cannot hang it.
func (b *Box) waitDocker(ctx context.Context, cmd *exec.Cmd, pgid int) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(b.c.Timeout)
	defer timer.Stop()
	name := b.c.Docker.container()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		b.Note("agy timed out after %s; its container was killed", b.c.Timeout)
		b.timedOut = true
		if _, err := b.docker("kill", name); err != nil {
			b.Note("docker kill: %v", err)
		}
	case <-ctx.Done():
		// Whole seconds, rounded up: StopGrace is positive (New), so at
		// least 1.
		secs := int(math.Ceil(b.c.StopGrace.Seconds()))
		if _, err := b.docker("stop", "-t", strconv.Itoa(secs), name); err != nil {
			b.Note("docker stop: %v", err)
		}
		// A client that already exited is reaped by Wait: its group is not
		// signalled, since its id may be another's by now.
		select {
		case err := <-done:
			return err
		default:
			syscall.Kill(-pgid, syscall.SIGTERM)
		}
	}
	select {
	case err := <-done:
		return err
	case <-time.After(b.c.StopGrace):
		syscall.Kill(-pgid, syscall.SIGKILL)
		return <-done
	}
}

// waitContainerExited synchronizes with dockerd after the docker run client
// exits so tmpfs/mount teardown transitions the container state to exited
// before containersLeft queries docker ps (BUG-539).
func (b *Box) waitContainerExited() {
	timeout := min(b.c.StopGrace/2, 5*time.Second)
	if timeout <= 0 {
		timeout = time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "wait", b.c.Docker.container())
	cmd.Env = FyrEnv()
	_ = cmd.Run()
}

// docker is one helper call, with the real environment and a deadline of
// its own.
func (b *Box) docker(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), helperTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = FyrEnv()
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			err = fmt.Errorf("%v: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
	}
	return string(out), err
}

// containersLeft is check e's container half: a container of this worker
// that is not exited or dead, one rm -f could not remove, or a docker call
// that failed fails the box processes left. It fails closed: an error is
// never read as an empty list.
func (b *Box) containersLeft() {
	rows, err := b.listContainers()
	if err != nil {
		b.containersFail("the container check failed, so it fails the run (remove the containers labelled fyr.work=%s and fyr.worker=%s by hand): %v", b.c.Docker.Stamp, b.c.Docker.Name, err)
		return
	}
	var live []string
	for _, r := range rows {
		if r[1] != "exited" && r[1] != "dead" {
			live = append(live, Printable(r[0]+" ("+r[1]+")"))
		}
	}
	if len(live) > 0 {
		b.containersFail("containers left by the run: %s", strings.Join(live, ", "))
	}
	for _, r := range rows {
		if _, err := b.docker("rm", "-f", r[0]); err != nil {
			b.containersFail("docker rm -f %s: %v", Printable(r[0]), err)
		}
	}
	if rows, err := b.listContainers(); err != nil {
		b.containersFail("the container check failed, so it fails the run: %v", err)
	} else if len(rows) > 0 {
		b.containersFail("containers not removed: %d", len(rows))
	}
}

// listContainers is this worker's containers, by both labels, as ID and
// state; a line it cannot read is an error, never skipped.
func (b *Box) listContainers() ([][2]string, error) {
	d := b.c.Docker
	out, err := b.docker("ps", "-a", "--filter", "label=fyr.work="+d.Stamp, "--filter", "label=fyr.worker="+d.Name, "--format", "{{.ID}} {{.State}}")
	if err != nil {
		return nil, err
	}
	var rows [][2]string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if id, state, ok := strings.Cut(strings.TrimSpace(l), " "); ok {
			rows = append(rows, [2]string{id, state})
		} else if l != "" {
			return nil, fmt.Errorf("docker ps: an unreadable line %q", Printable(l))
		}
	}
	return rows, nil
}

// containersFail notes the failure and fails the box processes left once.
func (b *Box) containersFail(format string, a ...any) {
	b.Note(format, a...)
	if !b.Has(ReasonProcs) {
		b.Fail(ReasonProcs)
	}
}
