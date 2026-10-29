package main

import (
	"os"
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
	os.Exit(m.Run())
}
