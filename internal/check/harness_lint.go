package check

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

const (
	// maxContextBytes is PRD G1's ceiling on context/*.md.
	maxContextBytes = 15000
	// maxRulesBlockBytes is PRD Known limits' ceiling on combined context/*.md
	// plus all rules/*.md (always-on and model_decision) before Antigravity
	// silently truncates (matching internal/harness/size_test.go).
	maxRulesBlockBytes = 24000
	// maxProjectAgentsBytes is ADR-535's ceiling on a tracked project root AGENTS.md.
	maxProjectAgentsBytes = 4500
	// maxAgentsTreeLines is the max contiguous box-drawing tree block in AGENTS.md.
	maxAgentsTreeLines = 15
)

// contextRulesBudgetRule enforces context/*.md <= 15,000 B and combined
// context/*.md + rules/*.md <= 24,000 B (including model_decision rules) when
// those directories exist in the checkout (drift-M7).
func (k *checker) contextRulesBudgetRule() []Finding {
	isMarked := k.noIndexed()
	var ctxBytes, ruleBytes int
	var hasCtx, hasRules bool
	for _, p := range k.tracked {
		if !strings.HasSuffix(p, ".md") || isMarked(path.Dir(p)) {
			continue
		}
		switch path.Dir(p) {
		case "context":
			hasCtx = true
			if data, err := k.readFile(filepath.FromSlash(p)); err == nil {
				ctxBytes += len(data)
			}
		case "rules":
			hasRules = true
			if data, err := k.readFile(filepath.FromSlash(p)); err == nil {
				ruleBytes += len(data)
			}
		}
	}
	var out []Finding
	if hasCtx && ctxBytes > maxContextBytes {
		out = append(out, Finding{
			Where: "context",
			Rule:  "budget",
			Text:  fmt.Sprintf("context/*.md is %d bytes, over the %d-byte ceiling", ctxBytes, maxContextBytes),
		})
	}
	total := ctxBytes + ruleBytes
	if (hasCtx || hasRules) && total > maxRulesBlockBytes {
		where := "context"
		if hasRules {
			where = "rules"
		}
		out = append(out, Finding{
			Where: where,
			Rule:  "budget",
			Text:  fmt.Sprintf("context/*.md + rules/*.md is %d bytes (context %d, rules %d), over the %d-byte ceiling", total, ctxBytes, ruleBytes, maxRulesBlockBytes),
		})
	}
	return out
}

// skillFrontmatterRule lints every tracked skills/<name>/SKILL.md frontmatter
// via harness.LintSkillFrontmatter (drift-M8), matching fyr install and
// TestSkillFrontmatterDescriptionsUnder140Chars (including local forks listed
// in skills/VENDORED.md).
func (k *checker) skillFrontmatterRule() []Finding {
	isMarked := k.noIndexed()
	var out []Finding
	for _, p := range k.tracked {
		parts := strings.Split(p, "/")
		if len(parts) != 3 || parts[0] != "skills" || parts[2] != "SKILL.md" {
			continue
		}
		if isMarked(path.Dir(p)) {
			continue
		}
		data, err := k.readFile(filepath.FromSlash(p))
		if err != nil {
			continue
		}
		if _, err := harness.LintSkillFrontmatter(parts[1], data); err != nil {
			out = append(out, Finding{Where: p, Rule: "frontmatter", Text: err.Error()})
		}
	}
	return out
}

// projectAgentsBudgetRule enforces ADR-535's size and directory-tree bounds on
// a tracked root AGENTS.md: <= 4500 bytes and no > 15-line box-drawing tree block.
func (k *checker) projectAgentsBudgetRule() []Finding {
	if !k.topFiles["AGENTS.md"] || k.noIndexed()(".") {
		return nil
	}
	data, err := k.readFile("AGENTS.md")
	if err != nil {
		return nil
	}
	var out []Finding
	if len(data) > maxProjectAgentsBytes {
		out = append(out, Finding{
			Where: "AGENTS.md",
			Rule:  "budget",
			Text:  fmt.Sprintf("AGENTS.md is %d bytes, over the %d-byte ceiling", len(data), maxProjectAgentsBytes),
		})
	}
	out = append(out, agentsTreeBlockFindings(string(data))...)
	return out
}

func agentsTreeBlockFindings(text string) []Finding {
	var out []Finding
	runStart, runLen := 0, 0
	flush := func() {
		if runLen > maxAgentsTreeLines {
			out = append(out, Finding{
				Where: "AGENTS.md",
				Line:  runStart,
				Rule:  "budget",
				Text:  fmt.Sprintf("AGENTS.md contains a %d-line directory tree block (max %d lines)", runLen, maxAgentsTreeLines),
			})
		}
		runStart, runLen = 0, 0
	}
	for i, l := range splitNormalizedLines(text) {
		if strings.Contains(l, "├──") || strings.Contains(l, "└──") {
			if runLen == 0 {
				runStart = i + 1
			}
			runLen++
			continue
		}
		flush()
	}
	flush()
	return out
}
