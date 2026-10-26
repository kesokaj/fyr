package harness

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// vendoredSkills reads skills/VENDORED.md and returns the skill directories
// listed there. Vendored text is never edited in place (AGENTS.md), so a
// prose check that walks it would report a defect nobody may fix; the fix
// for those is a re-fetch or a fork under another name.
func vendoredSkills(t *testing.T, repo string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, "skills", "VENDORED.md"))
	if err != nil {
		t.Fatalf("reading VENDORED.md: %v", err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cell := strings.TrimSpace(strings.Trim(strings.SplitN(strings.TrimSpace(line), "|", 3)[1], " `"))
		if cell == "" || strings.Contains(cell, " ") || strings.HasPrefix(cell, "-") {
			continue
		}
		if info, err := os.Stat(filepath.Join(repo, "skills", cell)); err == nil && info.IsDir() {
			out[cell] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("VENDORED.md named no skill directory; the parser or the file changed shape")
	}
	return out
}

// isVendoredPath reports whether rel, a slash-separated path relative to
// skills/, is a vendored key of VENDORED.md or lies under one: a skill
// vendored whole (`grilling`) or a vendored subtree inside a harness skill
// (`google-catalog/catalog`), whose prose is upstream's and is not checked.
func isVendoredPath(vendored map[string]bool, rel string) bool {
	for key := range vendored {
		if rel == key || strings.HasPrefix(rel, key+"/") {
			return true
		}
	}
	return false
}

// TestNoTmpInHarnessProse guards the scratch rule at the source: the
// context, the agents, the skills — their SKILL.md AND their references,
// where the rule drifted unseen (BUG-019) — the commands, the rules, and the
// repo docs may mention /tmp only to forbid it. Four records (BUG-003,
// BUG-007, BUG-014, BUG-019) are the reason this is a test and not a
// reminder. Vendored skills are exempt: their text may not be edited here.
func TestNoTmpInHarnessProse(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{"context", "agents", "commands", "rules", "README.md", "AGENTS.md"}
	vendored := vendoredSkills(t, repo)
	dirs, _ := filepath.Glob(filepath.Join(repo, "skills", "*"))
	for _, d := range dirs {
		info, err := os.Stat(d)
		if err != nil || !info.IsDir() || vendored[filepath.Base(d)] {
			continue
		}
		rel, _ := filepath.Rel(repo, d)
		roots = append(roots, rel)
	}
	allowed := func(line string) bool {
		l := strings.ToLower(line)
		return strings.Contains(l, "never /tmp") || strings.Contains(l, "never `/tmp`") ||
			strings.Contains(l, "not /tmp") || strings.Contains(l, "not `/tmp`") || strings.Contains(l, "instead of `/tmp`")
	}
	for _, root := range roots {
		err := filepath.WalkDir(filepath.Join(repo, root), func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				// A vendored subtree inside a harness skill is upstream's text.
				if rel, rerr := filepath.Rel(filepath.Join(repo, "skills"), path); rerr == nil && isVendoredPath(vendored, filepath.ToSlash(rel)) {
					return filepath.SkipDir
				}
			}
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, "/tmp") && !allowed(line) {
					rel, _ := filepath.Rel(repo, path)
					t.Errorf("%s:%d mentions /tmp outside a 'never /tmp' clause: %s", rel, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestVendoredRowsCarryAPin: every skill VENDORED.md lists names the
// repository it came from and a pin (a commit or a digest), so the copy can
// be checked against upstream and a local edit told from upstream content.
// Two rows read "not recorded when vendored" for a year (project review
// 2026-09-30, security finding 4).
func TestVendoredRowsCarryAPin(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "skills", "VENDORED.md"))
	if err != nil {
		t.Fatal(err)
	}
	pin := regexp.MustCompile(`\b[0-9a-f]{7,40}\b|sha256:[0-9a-f]{6,}`)
	vendored := vendoredSkills(t, repo)
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cell := strings.TrimSpace(strings.Trim(strings.SplitN(strings.TrimSpace(line), "|", 3)[1], " `"))
		if !vendored[cell] {
			continue
		}
		seen[cell] = true
		if strings.Contains(line, "not recorded") || !strings.Contains(line, "github.com/") && !strings.Contains(line, "https://") {
			t.Errorf("%s: the row names no source repository: %s", cell, line)
		}
		if !pin.MatchString(line) {
			t.Errorf("%s: the row carries no commit or digest pin: %s", cell, line)
		}
	}
	for name := range vendored {
		if !seen[name] {
			t.Errorf("%s: listed by vendoredSkills but not seen in a row", name)
		}
	}
}
