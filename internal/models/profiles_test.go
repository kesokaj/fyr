package models

import (
	"strings"
	"testing"
)

func TestModelClassification22Lineup(t *testing.T) {
	t.Parallel()
	lines := []string{
		"gemini-4-neon-high\tGemini 4 Neon (High)",
		"gemini-5-nova-medium\tGemini 5 Nova (Medium)",
		"gemini-4-carbon-high\tGemini 4 Carbon (High)",
		"gemini-4-carbon-medium\tGemini 4 Carbon (Medium)",
		"gemini-4-carbon-low\tGemini 4 Carbon (Low)",
		"gemini-4-argon-high\tGemini 4 Argon (High)",
		"gemini-4-hydrogen-xhigh\tGemini 4 Hydrogen (XHigh)",
		"gemini-4-hydrogen-medium\tGemini 4 Hydrogen (Medium)",
		"gemini-pro-orchestrator\tOrchestrator (Carbon)",
		"gemini-pro-orchestrator-neon\tOrchestrator (Neon)",
		"gemini-3.8-flash-high\tGemini 3.8 Flash (High)",
		"gemini-3.8-flash-medium\tGemini 3.8 Flash (Medium)",
		"gemini-2.5-flash-lite-low\tGemini 2.5 Flash Lite (Low)",
		"claude-opus-5-5-xhigh\tClaude Opus 5.5 (XHigh)",
		"claude-opus-5-5-high\tClaude Opus 5.5 (High)",
		"claude-sonnet-5-5-high\tClaude Sonnet 5.5 (High)",
		"claude-sonnet-5-5-medium\tClaude Sonnet 5.5 (Medium)",
		"claude-haiku-4-5-low\tClaude Haiku 4.5 (Low)",
		"gpt-oss-120b-medium\tGPT-OSS 120B (Medium, Retiring 2026-11-18)",
	}
	entries := ParseModelsOutput(strings.Join(lines, "\n") + "\n")
	if len(entries) != len(lines) {
		t.Fatalf("parsed %d entries, want %d", len(entries), len(lines))
	}
	byID := map[string]CatalogEntry{}
	for i := range entries {
		ApplyModelProfile(&entries[i])
		entries[i].Status = "active"
		byID[entries[i].ID] = entries[i]
	}

	checks := []struct {
		id          string
		family      string
		subfamily   string
		tier        string
		effort      string
		interactive bool
		retiring    bool
	}{
		{"gemini-4-neon-high", "gemini", "neon", "pro", "high", false, false},
		{"gemini-5-nova-medium", "gemini", "nova", "pro", "medium", false, false},
		{"gemini-4-hydrogen-xhigh", "gemini", "hydrogen", "pro", "xhigh", false, false},
		{"gemini-4-hydrogen-medium", "gemini", "hydrogen", "pro", "medium", false, false},
		{"gemini-4-argon-high", "gemini", "argon", "pro", "high", false, false},
		{"gemini-4-carbon-high", "gemini", "carbon", "pro", "high", false, false},
		{"gemini-pro-orchestrator", "gemini", "orchestrator", "pro", "orchestrator", true, false},
		{"gemini-pro-orchestrator-neon", "gemini", "orchestrator", "pro", "orchestrator", true, false},
		{"gemini-3.8-flash-medium", "gemini", "flash", "flash", "medium", false, false},
		{"gemini-2.5-flash-lite-low", "gemini", "flash-lite", "light", "low", false, false},
		{"claude-opus-5-5-xhigh", "anthropic", "opus", "pro", "xhigh", false, false},
		{"claude-sonnet-5-5-medium", "anthropic", "sonnet", "flash", "medium", false, false},
		{"claude-haiku-4-5-low", "anthropic", "haiku", "light", "low", false, false},
		{"gpt-oss-120b-medium", "other", "gpt-oss", "flash", "medium", false, true},
	}
	for _, c := range checks {
		got, ok := byID[c.id]
		if !ok {
			t.Fatalf("missing model %s", c.id)
		}
		if got.Family != c.family || got.Subfamily != c.subfamily || got.Tier != c.tier || got.Effort != c.effort || got.InteractiveOnly != c.interactive || (got.RetiringOn != "") != c.retiring {
			t.Errorf("%s: got (fam=%s sub=%s tier=%s eff=%s inter=%v ret=%q), want (fam=%s sub=%s tier=%s eff=%s inter=%v ret=%v)",
				c.id, got.Family, got.Subfamily, got.Tier, got.Effort, got.InteractiveOnly, got.RetiringOn,
				c.family, c.subfamily, c.tier, c.effort, c.interactive, c.retiring)
		}
	}

	builderFlashPick, err := PickFromEntries(entries, nil, "builder", "flash")
	if err != nil || byID[builderFlashPick.Model].Tier != "flash" {
		t.Errorf("builder:flash = %+v (err=%v), want flash-tier Gemini model", builderFlashPick, err)
	}
	builderLightPick, err := PickFromEntries(entries, nil, "mechanical", "light")
	if err != nil || builderLightPick.Model != "gemini-2.5-flash-lite-low" {
		t.Errorf("mechanical:light = %+v (err=%v), want gemini-2.5-flash-lite-low", builderLightPick, err)
	}
	for _, rt := range StandardRoleTiers {
		p, err := PickFromEntries(entries, nil, rt[0], rt[1])
		if err != nil {
			t.Fatalf("pick(%s,%s): %v", rt[0], rt[1], err)
		}
		if strings.Contains(p.Model, "orchestrator") || strings.Contains(p.Model, "gpt-oss") {
			t.Errorf("pick(%s,%s) chose forbidden model %q", rt[0], rt[1], p.Model)
		}
	}
}

