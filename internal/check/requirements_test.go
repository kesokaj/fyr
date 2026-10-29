package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rule (c) over a template-shape file (D22, D30, D31): one finding per item
// with no comment, an empty value, a placeholder, a not-yet-enforced value
// or an unresolved part; a path with :N, a Go test with a note, a record ID
// and a path with ::case resolve; a struck item and ## Could are skipped,
// and an item under ## Should is read.
func TestRequirementsRule(t *testing.T) {
	reqs := strings.Join([]string{
		"# Requirements — x",
		"",
		"<!-- One sentence per item; where the requirement is enforced. -->",
		"",
		"## Must — non-negotiable",
		"",
		"- **R1** — no comment at all.",
		"- **R2** — an empty value.",
		"  <!-- provenance: op, 2026-10-25; enforced by: -->",
		"- **R3** — the template's placeholder.",
		"  <!-- provenance: <who>, <date>; enforced by: <test | rule | guide path> -->",
		"- **R4** — not yet.",
		"  <!-- provenance: op; enforced by: not yet enforced: needs a test, TestX -->",
		"- **R5** — unresolved parts.",
		"  <!-- provenance: op; enforced by: TestX, TestNoSuch, internal/gone.go, cmd/fyr/main.go:99, ADR-999 -->",
		"- **R6** — resolves every way.",
		"  <!-- provenance: op;",
		"       enforced by: cmd/fyr/main.go:2, TestX § note, ADR-063, skills/x/SKILL.md::case, `GUIDE-testing` § 1 -->",
		"- **R7** — struck 2026-10-01: gone.",
		"- **R8** — a comment without the field.",
		"  <!-- provenance: op -->",
		"### a sub-heading ends the item",
		"  <!-- enforced by: TestX -->",
		"",
		"## Should — expected",
		"",
		"- **S1** — resolves.",
		"  <!-- enforced by: internal/a_test.go -->",
		"- **S2** — no comment.",
		"",
		"## Could — welcome",
		"",
		"- **C1** — never read.",
		"",
		"## Non-goals",
		"",
		"- **N1** — never read either.",
	}, "\n")
	dir := repo(t, map[string]string{
		"REQUIREMENTS.md":     reqs,
		"cmd/fyr/main.go":     "package main\n\nfunc main() {}\n",
		"internal/a_test.go":  "package a\n\nfunc TestX(t *testing.T) {}\n\n// func TestNoSuch( is a comment\n",
		"internal/b.go":       "package a\n\nfunc TestNoSuch() {}\n",
		"skills/x/SKILL.md":   "x\n",
		".agents/trail.jsonl": trailLine(t, "ADR-063", "adr", "accepted", "x") + trailLine(t, "GUIDE-testing", "guide", "current", "x"),
	})
	rep := run(t, dir)
	same(t, rep.Findings,
		"REQUIREMENTS.md:7 requirement", "REQUIREMENTS.md:8 requirement", "REQUIREMENTS.md:10 requirement",
		"REQUIREMENTS.md:12 requirement", "REQUIREMENTS.md:14 requirement", "REQUIREMENTS.md:20 requirement",
		"REQUIREMENTS.md:29 requirement",
	)
	why := map[int]string{10: "R3: `enforced by:` is the template's placeholder", 12: "R4: `enforced by:` is not yet enforced", 29: "S2: no `enforced by:` comment"}
	for _, f := range rep.Findings {
		if f.Line == 14 {
			for _, want := range []string{"`TestNoSuch`", "`internal/gone.go`", "`cmd/fyr/main.go:99`", "`ADR-999`"} {
				if !strings.Contains(f.Text, want) {
					t.Errorf("R5's finding %q does not name %s", f.Text, want)
				}
			}
			if strings.Contains(f.Text, "`TestX`") {
				t.Errorf("R5's finding %q names TestX, which resolves", f.Text)
			}
		}
		if w, ok := why[f.Line]; ok && f.Text != w {
			t.Errorf("line %d: %q, want %q", f.Line, f.Text, w)
		}
	}
	if strings.Contains(notes(rep), "REQUIREMENTS") {
		t.Errorf("a checked file still gives a note: %q", notes(rep))
	}
}

