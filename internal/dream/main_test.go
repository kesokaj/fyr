package dream

import (
	"os"
	"strings"
	"testing"
)

// TestMain drops every GIT_* variable, as the other packages' TestMain does:
// the collector runs git in fixture checkouts.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			os.Unsetenv(k)
		}
	}
	os.Exit(m.Run())
}
