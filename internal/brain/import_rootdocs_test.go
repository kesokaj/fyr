package brain

import (
	"os"
	"path/filepath"
	"testing"
)

// The import's reference closure starts from the root documents too
// (PLAN-2026-10-29-root-docs-and-fyr-serve, § Interfaces): a record named
// only in PRD.md, SDD.md or TDD.md is kept, and an unnamed one is dropped.
func TestImportKeepsWhatTheRootDocsName(t *testing.T) {
	for _, doc := range []string{"PRD.md", "SDD.md", "TDD.md"} {
		t.Run(doc, func(t *testing.T) {
			dir := repo(t)
			files := map[string]string{
				doc: "Decided in `.agents/docs/reviews/2026-01-04-named.md`.\n",
				".agents/docs/reviews/2026-01-04-named.md":   "# Review — named\n\nnothing\n",
				".agents/docs/reviews/2026-01-04-unnamed.md": "# Review — unnamed\n\nnothing\n",
			}
			for rel, text := range files {
				p := filepath.Join(dir, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			git(t, dir, "add", ".")
			git(t, dir, "commit", "-qm", "fixture")
			res, err := Import(opts(dir, t.TempDir()), "", false)
			if err != nil {
				t.Fatal(err)
			}
			got := importedIDs(res)
			if _, ok := got["REVIEW-2026-01-04-named"]; !ok {
				t.Errorf("the review %s names was not kept: %v", doc, keys(got))
			}
			if _, ok := got["REVIEW-2026-01-04-unnamed"]; ok {
				t.Errorf("the unnamed review was kept: %v", keys(got))
			}
		})
	}
}
