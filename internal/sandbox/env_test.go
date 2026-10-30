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
