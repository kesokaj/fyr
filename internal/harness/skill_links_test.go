package harness

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestSkillLinksResolve: every relative link and every backticked
// references/… or assets/… path in a skill's markdown names a file that
// exists. The golang skill once carried about thirty pointers to files that
// were never written (audit 2026-09-30, TS2); a pointer to a missing file
// sends the agent to improvise from memory. Skills vendored verbatim are
// skipped: their links are upstream's, and the fix there is a re-fetch.
func TestSkillLinksResolve(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	vendored := vendoredSkills(t, repo)
	forks := forkSkills(t, repo)
	skills := filepath.Join(repo, "skills")
	mdLink := regexp.MustCompile(`\]\(([^)\s]+)\)`)
	// A backticked path: references/x.md inside the skill, or <skill>/references/x.md
	// when one skill points into another; resolved against the file's directory,
	// then the skill's root, then skills/.
	tickPath := regexp.MustCompile("`((?:\\./)?(?:[a-z0-9-]+/)?(?:references|assets|examples|scripts)/[^`\\s]+)`")
	fence := regexp.MustCompile("(?s)```.*?```")
	span := regexp.MustCompile("`[^`\n]*`")
	var missing []string
	err = filepath.WalkDir(skills, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(skills, path)
		skill := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		if rel == "VENDORED.md" {
			return nil // the manifest names other skills' files by design
		}
		if isVendoredPath(vendored, filepath.ToSlash(rel)) && !forks[skill] {
			return nil // vendored verbatim, a whole skill or a subtree: upstream's links are upstream's
		}
		if forks[skill] {
			// A fork kept under its upstream name: only its SKILL.md is ours to check;
			// references/ and assets/ are upstream's text, dangling links included.
			if rel != filepath.Join(skill, "SKILL.md") {
				return nil
			}
		} else if vendored[skill] {
			return nil // vendored verbatim: upstream's links are upstream's
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dir := filepath.Dir(path)
		seen := map[string]bool{}
		check := func(target string) {
			target = strings.TrimSpace(target)
			switch {
			case target == "", strings.HasPrefix(target, "#"), strings.Contains(target, "://"),
				strings.HasPrefix(target, "mailto:"), strings.ContainsAny(target, "<>*"),
				strings.Contains(target, "$"), strings.Contains(target, "{"):
				return
			}
			if i := strings.IndexByte(target, '#'); i >= 0 {
				target = target[:i]
			}
			// A path ending in "/" names a directory; a bare directory is fine too.
			if seen[target] {
				return
			}
			seen[target] = true
			for _, base := range []string{dir, filepath.Join(skills, skill), skills} {
				if _, err := os.Stat(filepath.Join(base, target)); err == nil {
					return
				}
			}
			missing = append(missing, rel+" → "+target)
		}
		prose := fence.ReplaceAllString(string(data), "")
		// A markdown link inside a code span or fence is an example of the
		// format, not a pointer (`- [Title](file.md) — hook`).
		for _, m := range mdLink.FindAllStringSubmatch(span.ReplaceAllString(prose, ""), -1) {
			check(m[1])
		}
		for _, m := range tickPath.FindAllStringSubmatch(prose, -1) {
			// Only paths that look like a file (have an extension in the last segment).
			if filepath.Ext(m[1]) == "" {
				continue
			}
			check(m[1])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("dangling skill link: %s", m)
	}
}

// forkSkills reads VENDORED.md's second table (the forks, under their upstream names
// or a new one, headed "Publisher / author"): their SKILL.md is the harness's own text
// and is link-checked; their references/ and assets/ are not.
func forkSkills(t *testing.T, repo string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, "skills", "VENDORED.md"))
	if err != nil {
		t.Fatalf("reading VENDORED.md: %v", err)
	}
	out := map[string]bool{}
	inForks := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		if strings.Contains(line, "Publisher / author") {
			inForks = true
			continue
		}
		if strings.Contains(line, "| Source |") {
			inForks = false
			continue
		}
		if !inForks {
			continue
		}
		cell := strings.TrimSpace(strings.Trim(strings.SplitN(line, "|", 3)[1], " `"))
		if cell == "" || strings.HasPrefix(cell, "-") {
			continue
		}
		if info, err := os.Stat(filepath.Join(repo, "skills", cell)); err == nil && info.IsDir() {
			out[cell] = true
		}
	}
	return out
}
