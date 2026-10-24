package styrman_test

import (
	"bytes"
	"testing"

	styrman "github.com/kesokaj/styrman-harness"
	"github.com/kesokaj/styrman-harness/internal/harness"
)

// TestEmbeddedPlanEqualsCheckout: a release binary installs exactly what a
// checkout at the same commit installs — the same paths, bytes and modes. It
// fails when a harness directory is not embedded, when embed drops a file, or
// when a file's mode depends on the disk (embed records every file as 0444, so
// an executable file in the tree would deploy without its execute bit from the
// binary).
func TestEmbeddedPlanEqualsCheckout(t *testing.T) {
	fromTree, err := harness.Plan(harness.DirSource("."), nil)
	if err != nil {
		t.Fatalf("Plan from the tree: %v", err)
	}
	fromEmbed, err := harness.Plan(harness.Source{FS: styrman.Files}, nil)
	if err != nil {
		t.Fatalf("Plan from the embedded files: %v", err)
	}
	if len(fromTree) == 0 {
		t.Fatal("the tree plans no files; the test ran in the wrong directory")
	}
	embedded := map[string]harness.File{}
	for _, f := range fromEmbed {
		embedded[f.Rel] = f
	}
	for _, want := range fromTree {
		got, ok := embedded[want.Rel]
		switch {
		case !ok:
			t.Errorf("~/%s is planned from the tree, not from the embedded files", want.Rel)
		case !bytes.Equal(got.Data, want.Data):
			t.Errorf("~/%s differs in bytes", want.Rel)
		case got.Mode != want.Mode:
			t.Errorf("~/%s mode %v from the embedded files, %v from the tree", want.Rel, got.Mode, want.Mode)
		}
		delete(embedded, want.Rel)
	}
	for rel := range embedded {
		t.Errorf("~/%s is planned from the embedded files, not from the tree", rel)
	}
}
