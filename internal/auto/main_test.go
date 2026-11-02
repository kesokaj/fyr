package auto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain drops every GIT_* variable, as the other packages' TestMain does:
// the fixtures commit and push, and one exported by a hook would aim them at
// this checkout. Nor does the operator's git config reach them.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			os.Unsetenv(k)
		}
	}
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// macOS keeps TMPDIR under /var, a link to /private/var: resolved, a
	// t.TempDir path compares equal to the canonical one git and the code
	// under test report (PLAN-2026-11-02-1806-fyr-speed item 3).
	if tmp, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		os.Setenv("TMPDIR", tmp)
	}
	os.Exit(m.Run())
}
