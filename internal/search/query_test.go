package search

import (
	"reflect"
	"strings"
	"testing"
)

func TestChunkMarkdownHeadingsAndWindows(t *testing.T) {
	text := "# Top\nintro\n```sh\n# not a heading\n```\n## Second\n" + strings.Repeat("line\n", 85)
	cs := chunkText("x.md", text)
	if len(cs) != 4 {
		t.Fatalf("want 4 chunks (Top, Second split into 3), got %d: %+v", len(cs), cs)
	}
	if cs[0].heading != "Top" || cs[0].line != 1 || cs[1].heading != "Second" || cs[1].line != 6 || cs[2].line != 46 || cs[3].line != 86 {
		t.Errorf("unexpected chunks: %+v", cs)
	}
	go_ := chunkText("x.go", strings.Repeat("x\n", 41))
	if len(go_) != 2 || go_[1].line != 41 || go_[0].heading != "" {
		t.Errorf("code windows: %+v", go_)
	}
}

func TestRecordsBugADRPlan(t *testing.T) {
	// The head of a real bug file as the old docs trail held it (BUG-049,
	// before the brain migration): the file parser still reads an
	// unmigrated project's trail (brain plan D16).
	real := "---\nowner: bugs\ndate: 2026-09-16\nid: BUG-049\nstatus: fixed\narea: internal/harness (dirty check)\n---\n" +
		"# BUG-049: the BUG-046 dirty-checkout guard trusted git's configurable default\n\n## Symptom\nThe dirty-checkout guard used a bare `git\nstatus --porcelain`.\n"
	rs := parseRecords("bugs/BUG-049-dirty-check-untracked-files-config.md", real)
	if len(rs) != 1 || rs[0].id != "BUG-049" || rs[0].status == "" || rs[0].date == "" || rs[0].title == "" {
		t.Errorf("real bug record: %+v", rs)
	}
	adr := "# Decisions\n\n## ADR-040: An unsigned zip needs `--unverified`     (2026-09-28) — Superseded by ADR-041\n\ntext\n\n" +
		"## ADR-042: Dream records join     (2026-09-29) — Accepted; superseded in part by ADR-044 (no line)\n\n" +
		"## ADR-044: A dream record is not indexed     (2026-09-29) — Accepted, amends ADR-042\n"
	rs = parseRecords("architecture/DECISIONS.md", adr)
	if len(rs) != 3 || rs[0].status != "Superseded by ADR-041" || rs[1].status != "Accepted; superseded in part by ADR-044 (no line)" || rs[2].status != "Accepted, amends ADR-042" || rs[0].line != 3 {
		t.Errorf("ADR records: %+v", rs)
	}
	plan := "---\ndate: 2026-09-29\n---\n# Plan — x\n\n```markdown\n- [ ] template box\n```\n- [x] done\n- [ ] first\n- [ ] second\n"
	rs = parseRecords("plans/x.md", plan)
	if len(rs) != 1 || rs[0].status != "open: 2 unticked" || rs[0].line != 10 || rs[0].date != "2026-09-29" {
		t.Errorf("plan record: %+v", rs)
	}
	rs = parseRecords("plans/y.md", "# Plan\n```\n- [ ] only in a fence\n```\n")
	if len(rs) != 1 || rs[0].status != "closed" {
		t.Errorf("fenced box counted: %+v", rs)
	}
}

func TestRefsIDsAndPaths(t *testing.T) {
	rs := parseRefs("see BUG-049 and ADR-033a in `internal/harness/repo.go:12-30`.\nalso ./cmd/x/main.go, plans/x.md and a/internal/y.go; not BUG-49\n")
	want := map[string]int{"BUG-049": 1, "ADR-033a": 1, "internal/harness/repo.go": 1, "cmd/x/main.go": 2, "plans/x.md": 2, "internal/y.go": 2}
	got := map[string]int{}
	for _, r := range rs {
		got[r.target] = r.line
	}
	for k, line := range want {
		if got[k] != line {
			t.Errorf("ref %s: line %d, want %d (all: %v)", k, got[k], line, got)
		}
	}
	if _, ok := got["BUG-49"]; ok {
		t.Errorf("BUG-49 is not an ID")
	}
}

