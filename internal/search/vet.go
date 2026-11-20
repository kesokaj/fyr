package search

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// IntentMode is one of the 9 deterministic turn intents classified by ClassifyIntent.
type IntentMode string

const (
	IntentApproval   IntentMode = "APPROVAL"
	IntentCorrection IntentMode = "CORRECTION"
	IntentQuestion   IntentMode = "QUESTION"
	IntentTrivial    IntentMode = "TRIVIAL"
	IntentTask       IntentMode = "TASK"
	IntentBug        IntentMode = "BUG"
	IntentFeature    IntentMode = "FEATURE"
	IntentReview     IntentMode = "REVIEW"
	IntentRelease    IntentMode = "RELEASE"
)

// VetGate is the execution gate assigned by VetPrompt.
type VetGate string

const (
	GatePassThrough      VetGate = "PASS_THROUGH"
	GateEnrichAndProceed VetGate = "ENRICH_AND_PROCEED"
	GateVetFirst         VetGate = "VET_FIRST"
)

const maxSkillDigestBytes = 600

// SkillMeta describes a harness skill candidate for pre-invocation prefetching.
type SkillMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
	Digest      string `json:"digest"`
	Reason      string `json:"reason,omitempty"`
}

// MatchedRecord is a project or global record (MEMORY-*, BUG-*, ADR-*, PLAN-*)
// matched for the current turn.
type MatchedRecord struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Status     string  `json:"status,omitempty"`
	Title      string  `json:"title"`
	Provenance string  `json:"provenance,omitempty"`
	Body       string  `json:"body,omitempty"`
	StaleTag   string  `json:"staleTag,omitempty"`
	MemoryKind string  `json:"memoryKind,omitempty"`
	Validity   float64 `json:"validity,omitempty"`
	Global     bool    `json:"global,omitempty"`
}

// TurnBrainContext holds full-body memories, open bug prevention rules, ADR
// constraints, active plan phase, and detected conflicts for a turn.
type TurnBrainContext struct {
	Memories   []MatchedRecord `json:"memories,omitempty"`
	Bugs       []MatchedRecord `json:"bugs,omitempty"`
	ADRs       []MatchedRecord `json:"adrs,omitempty"`
	ActivePlan *MatchedRecord  `json:"activePlan,omitempty"`
	Conflicts  []string        `json:"conflicts,omitempty"`
}

// SliceResult holds the AST slice capsule and metadata for prompt vetting.
type SliceResult struct {
	Capsule        string         `json:"capsule,omitempty"`
	Pivots         int            `json:"pivots,omitempty"`
	SymbolHits     int            `json:"symbolHits,omitempty"`
	Files          []string       `json:"files,omitempty"`
	Confident      bool           `json:"confident,omitempty"`
	PkgCommunities map[string]int `json:"pkgCommunities,omitempty"`
}

// VetResult is the structured output of VetPrompt and BuildTurnEnvelope.
type VetResult struct {
	Intent             IntentMode      `json:"intent"`
	Gate               VetGate         `json:"gate"`
	Score              int             `json:"score"`
	Bypassed           bool            `json:"bypassed,omitempty"`
	SessionModel       string          `json:"sessionModel,omitempty"`
	SessionThinking    string          `json:"sessionThinking,omitempty"`
	Route              string          `json:"route,omitempty"`
	PriorTurnContext   string          `json:"priorTurnContext,omitempty"`
	TargetFiles        []string        `json:"targetFiles,omitempty"`
	SuggestedVerify    string          `json:"suggestedVerify,omitempty"`
	ImprovedPrompt     string          `json:"improvedPrompt,omitempty"`
	Subtasks           []SubtaskSlice  `json:"subtasks,omitempty"`
	CarriedFromTurn    int             `json:"carriedFromTurn,omitempty"`
	PresentAnchors     []string        `json:"presentAnchors,omitempty"`
	MissingAnchors     []string        `json:"missingAnchors,omitempty"`
	Skills             []SkillMeta     `json:"skills,omitempty"`
	SecurityRequired   bool            `json:"securityRequired,omitempty"`
	MatchedRecords     []MatchedRecord `json:"matchedRecords,omitempty"`
	Conflicts          []string        `json:"conflicts,omitempty"`
	FenceBoundary      string          `json:"fenceBoundary,omitempty"`
	UserMarkdownBanner string          `json:"userMarkdownBanner"`
	TurnContract       string          `json:"turnContract"`
	SystemMessage      string          `json:"systemMessage,omitempty"`
}

