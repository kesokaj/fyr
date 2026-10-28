package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noted reports whether a note names the skipped-files count and p.
func noted(rep Report, p string) bool {
	for _, n := range rep.Notes {
		if strings.Contains(n, "tracked files skipped") && strings.Contains(n, p) {
			return true
		}
	}
	return false
}

// A tracked _test.go that is a link is not read for the test names rule (c)
// resolves, and the note names it (IN55 of the 2026-10-27 audit): the
// requirement it would have enforced reads as unresolved, never as clean.
func TestTrackedTestSymlinkIsSkippedWithNote(t *testing.T) {
	reqs := "# Requirements — x\n\n## Must — non-negotiable\n\n- **R1** — a rule.\n  <!-- provenance: op; enforced by: TestX -->\n"
	dir := repo(t, map[string]string{"REQUIREMENTS.md": reqs, "internal/real.txt": "package a\n\nfunc TestX(t *testing.T) {}\n"})
	if err := os.Symlink("real.txt", filepath.Join(dir, "internal", "link_test.go")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "internal/link_test.go")
	git(t, dir, "commit", "-q", "-m", "link")
	if fi, err := os.Lstat(filepath.Join(dir, "internal", "link_test.go")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the fixture's link is not there: %v", err)
	}
	rep := run(t, dir)
	same(t, rep.Findings, "REQUIREMENTS.md:5 requirement")
	if !noted(rep, "internal/link_test.go") {
		t.Errorf("notes %q do not name the skipped internal/link_test.go", rep.Notes)
	}
}

// An unreadable skills/VENDORED.md (a link, here) joins the skipped note,
// and the skills it would have named are checked as the project's own
// (IN56); a checkout with none gives no note. It is a tracked *.md, so the
// docs rule's read names it (IN53); vendoredSkips needs no note of its own,
// and this pins that the file never leaves the docs set unnoticed.
func TestUnreadableVendoredIsNoted(t *testing.T) {
	files := map[string]string{
		"skills/table.txt":         "| Skill | Source |\n|-------|--------|\n| grilling | x |\n",
		"skills/grilling/SKILL.md": "`fyr frobnicate`\n",
		"internal/a.go":            "package a\n",
	}
	dir := repo(t, files)
	if err := os.Symlink("table.txt", filepath.Join(dir, "skills", "VENDORED.md")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "skills/VENDORED.md")
	git(t, dir, "commit", "-q", "-m", "link")
	rep := run(t, dir)
	same(t, rep.Findings, "skills/grilling/SKILL.md:1 verb")
	if !noted(rep, "skills/VENDORED.md") {
		t.Errorf("notes %q do not name the skipped skills/VENDORED.md", rep.Notes)
	}
	if rep := run(t, repo(t, files)); noted(rep, "VENDORED") || len(rep.Findings) != 1 {
		t.Errorf("no VENDORED.md: notes %q, findings %+v; want no skip note and the one finding", rep.Notes, rep.Findings)
	}
}
