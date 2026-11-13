package metrics

import "testing"

func TestNormalizeSessionModel(t *testing.T) {
	if got := normalizeSessionModel("gemini-4-carbon-medium", map[string]bool{"gemini-4-carbon-medium": true}); got != "gemini-4-carbon-medium" {
		t.Errorf("single model = %q, want gemini-4-carbon-medium", got)
	}
	if got := normalizeSessionModel("gemini-4-carbon-medium", map[string]bool{"gemini-4-carbon-medium": true, "gemini-4-hydrogen-medium": true}); got != "gemini-pro-orchestrator" {
		t.Errorf("carbon+hydrogen = %q, want gemini-pro-orchestrator", got)
	}
	if got := normalizeSessionModel("gemini-4-argon-high", map[string]bool{"gemini-4-argon-high": true, "gemini-4-hydrogen-high": true}); got != "gemini-pro-orchestrator-argon" {
		t.Errorf("argon+hydrogen = %q, want gemini-pro-orchestrator-argon", got)
	}
	if got := normalizeSessionModel("gemini-4-carbon-medium", map[string]bool{"gemini-4-carbon-medium": true, "claude-opus-5-5-medium": true}); got != "orchestrator(claude-opus-5-5-medium+gemini-4-carbon-medium)" {
		t.Errorf("mixed non-hydrogen = %q, want orchestrator(claude-opus-5-5-medium+gemini-4-carbon-medium)", got)
	}
}
