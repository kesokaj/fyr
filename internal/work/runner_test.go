package work

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/sandbox"
)

// CheckRunner takes the three runners and refuses anything else by name.
func TestCheckRunner(t *testing.T) {
	for _, r := range []string{RunnerAuto, RunnerHost, RunnerDocker} {
		if err := CheckRunner(r); err != nil {
			t.Errorf("CheckRunner(%q) = %v", r, err)
		}
	}
	for _, r := range []string{"", "podman", "Docker", "auto "} {
		if err := CheckRunner(r); err == nil || err.Error() != `--runner "`+r+`": give auto, host or docker` {
			t.Errorf("CheckRunner(%q) = %v", r, err)
		}
	}
}

// ChooseRunner is the one choice of fyr work and fyr auto (D5, ADR-504):
// auto is docker when the preflight passes and the host with the refusal,
// on one line, when it fails; any other runner is kept and the preflight is
// never called, so an explicit docker refuses rather than fall back.
func TestChooseRunner(t *testing.T) {
	for _, c := range []struct {
		name, runner, reason string
		err                  error
		want, wantReason     string
		probed               bool
	}{
		{"auto passes", RunnerAuto, "stale", nil, RunnerDocker, "", true},
		{"auto refused", RunnerAuto, "", errors.New("--runner docker: no Antigravity login\nat /h/tok"), RunnerHost, "no Antigravity login at /h/tok", true},
		{"auto refused, other prefix", RunnerAuto, "", errors.New("--image x:1: not on this machine"), RunnerHost, "--image x:1: not on this machine", true},
		{"auto refused, no message", RunnerAuto, "", errors.New(" \n"), RunnerHost, "the docker preflight failed", true},
		{"host", RunnerHost, "", errors.New("unused"), RunnerHost, "", false},
		{"host with a reason", RunnerHost, "no docker", nil, RunnerHost, "no docker", false},
		{"docker", RunnerDocker, "", errors.New("unused"), RunnerDocker, "", false},
		{"library default", "", "", errors.New("unused"), "", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			probed := false
			o := Options{Runner: c.runner, RunnerReason: c.reason}
			got, why := ChooseRunner(&o, func(p Options) error {
				probed = true
				if p.Runner != RunnerAuto {
					t.Errorf("the preflight saw --runner %q", p.Runner)
				}
				return c.err
			})
			if got != c.want || why != c.wantReason || probed != c.probed {
				t.Errorf("got %q %q probed %v, want %q %q probed %v", got, why, probed, c.want, c.wantReason, c.probed)
			}
			if o.Runner != got || o.RunnerReason != why {
				t.Errorf("Options left at %q %q, returned %q %q", o.Runner, o.RunnerReason, got, why)
			}
		})
	}
}

// A development build is refused the docker runner before anything else:
// auto is the host, saying why, and an explicit docker is the refusal, and
// neither reads the Keychain nor calls docker (ADR-504).
func TestRunnerDevelBuild(t *testing.T) {
	r, o := dockerRig(t)
	setHostOS(t, "darwin")
	o.Version = "(devel)"
	if err := os.Remove(filepath.Join(r.real, sandbox.TokenRel)); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(r.real, filepath.FromSlash(sandbox.KeychainsRel), "login.keychain-db"), "keychain")
	read := false
	t.Cleanup(sandbox.SetKeychainReader(func() (string, error) { read = true; return "", errors.New("unused") }))
	refusal := "--runner docker: this fyr is a development build ((devel)); run fyr install, then build the image from the installed fyr: " + BuildLine

	auto := o
	auto.Runner = RunnerAuto
	if got, why, err := Check(auto); err != nil || RunnerLabel(got, why) != "runner host ("+strings.TrimPrefix(refusal, "--runner docker: ")+")" {
		t.Errorf("auto: %q %q %v", got, why, err)
	}
	if _, _, err := Check(o); err == nil || err.Error() != refusal {
		t.Errorf("explicit docker: %v, want %q", err, refusal)
	}
	if got, why := ChooseRunner(&auto, nil); got != RunnerHost || !strings.HasPrefix(why, "this fyr is a development build") {
		t.Errorf("ChooseRunner with the default preflight: %q %q", got, why)
	}
	if read {
		t.Error("the Keychain was read")
	}
	if calls := r.read("docker.calls"); calls != "" {
		t.Errorf("docker was called: %q", calls)
	}
}