func TestSearchRanksAndCaps(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	var many strings.Builder
	for i := 0; i < 6; i++ {
		many.WriteString("## part\n")
		many.WriteString(strings.Repeat("lynx lynx lynx\n", 3))
	}
	writeFile(t, root, "many.md", many.String())
	writeFile(t, root, "once.md", "a lynx once\n")
	writeFile(t, root, ".agents/docs/bugs/BUG-001-lynx.md", "---\nid: BUG-001\nstatus: fixed\ndate: 2026-09-01\n---\n# BUG-001: lynx\n\nthe lynx escaped\n")
	commitAll(t, root, "add")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"lynx"}})
	if n := strings.Count(out, "many.md:"); n != perFileCap {
		t.Errorf("many.md printed %d times, want the cap %d:\n%s", n, perFileCap, out)
	}
	if !strings.Contains(out, ".agents/docs/bugs/BUG-001-lynx.md:6  bug BUG-001 fixed  2026-09-01") {
		t.Errorf("bug hit lacks its record line:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"lynx"}, Kind: "bug"})
	if strings.Contains(out, "many.md") || !strings.Contains(out, "BUG-001") {
		t.Errorf("--kind bug:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"lynx"}, Limit: 1})
	if !strings.HasPrefix(out, "search: 1 hits") {
		t.Errorf("--limit 1:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"lynx", "nonexistentword"}})
	if !strings.Contains(out, ", 0 with every word") || !strings.Contains(out, "once.md") {
		t.Errorf("OR fallback:\n%s", out)
	}
	// Chunks with every word come first; the rest of the slots are filled
	// with chunks holding any of them.
	writeFile(t, root, "both.md", "a lynx beside a heron\n")
	commitAll(t, root, "both")
	out = mustSearch(t, Options{Dir: root, Home: home, Words: []string{"lynx", "heron"}})
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], ", 1 with every word") || !strings.HasPrefix(lines[1], "both.md:1") || !strings.Contains(out, "once.md") {
		t.Errorf("AND first, then OR:\n%s", out)
	}
}

func TestSearchOperatorCharactersAreLiteral(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "x.md", "BUG-049 and a.go and NOT a keyword\n")
	commitAll(t, root, "add")
	for _, w := range [][]string{{"BUG-049"}, {"a.go"}, {"NOT"}, {`"`}, {"(", "BUG-049"}, {"*"}, {"col:on"}} {
		if _, err := runSearch(t, Options{Dir: root, Home: home, Words: w}); err != nil {
			t.Errorf("words %q: %v", w, err)
		}
	}
	if out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"BUG-049"}}); !strings.Contains(out, "x.md:1") {
		t.Errorf("BUG-049 not found:\n%s", out)
	}
}

