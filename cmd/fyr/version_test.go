package main

import (
	"runtime/debug"
	"testing"
)

func TestShouldRebuild(t *testing.T) {
	cases := []struct{ goOnPath, child, own, want bool }{
		{true, false, true, true},
		{true, true, true, false},
		{false, false, true, false},
		{false, true, true, false},
		{true, false, false, false}, // a plain copy never rebuilds (BUG-015)
		{false, false, false, false},
	}
	for _, c := range cases {
		if got := shouldRebuild(c.goOnPath, c.child, c.own); got != c.want {
			t.Errorf("shouldRebuild(%v, %v, %v) = %v", c.goOnPath, c.child, c.own, got)
		}
	}
}

func TestBinaryOlder(t *testing.T) {
	cases := []struct {
		rev, commit string
		want        bool
	}{
		{"b64ffe886729", "b64ffe8", false},
		{"b64ffe8", "b64ffe886729", false},
		{"b64ffe886729+dirty", "b64ffe8", false},
		{"50ac771abcde", "b64ffe8", true},
		{"(devel)", "b64ffe8", true},
		{"unknown", "b64ffe8", true},
		{"", "b64ffe8", false},
		{"b64ffe886729", "", false},
	}
	for _, c := range cases {
		if got := binaryOlder(c.rev, c.commit); got != c.want {
			t.Errorf("binaryOlder(%q, %q) = %v", c.rev, c.commit, got)
		}
	}
}

func TestVersionFromInfo(t *testing.T) {
	stamp := func(rev, modified string) *debug.BuildInfo {
		return &debug.BuildInfo{
			Main:     debug.Module{Version: "(devel)"},
			Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: rev}, {Key: "vcs.modified", Value: modified}},
		}
	}
	cases := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"clean", stamp("b64ffe886729abcdef0123456789abcdef012345", "false"), "b64ffe886729"},
		{"dirty", stamp("b64ffe886729abcdef0123456789abcdef012345", "true"), "b64ffe886729+dirty"},
		{"short revision kept whole", stamp("b64ffe8", "false"), "b64ffe8"},
		{"no revision", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, "v1.2.3"},
	}
	for _, c := range cases {
		if got := versionFromInfo(c.info); got != c.want {
			t.Errorf("%s: versionFromInfo = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestReleaseFromInfo: a release build uses -trimpath, which keeps -ldflags
// out of the build info, so status reads an installed release binary's
// version from the module version Go stamps at a clean tag; anything else
// falls back to the revision.
func TestReleaseFromInfo(t *testing.T) {
	rev := debug.BuildSetting{Key: "vcs.revision", Value: "b64ffe886729abcdef"}
	cases := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"built at a clean tag", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}, Settings: []debug.BuildSetting{rev, {Key: "vcs.modified", Value: "false"}}}, "v1.2.3"},
		{"go install at a tag", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, "v1.2.3"},
		{"pseudo-version", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.4-0.20260917090000-b64ffe886729+dirty"}, Settings: []debug.BuildSetting{rev, {Key: "vcs.modified", Value: "true"}}}, "b64ffe886729+dirty"},
	}
	for _, c := range cases {
		if got := releaseFromInfo(c.info); got != c.want {
			t.Errorf("%s: releaseFromInfo = %q, want %q", c.name, got, c.want)
		}
	}
}
