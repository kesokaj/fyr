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
	// maxAlwaysOnBlockBytes is PRD Known limits' ceiling on context/*.md plus
	// always-on rules/*.md before Antigravity silently truncates.
	maxAlwaysOnBlockBytes = 24000
)

// contextRulesBudgetRule enforces context/*.md <= 15,000 B and combined
// context/*.md + always-on rules/*.md <= 24,000 B when those directories
// exist in the checkout (drift-M7).
func (k *checker) contextRulesBudgetRule() []Finding {
	isMarked := k.noIndexed()
	var ctxBytes, alwaysRuleBytes int
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
			if data, err := k.readFile(filepath.FromSlash(p)); err == nil && ruleIsAlwaysOn(data) {
				alwaysRuleBytes += len(data)
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
	total := ctxBytes + alwaysRuleBytes
	if (hasCtx || hasRules) && total > maxAlwaysOnBlockBytes {
		where := "context"
		if hasRules {
			where = "rules"
		}
		out = append(out, Finding{
			Where: where,
			Rule:  "budget",
			Text:  fmt.Sprintf("context/*.md + always-on rules/*.md is %d bytes (context %d, always-on rules %d), over the %d-byte ceiling", total, ctxBytes, alwaysRuleBytes, maxAlwaysOnBlockBytes),
		})
	}
	return out
}

func ruleIsAlwaysOn(data []byte) bool {
	front, _, err := harness.Split(string(data))
	if err != nil {
		return true
	}
	tr := strings.TrimSpace(harness.Parse(front)["trigger"])
	return tr == "" || tr == "always_on"
}

// skillFrontmatterRule lints every tracked non-vendored skills/<name>/SKILL.md
// frontmatter via harness.LintSkillFrontmatter (drift-M8).
func (k *checker) skillFrontmatterRule() []Finding {
	skip := k.vendoredSkips()
	isMarked := k.noIndexed()
	var out []Finding
next:
	for _, p := range k.tracked {
		parts := strings.Split(p, "/")
		if len(parts) != 3 || parts[0] != "skills" || parts[2] != "SKILL.md" {
			continue
		}
		for _, s := range skip {
			if strings.HasPrefix(p, s) {
				continue next
			}
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
