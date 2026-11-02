package serve

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
)

// hashTree is every regular file under root, by path, with its hash; .git's
// own files are git's.
func hashTree(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[p] = sha256.Sum256(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestPagesWriteNothing walks every read page and finds no file changed but
// the named caches: everything of the project outside .agents/scratch/,
// trail.jsonl by name, and a metrics.db already at the current schema.
func TestPagesWriteNothing(t *testing.T) {
	f := newFix(t)
	r := f.run(t)
	bdb := brain.Path(f.home)
	mdb := metrics.Path(f.home)
	for _, p := range []string{bdb, mdb} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("precondition: %s is missing: %v", p, err)
		}
	}
	other := filepath.Join(t.TempDir(), "outside")
	write(t, filepath.Join(other, "keep.txt"), "kept\n")
	before := map[string][32]byte{}
	for _, root := range []string{f.repo, f.home, other} {
		for p, h := range hashTree(t, root) {
			before[p] = h
		}
	}
	for _, p := range f.readPages() {
		if w := get(t, r, "GET", p); w.Code != 200 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	oOut := f.options(nil)
	oOut.Dir = other
	rOut, err := NewRun(oOut)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range f.readPages() {
		if w := get(t, rOut, "GET", p); w.Code != 200 {
			t.Fatalf("outside %s: %d", p, w.Code)
		}
	}
	scratch := filepath.Join(f.repo, ".agents", "scratch") + string(filepath.Separator)
	conf := filepath.Join(f.home, harness.StateDir) + string(filepath.Separator)
	after := map[string][32]byte{}
	for _, root := range []string{f.repo, f.home, other} {
		for p, h := range hashTree(t, root) {
			after[p] = h
		}
	}
	for p, h := range after {
		if before[p] == h {
			continue
		}
		cache := strings.HasPrefix(p, scratch) || (strings.HasPrefix(p, conf) && p != mdb)
		if !cache {
			t.Errorf("a read page changed or created %s", p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			t.Errorf("a read page removed %s", p)
		}
	}
	if before[bdb] != after[bdb] || before[mdb] != after[mdb] {
		t.Error("brain.db or metrics.db changed")
	}
}
