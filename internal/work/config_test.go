package work

import (
	"debug/elf"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/sandbox"
)

func TestDockerAutoBuildFromEmbedded(t *testing.T) {
	r, o := dockerRig(t)
	o.Image = ""
	o.Repo = filepath.Join(r.tmp, "repo")
	o.AgyLinux = fakeELF(t, elfMachine["arm64"])
	r.on("docker-arch", "arm64")
	put(t, filepath.Join(o.Repo, "go.mod"), "module example.com/test\n\ngo 1.26.2\n")

	// Read-only Check (buildPredict) does not invoke docker buildx build: a
	// missing default image a run builds first reads as docker, saying so,
	// for --runner auto and an explicit docker alike (ADR-504a).
	r.on("docker-noimage", "")
	r.on("docker-server-arch", "arm64")
	auto := o
	auto.Runner = RunnerAuto
	for _, c := range []Options{auto, o} {
		if got, why, err := Check(c); got != RunnerDocker || err != nil || RunnerLabel(got, why) != "runner docker (fyr-work:local will be built first)" {
			t.Fatalf("Check --runner %s with missing image: got %q %q, %v, want docker, its image built first", c.Runner, got, why, err)
		}
	}
	if buildArgs := r.read("docker-build-args"); buildArgs != "" {
		t.Fatalf("Check invoked docker buildx build: %q", buildArgs)
	}

	// Real run (buildFirst) builds missing image from embedded docker/work.Dockerfile
	// staged under o.Home (never /tmp).
	o.build = buildFirst
	if err := DockerPreflight(o); err != nil {
		t.Fatalf("auto-build missing image: %v", err)
	}
	buildArgs := r.read("docker-build-args")
	if !strings.Contains(buildArgs, "buildx build --load -f "+o.Home) || !strings.Contains(buildArgs, "FYR_VERSION=vtest") || !strings.Contains(buildArgs, "-t fyr-work:local "+o.Repo) {
		t.Errorf("buildx args = %q", buildArgs)
	}

	// Stale image version is rebuilt automatically from embedded docker/work.Dockerfile.
	r.on("docker-version", "vold")
	if err := DockerPreflight(o); err != nil {
		t.Fatalf("auto-rebuild stale image: %v", err)
	}

	// Build failure surfaces the build error and the build line, and leaves no work dir behind.
	r.on("docker-noimage", "")
	r.on("docker-build-fail", "")
	err := DockerPreflight(o)
	if err == nil || !strings.Contains(err.Error(), "auto-build failed") || !strings.Contains(err.Error(), "build failed") || !strings.Contains(err.Error(), BuildLine) {
		t.Errorf("build failure: %v, want auto-build failed + build failed + %q", err, BuildLine)
	}
}

func TestDockerMacKeychainAndRosetta(t *testing.T) {
	r, o := dockerRig(t)
	setHostOS(t, "darwin")
	t.Cleanup(sandbox.SetHostOS("darwin"))
	r.on("docker-arch", "arm64")
	o.AgyLinux = ""

	// Remove TokenRel and set up macOS Keychain secret.
	if err := os.Remove(filepath.Join(r.real, sandbox.TokenRel)); err != nil {
		t.Fatal(err)
	}
	mkdir(t, filepath.Join(r.real, filepath.FromSlash(sandbox.KeychainsRel)))
	put(t, filepath.Join(r.real, filepath.FromSlash(sandbox.KeychainsRel), "login.keychain-db"), "keychain")
	secret := "go-keyring-base64:" + base64.StdEncoding.EncodeToString([]byte(`{"access_token":"mac-tok","token_type":"Bearer"}`))
	t.Cleanup(sandbox.SetKeychainReader(func() (string, error) { return secret, nil }))

	workAgy := filepath.Join(r.real, filepath.FromSlash(sandbox.WorkAgyRel))
	mkdir(t, filepath.Dir(workAgy))

	// Both arm64 (EM_AARCH64) and x86_64 (EM_X86_64 via Rosetta) pass on darwin arm64.
	for _, mach := range []elf.Machine{elf.EM_AARCH64, elf.EM_X86_64} {
		src := fakeELF(t, mach)
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(workAgy, b, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := DockerPreflight(o); err != nil {
			t.Fatalf("darwin arm64 with %v agy-linux: %v", mach, err)
		}
	}

	// Unsupported ELF architecture (EM_RISCV) is still refused.
	src := fakeELF(t, elf.EM_RISCV)
	b, _ := os.ReadFile(src)
	os.WriteFile(workAgy, b, 0o755)
	if err := DockerPreflight(o); err == nil || !strings.Contains(err.Error(), "built for") {
		t.Errorf("EM_RISCV accepted: %v", err)
	}

	// Symlink at WorkAgyRel is refused.
	os.Remove(workAgy)
	if err := os.Symlink(src, workAgy); err != nil {
		t.Fatal(err)
	}
	if err := DockerPreflight(o); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("symlink at WorkAgyRel accepted: %v", err)
	}
}

