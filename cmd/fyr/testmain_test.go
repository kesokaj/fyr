package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain runs as --kill's stand-in run when workHolderEnv is set; else it
// drops every GIT_* variable before the package's tests run. git
// exports GIT_DIR, GIT_INDEX_FILE and their kin inside a hook, and an
// operator may export them too; with one of them set, the fixtures' git
// calls, and the code under test that runs git in a fixture checkout, would
// act on another repository. A test that needs one sets it itself.
func TestMain(m *testing.M) {
	if root := os.Getenv(workHolderEnv); root != "" {
		os.Exit(workHolder(root))
	}
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
