package work

import (
	"runtime"
	"strings"
	"testing"
)

// DockerPreflight caches a passing imageVersion check by (home, image ID), so
// repeated checks of the same content-addressed image ID do not spawn another
// container (PLAN-2026-11-02-1806-fyr-speed Item 30).
func TestDockerImageVersionCachedByID(t *testing.T) {
	r, o := dockerRig(t)
	o.AgyLinux = fakeELF(t, elfMachine[runtime.GOARCH])
	if err := DockerPreflight(o); err != nil {
		t.Fatalf("first DockerPreflight: %v", err)
	}
	if err := DockerPreflight(o); err != nil {
		t.Fatalf("second DockerPreflight: %v", err)
	}
	versionRuns := 0
	for _, l := range strings.Split(r.read("docker.calls"), "\n") {
		if strings.HasPrefix(l, "run --rm") && strings.HasSuffix(l, "fyr version") {
			versionRuns++
		}
	}
	if versionRuns != 1 {
		t.Errorf("imageVersion container ran %d times for the same image ID, want 1:\n%s", versionRuns, r.read("docker.calls"))
	}
}
