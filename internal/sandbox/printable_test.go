package sandbox

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/search"
)

// TestPrintableMatchesSearch pins this package's copy of the printable
// filter to search.Printable, which it cannot import (D10).
func TestPrintableMatchesSearch(t *testing.T) {
	for _, s := range []string{"plain", "a\nb\rc\x1b[31m", "\u2028\u2029", "x\u202ey", "\ufe0f\u034f\u115f", "é ok", "\x7f\u0085"} {
		if got, want := Printable(s), search.Printable(s); got != want {
			t.Errorf("Printable(%q) = %q, search.Printable = %q", s, got, want)
		}
	}
}

// TestNoSQLiteDriver pins D10: internal/sandbox links no SQLite driver, as
// internal/eval, its importer, may not.
func TestNoSQLiteDriver(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list: %v", err)
	}
	if strings.Contains(string(out), "modernc.org/sqlite") {
		t.Error("internal/sandbox depends on modernc.org/sqlite (D10)")
	}
}
