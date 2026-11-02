package brain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain drops every GIT_* variable before the package's tests run, as
// the other packages that run git in a fixture do: with GIT_DIR set by a hook
// or the operator, a fixture's git calls would act on another repository.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			os.Unsetenv(k)
		}
	}
	// Nor the operator's git config: a global hooksPath or signing key
	// never reaches a fixture commit (the v5.0.0 gate's security review).
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
