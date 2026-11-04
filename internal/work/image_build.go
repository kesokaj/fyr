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

// canBuildImage reports whether o can build DefaultImage from the embedded
// docker/work.Dockerfile against o.Repo.
func canBuildImage(o Options, ref string) bool {
	if !o.autoBuild || ref != DefaultImage || o.Repo == "" || o.Version == "" || o.Version == "(devel)" || o.Version == "unknown" {
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
// DefaultImage (D7, ADR-503).
func buildEmbeddedImage(o Options) error {
	df, err := fyr.Files.ReadFile("docker/work.Dockerfile")
	if err != nil {
		return err
	}
	ig, err := fyr.Files.ReadFile("docker/work.Dockerfile.dockerignore")
	if err != nil {
		return err
	}
	stageHome := o.Home
	if stageHome == "" {
		stageHome = o.RealHome
	}
	workDir, err := sandbox.EnsureWorkDir(stageHome)
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

func resolveAndCheckImage(o Options, ref string) (id, arch string, err error) {
	id, arch, err = inspectImage(ref)
	if err != nil && canBuildImage(o, ref) {
		if berr := buildEmbeddedImage(o); berr == nil {
			id, arch, err = inspectImage(ref)
		} else {
			err = fmt.Errorf("%v (auto-build failed: %v)", err, berr)
		}
	}
	if err != nil {
		return "", "", fmt.Errorf("--image %s: %v; build it: %s", ref, err, BuildLine)
	}
	got, verr := imageVersion(id)
	if (verr != nil || got != o.Version) && canBuildImage(o, ref) {
		if berr := buildEmbeddedImage(o); berr == nil {
			if nid, narch, ierr := inspectImage(ref); ierr == nil {
				id, arch = nid, narch
				got, verr = imageVersion(id)
			}
		} else {
			verr = fmt.Errorf("%v (auto-build failed: %v)", verr, berr)
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
