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
			a := trailRepo(t)
			markRegistered(t, home, a)
			mustSearch(t, Options{Dir: a, Home: home, Words: []string{"ibis"}})
			if out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"ibis"}}); strings.Contains(out, "dropped") {
				t.Fatalf("a registered checkout was dropped:\n%s", out)
			}
			tc.unreg(t, home, a)
			out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"ibis"}})
			if !strings.Contains(out, "note: dropped "+a+" (not registered: fyr index --register)") {
				t.Errorf("unregistered row:\n%s", out)
			}
			if got := projectRoots(t, home); len(got) != 0 {
				t.Errorf("projects after the drop: %v", got)
			}
			if out := mustSearch(t, Options{Dir: t.TempDir(), Home: home, Global: true, Words: []string{"ibis"}}); strings.Contains(out, "dropped") {
				t.Errorf("dropped twice:\n%s", out)
			}
		})
	}
}
