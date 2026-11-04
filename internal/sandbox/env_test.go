package sandbox

import (
	"slices"
	"strings"
	"testing"
)

// Config.Zone reaches agy as one TZ entry on either runner, the host's
// environment and the container's -e pairs; with no Zone, eval's case,
// neither holds a TZ, whatever the caller's own TZ (workers D144).
func TestAgyEnvZone(t *testing.T) {
	t.Setenv("TZ", "America/New_York")
	tzs := func(env []string) []string {
		return slices.DeleteFunc(slices.Clone(env), func(kv string) bool { return !strings.HasPrefix(kv, "TZ=") })
	}
	b := New(Config{Dir: "/w/s1/w1", Zone: "Europe/Stockholm"})
	for name, env := range map[string][]string{"agyEnv": b.agyEnv(), "containerEnv": b.containerEnv()} {
		if got := tzs(env); !slices.Equal(got, []string{"TZ=Europe/Stockholm"}) {
			t.Errorf("%s: TZ entries %q, want [TZ=Europe/Stockholm]", name, got)
		}
	}
	b = New(Config{Dir: "/w/s1/w1"})
	for name, env := range map[string][]string{"agyEnv": b.agyEnv(), "containerEnv": b.containerEnv()} {
		if got := tzs(env); len(got) != 0 {
			t.Errorf("%s with no Zone: TZ entries %q, want none", name, got)
		}
	}
}

// DockerEnv is FyrEnv minus DOCKER_DEFAULT_PLATFORM, which would make a
// docker run ask for a platform the image is not built for; the daemon's
// own settings stay, and FyrEnv, the build's, keeps it (ADR-504a).
func TestDockerEnv(t *testing.T) {
	t.Setenv("DOCKER_DEFAULT_PLATFORM", "linux/s390x")
	t.Setenv("DOCKER_HOST", "unix:///fake/docker.sock")
	t.Setenv("FYR_REPO", "/elsewhere")
	t.Setenv("GIT_DIR", "/elsewhere/.git")
	if !slices.Contains(FyrEnv(), "DOCKER_DEFAULT_PLATFORM=linux/s390x") {
		t.Fatal("FyrEnv lacks DOCKER_DEFAULT_PLATFORM: its absence below would prove nothing")
	}
	env := DockerEnv()
	if !slices.Contains(env, "DOCKER_HOST=unix:///fake/docker.sock") {
		t.Error("DockerEnv dropped DOCKER_HOST") // never env itself: it holds the caller's tokens
	}
	for _, k := range []string{"DOCKER_DEFAULT_PLATFORM=", "FYR_REPO=", "GIT_DIR="} {
		if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, k) }) {
			t.Errorf("DockerEnv keeps %s", k)
		}
	}
}
