package vet

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// HistoryRow is one turn's persisted vetting state in .agents/scratch/search.db.
type HistoryRow struct {
	ConversationID  string
	TurnNum         int
	Intent          IntentMode
	Gate            VetGate
	Score           int
	RawPrompt       string
	ImprovedPrompt  string
	PresentAnchors  []string
	MissingAnchors  []string
	SuggestedSkills []string
	PivotFiles      []string
	SubtaskCount    int
	Directive       string
	DurationMS      int
	TimedOut        bool
	InjectedBytes   int
	CreatedAt       int64
}

var canonicalVetAnchors = []string{
	"target", "goal", "scope", "inputs_outputs", "constraints", "verification",
}

var vetAnchorWeights = map[string]int{
	"target":         25,
	"goal":           15,
	"scope":          15,
	"inputs_outputs": 15,
	"constraints":    15,
	"verification":   15,
}

// ComputeScore computes a deterministic 0..100 prompt quality score across
// the 6 anchors (target 25, goal 15, scope 15, inputs_outputs 15, constraints
// 15 or -40 on conflict, verification 15) and carries forward anchors from prev
// when prev was VET_FIRST or the current turn is a follow-up.
func ComputeScore(prompt string, intent IntentMode, slice SliceResult, brainCtx TurnBrainContext, prev *HistoryRow) (int, []string, []string) {
	present, _ := EvaluateAnchors(prompt, slice, brainCtx)
	if shouldCarryForwardVet(prompt, intent, slice, prev) {
		present = mergeAnchorLists(present, prev.PresentAnchors)
	}
	missing := computeMissingAnchors(present)
	score := 0
	for _, a := range present {
		if a == "constraints" && len(brainCtx.Conflicts) > 0 {
			continue
		}
		score += vetAnchorWeights[a]
	}
	if len(brainCtx.Conflicts) > 0 {
		score -= 40
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score, present, missing
}

func shouldCarryForwardVet(prompt string, intent IntentMode, slice SliceResult, prev *HistoryRow) bool {
	if prev == nil || intent == IntentTrivial {
		return false
	}
	if prev.Gate == GateVetFirst {
		return true
	}
	if intent == IntentFeature {
		if prev.Intent == IntentQuestion {
			return false
		}
		return sharesPivotSurface(prompt, slice.Files, prev.PivotFiles)
	}
	if len(strings.Fields(prompt)) <= 12 {
		return true
	}
	return sharesPivotSurface(prompt, slice.Files, prev.PivotFiles)
}

func sharesPivotSurface(prompt string, curFiles, prevFiles []string) bool {
	if len(prevFiles) == 0 {
		return false
	}
	low := strings.ToLower(prompt)
	for _, pf := range prevFiles {
		if slices.Contains(curFiles, pf) {
			return true
		}
		base := strings.ToLower(filepath.Base(pf))
		if base != "" && strings.Contains(low, base) {
			return true
		}
	}
	return false
}

func mergeAnchorLists(cur, prev []string) []string {
	seen := map[string]bool{}
	for _, a := range cur {
		seen[a] = true
	}
	for _, a := range prev {
		seen[a] = true
	}
	var out []string
	for _, a := range canonicalVetAnchors {
		if seen[a] {
			out = append(out, a)
		}
	}
	return out
}

func computeMissingAnchors(present []string) []string {
	var missing []string
	for _, a := range canonicalVetAnchors {
		if !slices.Contains(present, a) {
			missing = append(missing, a)
		}
	}
	return missing
}

// FilterSuggestedSkills suppresses skills suggested in recent turns unless curFiles introduces a new file surface.
func FilterSuggestedSkills(skills []SkillMeta, curFiles []string, recentSkills map[string]bool, recentPivots []string) []SkillMeta {
	if len(skills) == 0 || len(recentSkills) == 0 {
		return skills
	}
	if hasNewFileSurface(curFiles, recentPivots) {
		return skills
	}
	var out []SkillMeta
	for _, s := range skills {
		if !recentSkills[s.Name] {
			out = append(out, s)
		}
	}
	return out
}

func hasNewFileSurface(curFiles, recentPivots []string) bool {
	if len(curFiles) == 0 || len(recentPivots) == 0 {
		return false
	}
	for _, f := range curFiles {
		if !slices.Contains(recentPivots, f) {
			return true
		}
	}
	return false
}

func populateVetDeltas(res *Result, rawPrompt, cleanPrompt string, slice SliceResult, files []string, prev *HistoryRow) {
	if res == nil {
		return
	}
	ok := strings.HasPrefix(rawPrompt, "!")
	res.Bypassed = ok
	if (!res.Bypassed && (res.Intent == IntentApproval || res.Intent == IntentTrivial)) || res.Intent == IntentCorrection {
		return
	}
	if prev != nil && shouldCarryForwardVet(cleanPrompt, res.Intent, slice, prev) && prev.RawPrompt != "" && prev.RawPrompt != rawPrompt {
		res.PriorTurnContext = fmt.Sprintf("turn %d: %s", prev.TurnNum, CollapseOneLine(prev.RawPrompt, 180))
	}
	if len(files) > 0 {
		res.TargetFiles = append(res.TargetFiles, files[:min(6, len(files))]...)
	}
	if res.Intent != IntentQuestion && res.Gate != GateVetFirst && slices.Contains(res.MissingAnchors, "verification") {
		res.SuggestedVerify = inferConcreteVerifyCommand(files)
	}
}

// SynthesizeImprovedPrompt generates a compact delta-only [VET_IMPROVED_PROMPT]
// record for vet_history and fyr-process.md when non-redundant deltas exist
// and 45 <= Score < 80, follow-up to VET_FIRST applies, or conflicts exist.
func SynthesizeImprovedPrompt(_ string, res Result, slice SliceResult, brainCtx TurnBrainContext, prev *HistoryRow) string {
	if !shouldSynthesizeImprovedPrompt(res, brainCtx, prev) {
		return ""
	}
	lines := buildImprovedPromptLines(res, slice, brainCtx)
	if len(lines) == 0 && len(res.Subtasks) >= 2 {
		lines = append(lines, fmt.Sprintf("subtasks: %d isolated slices", len(res.Subtasks)))
	}
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("[VET_IMPROVED_PROMPT]\nscore: %d/100\n%s", res.Score, strings.Join(lines, "\n"))
}

func shouldSynthesizeImprovedPrompt(res Result, brainCtx TurnBrainContext, prev *HistoryRow) bool {
	if res.Gate != GateEnrichAndProceed || res.Intent == IntentQuestion || res.Intent == IntentCorrection {
		return false
	}
	if len(res.Subtasks) >= 2 {
		return true
	}
	if res.Score < 45 {
		return false
	}
	return res.Score < 80 || (prev != nil && prev.Gate == GateVetFirst) || len(brainCtx.Conflicts) > 0
}

func buildImprovedPromptLines(res Result, slice SliceResult, brainCtx TurnBrainContext) []string {
	var lines []string
	if prior := strings.TrimSpace(res.PriorTurnContext); prior != "" {
		lines = append(lines, "prior: "+prior)
	}
	if targets := formatImprovedPromptTargets(res.TargetFiles, slice); targets != "" {
		lines = append(lines, "targets: "+targets)
	}
	if verify := strings.TrimSpace(res.SuggestedVerify); verify != "" {
		lines = append(lines, "verify: "+verify)
	}
	if c := formatImprovedPromptConstraints(brainCtx); c != "" {
		lines = append(lines, "constraints: "+c)
	}
	return lines
}

func formatImprovedPromptTargets(targetFiles []string, slice SliceResult) string {
	if len(targetFiles) > 0 {
		return strings.Join(targetFiles, ", ")
	}
	if spans := formatImprovedPromptTargetSpans(slice); len(spans) > 0 {
		return strings.Join(spans, ", ")
	}
	return ""
}

func formatImprovedPromptTargetSpans(slice SliceResult) []string {
	var spans []string
	for _, line := range strings.Split(slice.Capsule, "\n") {
		if !strings.HasPrefix(line, "[Hop 0] ") && (!strings.HasPrefix(line, "[Hop 1] ") || strings.Contains(line, " snippet ")) {
			continue
		}
		rest := line[strings.IndexByte(line, ']')+2:]
		if cut := strings.Index(rest, " snippet — "); cut >= 0 {
			rest = rest[:cut]
		}
		rest = CollapseOneLine(strings.TrimSpace(strings.TrimSuffix(rest, "[EXTRACTED]")), 120)
		if rest != "" {
			spans = append(spans, rest)
			if len(spans) >= 5 {
				break
			}
		}
	}
	return spans
}

func formatImprovedPromptConstraints(brainCtx TurnBrainContext) string {
	var parts []string
	for _, adr := range brainCtx.ADRs[:min(2, len(brainCtx.ADRs))] {
		parts = append(parts, fmt.Sprintf("%s (%s)", adr.ID, CollapseOneLine(adr.Title, 60)))
	}
	for _, bug := range brainCtx.Bugs[:min(2, len(brainCtx.Bugs))] {
		parts = append(parts, fmt.Sprintf("%s (%s)", bug.ID, CollapseOneLine(bug.Title, 60)))
	}
	for _, c := range brainCtx.Conflicts[:min(2, len(brainCtx.Conflicts))] {
		parts = append(parts, CollapseOneLine(c, 80))
	}
	return strings.Join(parts, "; ")
}

func inferConcreteVerifyCommand(files []string) string {
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if ext == ".go" {
			dir := filepath.ToSlash(filepath.Dir(f))
			if dir == "." || dir == "" {
				return "go test -race ./..."
			}
			return fmt.Sprintf("go test -race ./%s/...", strings.TrimPrefix(dir, "./"))
		}
		if ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx" {
			return "npm test"
		}
	}
	return ""
}

// CollapseOneLine normalizes whitespace to single spaces and truncates at maxLen when > 0.
func CollapseOneLine(s string, maxLen int) string {
	collapsed := strings.Join(strings.Fields(s), " ")
	if maxLen > 0 && len(collapsed) > maxLen {
		return collapsed[:maxLen] + "..."
	}
	return collapsed
}