// A Mac with no linux agy (item 14's darwin test): --runner auto runs on
// the host and says why on the stamp and result lines, where an explicit
// --runner docker refuses and makes nothing (D5).
func TestRunAutoDarwinNoLinuxAgy(t *testing.T) {
	r, o := dockerRig(t)
	if err := os.Rename(filepath.Join(r.tmp, "agy-off-path", "agy"), filepath.Join(r.bin, "agy")); err != nil {
		t.Fatal(err)
	}
	setHostOS(t, "darwin")
	old := lazyFetchSafe
	lazyFetchSafe = func() bool { return true }
	t.Cleanup(func() { lazyFetchSafe = old })
	o.AgyLinux = ""
	refusal := "--runner docker on darwin: give --agy-linux PATH, or place a linux agy at " +
		filepath.Join(r.real, filepath.FromSlash(sandbox.WorkAgyRel)) + " for the image's " + runtime.GOARCH

	if _, err := runWith(o, r.kickoff("fix-d", "src/a.txt")); err == nil || err.Error() != refusal {
		t.Fatalf("--runner docker: err %v, want %q", err, refusal)
	}
	if _, err := os.Lstat(sandbox.Root(o.Home, "work")); !os.IsNotExist(err) || r.out.Len() != 0 {
		t.Fatalf("the refusal made the sandbox root (%v) or printed %q", err, r.out.String())
	}

	os.Remove(filepath.Join(r.ctl, "docker.calls"))
	o.Runner = RunnerAuto
	res, err := runWith(o, r.kickoff("fix-a", "src/a.txt"))
	if err != nil || res.Verdict != "PASS" || res.Runner != RunnerHost {
		t.Fatalf("--runner auto: err=%v res=%+v\n%s", err, res, r.out.String())
	}
	label := "  runner host (" + refusal + ")"
	if lines := strings.Split(r.out.String(), "\n"); !strings.HasPrefix(lines[0], "work: stamp ") || !strings.HasSuffix(lines[0], label) {
		t.Errorf("stamp line %q does not end with %q", lines[0], label)
	}
	if b := res.Block(); !strings.HasSuffix(b[0], label) {
		t.Errorf("result line %q does not end with %q", b[0], label)
	}
	calls := r.read("docker.calls")
	if !strings.Contains(calls, "image inspect") || strings.Contains(calls, "network create") {
		t.Errorf("auto did not probe docker, or its host run made a network:\n%s", calls)
	}
}

// When the docker preflight passes, --runner auto is docker, and its probe
// is the run's only docker check: one docker info and one image version
// check, then the run's network, whose failure here ends the run.
func TestRunAutoPicksDocker(t *testing.T) {
	r, o := dockerRig(t)
	o.Runner = RunnerAuto
	o.AgyLinux = fakeELF(t, elfMachine[runtime.GOARCH])
	r.on("docker-network-fail", "")
	if _, err := runWith(o, r.kickoff("fix-a", "src/a.txt")); err == nil || !strings.Contains(err.Error(), "--runner docker: the run's network fyr-work-") {
		t.Fatalf("err %v, want the docker run's network refusal", err)
	}
	info, version := 0, 0
	for _, l := range strings.Split(r.read("docker.calls"), "\n") {
		if strings.HasPrefix(l, "info --format") {
			info++
		}
		if strings.HasPrefix(l, "run --rm") {
			version++
		}
	}
	if info != 1 || version != 1 {
		t.Errorf("docker info ran %d times and the image version check %d, want once each:\n%s", info, version, r.read("docker.calls"))
	}
}

// predictRig is dockerRig with an install source, so its default image is
// one a run builds first (canBuildImage), and a linux agy of this
// machine's architecture.
func predictRig(t *testing.T) (*rig, Options) {
	r, o := dockerRig(t)
	o.Repo = filepath.Join(r.tmp, "repo")
	put(t, filepath.Join(o.Repo, "go.mod"), "module example.com/test\n\ngo 1.26.2\n")
	o.AgyLinux = fakeELF(t, elfMachine[runtime.GOARCH])
	return r, o
}

