package trail

import (
	"slices"
	"testing"
)

// TestAllowEnv: exact names, a `*` prefix, env's order kept, and nothing
// else passes, a name that merely starts with a listed one included.
func TestAllowEnv(t *testing.T) {
	env := []string{"TZ=UTC", "PATHEXT=x", "GEMINI_API_KEY=k", "LC_ALL=C", "PATH=/bin", "LANGUAGE=sv", "LC_TIME=sv_SE", "LANG=C.UTF-8", "noequals", "HOME=/h"}
	got := AllowEnv(env, "PATH", "LANG", "LC_*", "TZ")
	want := []string{"TZ=UTC", "LC_ALL=C", "PATH=/bin", "LC_TIME=sv_SE", "LANG=C.UTF-8"}
	if !slices.Equal(got, want) {
		t.Errorf("AllowEnv = %q, want %q", got, want)
	}
	if got := AllowEnv(env); len(got) != 0 {
		t.Errorf("no names kept %q", got)
	}
	// The result never shares env's backing array, so appending to it
	// cannot overwrite the caller's slice.
	if got := AllowEnv(env[:1], "TZ"); &got[0] == &env[0] {
		t.Error("AllowEnv aliases its input")
	}
}