// No enforced-by field anywhere gives the note and no finding (D30), and
// so does no file.
func TestRequirementsRuleNotes(t *testing.T) {
	rep := run(t, repo(t, map[string]string{"REQUIREMENTS.md": "## Must\n\n- **R1** — one, with no comment.\n"}))
	same(t, rep.Findings)
	if !strings.Contains(notes(rep), "REQUIREMENTS.md has no enforced-by field: not checked") {
		t.Errorf("notes %q", notes(rep))
	}
	rep = run(t, repo(t, map[string]string{"a.txt": "x\n"}))
	same(t, rep.Findings)
	if !strings.Contains(notes(rep), "no PRD.md or REQUIREMENTS.md") {
		t.Errorf("notes %q", notes(rep))
	}
	// A symlinked file is not read, and the note does not call it absent.
	dir := repo(t, map[string]string{"docs/r.md": "## Must\n\n- **R1** — x.\n  <!-- enforced by: TestGone -->\n"})
	if err := os.Symlink(filepath.Join("docs", "r.md"), filepath.Join(dir, "REQUIREMENTS.md")); err != nil {
		t.Fatal(err)
	}
	rep = run(t, dir)
	same(t, rep.Findings)
	if !strings.Contains(notes(rep), "REQUIREMENTS.md is not a regular file") {
		t.Errorf("symlink notes %q", notes(rep))
	}
}

// An `enforced by:` path resolves only when git tracks it (or a directory of
// tracked files): an ignored local file proves nothing on another checkout
// (IN54 of the 2026-10-27 audit).
func TestRequirementsResolveTrackedPathsOnly(t *testing.T) {
	reqs := strings.Join([]string{
		"# Requirements — x",
		"",
		"## Must — non-negotiable",
		"",
		"- **R1** — a tracked file.",
		"  <!-- enforced by: tracked.txt -->",
		"- **R2** — a tracked directory.",
		"  <!-- enforced by: internal -->",
		"- **R3** — an ignored local file.",
		"  <!-- enforced by: local.txt -->",
	}, "\n")
	dir := repo(t, map[string]string{
		"REQUIREMENTS.md":     reqs,
		"tracked.txt":         "x\n",
		"internal/a.go":       "package a\n",
		".gitignore":          "local.txt\n",
		".agents/trail.jsonl": trailLine(t, "ADR-063", "adr", "accepted", "x"),
	})
	os.WriteFile(filepath.Join(dir, "local.txt"), []byte("x\n"), 0o644)
	rep := run(t, dir)
	same(t, rep.Findings, "REQUIREMENTS.md:9 requirement")
}

// Rule (c) reads PRD.md, falls back to REQUIREMENTS.md, and with both reads
// PRD.md only and says so; every finding and note names the file read
// (PLAN-2026-10-29-root-docs-and-fyr-serve D6).
func TestRequirementsRuleReadsPRD(t *testing.T) {
	item := "## Must\n\n- **R1** — x.\n  <!-- enforced by: TestGone -->\n"
	other := "# t\n\n## Must\n\n- **R9** — y.\n  <!-- enforced by: TestAlsoGone -->\n"
	for _, tc := range []struct {
		name, note, notNote string
		files               map[string]string
		want                []string
	}{
		{"PRD only", "", "REQUIREMENTS", map[string]string{"PRD.md": item}, []string{"PRD.md:3 requirement"}},
		{"REQUIREMENTS only", "", "PRD", map[string]string{"REQUIREMENTS.md": item}, []string{"REQUIREMENTS.md:3 requirement"}},
		{"both", "REQUIREMENTS.md beside PRD.md: not read", "", map[string]string{"PRD.md": item, "REQUIREMENTS.md": other}, []string{"PRD.md:3 requirement"}},
		{"neither", "no PRD.md or REQUIREMENTS.md: requirements not checked", "", map[string]string{"a.txt": "x\n"}, nil},
		{"PRD without the field", "PRD.md has no enforced-by field: not checked", "", map[string]string{"PRD.md": "## Must\n\n- **R1** — x.\n"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := run(t, repo(t, tc.files))
			same(t, rep.Findings, tc.want...)
			if tc.note != "" && !strings.Contains(notes(rep), tc.note) {
				t.Errorf("notes %q, want %q", notes(rep), tc.note)
			}
			if tc.notNote != "" && strings.Contains(notes(rep), tc.notNote) {
				t.Errorf("notes %q name %s", notes(rep), tc.notNote)
			}
		})
	}
}

// The init-project PRD template is in the shape rule (c) reads: each of its
// Must and Should items is found, and found as the template's placeholder.
func TestRequirementsRuleReadsThePRDTemplate(t *testing.T) {
	tmpl, err := os.ReadFile(filepath.Join("..", "..", "skills", "init-project", "assets", "PRD.md"))
	if err != nil {
		t.Fatal(err)
	}
	rep := run(t, repo(t, map[string]string{"PRD.md": string(tmpl)}))
	if len(rep.Findings) < 2 {
		t.Fatalf("%d findings on the template, want one per Must and Should item: %+v", len(rep.Findings), rep.Findings)
	}
	for _, f := range rep.Findings {
		if f.Where != "PRD.md" || !strings.HasSuffix(f.Text, "`enforced by:` is the template's placeholder") {
			t.Errorf("finding %+v, want PRD.md and the placeholder", f)
		}
	}
}
