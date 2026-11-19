package search

import (
	"bytes"
	"strings"
	"testing"
)

func TestExtractMarkdownSymbolsAndSkeleton(t *testing.T) {
	md := `# Architecture Overview
Intro line before sections.

## Setup Steps
   ### Indented Subheading

` + "```bash\n# comment inside backtick fence should not be indexed\nfyr status\n```" + `

~~~markdown
## heading inside tilde fence should not be indexed
~~~

    # 4-space indented line should not be indexed

#### Deep Section
Final body line.
`
	syms, refs := extractMarkdownSymbols("docs/guide.md", md)
	_ = refs
	if len(syms) != 4 {
		t.Fatalf("got %d headings, want 4: %+v", len(syms), syms)
	}
	want := []struct {
		name, sig string
		start     int
		end       int
	}{
		{"Architecture Overview", "# Architecture Overview", 1, 3},
		{"Setup Steps", "## Setup Steps", 4, 4},
		{"Indented Subheading", "### Indented Subheading", 5, 17},
		{"Deep Section", "#### Deep Section", 18, 19},
	}
	for i, w := range want {
		got := syms[i]
		if got.name != w.name || got.sig != w.sig || got.kind != "heading" || got.pkg != "" || got.exported != 0 {
			t.Fatalf("sym[%d] = %+v, want name=%q sig=%q kind=heading pkg=\"\" exported=0", i, got, w.name, w.sig)
		}
		if got.startLine != w.start || got.endLine != w.end {
			t.Fatalf("sym[%d] span = %d..%d, want %d..%d", i, got.startLine, got.endLine, w.start, w.end)
		}
		if got.bodyHash == "" {
			t.Fatalf("sym[%d] missing bodyHash", i)
		}
	}

	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "pkg/svc.go", "package pkg\n\nfunc Run() error { return nil }\n")
	writeFile(t, root, "docs/guide.md", md)
	commitAll(t, root, "add go and md")
	var out bytes.Buffer
	if err := Index(Options{Dir: root, Home: home}, &out); err != nil {
		t.Fatalf("Index: %v", err)
	}
	skel := mustSearch(t, Options{Dir: root, Home: home, Skeleton: "docs/guide.md"})
	if !strings.Contains(skel, "# docs/guide.md") ||
		!strings.Contains(skel, "1-3  # Architecture Overview") ||
		!strings.Contains(skel, "5-17  ### Indented Subheading") ||
		!strings.Contains(skel, "18-19  #### Deep Section") {
		t.Fatalf("unexpected skeleton output:\n%s", skel)
	}
	if strings.Contains(skel, "comment inside backtick fence") || strings.Contains(skel, "heading inside tilde fence") || strings.Contains(skel, "4-space indented") {
		t.Fatalf("skeleton leaked non-heading lines:\n%s", skel)
	}
	var mapBuf bytes.Buffer
	if err := Search(Options{Dir: root, Home: home, Map: true}, &mapBuf); err != nil {
		t.Fatalf("Search map: %v", err)
	}
	if strings.Contains(mapBuf.String(), "docs (") {
		t.Fatalf("RepoMapSummary treated markdown dir as code package:\n%s", mapBuf.String())
	}
}
