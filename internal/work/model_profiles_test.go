package work

import (
	"context"
	"strings"
	"testing"
)

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
