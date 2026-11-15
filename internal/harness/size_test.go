package harness

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The Antigravity CLI builds ONE <user_rules> block from always-on rules in
// ~/.gemini/config/rules/ (while trigger: model_decision rules list path and
// description only) and cuts always-on rules at 24,000 bytes (BUG-085, measured
// 2026-09-21). Both combined and always-on budgets below stay strict.
const antigravityRulesBlockBytes = 24000

// renderedContextBudgetBytes is the combined block ceiling (rendered context
// plus every rules/<name>.md, always-on or triggered), pinned inside PRD §
// Known limits' 22,000-byte cap and Antigravity's 24,000-byte block (ADR-035).
// It is never raised by an implementer; a rule that does not fit is paid for
// by a deletion or a merge, or lives in a skill (PRD § Known limits).
const renderedContextBudgetBytes = 19500

// alwaysOnContextBudgetBytes caps the always-on slice (rendered context plus
// rules/*.md whose trigger is unset or always_on) per PLAN v9 Phase 1.
const alwaysOnContextBudgetBytes = 9000

// repoAgentsBudgetBytes enforces PRD G2 (wc -c AGENTS.md <= 4,500 B).
const repoAgentsBudgetBytes = 4500

type rulesBlockStats struct {
	ctx            int
	alwaysOnRules  int
	triggeredRules int
}

func (s rulesBlockStats) alwaysOn() int { return s.ctx + s.alwaysOnRules }
func (s rulesBlockStats) total() int    { return s.ctx + s.alwaysOnRules + s.triggeredRules }

func isAlwaysOnRule(d Doc) bool {
	return d.Trigger == "" || d.Trigger == "always_on"
}

func measureRulesBlock(src Source) (rulesBlockStats, error) {
	if _, err := fs.Stat(src.FS, "context/AGENTS.md"); err != nil {
		return rulesBlockStats{}, fmt.Errorf("reading context/AGENTS.md: %w", err)
	}
	inv, err := LoadInventory(src)
	if err != nil {
		return rulesBlockStats{}, err
	}
	ctx, err := RenderContext(src, inv)
	if err != nil {
		return rulesBlockStats{}, err
	}
	stats := rulesBlockStats{ctx: len(ctx)}
	for _, r := range inv.Rules {
		if isAlwaysOnRule(r) {
			stats.alwaysOnRules += len(r.Raw)
		} else {
			stats.triggeredRules += len(r.Raw)
		}
	}
	return stats, nil
}

func checkCombinedBlockBudget(s rulesBlockStats, budget int) error {
	if s.total() > budget {
		return fmt.Errorf("Antigravity rules block is %d bytes (context %d, always-on rules %d, triggered rules %d), budget %d (the CLI cuts the block at %d): cut %d bytes",
			s.total(), s.ctx, s.alwaysOnRules, s.triggeredRules, budget, antigravityRulesBlockBytes, s.total()-budget)
	}
	return nil
}

func checkAlwaysOnBudget(s rulesBlockStats, budget int) error {
	if s.alwaysOn() > budget {
		return fmt.Errorf("always-on context + rules is %d bytes (context %d, always-on rules %d; triggered rules %d), budget %d: cut or scope %d bytes",
			s.alwaysOn(), s.ctx, s.alwaysOnRules, s.triggeredRules, budget, s.alwaysOn()-budget)
	}
	return nil
}

// TestRenderedContextFitsAntigravityBlock renders THIS checkout the way
// `fyr install` does and measures the whole block Antigravity would
// build: the rendered context plus every rules/<name>.md, which deploys
// beside it. It reads the repository at ../.. like TestHarnessMap, so it
// fails — never skips — when run from anywhere else.
func TestRenderedContextFitsAntigravityBlock(t *testing.T) {
	stats, err := measureRulesBlock(DirSource(filepath.Join("..", "..")))
	if err != nil {
		t.Fatalf("this test reads the checkout at ../..: %v", err)
	}
	if err := checkCombinedBlockBudget(stats, renderedContextBudgetBytes); err != nil {
		t.Fatal(err)
	}
	t.Logf("Antigravity rules block is %d bytes (context %d, always-on rules %d, triggered rules %d), budget %d",
		stats.total(), stats.ctx, stats.alwaysOnRules, stats.triggeredRules, renderedContextBudgetBytes)
}

func TestAlwaysOnContextBudget(t *testing.T) {
	stats, err := measureRulesBlock(DirSource(filepath.Join("..", "..")))
	if err != nil {
		t.Fatalf("this test reads the checkout at ../..: %v", err)
	}
	if err := checkAlwaysOnBudget(stats, alwaysOnContextBudgetBytes); err != nil {
		t.Fatal(err)
	}
	t.Logf("always-on rules block is %d bytes (context %d, always-on rules %d; triggered rules %d), budget %d",
		stats.alwaysOn(), stats.ctx, stats.alwaysOnRules, stats.triggeredRules, alwaysOnContextBudgetBytes)
}

