package search

import "testing"

func TestIsExcludedASTPathVendoredSkills(t *testing.T) {
	excluded := []string{
		"skills/frontend-slate-ui/references/components/button.ts",
		"././skills/frontend-slate-ui/references/components/button.ts",
		"skills/frontend-slate-ui/./assets/tokens.css",
		".agents/skills/demo-ui/references/guide.md",
		".agents/skills/demo-ui/assets/theme.js",
	}
	for _, p := range excluded {
		if !isExcludedASTPath(p) {
			t.Errorf("isExcludedASTPath(%q) = false, want true", p)
		}
	}

	kept := []string{
		"skills/fyr-bug/SKILL.md",
		"skills/fyr-eval/scripts/run.sh",
		"internal/search/references/foo.go",
		"pkg/skills/combat/references/table.go",
		"cmd/fyr/main.go",
	}
	for _, p := range kept {
		if isExcludedASTPath(p) {
			t.Errorf("isExcludedASTPath(%q) = true, want false", p)
		}
	}

	if syms, _ := extractSymbolsAndRefs("skills/bash/references/testing.md", "# Heading\n"); len(syms) != 0 {
		t.Errorf("vendored reference markdown extracted %d symbols, want 0", len(syms))
	}
	if syms, _ := extractSymbolsAndRefs("skills/bash/SKILL.md", "# Bash\n"); len(syms) == 0 {
		t.Errorf("skills/bash/SKILL.md extracted 0 symbols, want > 0")
	}
	if !IsVendoredSkillPath(".agents/skills/demo-ui/references/guide.md") || IsVendoredSkillPath("skills/fyr-bug/SKILL.md") {
		t.Errorf("IsVendoredSkillPath classification mismatch")
	}
	if _, _, ok := vendoredExtractAlias("skills/demo/assets/bundle.min.js"); ok {
		t.Errorf("minified vendored asset should stay AST-excluded")
	}
	if norm, alias, ok := vendoredExtractAlias("skills/demo/references/button.ts"); !ok || norm != "skills/demo/references/button.ts" || alias == "" {
		t.Errorf("vendoredExtractAlias = (%q, %q, %v), want valid alias", norm, alias, ok)
	}
}
