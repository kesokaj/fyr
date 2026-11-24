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
			"## Must", "## Should", "## Could", "```markdown", "- **R1** — ", "- **S1** — ", "enforced by:", "## Known limits", "## Open questions"},
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

// TestProjectIndexingLifecycleSkills pins /fyr-init-project's indexing steps
// (Step 1 git init check + Step 6 fyr index & fyr search --map).
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

func assertDocContains(t *testing.T, path string, want, forbid []string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(b)
	if strings.HasPrefix(text, "---\n") {
		t.Errorf("%s must not carry YAML frontmatter", path)
	}
	low := strings.ToLower(text)
	for _, w := range want {
		if !strings.Contains(low, strings.ToLower(w)) {
			t.Errorf("%s missing %q", path, w)
		}
	}
	for _, bad := range forbid {
		if strings.Contains(low, strings.ToLower(bad)) {
			t.Errorf("%s contains forbidden token %q", path, bad)
		}
	}
	return text
}

// TestInitProjectAudienceSplit pins ADR-535 and ADR-536:
// Track 1 git-tracked human docs (README.md, SDD.md, TDD.md), Track 2 100%
// agent-read files and SQLite databases (AGENTS.md <= 4,500 B Telegraphic
// Markdown, plus brain.db GUIDE-testing / DEPLOY-* / SPEC-* records with
// agent-safety Caution/no-frontmatter guards), and Track 3 git shared contracts
// (PRD.md, TODO.md) plus machine-projectable SPEC-TEMPLATE.md.
func TestInitProjectAudienceSplit(t *testing.T) {
	const dir = "skills/fyr-init-project/assets/"

	agents := assertDocContains(t, dir+"AGENTS.md",
		[]string{
			"100% agent-read",
			"Telegraphic Markdown",
			"4,500 B",
			"onboarding prose",
			"directory-tree dump",
			"README.md",
			"## Setup commands",
			"## Deploy",
			"--contracts",
			"brain.db",
			"agent-only",
		},
		[]string{"e.g.", "i.e.", "Human docs (Track 1): `README.md`, `SDD.md`, `TDD.md`, `GUIDE-testing`, `DEPLOY-*`"},
	)
	if len(agents) > 4500 {
		t.Errorf("%sAGENTS.md is %d B, want <= 4500 B", dir, len(agents))
	}

	assertDocContains(t, dir+"README.md",
		[]string{
			"Track 1",
			"human-first",
			"docs-ste100-80",
			"No YAML frontmatter",
			"BLUF",
			"safety callout",
			"<= 12",
			"> 12",
			"AGENTS.md",
		},
		[]string{"e.g.", "i.e.", "etc.", "seamless", "foundational", "zero-ops"},
	)

	assertDocContains(t, dir+"TODO.md",
		[]string{"Track 3", "shared", "human", "agent"},
		nil,
	)

	spec := assertDocContains(t, dir+"records/SPEC-TEMPLATE.md",
		[]string{
			"agent-only",
			"brain.db",
			"## Purpose",
			"## Interfaces",
			"## Data",
			"## Failure modes",
			"## Verification",
			"| Trigger | Observable result | Retry | Action |",
			"fyr search --contracts",
			"spec-structure",
		},
		[]string{"e.g.", "i.e."},
	)
	if !strings.Contains(spec, "| Trigger | Observable result | Retry | Action |") {
		t.Errorf("%srecords/SPEC-TEMPLATE.md missing exact 4-column Failure modes table header", dir)
	}

	checkInitProjectAgentDBRecords(t, dir+"records/")
}

func checkInitProjectAgentDBRecords(t *testing.T, recDir string) {
	t.Helper()
	for _, name := range []string{"GUIDE-testing.md", "DEPLOY-local.md", "DEPLOY-remote.md", "DEPLOY-gcp.md"} {
		assertDocContains(t, recDir+name,
			[]string{"agent-only", "brain.db"},
			[]string{"Track 1: human-first", "docs-ste100-80"},
		)
	}
	// Destructive / traffic-shifting commands must still be preceded by a Caution safety callout.
	for name, cmd := range map[string]string{
		"DEPLOY-local.md":  "docker compose down -v",
		"DEPLOY-remote.md": "sudo systemctl restart",
		"DEPLOY-gcp.md":    "gcloud run services update-traffic",
	} {
		b, err := os.ReadFile(recDir + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(b)
		cmdIdx := strings.Index(text, cmd)
		cautionIdx := strings.Index(strings.ToLower(text), "caution")
		if cmdIdx < 0 {
			t.Errorf("%s missing expected command %q", name, cmd)
		} else if cautionIdx < 0 || cautionIdx > cmdIdx {
			t.Errorf("%s must place a Caution safety callout before %q (caution=%d, cmd=%d)", name, cmd, cautionIdx, cmdIdx)
		}
	}
}
