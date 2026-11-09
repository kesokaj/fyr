package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// traceFindings is the trace findings of rep alone: rule (c) also reads the
// PRD fixtures.
func traceFindings(rep Report) []Finding {
	var out []Finding
	for _, f := range rep.Findings {
		if f.Rule == "trace" {
			out = append(out, f)
		}
	}
	return out
}

// Rule (d) (plan D40): every Must and Should item of PRD.md has a row in
// SDD.md's trace that names a component, and every component of SDD.md has a
// row in TDD.md's trace. A struck item and ## Could are not read; a row in a
// fence, in a later section or under a heading that only begins with Trace
// does not count, and the first row with a key wins.
func TestTracesRule(t *testing.T) {
	prd := strings.Join([]string{
		"# PRD — x",
		"",
		"## Must — non-negotiable",
		"",
		"- **R1** — traced.",
		"- **R2** — a row naming no component.",
		"- **R3** — struck 2026-10-01: gone.",
		"",
		"## Should — expected",
		"",
		"- **S1** — no row.",
		"",
		"## Could — welcome",
		"",
		"- **C1** — never read.",
	}, "\n")
	sdd := strings.Join([]string{
		"# SDD — x",
		"",
		"## Components",
		"",
		"| ID | Component | Responsibility | Satisfies |",
		"|---|---|---|---|",
		"| M1 | `cmd/x` | one | R1 |",
		"| `M2` | `internal/a` | two | R2 |",
		"| M3 | `internal/b` | three | R1 |",
		"",
		"## Decisions",
		"",
		"| S1 | M1 |",
		"",
		"## Trace",
		"",
		"| Requirement | Components |",
		"|---|---|",
		"| R1 | M1, M3 |",
		"| R2 | none yet |",
		"| R2 | M1 |",
		"",
		"## Risks",
		"",
		"| S1 | M1 |",
		"",
		"```text",
		"| S1 | M1 |",
		"## Components",
		"| M9 | fenced |",
		"```",
	}, "\n")
	tdd := strings.Join([]string{
		"# TDD — x",
		"",
		"## T3 — b",
		"",
		"| M3 | T3 | TestB |",
		"",
		"## Traceability matrix",
		"",
		"| M3 | T3 | TestB |",
		"",
		"## Trace",
		"",
		"| Component | Section | Verified by |",
		"|---|---|---|",
		"| M1 | T1 | TestA |",
		"| `M2` | T2 | TestB |",
	}, "\n")
	rep := run(t, repo(t, map[string]string{"PRD.md": prd, "SDD.md": sdd, "TDD.md": tdd}))
	got := traceFindings(rep)
	same(t, got, "PRD.md:6 trace", "PRD.md:11 trace", "SDD.md:9 trace")
	want := map[string]string{
		"PRD.md:6":  "R2: SDD.md's trace row names no component",
		"PRD.md:11": "S1: no row in SDD.md's trace",
		"SDD.md:9":  "M3: no row in TDD.md's trace",
	}
	for _, f := range got {
		key := fmt.Sprintf("%s:%d", f.Where, f.Line)
		if f.Text != want[key] {
			t.Errorf("%s: %q, want %q", key, f.Text, want[key])
		}
	}
	if strings.Contains(notes(rep), "trace") {
		t.Errorf("a checked trio gives a trace note: %q", notes(rep))
	}
}

// What rule (d) cannot read is a note, never a silent pass: no SDD.md checks
// nothing; no PRD.md, a PRD.md with no template items, or no TDD.md leaves
// the other half checked.
func TestTracesRuleNotes(t *testing.T) {
	prd := "## Must\n\n- **R1** — x.\n"
	sdd := "## Components\n\n| M1 | a |\n\n## Trace\n\n| R1 | M1 |\n"
	tdd := "## Trace\n\n| M1 | T1 | TestA |\n"
	for _, tc := range []struct {
		name, note string
		files      map[string]string
		want       []string
	}{
		{"no SDD", "no SDD.md: traces not checked", map[string]string{"PRD.md": prd, "TDD.md": "## Trace\n"}, nil},
		{"no PRD", "no PRD.md: requirements not traced", map[string]string{"SDD.md": sdd, "TDD.md": "## Trace\n"}, []string{"SDD.md:3 trace"}},
		{"PRD without items", "PRD.md has no Must or Should items: requirements not traced",
			map[string]string{"PRD.md": "## Requirements\n\n### R0 — Purpose\n", "SDD.md": sdd, "TDD.md": tdd}, nil},
		{"no TDD", "no TDD.md: components not traced", map[string]string{"PRD.md": "## Must\n\n- **R2** — y.\n", "SDD.md": sdd}, []string{"PRD.md:3 trace"}},
		{"no components", "SDD.md has no M# component: components not traced", map[string]string{"PRD.md": prd, "SDD.md": "## Trace\n\n| R1 | M1 |\n", "TDD.md": tdd}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := run(t, repo(t, tc.files))
			same(t, traceFindings(rep), tc.want...)
			if !strings.Contains(notes(rep), tc.note) {
				t.Errorf("notes %q, want %q", notes(rep), tc.note)
			}
		})
	}
	// A symlinked file is not read, and the note does not call it absent.
	for name, what := range map[string]string{"SDD.md": "traces not checked", "PRD.md": "requirements not traced", "TDD.md": "components not traced"} {
		files := map[string]string{"PRD.md": prd, "SDD.md": sdd, "TDD.md": tdd, "docs/x.md": "## Trace\n"}
		delete(files, name)
		dir := repo(t, files)
		if err := os.Symlink(filepath.Join("docs", "x.md"), filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		rep := run(t, dir)
		same(t, traceFindings(rep))
		if want := name + " is not a regular file of at most 16 MiB: " + what; !strings.Contains(notes(rep), want) {
			t.Errorf("symlinked %s: notes %q, want %q", name, notes(rep), want)
		}
	}
}

// The fyr-init-project templates are in the shape rule (d) reads: their SDD
// traces R1 alone, so the template's R2 and S1 are gaps, and its one
// component M1 is traced in its TDD.
func TestTracesRuleReadsTheTemplates(t *testing.T) {
	files := map[string]string{}
	for _, name := range []string{"PRD.md", "SDD.md", "TDD.md"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "skills", "fyr-init-project", "assets", name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(b)
	}
	rep := run(t, repo(t, files))
	got := traceFindings(rep)
	if len(got) != 2 {
		t.Fatalf("trace findings on the templates %+v, want R2 and S1", got)
	}
	for i, id := range []string{"R2", "S1"} {
		if got[i].Where != "PRD.md" || got[i].Text != id+": no row in SDD.md's trace" {
			t.Errorf("finding %+v, want %s with no row", got[i], id)
		}
	}
}
