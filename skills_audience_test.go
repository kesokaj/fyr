package fyr_test

import "testing"

// TestSkillsAudienceSplit pins ADR-535 and ADR-536:
// the 5-track audience split (git human docs vs 100% agent-only SQLite
// databases brain.db, search.db, metrics.db) across docs-ste100-80,
// fyr-init-project, fyr-spec, fyr-review, SPEC-TEMPLATE.md,
// agents/documentation, and context/AGENTS.md.
func TestSkillsAudienceSplit(t *testing.T) {
	assertSkillTextCases(t, []skillTextCase{
		{
			name: "docs-ste100-80-5-track-matrix",
			path: "skills/docs-ste100-80/SKILL.md",
			want: []string{
				"Track 1", "Human Docs", "Workflow.md", "features/**/*.md",
				"Track 2", "100% agent-read", "Telegraphic Markdown", "4,500 B",
				"brain.db", "search.db", "metrics.db", "agent-only",
				"Track 3", "Shared Contracts",
				"Track 4", "Ephemeral", "fyr search --slice", "--contracts", "--skeleton",
				"Track 5", "Immutable Trail",
				"Never rewrite Track 2",
				"docs-style", "budget", "spec-structure", "docs-style-note",
				"and `spec-structure` (5 required `##` headings: `Purpose`, `Interfaces`, `Data`, `Failure modes`, `Verification`; plus an advisory note when `## Failure modes` lacks a 4-column table)",
				"glossary.json",
				">= 80%",
			},
			forbid: []string{
				"not enforced by `fyr check`",
				"`.agents/glossary.json`",
				"`features/**/*.md`, `GUIDE-*`, `DEPLOY-*`",
				", or when `SPEC-*` `## Failure modes` lacks a 4-column table",
			},
		},
		{
			name: "fyr-init-project-audience-split",
			path: "skills/fyr-init-project/SKILL.md",
			want: []string{
				"Track 1", "README.md",
				"Track 2", "100% agent-read", "Telegraphic Markdown", "4,500 B",
				"brain.db", "agent-only",
				"Track 3", "TODO.md", "PRD.md", "SPEC-*",
			},
			forbid: []string{
				"fill the sections (`docs-ste100-80` BLUF openings)",
				"Human docs (`README.md`, `SDD.md`, `TDD.md`, `GUIDE-*`, `DEPLOY-*`)",
				"follows Track 1 `docs-ste100-80`",
			},
		},
		{
			name: "fyr-spec-machine-projectable",
			path: "skills/fyr-spec/SKILL.md",
			want: []string{
				"brain.db", "agent-only",
				"## Purpose", "## Interfaces", "## Data", "## Failure modes", "## Verification",
				"| Trigger | Observable result | Retry | Action |",
				"fenced", "invariant",
				"fyr search --contracts",
				"spec-structure",
			},
			forbid: []string{
				"not enforced yet",
				"Follow the `docs-ste100-80` skill for prose clarity",
			},
		},
		{
			name: "spec-template-machine-projectable",
			path: "skills/fyr-init-project/assets/records/SPEC-TEMPLATE.md",
			want: []string{
				"agent-only", "brain.db",
				"## Purpose", "## Interfaces", "## Data", "## Failure modes", "## Verification",
				"| Trigger | Observable result | Retry | Action |",
				"fyr search --contracts",
				"spec-structure",
			},
			forbid: []string{
				"not enforced yet",
			},
		},
		{
			name: "documentation-agent-5-track",
			path: "agents/documentation/agent.md",
			want: []string{
				"Track 1", "docs-ste100-80",
				"Track 2", "Telegraphic Markdown", "4,500 B",
				"brain.db", "agent-only",
				"| Trigger | Observable result | Retry | Action |",
				"Track 5", "immutable",
				"fyr search --contracts",
				"spec-structure",
			},
			forbid: []string{"not enforced yet"},
		},
		{
			name: "fyr-review-docs-lens-db-split",
			path: "skills/fyr-review/references/lens-briefs.md",
			want: []string{
				"docs-ste100-80", "brain.db", "agent-only", "Caution",
				"check `guide`/`deploy` records for accuracy, no YAML frontmatter, and `Caution`/`Warning` callouts before destructive commands only",
				"check `SPEC-*` records for accuracy and `spec-structure`",
			},
			forbid: []string{
				"(`README.md`, `SDD.md`, `TDD.md`, `SPEC-*`, `GUIDE-*`, `DEPLOY-*`, `docs/*.md`)",
				"check accuracy, no YAML frontmatter",
			},
		},
		{
			name: "context-agents-audience-and-contracts",
			path: "context/AGENTS.md",
			want: []string{"docs-ste100-80", "agent-read", "SPEC-*", "--contracts"},
		},
	})
	if got := len(readSkill(t, "context/AGENTS.md")); got > 6300 {
		t.Errorf("context/AGENTS.md grew to %d B, want <= 6300 B (net-neutral or smaller)", got)
	}
}
