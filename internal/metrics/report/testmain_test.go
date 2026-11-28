package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain drops every GIT_* variable before the package's tests run so fixture
// git invocations never leak into an enclosing repository or user git config.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			os.Unsetenv(k)
		}
	}
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	if tmp, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		os.Setenv("TMPDIR", tmp)
	}
	os.Exit(m.Run())
}