func TestChangelogEnrichment(t *testing.T) {
	t.Parallel()
	entries := []CatalogEntry{
		{ID: "gemini-4-carbon-medium", Family: "gemini", Tier: "pro", Status: "active"},
		{ID: "gemini-4-neon-high", Family: "gemini", Tier: "pro", Status: "active", Evidence: ModelEvidence{WorkRuns: 3}},
		{ID: "gemini-pro-orchestrator", Family: "gemini", Tier: "pro", Status: "active"},
		{ID: "gpt-oss-120b-medium", Family: "other", Tier: "flash", Status: "active"},
		{ID: "custom-bare-v1", Family: "other", Tier: "pro", Status: "active"},
		{ID: "custom-retire-v1", Family: "other", Tier: "pro", Status: "active", Evidence: ModelEvidence{WorkRuns: 1}},
		{ID: "custom-deprecate-v1", Family: "other", Tier: "pro", Status: "active", Evidence: ModelEvidence{WorkRuns: 1}},
	}
	for i := range entries {
		ApplyModelProfile(&entries[i])
	}
	applyChangelogText(
		"2026-11-01 Notice: gpt-oss-120b retiring on 2026-11-18\n"+
			"2026-11-02 Release: Gemini 4 Carbon excels at deep coding.\n"+
			"2026-11-03 Release: Gemini 4 Neon adds fast reasoning.\n"+
			"2026-11-01 Notice: custom-retire-v1 retiring on 2026-11-25\n"+
			"2026-11-01 Notice: custom-deprecate-v1 deprecated in favor of v2",
		entries,
	)
	if entries[0].ChangelogNote == "" || entries[1].ChangelogNote == "" {
		t.Errorf("expected ChangelogNote on carbon and neon entries: %+v / %+v", entries[0], entries[1])
	}
	if entries[3].RetiringOn != "2026-11-18" {
		t.Errorf("gpt-oss RetiringOn = %q, want 2026-11-18", entries[3].RetiringOn)
	}
	if entries[5].RetiringOn != "2026-11-25" {
		t.Errorf("custom-retire-v1 RetiringOn = %q, want 2026-11-25", entries[5].RetiringOn)
	}
	if entries[6].RetiringOn != "" || entries[6].ChangelogNote == "" {
		t.Errorf("non-dated deprecation falsely populated RetiringOn or dropped note: %+v", entries[6])
	}
	if entries[2].RetiringOn != "" || entries[4].RetiringOn != "" || entries[4].ChangelogNote != "" {
		t.Errorf("bare model IDs falsely matched unrelated changelog line: orch=%+v bare=%+v", entries[2], entries[4])
	}
}

func TestChangelogContextWindowPrecedence(t *testing.T) {
	t.Parallel()
	entries := []CatalogEntry{
		{ID: "gemini-4-carbon-high", DisplayName: "Gemini 4 Carbon (High)", Family: "gemini", Tier: "pro", Status: "active"},
		{ID: "claude-opus-5-5-high", DisplayName: "Claude Opus 5.5 (High, 500K context)", Family: "anthropic", Tier: "pro", Status: "active"},
	}
	for i := range entries {
		ApplyModelProfile(&entries[i])
	}
	// Newest changelog entries appear first; older lines below must NOT overwrite newer ContextWindow,
	// and DisplayName context hints must never be overwritten by changelog lines.
	applyChangelogText(
		"2026-11-20: Gemini 4 Carbon upgraded to 2M context window.\n"+
			"2026-10-01: Gemini 4 Carbon launched with 500K context window.\n"+
			"2026-11-15: Claude Opus 5.5 supports 200K context window.\n",
		entries,
	)
	if entries[0].ContextWindow != 2_000_000 {
		t.Errorf("older changelog line overwrote newer ContextWindow: got %d, want 2000000", entries[0].ContextWindow)
	}
	if entries[1].ContextWindow != 500_000 {
		t.Errorf("changelog line overwrote DisplayName ContextWindow hint: got %d, want 500000", entries[1].ContextWindow)
	}
}

func TestResolveScopeRoleAndTier(t *testing.T) {
	t.Parallel()
	docsRole, docsTier, _ := ResolveScopeRoleAndTier([]string{"README.md", "SDD.md"}, "clarify section")
	if docsRole != "mechanical" || docsTier != "flash" {
		t.Errorf("docs scope = (%s,%s), want (mechanical,flash)", docsRole, docsTier)
	}
	secRole, secTier, _ := ResolveScopeRoleAndTier([]string{"internal/search/hook.go"}, "update guard")
	if secRole != "builder" || secTier != "pro" {
		t.Errorf("hook scope = (%s,%s), want (builder,pro)", secRole, secTier)
	}
	archRole, archTier, _ := ResolveScopeRoleAndTier([]string{"cmd/fyr/a.go"}, "architecture refactor")
	if archRole != "builder" || archTier != "pro" {
		t.Errorf("architecture scope = (%s,%s), want (builder,pro)", archRole, archTier)
	}
	smallRole, smallTier, _ := ResolveScopeRoleAndTier([]string{"cmd/fyr/a.go", "cmd/fyr/b.go"}, "add flag")
	if smallRole != "builder" || smallTier != "flash" {
		t.Errorf("small code scope = (%s,%s), want (builder,flash)", smallRole, smallTier)
	}
}
