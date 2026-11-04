package work

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/kesokaj/fyr"
	"github.com/kesokaj/fyr/internal/sandbox"
)

const buildTimeout = 5 * time.Minute

var goMinorRe = regexp.MustCompile(`(?m)^go\s+([0-9]+\.[0-9]+)`)

// buildMode is what the docker refusals do with a missing or stale
// DefaultImage that o can build (canBuildImage), as Options.build.
type buildMode uint8

const (
	// buildNever refuses it like any other image, building nothing: the
	// zero value, DockerPreflight's, so fyr auto's probe builds no image.
	buildNever buildMode = iota
	// buildFirst builds it, then checks the new image: Run's (item 12).
	buildFirst
	// buildPredict builds nothing: it runs the build's own checks, read
	// only, and passes with the architecture the build gives (predictBuild),
	// so Check reads as the runner a run picks (ADR-504a).
	buildPredict
)

// archRe is the shape of an architecture name docker prints, so nothing
// else reaches linuxAgy's check or a message.
var archRe = regexp.MustCompile(`^[a-z0-9]{1,16}$`)

// canBuildImage reports whether o can build DefaultImage from the embedded
// docker/work.Dockerfile against o.Repo; never under buildNever.
func canBuildImage(o Options, ref string) bool {
	if o.build == buildNever || ref != DefaultImage || o.Repo == "" || o.Version == "" || o.Version == "(devel)" || o.Version == "unknown" {
		return false
	}
	st, err := os.Stat(filepath.Join(o.Repo, "go.mod"))
	return err == nil && st.Mode().IsRegular()
}

// goMinor returns the Go major.minor version for the image build: the host
// runtime's Go major.minor when available, or go.mod's go line.
func goMinor(repo string) string {
	if m := regexp.MustCompile(`^go([0-9]+\.[0-9]+)`).FindStringSubmatch(runtime.Version()); len(m) == 2 {
		return m[1]
	}
	if b, err := os.ReadFile(filepath.Join(repo, "go.mod")); err == nil {
		if m := goMinorRe.FindSubmatch(b); len(m) == 2 {
			return string(m[1])
		}
	}
	return "1.26"
}

// buildEmbeddedImage stages the embedded docker/work.Dockerfile and
// docker/work.Dockerfile.dockerignore under <home>/.config/fyr/work (never
// /tmp) and runs `docker buildx build --load` against o.Repo to build
// DefaultImage (D7, ADR-503) for the daemon's architecture (daemonArch, ADR-504a):
// neither DOCKER_DEFAULT_PLATFORM nor a builder's default gives it another than
// predictBuild's, and the clients that run it ask for none (sandbox.DockerEnv).
func buildEmbeddedImage(o Options) error {
	df, err := fyr.Files.ReadFile("docker/work.Dockerfile")
	if err != nil {
		return err
	}
	ig, err := fyr.Files.ReadFile("docker/work.Dockerfile.dockerignore")
	if err != nil {
		return err
	}
	arch, err := daemonArch()
	if err != nil {
		return err
	}
	workDir, err := sandbox.EnsureWorkDir(stageHome(o))
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(workDir, "docker-build-*")
	if err != nil {
		_ = os.Remove(workDir)
		_ = os.Remove(filepath.Dir(workDir))
		return err
	}
	defer func() {
		os.RemoveAll(tmp)
		_ = os.Remove(workDir)
		_ = os.Remove(filepath.Dir(workDir))
	}()
	dfPath := filepath.Join(tmp, "work.Dockerfile")
	if err := os.WriteFile(dfPath, df, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(dfPath+".dockerignore", ig, 0o600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "buildx", "build", "--load",
		"-f", dfPath,
		"--build-arg", "GO_MINOR="+goMinor(o.Repo),
		"--build-arg", "FYR_VERSION="+o.Version,
		"--platform", "linux/"+arch,
		"-t", DefaultImage,
		o.Repo)
	cmd.Env = sandbox.FyrEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return &dockerCmdErr{err: err, out: strings.TrimSpace(string(out))}
	}
	return nil
}

type dockerCmdErr struct {
	err error
	out string
}