var (
	fileTokenRe  = regexp.MustCompile(`(?:^|[\s` + "`" + `'\"(]|\./)(?:cmd|internal|skills|rules|context|agents|docker|\.agents|scripts|pkg|web|ui|src|docs|test|tests|api|app|lib)/(?:[A-Za-z0-9_.-]+/)*[A-Za-z0-9_.-]+|(?:[A-Za-z0-9_.-]+/)*[A-Za-z0-9_-]+\.(?:go|ts|tsx|js|jsx|py|sh|md|sql|json|yaml|yml|toml|css|html|svg|proto|txt)\b|\b(?:Dockerfile|Makefile)\b`)
	identTokenRe = regexp.MustCompile("`[^`]+`|\\b[A-Z][A-Za-z0-9]+\\.[A-Za-z0-9_]+\\b|\\b[a-z]+[A-Z][A-Za-z0-9_]*\\b|\\b[A-Z][a-z0-9]+[A-Z][A-Za-z0-9_]*\\b|\\b[a-z0-9]+_[a-z0-9_]+\\b")
	flagOrRange  = regexp.MustCompile(`--[a-z][a-z0-9-]*|:\d+(?:-\d+)?\b|\b(?:func|type|struct|interface|return|error|panic|nil|exit)\b`)
)

// EvaluateAnchors checks crew's 6 prompt anchors (target, goal, scope,
// inputs_outputs, constraints, verification) and returns present and missing lists.
func EvaluateAnchors(prompt string, slice SliceResult, brainCtx TurnBrainContext) ([]string, []string) {
	p := strings.TrimSpace(prompt)
	low := strings.ToLower(p)
	sliceConfident := slice.Confident || slice.Pivots > 0
	hasTarget := (sliceConfident && (slice.SymbolHits > 0 || len(slice.Files) > 0)) || slice.Pivots > 0 ||
		fileTokenRe.MatchString(p) || identTokenRe.MatchString(p)
	hasGoal := len(strings.Fields(p)) >= 3
	hasScope := (sliceConfident && len(slice.Files) > 0) || fileTokenRe.MatchString(p) ||
		containsAnyWord(low, "only", "in", "package", "file", "module", "phase", "without")
	hasIO := flagOrRange.MatchString(p) || strings.ContainsAny(p, "()`[]{}") ||
		containsAnyWord(low, "flag", "json", "input", "output", "returns", "error", "repro", "expect", "command", "stdin", "stdout")
	hasConstraints := len(brainCtx.Conflicts) == 0 && (len(brainCtx.ADRs) > 0 || len(brainCtx.Memories) > 0 ||
		containsAnyWord(low, "adr", "prd", "bug", "ms", "bytes", "tokens", "stdlib", "limit", "budget", "never", "must", "approvals"))
	hasVerify := containsAnyWord(low, "test", "tests", "verify", "expect", "benchmark", "repro", "check", "pass")

	checks := []struct {
		name string
		ok   bool
	}{
		{"target", hasTarget}, {"goal", hasGoal}, {"scope", hasScope},
		{"inputs_outputs", hasIO}, {"constraints", hasConstraints}, {"verification", hasVerify},
	}
	var present, missing []string
	for _, c := range checks {
		if c.ok {
			present = append(present, c.name)
		} else {
			missing = append(missing, c.name)
		}
	}
	return present, missing
}

