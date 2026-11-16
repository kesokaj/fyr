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
	if got := byPrefix(b.agyEnv(), "TZ="); !slices.Equal(got, []string{"TZ=Europe/Stockholm"}) {
		t.Errorf("agyEnv: TZ entries %q, want [TZ=Europe/Stockholm]", got)
	}
	if got := byPrefix(b.agyEnv(), "TMPDIR="); !slices.Equal(got, []string{"TMPDIR=" + filepath.Join(b.Dir(), "tmp")}) {
		t.Errorf("agyEnv TMPDIR %q, want %s/tmp", got, b.Dir())
	}
	b = New(Config{Dir: "/w/s1/w1"})
	if got := byPrefix(b.agyEnv(), "TZ="); len(got) != 0 {
		t.Errorf("agyEnv with no Zone: TZ entries %q, want none", got)
	}
}
