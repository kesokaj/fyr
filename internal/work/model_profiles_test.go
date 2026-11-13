package work

import (
	"context"
	"strings"
	"testing"
)

func TestModelClassification22Lineup(t *testing.T) {
	lines := []string{
		"gemini-4-carbon-high\tGemini 4 Carbon (High)",
		"gemini-4-carbon-medium\tGemini 4 Carbon (Medium)",
		"gemini-4-carbon-low\tGemini 4 Carbon (Low)",
		"gemini-4-argon-high\tGemini 4 Argon (High)",
		"gemini-4-argon-medium\tGemini 4 Argon (Medium)",
		"gemini-4-argon-low\tGemini 4 Argon (Low)",
		"gemini-4-hydrogen-xhigh\tGemini 4 Hydrogen (XHigh)",
		"gemini-4-hydrogen-high\tGemini 4 Hydrogen (High)",
		"gemini-4-hydrogen-medium\tGemini 4 Hydrogen (Medium)",
		"gemini-4-hydrogen-low\tGemini 4 Hydrogen (Low)",
		"gemini-pro-orchestrator\tOrchestrator (Carbon)",
		"gemini-pro-orchestrator-argon\tOrchestrator (Argon)",
		"gemini-3.8-flash-high\tGemini 3.8 Flash (High)",
		"gemini-3.8-flash-medium\tGemini 3.8 Flash (Medium)",
		"gemini-3.8-flash-low\tGemini 3.8 Flash (Low)",
		"claude-opus-5-5-xhigh\tClaude Opus 5.5 (XHigh)",
		"claude-opus-5-5-high\tClaude Opus 5.5 (High)",
		"claude-opus-5-5-medium\tClaude Opus 5.5 (Medium)",
		"claude-sonnet-5-5-high\tClaude Sonnet 5.5 (High)",
		"claude-sonnet-5-5-medium\tClaude Sonnet 5.5 (Medium)",
		"claude-sonnet-5-5-low\tClaude Sonnet 5.5 (Low)",
		"gpt-oss-120b-medium\tGPT-OSS 120B (Medium)",
	}
	entries := ParseModelsOutput(strings.Join(lines, "\n") + "\n")
	if len(entries) != 22 {
		t.Fatalf("parsed %d entries, want 22", len(entries))
	}
	byID := map[string]CatalogEntry{}
	for _, e := range entries {
		applyModelProfile(&e)
		e.Status = "active"
		byID[e.ID] = e
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
		{"gemini-4-hydrogen-xhigh", "gemini", "hydrogen", "pro", "xhigh", false, false},
		{"gemini-4-hydrogen-high", "gemini", "hydrogen", "pro", "high", false, false},
		{"gemini-4-hydrogen-medium", "gemini", "hydrogen", "flash", "medium", false, false},
		{"gemini-4-hydrogen-low", "gemini", "hydrogen", "flash", "low", false, false},
		{"gemini-4-argon-high", "gemini", "argon", "pro", "high", false, false},
		{"gemini-4-carbon-high", "gemini", "carbon", "pro", "high", false, false},
		{"gemini-pro-orchestrator", "gemini", "orchestrator", "pro", "orchestrator", true, false},
		{"gemini-pro-orchestrator-argon", "gemini", "orchestrator", "pro", "orchestrator", true, false},
		{"gemini-3.8-flash-medium", "gemini", "flash", "flash", "medium", false, false},
		{"claude-opus-5-5-xhigh", "anthropic", "opus", "pro", "xhigh", false, false},
		{"claude-sonnet-5-5-medium", "anthropic", "sonnet", "flash", "medium", false, false},
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

	var all []CatalogEntry
	for _, e := range byID {
		all = append(all, e)
	}
	plannerPick, err := pickFromEntries(all, nil, "planner", "pro")
	if err != nil || !strings.HasPrefix(plannerPick.Model, "gemini-4-argon-") {
		t.Errorf("planner:pro = %+v (err=%v), want gemini-4-argon-*", plannerPick, err)
	}
	builderPick, err := pickFromEntries(all, nil, "builder", "pro")
	if err != nil || !strings.HasPrefix(builderPick.Model, "gemini-4-carbon-") {
		t.Errorf("builder:pro = %+v (err=%v), want gemini-4-carbon-*", builderPick, err)
	}
	builderFlashPick, err := pickFromEntries(all, nil, "builder", "flash")
	if err != nil || builderFlashPick.Model != "gemini-4-hydrogen-medium" || byID[builderFlashPick.Model].Tier != "flash" {
		t.Errorf("builder:flash = %+v (err=%v), want flash-tier gemini-4-hydrogen-medium", builderFlashPick, err)
	}
	for _, rt := range StandardRoleTiers {
		p, err := pickFromEntries(all, nil, rt[0], rt[1])
		if err != nil {
			t.Fatalf("pick(%s,%s): %v", rt[0], rt[1], err)
		}
		if strings.Contains(p.Model, "orchestrator") || strings.Contains(p.Model, "gpt-oss") {
			t.Errorf("pick(%s,%s) chose forbidden model %q", rt[0], rt[1], p.Model)
		}
	}
}

func TestChangelogEnrichmentAndUnjudgedCanaries(t *testing.T) {
	entries := []CatalogEntry{
		{ID: "gemini-4-carbon-medium", Family: "gemini", Tier: "pro", Status: "active"},
		{ID: "gemini-4-argon-high", Family: "gemini", Tier: "pro", Status: "active", Evidence: ModelEvidence{WorkRuns: 3}},
		{ID: "gemini-pro-orchestrator", Family: "gemini", Tier: "pro", Status: "active"},
		{ID: "gpt-oss-120b-medium", Family: "other", Tier: "flash", Status: "active"},
		{ID: "custom-bare-v1", Family: "other", Tier: "pro", Status: "active"},
	}
	for i := range entries {
		applyModelProfile(&entries[i])
	}
	applyChangelogText("2026-11-01 Notice: gpt-oss-120b retiring on 2026-11-18\n2026-11-02 Release: Gemini 4 Carbon excels at deep coding.", entries)
	if entries[0].ChangelogNote == "" {
		t.Errorf("expected ChangelogNote on carbon entry")
	}
	if entries[3].RetiringOn != "2026-11-18" {
		t.Errorf("gpt-oss RetiringOn = %q, want 2026-11-18", entries[3].RetiringOn)
	}
	if entries[2].RetiringOn != "" || entries[4].RetiringOn != "" || entries[4].ChangelogNote != "" {
		t.Errorf("bare model IDs falsely matched unrelated changelog line: orch=%+v bare=%+v", entries[2], entries[4])
	}

	unjudged := UnjudgedActiveModels(Catalog{Entries: entries})
	if len(unjudged) != 2 || unjudged[0].ID != "gemini-4-carbon-medium" || unjudged[1].ID != "custom-bare-v1" {
		t.Errorf("UnjudgedActiveModels = %+v, want [gemini-4-carbon-medium, custom-bare-v1]", unjudged)
	}
}

func TestResolveAutoRoleAndKickoffAutoPicker(t *testing.T) {
	docsRole, docsTier, _ := ResolveAutoRoleAndTier(Kickoff{Files: []string{"README.md", "SDD.md"}, Text: "clarify section"})
	if docsRole != "mechanical" || docsTier != "flash" {
		t.Errorf("docs kickoff = (%s,%s), want (mechanical,flash)", docsRole, docsTier)
	}
	secRole, secTier, _ := ResolveAutoRoleAndTier(Kickoff{Files: []string{"internal/search/hook.go"}, Text: "update guard"})
	if secRole != "builder" || secTier != "pro" {
		t.Errorf("hook kickoff = (%s,%s), want (builder,pro)", secRole, secTier)
	}
	archRole, archTier, _ := ResolveAutoRoleAndTier(Kickoff{Files: []string{"cmd/fyr/a.go"}, Text: "architecture refactor"})
	if archRole != "builder" || archTier != "pro" {
		t.Errorf("architecture kickoff = (%s,%s), want (builder,pro)", archRole, archTier)
	}
	smallRole, smallTier, _ := ResolveAutoRoleAndTier(Kickoff{Files: []string{"cmd/fyr/a.go", "cmd/fyr/b.go"}, Text: "add flag"})
	if smallRole != "builder" || smallTier != "flash" {
		t.Errorf("small code kickoff = (%s,%s), want (builder,flash)", smallRole, smallTier)
	}

	listed := map[string]bool{
		"gemini-4-carbon-medium":  true,
		"gemini-4-hydrogen-high":  true,
		"gemini-3.8-flash-medium": true,
		"claude-opus-5-5-medium":  true,
		"gemini-pro-orchestrator": true,
	}
	ids := []string{
		"gemini-4-carbon-medium",
		"gemini-4-hydrogen-high",
		"gemini-3.8-flash-medium",
		"claude-opus-5-5-medium",
		"gemini-pro-orchestrator",
	}
	ks := []Kickoff{
		{Path: "k1.md", Model: "auto", JudgeModel: "auto", Files: []string{"README.md"}, Text: "update doc"},
		{Path: "k2.md", Model: "auto", Files: []string{"internal/search/hook.go"}, Text: "update hook"},
		{Path: "k3.md", Model: "", Files: []string{"cmd/fyr/main.go"}, Text: "omitted stays empty"},
	}
	o := Options{Home: t.TempDir()}
	if err := resolveAutoModels(context.Background(), &o, ks, listed, ids); err != nil {
		t.Fatalf("resolveAutoModels: %v", err)
	}
	if ks[0].Model != "gemini-3.8-flash-medium" {
		t.Errorf("k1 auto model = %q, want gemini-3.8-flash-medium", ks[0].Model)
	}
	if ks[0].JudgeModel != "" {
		t.Errorf("k1 auto judge = %q, want empty (dynamic cross-family)", ks[0].JudgeModel)
	}
	if ks[1].Model != "gemini-4-carbon-medium" {
		t.Errorf("k2 auto model = %q, want gemini-4-carbon-medium", ks[1].Model)
	}
	if ks[2].Model != "" {
		t.Errorf("omitted Model must stay empty, got %q", ks[2].Model)
	}

	bad := []Kickoff{{Path: "kbad.md", Model: "gemini-pro-orchestrator"}}
	if err := resolveAutoModels(context.Background(), &o, bad, listed, ids); err == nil || !strings.Contains(err.Error(), "interactive orchestrator") {
		t.Errorf("expected interactive orchestrator refusal, got %v", err)
	}

	// Regression: work.Check passes ks=nil with Options.Model=="auto" and must not panic.
	oAuto := Options{Home: o.Home, Model: "auto"}
	if err := resolveAutoModels(context.Background(), &oAuto, nil, listed, ids); err != nil {
		t.Fatalf("resolveAutoModels(ks=nil, Model=auto): %v", err)
	}
	if oAuto.Model != "gemini-4-carbon-medium" {
		t.Errorf("oAuto.Model = %q, want gemini-4-carbon-medium", oAuto.Model)
	}
}
