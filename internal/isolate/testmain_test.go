package isolate

import (
	"os"
	"strings"
	"testing"
)

// TestMain completes confined child init when FYR_ISOLATE_INIT=1 before
// scrubbing GIT_* variables for fixture isolation.
func TestMain(m *testing.M) {
	MaybeRunInit()
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			os.Unsetenv(k)
		}
	}
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Exit(m.Run())
}
