package dream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/brain"
)

// Gate review of v3.0.0 (security round 2): the dry run's copy goes under
// ~/.config/fyr whenever that directory exists, store or not, so a killed
// run leaves nothing removeCopies cannot sweep; with no such directory it
// goes to the system's, and ~/.config/fyr is never created.
func TestBrainCopyStaysUnderTheStateDir(t *testing.T) {
	t.Run("state dir without a store", func(t *testing.T) {
		home := t.TempDir()
		conf := filepath.Dir(brain.Path(home))
		if err := os.MkdirAll(conf, 0o700); err != nil {
			t.Fatal(err)
		}
		tmp, cleanup, err := brainCopy(home)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		if filepath.Dir(tmp) != conf || !strings.HasPrefix(filepath.Base(tmp), "dream-brain-") {
			t.Errorf("copy at %s, want a dream-brain-* under %s", tmp, conf)
		}
	})
	t.Run("no state dir", func(t *testing.T) {
		home := t.TempDir()
		tmp, cleanup, err := brainCopy(home)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		if strings.HasPrefix(tmp, home) {
			t.Errorf("copy at %s, inside the home", tmp)
		}
		if _, err := os.Lstat(filepath.Dir(brain.Path(home))); !os.IsNotExist(err) {
			t.Error("a dry run created the state dir")
		}
	})
}