// renderedContextTargetBytes is the harness's own pin for the rendered global
// context (context/*.md plus Marker and Canary), lowered from PRD G1's 15,000-byte
// ceiling to 13,000 by the D11 Item 35 context diet (plan 2026-10-25-harness-audit-fix
// D10; PLAN-2026-11-02-1806-fyr-speed Item 35).
const renderedContextTargetBytes = 13000

func TestRenderedContextUnderTarget(t *testing.T) {
	src := DirSource(filepath.Join("..", ".."))
	if _, err := fs.Stat(src.FS, "context/AGENTS.md"); err != nil {
		t.Fatalf("this test reads the checkout at ../..: %v", err)
	}
	inv, err := LoadInventory(src)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := RenderContext(src, inv)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx) > renderedContextTargetBytes {
		t.Fatalf("rendered context is %d bytes, target %d: move a whole rule into the skill or agent that acts on it, never shorten a sentence", len(ctx), renderedContextTargetBytes)
	}
	t.Logf("rendered context is %d bytes, target %d", len(ctx), renderedContextTargetBytes)
}

func TestRepoAgentsUnderTarget(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > repoAgentsBudgetBytes {
		t.Fatalf("repo AGENTS.md is %d bytes, target <= %d (PRD G2): move repo rules into .agents/rules/*.md", len(b), repoAgentsBudgetBytes)
	}
	t.Logf("repo AGENTS.md is %d bytes, target <= %d", len(b), repoAgentsBudgetBytes)
}

func TestSizeChecksFailOnOversizedRule(t *testing.T) {
	alwaysOver := mapSource(map[string]string{
		"context/AGENTS.md":     "# AGENTS.md\n",
		"skills/alpha/SKILL.md": "---\nname: alpha\ndescription: a\n---\nbody\n",
		"rules/big.md":          "---\ntrigger: always_on\n---\n" + strings.Repeat("a", alwaysOnContextBudgetBytes+1),
	})
	stats, err := measureRulesBlock(alwaysOver)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkAlwaysOnBudget(stats, alwaysOnContextBudgetBytes); err == nil {
		t.Errorf("oversized always_on rule (%d B) unexpectedly passed always-on budget %d", stats.alwaysOn(), alwaysOnContextBudgetBytes)
	}
	if err := checkCombinedBlockBudget(stats, renderedContextBudgetBytes); err != nil {
		t.Errorf("always-on-only overflow (%d B) should stay under combined budget %d: %v", stats.total(), renderedContextBudgetBytes, err)
	}

	triggeredOver := mapSource(map[string]string{
		"context/AGENTS.md":     "# AGENTS.md\n",
		"skills/alpha/SKILL.md": "---\nname: alpha\ndescription: a\n---\nbody\n",
		"rules/big.md":          "---\ntrigger: model_decision\ndescription: \"probe\"\n---\n" + strings.Repeat("b", renderedContextBudgetBytes+1),
	})
	stats2, err := measureRulesBlock(triggeredOver)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkAlwaysOnBudget(stats2, alwaysOnContextBudgetBytes); err != nil {
		t.Errorf("triggered rule should not count against always-on budget: %v", err)
	}
	if err := checkCombinedBlockBudget(stats2, renderedContextBudgetBytes); err == nil {
		t.Errorf("oversized model_decision rule (%d B) unexpectedly passed combined budget %d", stats2.total(), renderedContextBudgetBytes)
	}
}

// TestSkillListingBudget pins what the CLI must list at session start: the
// names and descriptions of every model-invocable skill. Claude Code shows
// descriptions for about 19,800 bytes of them and bare names beyond that
// (measured 2026-10-01 with 173 skills installed: descriptions stopped after
// the 41st alphabetically; plans/2026-10-01-research-google-skills-catalog.md),
// so a skill past the budget has nothing that tells the model when to load it.
// Antigravity's budget is larger (about 44,000) and drops the longest first.
func TestSkillListingBudget(t *testing.T) {
	const budget = 19500
	skills, err := loadSkills(DirSource(filepath.Join("..", "..")))
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		name string
		n    int
	}
	var sum int
	var all []entry
	for _, s := range skills {
		if s.SlashOnly {
			continue // not listed to the model
		}
		sum += len(s.Name) + len(s.Description)
		all = append(all, entry{s.Name, len(s.Description)})
	}
	if sum <= budget {
		t.Logf("skill listing is %d bytes of names and descriptions over %d skills, budget %d", sum, len(all), budget)
		return
	}
	sort.Slice(all, func(i, j int) bool { return all[i].n > all[j].n })
	var longest []string
	for _, e := range all[:5] {
		longest = append(longest, fmt.Sprintf("%s (%d)", e.name, e.n))
	}
	t.Errorf("skill listing is %d bytes of names and descriptions, over the budget of %d: shorten a description; the longest are %s", sum, budget, strings.Join(longest, ", "))
}