func vetPromptCore(prompt string, intent IntentMode, slice SliceResult, brainCtx TurnBrainContext, catalog []SkillMeta) VetResult {
	return vetPromptCoreWithHistory(prompt, intent, slice, brainCtx, catalog, nil, nil, nil)
}

func vetPromptCoreWithHistory(
	prompt string,
	intent IntentMode,
	slice SliceResult,
	brainCtx TurnBrainContext,
	catalog []SkillMeta,
	prev *VetHistoryRow,
	recentSkills map[string]bool,
	recentPivots []string,
) VetResult {
	trimmed := StripSandboxPreamble(strings.TrimSpace(prompt))
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(trimmed, "!"))
	if intent == "" {
		intent = ClassifyIntent(cleanPrompt, slice.SymbolHits)
	}
	conflicts := detectTurnConflicts(cleanPrompt, brainCtx)
	ctxWithConflicts := brainCtx
	ctxWithConflicts.Conflicts = conflicts
	score, present, missing := ComputeVetScore(cleanPrompt, intent, slice, ctxWithConflicts, prev)
	carriedFrom := 0
	if prev != nil && shouldCarryForwardVet(cleanPrompt, intent, slice, prev) {
		carriedFrom = prev.TurnNum
	}
	files := extractPromptAndSliceFiles(cleanPrompt, slice.Files)
	if carriedFrom > 0 && len(prev.PivotFiles) > 0 {
		files = extractPromptAndSliceFiles(cleanPrompt, append(append([]string{}, slice.Files...), prev.PivotFiles...))
	}
	skills, secReq := MatchSkills(cleanPrompt, intent, files, catalog)
	skills = filterSuggestedSkills(skills, files, recentSkills, recentPivots)
	res := VetResult{
		Intent:           intent,
		Gate:             decideVetGate(trimmed, intent, present, conflicts),
		Score:            score,
		CarriedFromTurn:  carriedFrom,
		PresentAnchors:   present,
		MissingAnchors:   missing,
		Skills:           skills,
		SecurityRequired: secReq,
		MatchedRecords:   collectMatchedRecords(brainCtx),
		Conflicts:        conflicts,
	}
	populateVetDeltas(&res, trimmed, cleanPrompt, slice, files, prev)
	if !res.Bypassed && res.Gate != GatePassThrough {
		res.Subtasks = DecomposeSubtasks(cleanPrompt, intent, slice, files, catalog)
	}
	res.ImprovedPrompt = SynthesizeImprovedPrompt(cleanPrompt, res, slice, ctxWithConflicts, prev)
	attachAdvisoryRoute(cleanPrompt, &res, slice, ctxWithConflicts)
	return res
}

func decideVetGate(rawPrompt string, intent IntentMode, present, conflicts []string) VetGate {
	if rawPrompt == "" || strings.HasPrefix(rawPrompt, "!") {
		return GatePassThrough
	}
	low := strings.ToLower(rawPrompt)
	if strings.HasPrefix(rawPrompt, "/") || strings.HasPrefix(low, "run /") || intent == IntentApproval || intent == IntentTrivial {
		return GatePassThrough
	}
	if isKickoffPrompt(rawPrompt) {
		return GateEnrichAndProceed
	}
	if len(conflicts) > 0 {
		return GateVetFirst
	}
	if hasAutonomousWaiver(low) {
		return GateEnrichAndProceed
	}
	hasTarget := slices.Contains(present, "target")
	hasIO := slices.Contains(present, "inputs_outputs")
	hasScope := slices.Contains(present, "scope")
	if intent == IntentFeature && (!hasTarget || (!hasIO && !hasScope) || len(present) < 3) {
		return GateVetFirst
	}
	if intent == IntentBug && !hasTarget && !hasIO {
		return GateVetFirst
	}
	return GateEnrichAndProceed
}

