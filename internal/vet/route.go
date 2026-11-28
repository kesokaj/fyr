package vet

import (
	"regexp"
	"strings"
)

var (
	numberedTasksRe = regexp.MustCompile(`(?is)(?:^|[\s:;])(?:\(?1\)|1\.)\s+\S+.*[\s;](?:\(?2\)|2\.)\s+\S+`)
	multiPhaseRe    = regexp.MustCompile(`(?i)\b(?:phase\s*1\b.*\bphase\s*2|(?:[2-9]|\d{2,}|two|three|four|five|multiple|several)\s+(?:independent\s+)?(?:phases|tasks|waves)|multi-phase)\b`)
)

// RouteFeature returns a concise advisory fyr feature route for prompt and
// intent within VetPrompt's < 25 ms latency budget, or "" on pass-through turns.
func RouteFeature(prompt string, intent IntentMode, skills []SkillMeta) string {
	clean := strings.TrimSpace(StripSandboxPreamble(strings.TrimSpace(prompt)))
	clean = strings.TrimSpace(strings.TrimPrefix(clean, "!"))
	if clean == "" || IsWorkerBriefPrompt(clean) {
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
	if hasLookupCues(low) {
		return "fyr search --slice"
	}
	if !IsWorkerBriefPrompt(clean) && !IsDiagnosticReadOnlyPrompt(clean) && hasBatchOrMultiPhaseCues(clean, low) {
		return "/fyr-swarm (isolated subtasks; zero shared memory)"
	}
	switch {
	case hasSkillName(skills, "fyr-triage"):
		return "/fyr-triage"
	case hasSkillName(skills, "fyr-handoff"):
		return "/fyr-handoff (commits, pushes, fyr metrics ingest once)"
	case hasSkillName(skills, "docs-ste100-80"):
		return "docs-ste100-80 + fyr check"
	case ContainsAnySubstr(low, "pick a model", "choose a model", "which model", "fyr models"):
		return "fyr models --pick <role> [--tier pro|flash]"
	case ContainsAnySubstr(low, "fyr eval", "behavioural run", "behavioral run"):
		return "fyr eval <row>"
	default:
		return ""
	}
}

func hasBatchOrMultiPhaseCues(clean, low string) bool {
	if HasExplicitMultiPhaseOrParallelCues(low) {
		return true
	}
	return hasIndependentTasks(clean, low)
}

// HasExplicitMultiPhaseOrParallelCues reports whether low names multi-phase, wave, swarm, or parallel execution.
func HasExplicitMultiPhaseOrParallelCues(low string) bool {
	if multiPhaseRe.MatchString(low) || ContainsAnyWord(low, "wave", "waves", "fyr-swarm") || strings.Contains(low, "/fyr-swarm") {
		return true
	}
	return ContainsAnyWord(low, "parallel") && !strings.Contains(low, "not parallel") && !strings.Contains(low, "no parallel")
}

func hasIndependentTasks(clean, low string) bool {
	if strings.HasPrefix(low, "where ") {
		return false
	}
	if !numberedTasksRe.MatchString(clean) && countActionClauses(clean) < 2 {
		return false
	}
	return countDistinctExplicitPackages(clean, splitDecomposeClauses(clean), nil) >= 2
}

func countActionClauses(clean string) int {
	return len(splitSemicolonActionClauses(clean))
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

func hasLookupCues(low string) bool {
	return ContainsAnySubstr(low, "where is", "where are") ||
		ContainsAnyWord(low, "find", "trace", "caller", "callers", "reference", "references")
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

// AttachAdvisoryRoute sets res.Route and appends the advisory route line to res.ImprovedPrompt when applicable.
func AttachAdvisoryRoute(cleanPrompt string, res *Result, _ SliceResult, _ TurnBrainContext) {
	if res == nil {
		return
	}
	res.Route = RouteFeature(cleanPrompt, res.Intent, res.Skills)
	if res.Gate == GateEnrichAndProceed && res.Intent != IntentQuestion && res.Intent != IntentCorrection && len(res.Subtasks) >= 2 {
		res.Route = "/fyr-swarm (isolated subtasks; zero shared memory)"
	}
	if res.Route == "" || res.Gate != GateEnrichAndProceed || res.Intent == IntentCorrection {
		return
	}
	if res.ImprovedPrompt != "" {
		res.ImprovedPrompt += "\nroute: " + res.Route
	}
}