func (e *dockerCmdErr) Error() string {
	if e.out == "" {
		return e.err.Error()
	}
	return e.err.Error() + ": " + e.out
}

// stageHome is the home whose work directory a build stages its files in:
// o.Home, else o.RealHome.
func stageHome(o Options) string {
	if o.Home != "" {
		return o.Home
	}
	return o.RealHome
}

// daemonArch is the daemon's architecture, the one buildEmbeddedImage
// builds the image for.
func daemonArch() (string, error) {
	out, err := docker("version", "--format", "{{.Server.Arch}}")
	if err != nil {
		return "", fmt.Errorf("docker version: %v", err)
	}
	arch := strings.TrimSpace(out)
	if !archRe.MatchString(arch) {
		return "", fmt.Errorf("docker version printed %q", out)
	}
	return arch, nil
}

// predictBuild is buildEmbeddedImage's checks before its build, in its
// order and read only: the daemon's architecture, which it returns, and
// the work directory it stages in (sandbox.CheckWorkDir), so a build that
// refuses there refuses --check too, with the same words (ADR-504a).
func predictBuild(o Options) (string, error) {
	arch, err := daemonArch()
	if err != nil {
		return "", err
	}
	if err := sandbox.CheckWorkDir(stageHome(o)); err != nil {
		return "", err
	}
	return arch, nil
}

// buildOrPredict is what o.build does with a missing or stale DefaultImage
// that o can build: buildFirst builds it, and buildPredict predicts the
// build (predicted, with the architecture it gives) and builds nothing.
func buildOrPredict(o Options) (predicted bool, arch string, err error) {
	if o.build == buildPredict {
		arch, err = predictBuild(o)
		return true, arch, err
	}
	return false, "", buildEmbeddedImage(o)
}

// resolveAndCheckImage is the image ref names, by its ID and architecture,
// when it is on this machine at this fyr's version. A missing or stale one
// that o can build (canBuildImage) is built first under buildFirst; under
// buildPredict nothing is built and the ID is "", with the architecture
// the build gives (predictBuild, ADR-504a). A build that fails, or its
// prediction, is the refusal, in the same words.
func resolveAndCheckImage(o Options, ref string) (id, arch string, err error) {
	id, arch, err = inspectImage(ref)
	if err != nil && canBuildImage(o, ref) {
		predicted, parch, berr := buildOrPredict(o)
		switch {
		case berr != nil:
			err = fmt.Errorf("%v (auto-build failed: %v)", err, berr)
		case predicted:
			return "", parch, nil
		default:
			id, arch, err = inspectImage(ref)
		}
	}
	if err != nil {
		return "", "", fmt.Errorf("--image %s: %v; build it: %s", ref, err, BuildLine)
	}
	return checkImageVersion(o, ref, id, arch)
}

// checkImageVersion is resolveAndCheckImage's version check of the image
// id that ref named: it holds this fyr's version, else it is rebuilt or
// predicted as resolveAndCheckImage says, or refused.
func checkImageVersion(o Options, ref, id, arch string) (string, string, error) {
	got, verr := imageVersion(id)
	if (verr != nil || got != o.Version) && canBuildImage(o, ref) {
		predicted, parch, berr := buildOrPredict(o)
		switch {
		case berr != nil:
			verr = fmt.Errorf("%v (auto-build failed: %v)", verr, berr)
		case predicted:
			return "", parch, nil
		default:
			if nid, narch, ierr := inspectImage(ref); ierr == nil {
				id, arch = nid, narch
				got, verr = imageVersion(id)
			}
		}
	}
	if verr != nil || got != o.Version {
		return "", "", fmt.Errorf("--image %s holds fyr %q, not this fyr's %s (%v); rebuild it: %s", ref, got, o.Version, verr, BuildLine)
	}
	return id, arch, nil
}

func newDockerPlan(id, agy string, o Options, ncpu, memGB int) *dockerPlan {
	ram, cpus, disk := o.RAMGB, o.CPUs, o.DiskGB
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
	return &dockerPlan{image: id, agy: agy, ramGB: ram, cpus: cpus, diskGB: disk}
}
