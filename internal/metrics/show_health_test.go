package metrics

import (
	"testing"

	"github.com/kesokaj/fyr/internal/metrics/show"
	"github.com/kesokaj/fyr/internal/search"
)

func TestSubagentCapForRoleAlignedWithSearch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		role      string
		wantAudit bool
		wantCap   int
	}{
		// Empty/blank subagent agent_type intentionally falls back to AuditSubagentCap (40),
		// whereas search.IsAuditSubagentRole treats empty as unresolved (false).
		{"", false, show.AuditSubagentCap},
		{"   ", false, show.AuditSubagentCap},
		// Main is not an audit subagent role.
		{"main", false, show.BuilderSubagentCap},
		{"MAIN", false, show.BuilderSubagentCap},
		// Builder / coder / self roles get the 120-call budget.
		{"self", false, show.BuilderSubagentCap},
		{"builder", false, show.BuilderSubagentCap},
		{"builder-phase0", false, show.BuilderSubagentCap},
		{"workflow-builder-8a62522f", false, show.BuilderSubagentCap},
		{"coder", false, show.BuilderSubagentCap},
		{"coder-1", false, show.BuilderSubagentCap},
		// Audit, specialized, and custom/unknown roles align on the 40-call audit budget.
		{"worker-1", true, show.AuditSubagentCap},
		{"worker-2", true, show.AuditSubagentCap},
		{"worker-x", true, show.AuditSubagentCap},
		{"implementer", true, show.AuditSubagentCap},
		{"reviewer", true, show.AuditSubagentCap},
		{"workflow-reviewer-abc", true, show.AuditSubagentCap},
		{"security", true, show.AuditSubagentCap},
		{"verifier", true, show.AuditSubagentCap},
		{"explorer", true, show.AuditSubagentCap},
		{"dreamer", true, show.AuditSubagentCap},
		{"documentation", true, show.AuditSubagentCap},
		{"research", true, show.AuditSubagentCap},
		{"sub-extra", true, show.AuditSubagentCap},
		{"judge", true, show.AuditSubagentCap},
		{"custom-role", true, show.AuditSubagentCap},
		{"custom-audit", true, show.AuditSubagentCap},
		{"unknown-role", true, show.AuditSubagentCap},
	}
	for _, tc := range cases {
		if gotAudit := search.IsAuditSubagentRole(tc.role); gotAudit != tc.wantAudit {
			t.Errorf("search.IsAuditSubagentRole(%q) = %v, want %v", tc.role, gotAudit, tc.wantAudit)
		}
		if gotCap := subagentCapForRole(tc.role); gotCap != tc.wantCap {
			t.Errorf("subagentCapForRole(%q) = %d, want %d", tc.role, gotCap, tc.wantCap)
		}
	}
}