func detectTurnConflicts(prompt string, brainCtx TurnBrainContext) []string {
	seen := map[string]bool{}
	var out []string
	add := func(c string) {
		if c = strings.Join(strings.Fields(c), " "); c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	for _, c := range brainCtx.Conflicts {
		add(c)
	}
	low := strings.ToLower(prompt)
	if !strings.Contains(low, "no ") && !strings.Contains(low, "without ") && !strings.Contains(low, "forbid") {
		if strings.Contains(low, "background daemon") || strings.Contains(low, "mcp server") || strings.Contains(low, "http proxy") || strings.Contains(low, "dolt server") {
			add("PRD.md R3 / ADR-511: background daemons, MCP servers, and HTTP proxies are forbidden")
		}
		if strings.Contains(low, "tree-sitter") || strings.Contains(low, "go-tree-sitter") || strings.Contains(low, "cgo_enabled=1") || strings.Contains(low, "enable cgo") {
			add("PRD.md R4/R8 / ADR-511: CGo and Tree-sitter C bindings violate the CGO_ENABLED=0 static binary invariant")
		}
	}
	for _, adr := range brainCtx.ADRs {
		if c := CheckADRConflict(low, adr); c != "" {
			add(c)
		}
	}
	return out
}

// CheckADRConflict reports a conflict string when lowPrompt matches a rejected
// or forbidden phrase in adr's title or body.
func CheckADRConflict(lowPrompt string, adr MatchedRecord) string {
	text := strings.ToLower(adr.Title + "\n" + adr.Body)
	safeTitle := strings.Join(strings.Fields(adr.Title), " ")
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		for _, p := range adrConflictPrefixes {
			if idx := strings.Index(line, p.prefix); idx >= 0 {
				for _, phrase := range extractConflictPhrases(line[idx+len(p.prefix):], p.minWords) {
					if phrase != "" && strings.Contains(lowPrompt, phrase) {
						return fmt.Sprintf("conflicts with %s (%s): %q", adr.ID, safeTitle, phrase)
					}
				}
			}
		}
	}
	return ""
}

var adrConflictPrefixes = []struct {
	prefix   string
	minWords int
}{
	{"rejected:", 1},
	{"forbid:", 1},
	{"banned:", 1},
	{"do not use ", 2},
	{"never ", 2},
}

func extractConflictPhrases(rest string, minWords int) []string {
	if cut := strings.Index(rest, ". "); cut >= 0 {
		rest = rest[:cut]
	}
	if minWords >= 2 {
		if cut := strings.IndexAny(rest, ".;:()"); cut >= 0 {
			rest = rest[:cut]
		}
	}
	var phrases []string
	for _, part := range strings.FieldsFunc(rest, func(r rune) bool {
		return r == ',' || r == ';' || r == '(' || r == ')' || r == ':'
	}) {
		w := strings.Fields(strings.Trim(part, "`*\"'. "))
		if minWords >= 2 && slices.ContainsFunc(w, func(tok string) bool {
			return strings.HasPrefix(tok, ".") || strings.HasPrefix(tok, "~") || strings.Contains(tok, "/")
		}) {
			continue
		}
		if len(w) >= minWords && len(w) <= 4 {
			if cand := strings.Join(w, " "); len(cand) >= 4 && !stopWordsVet[cand] {
				phrases = append(phrases, cand)
			}
		}
	}
	return phrases
}

var stopWordsVet = map[string]bool{
	"none": true, "true": true, "false": true, "this": true, "that": true,
	"with": true, "from": true, "into": true, "only": true, "when": true,
	"search": true, "agents": true, "status": true, "record": true, "records": true,
	"memory": true, "metrics": true, "config": true, "files": true, "branch": true,
	"worker": true, "workers": true, "skills": true, "rules": true, "tests": true,
	"check": true, "index": true, "store": true, "hooks": true,
}
