package main

import (
	"debug/elf"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/work"
)

// writeAgyLinux is a 64-byte linux amd64 ELF header, enough for the docker
// preflight's check of a given --agy-linux.
func writeAgyLinux(t *testing.T) string {
	t.Helper()
	var elfBin [64]byte
	copy(elfBin[:4], "\x7fELF")
	elfBin[4], elfBin[5], elfBin[6] = byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)
	elfBin[16], elfBin[18], elfBin[20], elfBin[52] = byte(elf.ET_EXEC), byte(elf.EM_X86_64), byte(elf.EV_CURRENT), 64
	p := filepath.Join(t.TempDir(), "agy-linux")
	if err := os.WriteFile(p, elfBin[:], 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// dockerCalls is what the rig's recording docker was asked, one call a line.
func (r *workRig) dockerCalls() string {
	calls, _ := os.ReadFile(filepath.Join(r.ctl, "docker.calls"))
	return string(calls)
}

// TestWorkVerbRunnerAutoDevel: the default --runner auto takes the docker
// runner's flags, and a development build, which docker refuses first, runs
// on the host saying so, for --check and a run alike, without asking docker
// (D5, ADR-504).
func TestWorkVerbRunnerAutoDevel(t *testing.T) {
	r := newWorkRig(t)
	devel := "runner host (this fyr is a development build ((devel)); run fyr install, then build the image from the installed fyr: " + work.BuildLine + ")"
	if out, code := r.work("--check", "--image", "x:1", "--agy-linux", "/x/agy"); code != 0 || out != "work: "+devel+"\n" {
		t.Fatalf("--check: exit %d\ngot:  %q\nwant: %q", code, out, "work: "+devel+"\n")
	}
	out, code := r.work("--image", "x:1", "--agy-linux", "/x/agy", r.kickoff("fix-a"))
	if lines := strings.Split(out, "\n"); code != 0 || !strings.HasSuffix(lines[0], "  "+devel) || !strings.Contains(out, "\nwork: fix-a  PASS  ") {
		t.Fatalf("run: exit %d, want 0 with a host PASS saying why:\n%s", code, out)
	}
	if calls := r.dockerCalls(); calls != "" {
		t.Errorf("a development build asked docker: %q", calls)
	}
}

// TestWorkVerbRunnerAutoInstalled: from an installed build, --runner auto is
// docker when the docker preflight passes and the host with its refusal
// when it does not, while an explicit --runner docker refuses with that
// refusal, exit 2 (D5, ADR-504).
func TestWorkVerbRunnerAutoInstalled(t *testing.T) {
	r := newWorkRig(t)
	old := version
	version = "vtest"
	t.Cleanup(func() { version = old })
	agyLinux := writeAgyLinux(t)
	if out, code := r.work("--check", "--image", "good:1", "--agy-linux", agyLinux); code != 0 || out != "work: runner docker\n" {
		t.Fatalf("--check, docker ready: exit %d, got %q", code, out)
	}
	missing := "runner host (--image other:1: not on this machine"
	if out, code := r.work("--check", "--image", "other:1"); code != 0 || !strings.HasPrefix(out, "work: "+missing) {
		t.Fatalf("--check, no image: exit %d, got %q, want %q...", code, out, "work: "+missing)
	}
	if out, code := r.work("--check", "--runner", "docker", "--image", "other:1"); code != 2 || !strings.HasPrefix(out, "work: --image other:1: not on this machine") {
		t.Fatalf("--check --runner docker, no image: exit %d, got %q", code, out)
	}
	out, code := r.work("--image", "other:1", r.kickoff("fix-a"))
	if lines := strings.Split(out, "\n"); code != 0 || !strings.Contains(lines[0], "  "+missing) || !strings.Contains(out, "\nwork: fix-a  PASS  ") {
		t.Fatalf("run, no image: exit %d, want 0 with a host PASS saying why:\n%s", code, out)
	}
	if calls := r.dockerCalls(); strings.Contains(calls, "network create") || strings.Contains(calls, "--name fyr-work-") {
		t.Errorf("a host fallback made a network or a session:\n%s", calls)
	}
	if _, code := r.work("--runner", "docker", "--image", "other:1", r.kickoff("fix-b")); code != 2 {
		t.Errorf("--runner docker, no image: exit %d, want 2", code)
	}
}

// TestWorkVerbCheckPredictsBuild: from an installed build with an install
// source, a missing default image is one a run builds first, so --check
// reads as docker and says so, for auto and an explicit docker alike, exit
// 0, asking docker its architecture and building nothing (ADR-504a).
func TestWorkVerbCheckPredictsBuild(t *testing.T) {
	r := newWorkRig(t)
	old := version
	version = "vtest"
	t.Cleanup(func() { version = old })
	agyLinux := writeAgyLinux(t)
	want := "work: runner docker (fyr-work:local will be built first)\n"
	for _, args := range [][]string{{"--check"}, {"--check", "--runner", "docker"}} {
		if out, code := r.work(append(args, "--agy-linux", agyLinux)...); code != 0 || out != want {
			t.Errorf("%v: exit %d, got %q, want %q", args, code, out, want)
		}
	}
	if calls := r.dockerCalls(); !strings.Contains(calls, "version --format") || strings.Contains(calls, "buildx") {
		t.Errorf("--check did not predict the build, or built:\n%s", calls)
	}
	if _, err := os.Lstat(sandbox.Root(r.home, "work")); !os.IsNotExist(err) {
		t.Errorf("--check made the sandbox root: %v", err)
	}

	// --check refuses a bad --repo or non-checkout cwd as a run does, and
	// `fyr work config --workers` repairs a malformed config.json.
	if out, code := r.work("--check", "--repo", filepath.Join(r.project, "missing")); code != 2 || !strings.Contains(out, "work: ") {
		t.Errorf("--check bad --repo: exit %d, got %q", code, out)
	}
	cfgPath := work.ConfigPath(r.home)
	os.MkdirAll(filepath.Dir(cfgPath), 0o700)
	os.WriteFile(cfgPath, []byte(`{"workers":99,"ram_gb":8,"cpus":2,"disk_gb":10}`), 0o600)
	if out, code := r.work("config"); code != 2 || !strings.Contains(out, cfgPath) {
		t.Errorf("config read on bad file: exit %d, got %q", code, out)
	}
	if out, code := r.work("config", "--workers", "6"); code != 0 || !strings.Contains(out, "workers=6") {
		t.Errorf("config repair on bad file: exit %d, got %q", code, out)
	}
	t.Chdir(t.TempDir())
	if out, code := r.work("--check"); code != 2 || !strings.Contains(out, "is not a git checkout") {
		t.Errorf("--check outside checkout: exit %d, got %q", code, out)
	}
}
