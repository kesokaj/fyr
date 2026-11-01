package auto

import (
	"os"
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
	os.Exit(m.Run())
}
