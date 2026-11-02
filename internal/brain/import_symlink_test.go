package brain

import (
	"os"
	"path/filepath"
	"testing"
)

// BUG-503: a docs root named from a checkout reached through a symlink
// (macOS's /var is /private/var; any linked workspace) was joined to the
// unresolved working directory and compared with git's resolved top level,
// so a directory inside the checkout was refused as outside it.
func TestImportDocsRootThroughLinkedCheckout(t *testing.T) {
	dir := repo(t)
	for rel, text := range map[string]string{
		"trail/plans/2026-01-01-open.md":  "# Open\n\n- [ ] left\n\nsee `testing/2026-01-02-log.md`\n",
		"trail/testing/2026-01-02-log.md": "# Log\n\nPASS\n",
	} {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "trail")
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{"trail", filepath.Join(link, "trail")} {
		res, err := Import(opts(link, t.TempDir()), arg, true)
		if err != nil {
			t.Fatalf("import %s from the linked checkout: %v", arg, err)
		}
		if got := importedIDs(res); len(got) != 2 {
			t.Errorf("import %s: want the open plan and the log it cites, got %v", arg, keys(got))
		}
	}
	// A docs root that is itself a link is still refused.
	if err := os.Symlink(filepath.Join(dir, "trail"), filepath.Join(dir, "linked-trail")); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(opts(link, t.TempDir()), "linked-trail", true); err == nil {
		t.Error("a symlinked docs root was accepted")
	}
}
