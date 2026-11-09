package fyr_test

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
)

// TestInitProjectAndDreamNameOneCLI: /init-project and /dream once told an
// Antigravity-only machine from the install manifest's vendor and wrote or
// read Claude Code's files on any other (plan fyr Phase 3b). With one CLI
// there is no other kind of machine: neither skill reads the vendor, and
// neither names a `.claude` path or Claude Code's old memory directory.
func TestInitProjectAndDreamNameOneCLI(t *testing.T) {
	for _, path := range []string{"skills/fyr-init-project/SKILL.md", "skills/fyr-dream/SKILL.md"} {
		skill, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, gone := range []string{`"vendor"`, ".claude", "CLAUDE.md", "autoMemory", "Claude Code"} {
			if strings.Contains(string(skill), gone) {
				t.Errorf("%s still names %s", path, gone)
			}
		}
	}
}

// TestInitProjectRootDocs pins the root documents init-project scaffolds
// (PLAN-2026-10-29-root-docs-and-fyr-serve § Interfaces): PRD, SDD, TDD and
// TODO ship and REQUIREMENTS and ROADMAP do not; each of PRD, SDD and TDD
// links the other two by relative path; the IDs flow R# → M# → T# → tasks;
// and no template names a file the scaffold no longer has.
func TestInitProjectRootDocs(t *testing.T) {
	const dir = "skills/fyr-init-project/assets/"
	for _, gone := range []string{"REQUIREMENTS.md", "ROADMAP.md"} {
		if _, err := os.Lstat(dir + gone); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s%s still ships (%v)", dir, gone, err)
		}
	}
	want := map[string][]string{
		"PRD.md": {"](SDD.md)", "](TDD.md)", "## Problem", "## Users", "## Goals", "| G1 |", "## Non-goals",
			"## Must", "## Should", "## Could", "- **R1** — ", "- **S1** — ", "enforced by:", "## Known limits", "## Open questions"},
		"SDD.md": {"](PRD.md)", "](TDD.md)", "## Context", "## Components", "| M1 |", "R1", "## Flows", "## Decisions",
			"## Cross-cutting", "## Deployment", "## Risks", "## Trace", "```mermaid"},
		"TDD.md": {"](PRD.md)", "](SDD.md)", "## T1 — ", "Implements: M1", "SPEC-", "## Data model", "## Testing strategy",
			"GUIDE-testing", "## Build, run and deploy", "DEPLOY-", "## Trace"},
		"TODO.md":   {"## Now", "## Next", "## Backlog", "→ T", "(added YYYY-MM-DD, "},
		"AGENTS.md": {"`PRD.md`", "`SDD.md`", "`TDD.md`", "`TODO.md`"},
	}
	for name, needles := range want {
		data, err := os.ReadFile(dir + name)
		if err != nil {
			t.Error(err)
			continue
		}
		text := string(data)
		for _, n := range needles {
			if !strings.Contains(text, n) {
				t.Errorf("%s%s lacks %q", dir, name, n)
			}
		}
		for _, old := range []string{"REQUIREMENTS", "ROADMAP", "Empty (this title and note only)"} {
			if strings.Contains(text, old) {
				t.Errorf("%s%s still names %q", dir, name, old)
			}
		}
		for i, block := range strings.Split(text, "```mermaid\n")[1:] {
			first := strings.TrimSpace(strings.SplitN(block, "\n", 2)[0])
			if !strings.HasPrefix(first, "flowchart ") && first != "sequenceDiagram" {
				t.Errorf("%s%s: mermaid block %d opens %q, want a flowchart or a sequenceDiagram", dir, name, i+1, first)
			}
		}
	}
}

// TestProjectIndexingLifecycleSkills pins the 3-skill project indexing lifecycle
// (ADR-514): /fyr-init-project (Step 1 git init check + Step 6 fyr index &
// fyr search --map), /fyr-index-project (MVP zero-scaffold indexing), and
// /fyr-refresh-project-index (incremental/--rebuild refresh & --map diagnostics).
func TestProjectIndexingLifecycleSkills(t *testing.T) {
	checks := map[string][]string{
		"skills/fyr-init-project/SKILL.md": {
			"name: fyr-init-project",
			"git rev-parse --show-toplevel",
			"git init",
			"fyr index",
			"fyr search --map",
			"fyr index --register",
		},
		"skills/fyr-index-project/SKILL.md": {
			"name: fyr-index-project",
			"git rev-parse --show-toplevel",
			"git init",
			".agents/scratch/*",
			"fyr index",
			"fyr search --map",
			"fyr index --register",
			"/fyr-init-project",
		},
		"skills/fyr-refresh-project-index/SKILL.md": {
			"name: fyr-refresh-project-index",
			"fyr index",
			"fyr index --rebuild",
			"fyr search --map",
			"fyr index --global",
		},
	}
	for path, needles := range checks {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, want := range needles {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q", path, want)
			}
		}
	}
}
