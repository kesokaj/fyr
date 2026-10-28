package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// A row of the global index whose checkout no longer carries this home's
// marker is dropped on the next --global, with a note, and answers nothing:
// an opt-out (the marker deleted), and a home whose key is gone, which
// unregisters every checkout at once (ADR-068, ADR-068a, audit-choices D3).
func TestGlobalDropsUnregisteredRow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		unreg func(t *testing.T, home, root string)
	}{
		{"marker deleted", func(t *testing.T, _, root string) {
			if err := os.Remove(filepath.Join(root, ".git", "fyr-registered")); err != nil {
				t.Fatal(err)
			}
		}},
		{"key deleted", func(t *testing.T, home, _ string) {
			if err := os.Remove(filepath.Join(home, harness.StateDir, "registration.key")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := globalHome(t)
			a := newRepo(t)
			writeFile(t, a, ".agents/docs/bugs/BUG-003-ibis.md", "---\nid: BUG-003\nstatus: open\n---\n# the ibis failure\n")
			commitAll(t, a, "add")
			markRegistered(t, home, a)
			mustSearch(t, Options{Dir: a, Home: home, Words: []string{"ibis"}})
			if out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"ibis"}}); !strings.Contains(out, "BUG-003") {
				t.Fatalf("the registered checkout did not answer:\n%s", out)
			}
			tc.unreg(t, home, a)
			out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"ibis"}})
			if !strings.Contains(out, "note: dropped "+a+" (not registered: fyr index --register)") || strings.Contains(out, "BUG-003") {
				t.Errorf("unregistered row:\n%s", out)
			}
			if out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"ibis"}}); strings.Contains(out, "dropped") {
				t.Errorf("dropped twice:\n%s", out)
			}
		})
	}
}