func TestRefs(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, ".agents/docs/architecture/DECISIONS.md", "# Decisions\n\n## ADR-040: Old     (2026-09-28) — Superseded by ADR-041\n\n## ADR-041: New     (2026-09-28) — Accepted\n\nsupersedes ADR-040\n")
	writeFile(t, root, ".agents/docs/plans/p.md", "---\ndate: 2026-09-20\n---\n# P\n\ntouches `internal/pkg/z.go` and plans/q.md\n")
	writeFile(t, root, ".agents/docs/plans/q.md", "# Q\n")
	writeFile(t, root, "internal/pkg/z.go", "package pkg // ADR-040\n")
	commitAll(t, root, "add")
	out := mustSearch(t, Options{Dir: root, Home: home, Refs: "ADR-040"})
	if !strings.HasPrefix(out, "ADR-040  .agents/docs/architecture/DECISIONS.md:3  Superseded by ADR-041  2026-09-28  Old") {
		t.Errorf("ADR-040 status line:\n%s", out)
	}
	if !strings.Contains(out, "internal/pkg/z.go:1  code") || !strings.Contains(out, "DECISIONS.md:7  adr ADR-041 Accepted") {
		t.Errorf("ADR-040 references:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Refs: "internal/pkg/z.go"})
	if !strings.HasPrefix(out, "internal/pkg/z.go  tracked") || !strings.Contains(out, "plans/p.md:6  plan closed  2026-09-20") {
		t.Errorf("path refs:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Refs: ".agents/docs/plans/q.md"})
	if !strings.Contains(out, "plans/p.md:6") {
		t.Errorf("docs-relative ref not matched:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Refs: "internal/"})
	if !strings.HasPrefix(out, "internal/  directory") || !strings.Contains(out, "plans/p.md:6") {
		t.Errorf("directory refs:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Refs: "z.go"})
	if !strings.HasPrefix(out, "z.go  absent\nhint: did you mean --refs internal/pkg/z.go\n") {
		t.Errorf("bare file name, want a hint:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Refs: "ADR-999"})
	if !strings.HasPrefix(out, "ADR-999  no record") {
		t.Errorf("unknown ID:\n%s", out)
	}
	out = mustSearch(t, Options{Dir: root, Home: home, Refs: "ADR-040", Kind: "code"})
	if strings.Contains(out, "DECISIONS.md:7") || !strings.Contains(out, "z.go") {
		t.Errorf("--kind code on refs:\n%s", out)
	}
}

// parseRefs skips lines that cannot hold a reference before it runs its
// regular expressions; the result must be what the expressions alone give.
func TestRefsPrefilterIsEquivalent(t *testing.T) {
	text := strings.Join([]string{
		"only an id: ADR-033a",
		"only a bug: BUG-049.",
		"a path with a slash: internal/harness/repo.go:12",
		"a bare name: main.go and notes.md",
		"prose with a full stop. And another one. No reference here",
		"a dotted word like e.g. or v1.2.3 but no extension",
		"mixed BUG-001 in cmd/x/y.ts, and go.mod; also z.tf",
		"a directory only: skills/feature/",
		"",
	}, "\n")
	// One bare name per accepted extension, no slash on the line.
	for _, e := range refExtensions {
		text += "\nsee name_" + e + "." + e + " here"
	}
	var want []ref
	for i, l := range splitLines(text) {
		seen := map[string]bool{}
		add := func(x string) {
			if x != "" && !seen[x] {
				seen[x] = true
				want = append(want, ref{line: i + 1, target: x})
			}
		}
		for _, m := range idToken.FindAllString(l, -1) {
			add(m)
		}
		for _, m := range pathToken.FindAllString(l, -1) {
			if x := normalizeRef(m); strings.ContainsAny(x, "/.") && !idToken.MatchString(x) {
				add(x)
			}
		}
	}
	got := parseRefs(text)
	if len(want) < 10+len(refExtensions) {
		t.Fatalf("fixture too thin: %d references", len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("prefiltered parseRefs differs:\n got %v\nwant %v", got, want)
	}
}

// TestNoDuplicateHitAcrossRounds: a chunk whose matched term is not on its
// first line was printed twice, once by the every-word round (its line
// shifted to the term) and once by the any-word round (the chunk's first
// line), and counted twice against --limit and the per-file cap (audit of
// 2026-09-30, SD2).
func TestNoDuplicateHitAcrossRounds(t *testing.T) {
	root := newRepo(t)
	home := t.TempDir()
	writeFile(t, root, "notes.md", "# notes\nline two\nline three\nline four\nzebra quokka here now\n")
	commitAll(t, root, "add")
	out := mustSearch(t, Options{Dir: root, Home: home, Words: []string{"zebra", "quokka"}})
	if n := strings.Count(out, "notes.md:"); n != 1 {
		t.Errorf("notes.md printed %d times, want 1:\n%s", n, out)
	}
	if !strings.HasPrefix(out, "search: 1 hits") || !strings.Contains(out, ", 1 with every word") {
		t.Errorf("header counts the duplicate:\n%s", out)
	}
}