// --check predicts the build a run makes first (ADR-504a): a missing or
// stale default image a run builds reads as docker, saying so, for auto
// and an explicit docker alike, and nothing is built or made. The linux
// agy is checked against the daemon's architecture, which a build gives
// the image, so a refusal the run would meet after its build is --check's
// too; an image no run builds (another --image, no install source) reads
// as it did, the host for auto and the refusal for docker.
func TestCheckPredictsBuild(t *testing.T) {
	built := "runner docker (fyr-work:local will be built first)"
	for _, c := range []struct {
		name, want string // built, or what auto's host reason and docker's refusal say
		setup      func(r *rig, o *Options)
	}{
		{"stale", built, func(r *rig, o *Options) { r.on("docker-version", "vold") }},
		{"missing", built, func(r *rig, o *Options) { r.on("docker-noimage", "") }},
		// A build gives the daemon's architecture, whatever the stale image's.
		{"stale-other-arch", built, func(r *rig, o *Options) {
			r.on("docker-version", "vold")
			r.on("docker-arch", otherArch())
			r.on("docker-server-arch", runtime.GOARCH)
		}},
		{"agy-other-arch", "built for", func(r *rig, o *Options) {
			r.on("docker-noimage", "")
			r.on("docker-server-arch", otherArch())
		}},
		{"unknown-arch", "not one --runner docker knows", func(r *rig, o *Options) {
			r.on("docker-noimage", "")
			r.on("docker-server-arch", "riscv64")
		}},
		{"version-fails", "docker version: exit status 1: Cannot connect to the Docker daemon", func(r *rig, o *Options) {
			r.on("docker-noimage", "")
			r.on("docker-server-fail", "")
		}},
		{"version-odd", `docker version printed "amd64 --privileged\n"`, func(r *rig, o *Options) {
			r.on("docker-version", "vold")
			r.on("docker-server-arch", "amd64 --privileged\n")
		}},
		{"other-image", "--image x:1: not on this machine", func(r *rig, o *Options) {
			o.Image = "x:1"
			r.on("docker-noimage", "")
		}},
		{"no-source", "build it: " + BuildLine, func(r *rig, o *Options) {
			o.Repo = ""
			r.on("docker-noimage", "")
		}},
		{"stale-no-source", `holds fyr "vold"`, func(r *rig, o *Options) {
			o.Repo = ""
			r.on("docker-version", "vold")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, o := predictRig(t)
			c.setup(r, &o)
			auto := o
			auto.Runner = RunnerAuto
			got, why, err := Check(auto)
			label := RunnerLabel(got, why)
			dgot, dwhy, derr := Check(o)
			switch {
			case c.want == built:
				if err != nil || label != built || derr != nil || RunnerLabel(dgot, dwhy) != built {
					t.Errorf("auto: %q %v; explicit docker: %q %q %v; want %q", label, err, dgot, dwhy, derr, built)
				}
			case err != nil || got != RunnerHost || !strings.Contains(label, c.want):
				t.Errorf("auto: %q %v, want the host saying %q", label, err, c.want)
			case derr == nil || !strings.Contains(derr.Error(), c.want) || dgot != "" || dwhy != "":
				t.Errorf("explicit docker: %q %q %v, want the refusal %q", dgot, dwhy, derr, c.want)
			}
			calls := r.read("docker.calls")
			for _, made := range []string{"buildx", "network create", "--name fyr-work-"} {
				if strings.Contains(calls, made) {
					t.Errorf("Check ran docker %s:\n%s", made, calls)
				}
			}
			if _, err := os.Lstat(sandbox.Root(o.Home, "work")); !os.IsNotExist(err) {
				t.Errorf("Check made the sandbox root: %v", err)
			}
		})
	}
}

// fyr auto's probe, DockerPreflight, neither builds nor predicts: a stale
// image a run would build first is its refusal, so auto's sweep runs on
// the host (ADR-504; ADR-504a changed --check only).
func TestDockerPreflightPredictsNothing(t *testing.T) {
	r, o := predictRig(t)
	r.on("docker-version", "vold")
	if err := DockerPreflight(o); err == nil || !strings.Contains(err.Error(), `holds fyr "vold"`) {
		t.Errorf("DockerPreflight on a stale image: %v, want its refusal", err)
	}
	auto := o
	auto.Runner = RunnerAuto
	if got, why := ChooseRunner(&auto, nil); got != RunnerHost || !strings.Contains(why, `holds fyr "vold"`) {
		t.Errorf("ChooseRunner with the default probe: %q %q", got, why)
	}
	if calls := r.read("docker.calls"); strings.Contains(calls, "buildx") || strings.Contains(calls, "version --format") {
		t.Errorf("the probe built or predicted an image:\n%s", calls)
	}
}

