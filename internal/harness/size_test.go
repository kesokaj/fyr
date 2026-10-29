package harness

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The Antigravity CLI builds ONE <user_rules> block from every file in
// ~/.gemini/config/rules/ and cuts it at this many bytes, marking the cut
// with "<truncated N bytes>" (BUG-085, measured 2026-09-21; the vendor page
// says 12,000 characters per file). A second file costs the first one room.
const antigravityRulesBlockBytes = 24000

// renderedContextBudgetBytes is the ceiling this repo pins itself to: an 8%
// margin under the observed cap (ADR-035). It is never raised by an
// implementer; a rule that does not fit is paid for by a deletion or a
// merge, or lives in a skill (PRD § Known limits).
const renderedContextBudgetBytes = 22000

// TestRenderedContextFitsAntigravityBlock renders THIS checkout the way
// `fyr install` does and measures the whole block Antigravity would
// build: the rendered context plus every rules/<name>.md, which deploys
// beside it. It reads the repository at ../.. like TestHarnessMap, so it
// fails — never skips — when run from anywhere else.
func TestRenderedContextFitsAntigravityBlock(t *testing.T) {
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
	rules := 0
	for _, r := range inv.Rules {
		rules += len(r.Raw)
	}
	total := len(ctx) + rules
	if total > renderedContextBudgetBytes {
		t.Fatalf("Antigravity rules block is %d bytes (context %d, rules %d), budget %d (the CLI cuts the block at %d): cut %d bytes",
			total, len(ctx), rules, renderedContextBudgetBytes, antigravityRulesBlockBytes, total-renderedContextBudgetBytes)
	}
	t.Logf("Antigravity rules block is %d bytes (context %d, rules %d), budget %d", total, len(ctx), rules, renderedContextBudgetBytes)
}

// renderedContextTargetBytes is the harness's own ceiling for the hand-written
// context (the render before its generated `## Installed` inventory), under the Antigravity block pin above: the audit of 2026-09-30
// found the context 94 bytes from that pin, so no rule could be added and no
// rules/ file deployed (plan audit-fix, C1, D2b). Adherence tracks the number
// of distinct instructions, not bytes, so the cut moved whole rules into the
// skill or agent that acts on them. This test fails above 15,000, the only
// ceiling for the hand-written part (plan 2026-10-25-harness-audit-fix D10).
const renderedContextTargetBytes = 15000

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
	// The hand-written part only: the generated `## Installed` inventory grows with
	// every skill dropped in, and a file drop must never fail this pin (R5); the
	// whole render, inventory included, stays under the Antigravity block in the
	// test above.
	written := string(ctx)
	if i := strings.Index(written, "\n## Installed"); i >= 0 {
		written = written[:i]
	}
	if len(written) > renderedContextTargetBytes {
		t.Fatalf("hand-written context is %d bytes, target %d: move a whole rule into the skill or agent that acts on it, never shorten a sentence", len(written), renderedContextTargetBytes)
	}
	t.Logf("hand-written context is %d bytes (render %d with the inventory), target %d", len(written), len(ctx), renderedContextTargetBytes)
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
