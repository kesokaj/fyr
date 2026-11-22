package models

import (
	"testing"
	"time"
)

func TestEffectiveContextBudgetAndKnee(t *testing.T) {
	cases := []struct {
		model      string
		wantBudget int64
		wantKnee   int64
	}{
		{"gemini-4-neon-high", 500000, 300000},
		{"models/gemini-4-neon-high", 500000, 300000},
		{"gemini-4-carbon-medium", 500000, 300000},
		{"gemini-4-argon-low", 500000, 300000},
		{"gemini-4-hydrogen-xhigh", 500000, 300000},
		{"gemini-pro-orchestrator-neon", 500000, 300000},
		{"orchestrator(gemini-4-carbon+gemini-4-hydrogen)", 500000, 300000},
		{"orchestrator(gpt-oss-120b+claude-opus-4-6)", 300000, 180000},
		{"orchestrator(gpt-oss-120b)", 128000, 76800},
		{"orchestrator()", 300000, 180000},
		{"gemini-flash-worker-agy", 500000, 300000},
		{"claude-opus-5-5-high", 500000, 300000},
		{"models/claude-opus-5-5-high", 500000, 300000},
		{"claude-sonnet-5-5-medium", 500000, 300000},
		{"claude-opus-4-6", 300000, 180000},
		{"gemini-3.8-flash-high", 300000, 180000},
		{"gpt-oss-120b-medium", 128000, 76800},
		{"models/gpt-oss-120b-medium", 128000, 76800},
		{"", 300000, 180000},
		{"custom-unknown-model", 300000, 180000},
	}
	for _, tc := range cases {
		if got := EffectiveContextBudget(tc.model); got != tc.wantBudget {
			t.Errorf("EffectiveContextBudget(%q) = %d, want %d", tc.model, got, tc.wantBudget)
		}
		if got := ContextKneeTokens(tc.model); got != tc.wantKnee {
			t.Errorf("ContextKneeTokens(%q) = %d, want %d", tc.model, got, tc.wantKnee)
		}
	}
}

func TestContextWindowOverridesAndPreserveOnMerge(t *testing.T) {
	home := t.TempDir()
	cat := Catalog{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Entries: []CatalogEntry{
			{ID: "gemini-4-neon-high", DisplayName: "Neon", Status: "active", ContextWindow: 700000},
			{ID: "gemini-4-argon-high", DisplayName: "Argon", Status: "active", ContextWindow: 1000000},
			{ID: "claude-opus-5-5-high", DisplayName: "Opus", Status: "active"},
		},
	}
	if err := SaveCatalog(home, cat); err != nil {
		t.Fatalf("SaveCatalog: %v", err)
	}
	// Explicit mid-range operator override (700k) keeps 60% = 420k.
	if got := ContextKneeTokensForHome(home, "GEMINI-4-NEON-HIGH"); got != 420000 {
		t.Fatalf("ContextKneeTokensForHome override = %d, want 420000", got)
	}
	if got := ContextKneeTokensForHome(home, "models/gemini-4-neon-high"); got != 420000 {
		t.Fatalf("ContextKneeTokensForHome models/ override = %d, want 420000", got)
	}
	// 1M physical window is calibrated and capped at 300k empirical degradation onset.
	if got := ContextKneeTokensForHome(home, "gemini-4-argon-high"); got != 300000 {
		t.Fatalf("ContextKneeTokensForHome 1M calibrated = %d, want 300000", got)
	}
	if got := ContextKneeTokensForHome(home, "claude-opus-5-5-high"); got != 300000 {
		t.Fatalf("ContextKneeTokensForHome default = %d, want 300000", got)
	}
	merged := mergeCatalogEntries("2026-11-20", []CatalogEntry{
		{ID: "gemini-4-neon-high", DisplayName: "Neon"},
	}, &cat, nil)
	if len(merged) != 3 || merged[0].ContextWindow != 700000 {
		t.Fatalf("merged entries lost ContextWindow override: %+v", merged)
	}
	overrides := ContextWindowOverrides(home)
	if got := ContextBudgetWithOverrides("orchestrator(gemini-4-neon-high+gpt-oss-120b)", overrides); got != 700000 {
		t.Fatalf("composite override budget = %d, want 700000", got)
	}
}
