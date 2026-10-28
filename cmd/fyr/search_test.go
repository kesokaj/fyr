package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestReorderSearchFlags(t *testing.T) {
	// --limit, --kind and --refs take values: were reorder not told so they
	// would swallow the next word (BUG-002's shape).
	cases := map[string][]string{
		"search zip --kind bug verify --limit 3": {"--kind", "bug", "--limit", "3", "search", "zip", "verify"},
		"search --refs BUG-049 --global":         {"--refs", "BUG-049", "--global", "search"},
		"search --open --kind plan":              {"--open", "--kind", "plan", "search"},
		"index --rebuild --home /h":              {"--rebuild", "--home", "/h", "index"},
		"search -- --from-zip is a word":         {"--", "search", "--from-zip", "is", "a", "word"},
	}
	for in, want := range cases {
		if got := reorder(allFlags(), strings.Fields(in)); !reflect.DeepEqual(got, want) {
			t.Errorf("reorder(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearchVerb(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.invalid"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".agents/scratch/*\n"), 0o644)
	os.WriteFile(filepath.Join(root, "notes.md"), []byte("# Notes\n\nthe marmot decision\n"), 0o644)
	t.Chdir(root)
	home := t.TempDir()

	var out bytes.Buffer
	if err := run([]string{"search", "marmot", "--home", home}, &out); err != nil {
		t.Fatalf("search: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "notes.md:3  doc") {
		t.Errorf("search output:\n%s", out.String())
	}
	out.Reset()
	if err := run([]string{"index", "--home", home}, &out); err != nil {
		t.Fatalf("index: %v", err)
	}
	if !strings.Contains(out.String(), "files    2") {
		t.Errorf("index output:\n%s", out.String())
	}
	out.Reset()
	// A usage error: exit 2, the reason on the writer (BUG-168).
	err := run([]string{"search", "--limit", "99", "marmot", "--home", home}, &out)
	var code exitCode
	if !errors.As(err, &code) || code != 2 || !strings.Contains(out.String(), "--limit") {
		t.Errorf("--limit 99: err = %v, out = %q, want exit 2 naming --limit", err, out.String())
	}
	// --global runs from an unregistered checkout and creates the global
	// search index (registration is TestIndexRegisterWritesMarkerThenIndexes').
	out.Reset()
	if err := run([]string{"search", "--global", "marmot", "--home", home}, &out); err != nil {
		t.Fatalf("search --global: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir, "search.db")); err != nil {
		t.Errorf("global search index not created: %v", err)
	}
	if st, _ := exec.Command("git", "-C", root, "status", "--porcelain").Output(); strings.Contains(string(st), ".agents") {
		t.Errorf("the index dirtied the checkout: %s", st)
	}
}

// TestUsageMatchesDocComment: every verb the short page lists is in the
// package doc comment, and back.
func TestUsageMatchesDocComment(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^//\tfyr ([a-z]+)`).FindAllStringSubmatch(string(src), -1) {
		doc[m[1]] = true
	}
	use := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^  ([a-z]+) `).FindAllStringSubmatch(shortPage, -1) {
		use[m[1]] = true
	}
	keys := func(m map[string]bool) []string {
		var k []string
		for v := range m {
			k = append(k, v)
		}
		sort.Strings(k)
		return k
	}
	if !reflect.DeepEqual(keys(doc), keys(use)) {
		t.Errorf("doc comment verbs %v, usage verbs %v", keys(doc), keys(use))
	}
	for _, v := range []string{"search", "index"} {
		if !use[v] {
			t.Errorf("usage lacks %s", v)
		}
	}
}
