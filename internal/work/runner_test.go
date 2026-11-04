package work

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
