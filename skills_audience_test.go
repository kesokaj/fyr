package fyr_test

import "testing"

// TestSkillsAudienceSplit pins structural ADR-535 and ADR-536 markers:
// the 5-track audience split (git human docs vs agent-only SQLite databases
// brain.db, search.db, metrics.db), required SPEC-* headings and 4-column
// failure table header, and the <= 6300 B context/AGENTS.md budget.
func TestSkillsAudienceSplit(t *testing.T) {
	assertSkillTextCases(t, []skillTextCase{
		{
			name: "docs-ste100-80-5-track-matrix",
			path: "skills/docs-ste100-80/SKILL.md",
			want: []string{
				"Track 1", "Track 2", "Track 3", "Track 4", "Track 5",
				"4,500 B", "brain.db", "search.db", "metrics.db",
				"fyr search --slice", "--contracts", "--skeleton",
				"docs-style", "budget", "spec-structure", "docs-style-note", "glossary.json",
			},
			forbid: []string{"`.agents/glossary.json`"},
		},
		{
			name: "fyr-init-project-audience-split",
			path: "skills/fyr-init-project/SKILL.md",
			want: []string{"Track 1", "Track 2", "Track 3", "4,500 B", "brain.db", "SPEC-*"},
		},
		{
			name: "fyr-spec-machine-projectable",
			path: "skills/fyr-spec/SKILL.md",
			want: []string{
				"brain.db", "agent-only",
				"## Purpose", "## Interfaces", "## Data", "## Failure modes", "## Verification",
				"| Trigger | Observable result | Retry | Action |",
				"fyr search --contracts", "spec-structure",
			},
		},
		{
			name: "spec-template-machine-projectable",
			path: "skills/fyr-init-project/assets/records/SPEC-TEMPLATE.md",
			want: []string{
				"brain.db", "agent-only",
				"## Purpose", "## Interfaces", "## Data", "## Failure modes", "## Verification",
				"| Trigger | Observable result | Retry | Action |",
				"fyr search --contracts", "spec-structure",
			},
		},
		{
			name: "documentation-agent-5-track",
			path: "agents/documentation/agent.md",
			want: []string{
				"Track 1", "Track 2", "Track 5", "4,500 B", "brain.db",
				"| Trigger | Observable result | Retry | Action |",
				"fyr search --contracts", "spec-structure",
			},
		},
		{
			name: "fyr-review-docs-lens-db-split",
			path: "skills/fyr-review/references/lens-briefs.md",
			want: []string{"docs-ste100-80", "brain.db", "Caution", "spec-structure"},
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