// --check's prediction is the run's verdict (ADR-504a). Each arrangement
// asks Check, auto and an explicit docker, then the run's preflight
// (buildFirst) on the same rig: where the run's build refuses before it
// builds, --check refuses in the same words, and the image a run builds is
// of the daemon's architecture, the one --check checked the linux agy
// against, whatever DOCKER_DEFAULT_PLATFORM or the stale image say; the
// version check after the build asks for no platform, so the fake daemon,
// which refuses another one, runs it (sandbox.DockerEnv).
func TestCheckMatchesRun(t *testing.T) {
	built := "runner docker (fyr-work:local will be built first)"
	aside := func(t *testing.T, r *rig, dir string) { // dir, through a symlink
		t.Helper()
		to := filepath.Join(r.tmp, "aside-"+filepath.Base(dir))
		if err := os.Rename(dir, to); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(to, dir); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, refusal string // refusal: "" when the run builds and passes
		setup         func(t *testing.T, r *rig, o *Options)
	}{
		{"default-platform-missing", "", func(t *testing.T, r *rig, o *Options) {
			t.Setenv("DOCKER_DEFAULT_PLATFORM", "linux/"+otherArch())
			r.on("docker-noimage", "")
		}},
		{"default-platform-stale", "", func(t *testing.T, r *rig, o *Options) {
			t.Setenv("DOCKER_DEFAULT_PLATFORM", "linux/"+otherArch())
			r.on("docker-version", "vold")
		}},
		{"stale-other-arch", "", func(t *testing.T, r *rig, o *Options) {
			r.on("docker-version", "vold")
			r.on("docker-arch", otherArch())
			r.on("docker-server-arch", runtime.GOARCH)
		}},
		{"config-symlink", "/.config is not a regular directory", func(t *testing.T, r *rig, o *Options) {
			r.on("docker-noimage", "")
			aside(t, r, filepath.Join(o.Home, ".config"))
		}},
		{"fyr-symlink", "/.config/fyr is not a regular directory", func(t *testing.T, r *rig, o *Options) {
			r.on("docker-version", "vold")
			aside(t, r, filepath.Join(o.Home, harness.StateDir))
		}},
		// Both fail: the build asks the daemon before its work directory.
		{"version-fails-first", "docker version: exit status 1", func(t *testing.T, r *rig, o *Options) {
			r.on("docker-noimage", "")
			r.on("docker-server-fail", "")
			aside(t, r, filepath.Join(o.Home, ".config"))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, o := predictRig(t)
			c.setup(t, r, &o)
			auto := o
			auto.Runner = RunnerAuto
			got, why, err := Check(auto)
			dgot, dwhy, derr := Check(o)
			if calls := r.read("docker.calls"); strings.Contains(calls, "buildx") {
				t.Fatalf("Check built an image:\n%s", calls)
			}
			o.build = buildFirst
			rerr := DockerPreflight(o)
			if c.refusal == "" {
				if err != nil || RunnerLabel(got, why) != built || derr != nil || RunnerLabel(dgot, dwhy) != built {
					t.Errorf("auto: %q %q %v; explicit docker: %q %q %v; want %q", got, why, err, dgot, dwhy, derr, built)
				}
				if args := r.read("docker-build-args"); rerr != nil || !strings.Contains(args, "--platform linux/"+runtime.GOARCH) {
					t.Errorf("the run: %v, its build %q; want it to pass, built for %s", rerr, args, runtime.GOARCH)
				}
				return
			}
			if rerr == nil || !strings.Contains(rerr.Error(), c.refusal) || strings.Contains(r.read("docker.calls"), "buildx") {
				t.Fatalf("the run: %v; want its refusal %q, before it builds", rerr, c.refusal)
			}
			if derr == nil || derr.Error() != rerr.Error() || dgot != "" || dwhy != "" {
				t.Errorf("explicit docker: %q %q %v; want the run's refusal %q", dgot, dwhy, derr, rerr)
			}
			if err != nil || got != RunnerHost || why != trimDockerErr(rerr.Error()) {
				t.Errorf("auto: %q %q %v; want the host, saying the run's refusal", got, why, err)
			}
		})
	}
}
