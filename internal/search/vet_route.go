package search

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	inlineCodeSpanRe = regexp.MustCompile("`[^`]*`")
	numberedTasksRe  = regexp.MustCompile(`(?is)(?:^|[\s:;])(?:\(?1\)|1\.)\s+\S+.*[\s;](?:\(?2\)|2\.)\s+\S+`)
	multiPhaseRe     = regexp.MustCompile(`(?i)\b(?:phase\s*1\b.*\bphase\s*2|(?:[2-9]|\d{2,}|two|three|four|five|multiple|several)\s+(?:independent\s+)?(?:phases|tasks|waves)|multi-phase)\b`)
)

// RouteFeature returns a concise advisory fyr feature route for prompt and
// intent within VetPrompt's < 25 ms latency budget, or "" on pass-through turns.
func RouteFeature(prompt string, intent IntentMode, skills []SkillMeta) string {
	clean := strings.TrimSpace(StripSandboxPreamble(strings.TrimSpace(prompt)))
	clean = strings.TrimSpace(strings.TrimPrefix(clean, "!"))
	if clean == "" || isKickoffPrompt(clean) {
		return ""
	}
	if intent == "" {
		intent = ClassifyIntent(clean, 0)
	}
	low := strings.ToLower(clean)
	switch intent {
	case IntentApproval, IntentTrivial, IntentCorrection:
		return ""
	case IntentRelease:
		return "/fyr-preflight"
	case IntentReview:
		return "/fyr-review --judge (cross-family)"
	case IntentBug:
		return "/fyr-bug (reproduce with failing test first)"
	case IntentFeature:
		if hasBatchOrMultiPhaseCues(clean, low) {
			return "/fyr-work (waves with model:, judge:, base-check:)"
		}
		return "/fyr-feature (pre-build interview first)"
	case IntentQuestion:
		return "fyr search --slice"
	case IntentTask:
		return routeTaskIntent(clean, low, skills)
	default:
		return ""
	}
}

func routeTaskIntent(clean, low string, skills []SkillMeta) string {
	if hasIndependentTasks(clean, low) {
		return "/fyr-work <task>; <task>"
	}
	if hasLookupCues(low) {
		return "fyr search --slice"
	}
	switch {
	case hasSkillName(skills, "fyr-triage"):
		return "/fyr-triage → /fyr-work Kickoffs"
	case hasSkillName(skills, "fyr-handoff"):
		return "/fyr-handoff (commits, pushes, fyr metrics ingest once)"
	case hasSkillName(skills, "fyr-docs"):
		return "fyr-docs + fyr check"
	case containsAnySubstr(low, "pick a model", "choose a model", "which model", "fyr models"):
		return "fyr models --pick <role> [--tier pro|flash]"
	case containsAnySubstr(low, "fyr eval", "behavioural run", "behavioral run"):
		return "fyr eval <row>"
	default:
		return ""
	}
}

func hasBatchOrMultiPhaseCues(clean, low string) bool {
	if hasIndependentTasks(clean, low) || multiPhaseRe.MatchString(low) {
		return true
	}
	if containsAnyWord(low, "wave", "waves") {
		return true
	}
	return containsAnyWord(low, "parallel") && !strings.Contains(low, "not parallel") && !strings.Contains(low, "no parallel")
}

func hasIndependentTasks(clean, low string) bool {
	if numberedTasksRe.MatchString(clean) {
		return true
	}
	return countActionClauses(clean) >= 2 && !strings.HasPrefix(low, "where ")
}

func countActionClauses(clean string) int {
	stripped := inlineCodeSpanRe.ReplaceAllString(clean, "code")
	if !strings.Contains(stripped, ";") {
		return 0
	}
	n := 0
	for _, raw := range strings.Split(stripped, ";") {
		clause := strings.TrimSpace(raw)
		if len(strings.Fields(clause)) < 2 || isConstraintClause(strings.ToLower(clause)) {
			continue
		}
		n++
	}
	return n
}

func isConstraintClause(lowClause string) bool {
	for _, prefix := range []string{
		"do not ", "don't ", "never ", "without ", "no ", "only ", "keep ", "leaving ", "there is no ",
	} {
		if strings.HasPrefix(lowClause, prefix) {
			return true
		}
	}
	return false
}

func isKickoffLow(low string) bool {
	return strings.Contains(low, "standing approvals (closed set):") ||
		strings.Contains(low, "standing approvals:") ||
		(strings.HasPrefix(low, "project:") && strings.Contains(low, "files:"))
}

func hasLookupCues(low string) bool {
	return containsAnySubstr(low, "where is", "where are") ||
		containsAnyWord(low, "find", "trace", "caller", "callers", "reference", "references")
}

func hasSkillName(skills []SkillMeta, names ...string) bool {
	for _, s := range skills {
		for _, want := range names {
			if s.Name == want {
				return true
			}
		}
	}
	return false
}

func attachAdvisoryRoute(cleanPrompt string, res *VetResult, slice SliceResult, brainCtx TurnBrainContext) {
	if res == nil {
		return
	}
	res.Route = RouteFeature(cleanPrompt, res.Intent, res.Skills)
	if res.Route == "" || res.Gate != GateEnrichAndProceed || res.Intent == IntentCorrection {
		return
	}
	if res.ImprovedPrompt != "" {
		res.ImprovedPrompt += "\nroute: " + res.Route
		return
	}
	files := extractPromptAndSliceFiles(cleanPrompt, slice.Files)
	scopeStr := "project"
	if len(files) > 0 {
		scopeStr = strings.Join(files[:min(6, len(files))], ", ")
	}
	var b strings.Builder
	b.WriteString("[VET_IMPROVED_PROMPT]\n")
	fmt.Fprintf(&b, "score: %d/100\n", res.Score)
	fmt.Fprintf(&b, "task: %s\n", collapseOneLine(cleanPrompt, 280))
	fmt.Fprintf(&b, "targets: %s\n", formatImprovedPromptTargets(cleanPrompt, slice))
	fmt.Fprintf(&b, "scope: %s\n", scopeStr)
	fmt.Fprintf(&b, "constraints: %s\n", formatImprovedPromptConstraints(brainCtx))
	fmt.Fprintf(&b, "verify: %s\n", inferVerifyCommand(files))
	fmt.Fprintf(&b, "route: %s", res.Route)
	res.ImprovedPrompt = b.String()
}
