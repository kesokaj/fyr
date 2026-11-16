package isolate

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAgyEnvZone(t *testing.T) {
	t.Setenv("TZ", "America/New_York")
	byPrefix := func(env []string, pfx string) []string {
		return slices.DeleteFunc(slices.Clone(env), func(kv string) bool { return !strings.HasPrefix(kv, pfx) })
	}
	b := New(Config{Dir: "/w/s1/w1", Zone: "Europe/Stockholm"})
	for name, env := range map[string][]string{"agyEnv": b.agyEnv(), "containerEnv": b.containerEnv()} {
		if got := byPrefix(env, "TZ="); !slices.Equal(got, []string{"TZ=Europe/Stockholm"}) {
			t.Errorf("%s: TZ entries %q, want [TZ=Europe/Stockholm]", name, got)
		}
	}
	if got := byPrefix(b.agyEnv(), "TMPDIR="); !slices.Equal(got, []string{"TMPDIR=" + filepath.Join(b.Dir(), "tmp")}) {
		t.Errorf("agyEnv TMPDIR %q, want %s/tmp", got, b.Dir())
	}
	if got := byPrefix(b.containerEnv(), "TMPDIR="); !slices.Equal(got, []string{"TMPDIR=/tmp"}) {
		t.Errorf("containerEnv TMPDIR %q, want [TMPDIR=/tmp]", got)
	}
	b = New(Config{Dir: "/w/s1/w1"})
	for name, env := range map[string][]string{"agyEnv": b.agyEnv(), "containerEnv": b.containerEnv()} {
		if got := byPrefix(env, "TZ="); len(got) != 0 {
			t.Errorf("%s with no Zone: TZ entries %q, want none", name, got)
		}
	}
}

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
		t.Error("DockerEnv dropped DOCKER_HOST")
	}
	for _, k := range []string{"DOCKER_DEFAULT_PLATFORM=", "FYR_REPO=", "GIT_DIR="} {
		if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, k) }) {
			t.Errorf("DockerEnv keeps %s", k)
		}
	}
}
