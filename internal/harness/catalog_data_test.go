package harness

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestCatalogMatchesChecksums pins the vendored tree to what the refresh
// fetched, in both directions: a changed or added file has no matching line,
// and a line with no file is a deletion.
func TestCatalogMatchesChecksums(t *testing.T) {
	dir := catalogDir(t)
	catalogEntries(t, dir)
	got := catalogSums(t, dir)
	file := filepath.Join(dir, "CHECKSUMS")
	if catalogWriteMode(t) {
		paths := make([]string, 0, len(got))
		for p := range got {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		var b strings.Builder
		for _, p := range paths {
			fmt.Fprintf(&b, "%s  %s\n", got[p], p)
		}
		if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("no CHECKSUMS: %v", err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		sum, path, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 {
			t.Fatalf("CHECKSUMS line is not `<sha256>  <path>`: %q", line)
		}
		want[path] = sum
	}
	for p, sum := range got {
		switch w, ok := want[p]; {
		case !ok:
			t.Errorf("%s is in the catalog and not in CHECKSUMS", p)
		case w != sum:
			t.Errorf("%s differs from its CHECKSUMS line: the vendored file was edited", p)
		}
	}
	for p := range want {
		if _, ok := got[p]; !ok {
			t.Errorf("CHECKSUMS names %s, which is not in the catalog", p)
		}
	}
}

// TestCatalogIndexMatchesEntries re-derives the index from every entry's
// frontmatter through the installer's own parser: a missing line, an extra
// line and a stale description all differ from it.
func TestCatalogIndexMatchesEntries(t *testing.T) {
	dir := catalogDir(t)
	entries := catalogEntries(t, dir)
	var lines []string
	for _, name := range entries {
		data, err := os.ReadFile(filepath.Join(dir, name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		front, _, err := Split(string(data))
		if err != nil {
			t.Fatalf("catalog entry %s: SKILL.md has no frontmatter: %v", name, err)
		}
		fm := Parse(front)
		if fm["name"] == "" || fm["description"] == "" {
			t.Fatalf("catalog entry %s: SKILL.md frontmatter lacks name or description", name)
		}
		lines = append(lines, catalogIndexLine(name, fm["description"]))
	}
	want := catalogIndexHeader + strings.Join(lines, "\n") + "\n"
	file := filepath.Join(dir, "INDEX.md")
	if catalogWriteMode(t) {
		if err := os.WriteFile(file, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("no INDEX.md: %v", err)
	}
	if string(data) == want {
		return
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(data), "\n") {
		have[l] = true
	}
	wanted := map[string]bool{}
	for _, l := range strings.Split(want, "\n") {
		wanted[l] = true
		if !have[l] {
			t.Errorf("INDEX.md lacks the line %q", l)
		}
	}
	for l := range have {
		if !wanted[l] {
			t.Errorf("INDEX.md has a line no entry produces: %q", l)
		}
	}
	if !t.Failed() {
		t.Error("INDEX.md holds the right lines in the wrong order or count")
	}
}

// TestCatalogFilesAreCarryable: what the harness cannot carry byte-identical or
// would mis-handle is refused at the source — an executable bit (the embed has
// none, BUG-137), a symlink (the installer refuses it), and a Go file or module
// file (the Go tool would compile the one and the embed would drop the
// directory for the other).
func TestCatalogFilesAreCarryable(t *testing.T) {
	dir := catalogDir(t)
	catalogEntries(t, dir)
	// The search marker travels with the catalog: without it `fyr search`
	// indexes the entries and the checkout's own files drop in the hits.
	if _, err := os.Stat(filepath.Join(dir, ".fyr-noindex")); err != nil {
		t.Errorf("the catalog has no .fyr-noindex marker: %v", err)
	}
	root := dir
	if os.Getenv("CATALOG_DIR") == "" {
		root = filepath.Dir(dir) // the whole router skill, its scripts included
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			t.Errorf("%s is a symlink", rel)
		case d.IsDir():
		case info.Mode()&0o111 != 0:
			t.Errorf("%s has an executable bit", rel)
		case d.Name() == "go.mod" || d.Name() == "go.sum" || strings.HasSuffix(d.Name(), ".go"):
			t.Errorf("%s is a Go file inside the module", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestCatalogExcludedEntriesAreAbsent: an entry the security review excluded
// stays out — the manifest's list is what the refresh applies, and a refresh
// that brought one back fails here.
func TestCatalogExcludedEntriesAreAbsent(t *testing.T) {
	dir := catalogDir(t)
	catalogEntries(t, dir)
	for _, name := range catalogExcluded(t, dir) {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s is listed under ## Excluded in MANIFEST.md and is in the catalog", name)
		}
	}
}

// TestCatalogWriteModeNeedsCatalogDir: CATALOG_WRITE=1 alone must not rewrite
// the checked-in CHECKSUMS.
func TestCatalogWriteModeNeedsCatalogDir(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a nested go test")
	}
	if os.Getenv("CATALOG_WRITE") == "1" {
		t.Skip("inside the write mode itself")
	}
	file := filepath.Join(catalogDir(t), "CHECKSUMS")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.WriteFile(file, before, 0o644) }) // a broken guard must not leave the file rewritten
	cmd := exec.Command("go", "test", ".", "-run", "^TestCatalogMatchesChecksums$", "-count=1")
	cmd.Env = fixtureEnv("CATALOG_WRITE=1", "CATALOG_DIR=")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "needs CATALOG_DIR") {
		t.Errorf("CATALOG_WRITE=1 without CATALOG_DIR was accepted (err %v):\n%s", err, out)
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
		t.Error("the checked-in CHECKSUMS was rewritten")
	}
	// The other half of the pair: CATALOG_DIR without the write mode would
	// compare some other tree and pass for a catalog that is not this one.
	cmd = exec.Command("go", "test", ".", "-run", "^TestCatalogMatchesChecksums$", "-count=1")
	cmd.Env = fixtureEnv("CATALOG_WRITE=", "CATALOG_DIR="+t.TempDir())
	out, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "only for the refresh script's write mode") {
		t.Errorf("CATALOG_DIR without CATALOG_WRITE=1 was accepted (err %v):\n%s", err, out)
	}
}
