package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The Google skills catalog (plan 2026-10-01-google-skills-catalog): the skills
// published by Google, vendored verbatim under skills/google-catalog/catalog/
// and reached through one router skill, because installed as global skills
// they push the harness's own skills out of the CLI's listing.
//
// CATALOG_DIR points the catalog tests at another directory and
// CATALOG_WRITE=1 makes the two derived files be written instead of compared:
// that is how scripts/refresh-catalog.sh builds INDEX.md and CHECKSUMS, so the
// index is derived by the installer's own frontmatter parser and nothing is
// parsed twice.

// catalogHarnessFiles are written by the harness, not vendored: CHECKSUMS does
// not cover them.
var catalogHarnessFiles = map[string]bool{"INDEX.md": true, "MANIFEST.md": true, "CHECKSUMS": true, ".fyr-noindex": true}

const catalogIndexHeader = "# Google skills catalog — index\n\nOne line per entry: the directory name under `catalog/`, then the start of its description. Grep this file for the product or task words, then read `catalog/<name>/SKILL.md`.\n\n"

// catalogWriteMode reports whether the derived files are to be written. That
// happens only on the refresh script's build directory: CATALOG_WRITE without
// CATALOG_DIR would regenerate the checked-in CHECKSUMS from whatever is on
// disk and turn the integrity test into its own answer (security review).
func catalogWriteMode(t *testing.T) bool {
	t.Helper()
	if os.Getenv("CATALOG_WRITE") != "1" {
		return false
	}
	if os.Getenv("CATALOG_DIR") == "" {
		t.Fatal("CATALOG_WRITE=1 needs CATALOG_DIR: the checked-in INDEX.md and CHECKSUMS are written only by the refresh script, into its build directory")
	}
	return true
}

func catalogDir(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("CATALOG_DIR"); d != "" {
		if os.Getenv("CATALOG_WRITE") != "1" {
			// Compare mode on another tree would pass for a catalog that is
			// not the checked-in one (security review T1).
			t.Fatal("CATALOG_DIR is only for the refresh script's write mode (CATALOG_WRITE=1)")
		}
		return d
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(repo, "skills", "google-catalog", "catalog")
}

// catalogEntries are the directories holding a SKILL.md, sorted. An empty
// catalog fails the caller: a test over zero files proves nothing.
func catalogEntries(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	des, _ := os.ReadDir(dir)
	for _, de := range des {
		if !de.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, de.Name(), "SKILL.md")); err == nil {
			out = append(out, de.Name())
		} else {
			t.Errorf("catalog entry %s: no SKILL.md", de.Name())
		}
	}
	if len(out) == 0 {
		t.Fatalf("the catalog is empty: no entry with a SKILL.md under %s", dir)
	}
	sort.Strings(out)
	return out
}

// catalogSums maps every vendored file (slash path relative to the catalog)
// to its sha256.
func catalogSums(t *testing.T, dir string) map[string]string {
	t.Helper()
	sums := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if catalogHarnessFiles[rel] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		sums[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sums
}

// catalogIndexLine is one INDEX.md line: the entry's directory name and its
// description as Parse reads it, cut at 220 runes.
func catalogIndexLine(name, description string) string {
	r := []rune(description)
	if len(r) > 220 {
		r = r[:220]
	}
	return "- `" + name + "` — " + strings.TrimSpace(string(r))
}

// catalogExcluded reads the names under the manifest's "## Excluded".
func catalogExcluded(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "MANIFEST.md"))
	if err != nil {
		t.Fatalf("no MANIFEST.md: %v", err)
	}
	var out []string
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			in = line == "## Excluded"
		case in && strings.HasPrefix(line, "- `"):
			if name, _, ok := strings.Cut(line[3:], "`"); ok {
				out = append(out, name)
			}
		}
	}
	return out
}
