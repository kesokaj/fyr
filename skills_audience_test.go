package fyr_test

import "testing"

// TestSkillsAudienceSplit pins Phases 2 & 4b of PLAN-2026-11-24-1704 (ADR-535):
// the 5-track audience split across docs-ste100-80, fyr-init-project, fyr-spec,
// SPEC-TEMPLATE.md, agents/documentation, and context/AGENTS.md, including
// landed fyr search --contracts, glossary.json, and fyr check deterministic
// docs-style / budget / spec-structure rules.
func TestSkillsAudienceSplit(t *testing.T) {
	assertSkillTextCases(t, []skillTextCase{
		{
			name: "docs-ste100-80-5-track-matrix",
			path: "skills/docs-ste100-80/SKILL.md",
			want: []string{
				"Track 1", "Human Docs", "Workflow.md", "features/**/*.md",
				"Track 2", "100% agent-read", "Telegraphic Markdown", "4,500 B",
				"Track 3", "Shared Contracts",
				"Track 4", "Ephemeral", "fyr search --slice", "--contracts", "--skeleton",
				"Track 5", "Immutable Trail",
				"Never rewrite Track 2",
				"docs-style", "budget", "spec-structure", "docs-style-note",
				"glossary.json",
				">= 80%",
			},
			forbid: []string{
				"not enforced by `fyr check`",
				"`.agents/glossary.json`",
			},
		},
		{
			name: "fyr-init-project-audience-split",
			path: "skills/fyr-init-project/SKILL.md",
			want: []string{
				"Track 1", "README.md",
				"Track 2", "100% agent-read", "Telegraphic Markdown", "4,500 B",
				"Track 3", "TODO.md", "PRD.md", "SPEC-*",
			},
			forbid: []string{"fill the sections (`docs-ste100-80` BLUF openings)"},
		},
		{
			name: "fyr-spec-machine-projectable",
			path: "skills/fyr-spec/SKILL.md",
			want: []string{
				"Track 3",
				"## Purpose", "## Interfaces", "## Data", "## Failure modes", "## Verification",
				"| Trigger | Observable result | Retry | Action |",
				"fenced", "invariant",
				"fyr search --contracts",
				"spec-structure",
			},
			forbid: []string{
				"not enforced yet",
			},
		},
		{
			name: "spec-template-machine-projectable",
			path: "skills/fyr-init-project/assets/records/SPEC-TEMPLATE.md",
			want: []string{
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
				"Track 3", "| Trigger | Observable result | Retry | Action |",
				"Track 5", "immutable",
				"fyr search --contracts",
				"spec-structure",
			},
			forbid: []string{"not enforced yet"},
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
