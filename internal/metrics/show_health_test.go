package metrics

import (
	"testing"

	"github.com/kesokaj/fyr/internal/search"
)

func TestSubagentCapForRoleAlignedWithSearch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		role      string
		wantAudit bool
		wantCap   int
	}{
		// Empty/blank subagent agent_type intentionally falls back to auditSubagentCap (40),
		// whereas search.IsAuditSubagentRole treats empty as unresolved (false).
		{"", false, auditSubagentCap},
		{"   ", false, auditSubagentCap},
		// Main is not an audit subagent role.
		{"main", false, builderSubagentCap},
		{"MAIN", false, builderSubagentCap},
		// Builder / coder / self roles get the 120-call budget.
		{"self", false, builderSubagentCap},
		{"builder", false, builderSubagentCap},
		{"builder-phase0", false, builderSubagentCap},
		{"workflow-builder-8a62522f", false, builderSubagentCap},
		{"coder", false, builderSubagentCap},
		{"coder-1", false, builderSubagentCap},
		// Audit, specialized, and custom/unknown roles align on the 40-call audit budget.
		{"worker-1", true, auditSubagentCap},
		{"worker-2", true, auditSubagentCap},
		{"worker-x", true, auditSubagentCap},
		{"implementer", true, auditSubagentCap},
		{"reviewer", true, auditSubagentCap},
		{"workflow-reviewer-abc", true, auditSubagentCap},
		{"security", true, auditSubagentCap},
		{"verifier", true, auditSubagentCap},
		{"explorer", true, auditSubagentCap},
		{"dreamer", true, auditSubagentCap},
		{"documentation", true, auditSubagentCap},
		{"research", true, auditSubagentCap},
		{"sub-extra", true, auditSubagentCap},
		{"judge", true, auditSubagentCap},
		{"custom-role", true, auditSubagentCap},
		{"custom-audit", true, auditSubagentCap},
		{"unknown-role", true, auditSubagentCap},
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