func TestWorkConfigAndResourceClamping(t *testing.T) {
	home := outsideTemp(t)
	cfg, err := LoadConfig(home)
	if err != nil || cfg != DefaultConfig() {
		t.Fatalf("default LoadConfig: %+v, %v", cfg, err)
	}
	custom := Config{Workers: 12, RAMGB: 16, CPUs: 6, DiskGB: 20}
	if err := SaveConfig(home, custom); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := LoadConfig(home)
	if err != nil || got != custom {
		t.Fatalf("LoadConfig after SaveConfig: %+v, %v", got, err)
	}
	if want := "work config: workers=12  ram=16g  cpu=6  disk=20g"; got.String() != want {
		t.Errorf("String() = %q, want %q", got.String(), want)
	}

	for _, bad := range []Config{
		{Workers: 0, RAMGB: 8, CPUs: 2, DiskGB: 10},
		{Workers: 33, RAMGB: 8, CPUs: 2, DiskGB: 10},
		{Workers: 8, RAMGB: 1, CPUs: 2, DiskGB: 10},
		{Workers: 8, RAMGB: 65, CPUs: 2, DiskGB: 10},
		{Workers: 8, RAMGB: 8, CPUs: 0, DiskGB: 10},
		{Workers: 8, RAMGB: 8, CPUs: 17, DiskGB: 10},
		{Workers: 8, RAMGB: 8, CPUs: 2, DiskGB: 1},
		{Workers: 8, RAMGB: 8, CPUs: 2, DiskGB: 101},
	} {
		if err := SaveConfig(home, bad); err == nil {
			t.Errorf("SaveConfig accepted out-of-range %+v", bad)
		}
	}

	// Clamping to Docker VM limits (Colima 4 CPUs, 8 GiB RAM, and rounding MemTotal up to GiB).
	plan := newDockerPlan(imgID, "/opt/agy", Options{RAMGB: 16, CPUs: 8, DiskGB: 10}, 4, 8)
	if plan.cpus != 4 || plan.ramGB != 8 || plan.diskGB != 10 {
		t.Errorf("clamped plan = %+v, want cpus=4 ramGB=8 diskGB=10", plan)
	}

	r, _ := dockerRig(t)
	r.on("docker-info", "true true true true 4 8321499136\n")
	if ncpu, memGB, err := daemonLimits(); err != nil || ncpu != 4 || memGB != 8 {
		t.Errorf("daemonLimits(~7.75 GiB) = %d, %d, %v; want 4, 8, nil", ncpu, memGB, err)
	}
	r.on("docker-info", "true true true true 2 1986000000\n")
	if ncpu, memGB, err := daemonLimits(); err != nil || ncpu != 2 || memGB != 2 {
		t.Errorf("daemonLimits(~1.85 GiB) = %d, %d, %v; want 2, 2, nil", ncpu, memGB, err)
	}
	r.on("docker-info", "true true true true bad 1986000000\n")
	if _, _, err := daemonLimits(); err == nil {
		t.Error("daemonLimits accepted non-integer NCPU")
	}
}

func TestWorkerDiskLimit(t *testing.T) {
	oldUnit := diskUnitBytes
	diskUnitBytes = 10
	t.Cleanup(func() { diskUnitBytes = oldUnit })

	r := newRig(t)
	o := r.opts()
	o.DiskGB = 2
	res, err := runWith(o, r.kickoff("disk-over", "src/a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	wantReasons(t, res, ReasonDiskLimit)
	if !strings.Contains(strings.Join(res.Notes, "\n"), "over --disk 2g") {
		t.Errorf("missing disk limit note in %v", res.Notes)
	}

	// When the sandbox is not intact (!w.b.Intact()), checkDisk returns early without adding ReasonDiskLimit.
	w := &worker{
		o:   Options{DiskGB: 1},
		res: Result{Dir: r.tmp},
		b:   sandbox.New(sandbox.Config{Dir: filepath.Join(r.tmp, "unmade")}),
	}
	w.b.Fail(sandbox.ReasonReplaced)
	w.checkDisk()
	if got := w.b.Reasons(); len(got) != 1 || got[0] != sandbox.ReasonReplaced {
		t.Errorf("checkDisk on non-intact box: reasons = %v, want [%q]", got, sandbox.ReasonReplaced)
	}
}
