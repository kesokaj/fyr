package vet

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

// VetGate is the execution gate assigned by prompt vetting.
type VetGate string

const (
	GatePassThrough      VetGate = "PASS_THROUGH"
	GateEnrichAndProceed VetGate = "ENRICH_AND_PROCEED"
	GateVetFirst         VetGate = "VET_FIRST"
)

// MaxSkillDigestBytes caps per-skill digest bytes in pre-invocation output.
const MaxSkillDigestBytes = 600

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

// Result is the structured output of prompt vetting and turn envelope assembly.
type Result struct {
	Intent              IntentMode      `json:"intent"`
	Gate                VetGate         `json:"gate"`
	Score               int             `json:"score"`
	Bypassed            bool            `json:"bypassed,omitempty"`
	SessionModel        string          `json:"sessionModel,omitempty"`
	SessionModelDisplay string          `json:"sessionModelDisplay,omitempty"`
	SessionThinking     string          `json:"sessionThinking,omitempty"`
	Route               string          `json:"route,omitempty"`
	PriorTurnContext    string          `json:"priorTurnContext,omitempty"`
	TargetFiles         []string        `json:"targetFiles,omitempty"`
	SuggestedVerify     string          `json:"suggestedVerify,omitempty"`
	ImprovedPrompt      string          `json:"improvedPrompt,omitempty"`
	Subtasks            []SubtaskSlice  `json:"subtasks,omitempty"`
	CarriedFromTurn     int             `json:"carriedFromTurn,omitempty"`
	PresentAnchors      []string        `json:"presentAnchors,omitempty"`
	MissingAnchors      []string        `json:"missingAnchors,omitempty"`
	Skills              []SkillMeta     `json:"skills,omitempty"`
	SecurityRequired    bool            `json:"securityRequired,omitempty"`
	MatchedRecords      []MatchedRecord `json:"matchedRecords,omitempty"`
	Conflicts           []string        `json:"conflicts,omitempty"`
	FenceBoundary       string          `json:"fenceBoundary,omitempty"`
	UserMarkdownBanner  string          `json:"userMarkdownBanner"`
	TurnContract        string          `json:"turnContract"`
	SystemMessage       string          `json:"systemMessage,omitempty"`
}

var (
	// FilePathRe matches file and directory paths mentioned in prompts.
	FilePathRe = regexp.MustCompile(`(?:^|[\s` + "`" + `'\"(]|\./)(?:cmd|internal|skills|rules|context|agents|docker|\.agents|scripts|pkg|web|ui|src|docs|test|tests|api|app|lib)/(?:[A-Za-z0-9_.-]+/)*[A-Za-z0-9_.-]+|(?:[A-Za-z0-9_.-]+/)*[A-Za-z0-9_-]+\.(?:go|ts|tsx|js|jsx|py|sh|md|sql|json|yaml|yml|toml|css|html|svg|proto|txt)\b|\b(?:Dockerfile|Makefile)\b`)
	// IdentExprRe matches code identifiers (backticked, camelCase, snake_case, pkg.Sym).
	IdentExprRe = regexp.MustCompile("`[^`]+`|\\b[A-Z][A-Za-z0-9]+\\.[A-Za-z0-9_]+\\b|\\b[a-z]+[A-Z][A-Za-z0-9_]*\\b|\\b[A-Z][a-z0-9]+[A-Z][A-Za-z0-9_]*\\b|\\b[a-z0-9]+_[a-z0-9_]+\\b")
	flagOrRange = regexp.MustCompile(`--[a-z][a-z0-9-]*|:\d+(?:-\d+)?\b|\b(?:func|type|struct|interface|return|error|panic|nil|exit)\b`)
)

// Directive computes the ordered pre-invocation directive label for res.
func Directive(res Result) string {
	switch res.Gate {
	case GatePassThrough:
		return "PASS_THROUGH"
	case GateVetFirst:
		return "VET_FIRST"
	}
	if len(res.Subtasks) >= 2 {
		return "COORDINATE_SUBTASKS"
	}
	switch res.Intent {
	case IntentQuestion:
		return "ANSWER_ONLY"
	case IntentCorrection:
		return "CORRECTION"
	}
	if strings.TrimSpace(res.ImprovedPrompt) != "" {
		return "EXECUTE_IMPROVED_PROMPT"
	}
	return "ENRICH_AND_PROCEED"
}

// EvaluateAnchors checks the 6 prompt anchors (target, goal, scope,
// inputs_outputs, constraints, verification) and returns present and missing lists.
func EvaluateAnchors(prompt string, slice SliceResult, brainCtx TurnBrainContext) ([]string, []string) {
	p := strings.TrimSpace(prompt)
	low := strings.ToLower(p)
	sliceConfident := slice.Confident || slice.Pivots > 0
	hasTarget := (sliceConfident && (slice.SymbolHits > 0 || len(slice.Files) > 0)) || slice.Pivots > 0 ||
		FilePathRe.MatchString(p) || IdentExprRe.MatchString(p)
	hasGoal := len(strings.Fields(p)) >= 3
	hasScope := (sliceConfident && len(slice.Files) > 0) || FilePathRe.MatchString(p) ||
		ContainsAnyWord(low, "only", "in", "package", "file", "module", "phase", "without", "handler", "middleware", "server", "config")
	hasIO := flagOrRange.MatchString(p) || strings.ContainsAny(p, "()`[]{}") ||
		ContainsAnyWord(low, "flag", "json", "input", "output", "returns", "returning", "error", "repro", "expect", "command", "stdin", "stdout", "status", "http", "endpoint", "response", "request", "timeout", "retry", "csv", "yaml", "xml", "exit", "format", "formatting", "pagination")
	hasConstraints := len(brainCtx.Conflicts) == 0 && (len(brainCtx.ADRs) > 0 || len(brainCtx.Memories) > 0 ||
		ContainsAnyWord(low, "adr", "prd", "bug", "ms", "bytes", "tokens", "stdlib", "limit", "budget", "never", "must", "approvals"))
	hasVerify := ContainsAnyWord(low, "test", "tests", "verify", "expect", "benchmark", "repro", "check", "pass")

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

// VetPromptCoreWithHistory executes deterministic vetting using prior turn
// history and recent skill suggestions.
func VetPromptCoreWithHistory(
	prompt string,
	intent IntentMode,
	slice SliceResult,
	brainCtx TurnBrainContext,
	catalog []SkillMeta,
	prev *HistoryRow,
	recentSkills map[string]bool,
	recentPivots []string,
) Result {
	trimmed := StripSandboxPreamble(strings.TrimSpace(prompt))
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(trimmed, "!"))
	if intent == "" {
		intent = ClassifyIntent(cleanPrompt, slice.SymbolHits)
	}
	conflicts := DetectTurnConflicts(cleanPrompt, brainCtx)
	ctxWithConflicts := brainCtx
	ctxWithConflicts.Conflicts = conflicts
	score, present, missing := ComputeScore(cleanPrompt, intent, slice, ctxWithConflicts, prev)
	carriedFrom, files := resolveCarryAndFiles(cleanPrompt, intent, slice, prev)
	skills, secReq := MatchSkills(cleanPrompt, intent, files, catalog)
	skills = FilterSuggestedSkills(skills, files, recentSkills, recentPivots)
	res := Result{
		Intent:           intent,
		Gate:             DecideGate(trimmed, intent, slice, present, conflicts),
		Score:            score,
		CarriedFromTurn:  carriedFrom,
		PresentAnchors:   present,
		MissingAnchors:   missing,
		Skills:           skills,
		SecurityRequired: secReq,
		MatchedRecords:   CollectMatchedRecords(brainCtx),
		Conflicts:        conflicts,
	}
	populateVetDeltas(&res, trimmed, cleanPrompt, slice, files, prev)
	if !res.Bypassed && res.Gate != GatePassThrough {
		res.Subtasks = DecomposeSubtasks(cleanPrompt, intent, slice, files, catalog)
	}
	res.ImprovedPrompt = SynthesizeImprovedPrompt(cleanPrompt, res, slice, ctxWithConflicts, prev)
	AttachAdvisoryRoute(cleanPrompt, &res, slice, ctxWithConflicts)
	return res
}

func resolveCarryAndFiles(cleanPrompt string, intent IntentMode, slice SliceResult, prev *HistoryRow) (int, []string) {
	carriedFrom := 0
	if prev != nil && shouldCarryForwardVet(cleanPrompt, intent, slice, prev) {
		carriedFrom = prev.TurnNum
	}
	files := ExtractPromptAndSliceFiles(cleanPrompt, slice.Files)
	if carriedFrom > 0 && len(prev.PivotFiles) > 0 {
		merged := append(append([]string{}, slice.Files...), prev.PivotFiles...)
		files = ExtractPromptAndSliceFiles(cleanPrompt, merged)
	}
	return carriedFrom, files
}

// DecideGate determines whether a turn passes through, enriches, or requires VET_FIRST.
func DecideGate(rawPrompt string, intent IntentMode, slice SliceResult, present, conflicts []string) VetGate {
	if rawPrompt == "" || strings.HasPrefix(rawPrompt, "!") {
		return GatePassThrough
	}
	low := strings.ToLower(rawPrompt)
	if strings.HasPrefix(rawPrompt, "/") || strings.HasPrefix(low, "run /") || intent == IntentApproval || intent == IntentTrivial {
		return GatePassThrough
	}
	if IsKickoffPrompt(rawPrompt) {
		return GateEnrichAndProceed
	}
	if len(conflicts) > 0 {
		return GateVetFirst
	}
	if HasAutonomousWaiver(low) {
		return GateEnrichAndProceed
	}
	if (intent == IntentFeature || intent == IntentBug) && isUltraVagueZeroHit(rawPrompt, slice, present) {
		return GateVetFirst
	}
	return GateEnrichAndProceed
}

func isUltraVagueZeroHit(rawPrompt string, slice SliceResult, present []string) bool {
	sliceConfident := slice.Confident || slice.Pivots > 0
	if sliceConfident && (slice.SymbolHits > 0 || len(slice.Files) > 0) {
		return false
	}
	if slices.Contains(present, "target") || slices.Contains(present, "scope") ||
		slices.Contains(present, "inputs_outputs") || slices.Contains(present, "verification") {
		return false
	}
	return len(strings.Fields(rawPrompt)) <= 6
}

// DetectTurnConflicts detects built-in PRD/ADR invariant violations and ADR rejections.
func DetectTurnConflicts(prompt string, brainCtx TurnBrainContext) []string {
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
	addBuiltinInvariantConflicts(low, add)
	for _, adr := range brainCtx.ADRs {
		if c := CheckADRConflict(low, adr); c != "" {
			add(c)
		}
	}
	return out
}

func addBuiltinInvariantConflicts(low string, add func(string)) {
	if strings.Contains(low, "no ") || strings.Contains(low, "without ") || strings.Contains(low, "forbid") {
		return
	}
	if ContainsAnySubstr(low, "background daemon", "mcp server", "http proxy", "dolt server") {
		add("PRD.md R3 / ADR-511: background daemons, MCP servers, and HTTP proxies are forbidden")
	}
	if ContainsAnySubstr(low, "tree-sitter", "go-tree-sitter", "cgo_enabled=1", "enable cgo") {
		add("PRD.md R4/R8 / ADR-511: CGo and Tree-sitter C bindings violate the CGO_ENABLED=0 static binary invariant")
	}
}

// CheckADRConflict reports a conflict string when lowPrompt matches a rejected
// or forbidden phrase in adr's title or body.
func CheckADRConflict(lowPrompt string, adr MatchedRecord) string {
	text := strings.ToLower(adr.Title + "\n" + adr.Body)
	safeTitle := strings.Join(strings.Fields(adr.Title), " ")
	for _, line := range strings.Split(text, "\n") {
		if phrase := matchADRConflictLine(lowPrompt, strings.TrimSpace(line)); phrase != "" {
			return fmt.Sprintf("conflicts with %s (%s): %q", adr.ID, safeTitle, phrase)
		}
	}
	return ""
}

func matchADRConflictLine(lowPrompt, line string) string {
	for _, p := range adrConflictPrefixes {
		idx := strings.Index(line, p.prefix)
		if idx < 0 {
			continue
		}
		if phrase := matchADRConflictRest(lowPrompt, line[idx+len(p.prefix):], p.minWords); phrase != "" {
			return phrase
		}
	}
	return ""
}

func matchADRConflictRest(lowPrompt, rest string, minWords int) string {
	for _, phrase := range extractConflictPhrases(rest, minWords) {
		if phrase != "" && strings.Contains(lowPrompt, phrase) {
			return phrase
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
	rest = trimConflictRest(rest, minWords)
	var phrases []string
	for _, part := range strings.FieldsFunc(rest, isConflictSep) {
		if cand := cleanConflictCandidate(part, minWords); cand != "" {
			phrases = append(phrases, cand)
		}
	}
	return phrases
}

func trimConflictRest(rest string, minWords int) string {
	if cut := strings.Index(rest, ". "); cut >= 0 {
		rest = rest[:cut]
	}
	if minWords < 2 {
		return rest
	}
	if cut := strings.IndexAny(rest, ".;:()"); cut >= 0 {
		return rest[:cut]
	}
	return rest
}

func isConflictSep(r rune) bool {
	return r == ',' || r == ';' || r == '(' || r == ')' || r == ':'
}

func cleanConflictCandidate(part string, minWords int) string {
	w := strings.Fields(strings.Trim(part, "`*\"'. "))
	if len(w) < minWords || len(w) > 4 {
		return ""
	}
	if minWords >= 2 && hasConflictPathToken(w) {
		return ""
	}
	cand := strings.Join(w, " ")
	if len(cand) < 4 || stopWordsVet[cand] {
		return ""
	}
	return cand
}

func hasConflictPathToken(words []string) bool {
	return slices.ContainsFunc(words, func(tok string) bool {
		return strings.HasPrefix(tok, ".") || strings.HasPrefix(tok, "~") || strings.Contains(tok, "/")
	})
}

var stopWordsVet = map[string]bool{
	"none": true, "true": true, "false": true, "this": true, "that": true,
	"with": true, "from": true, "into": true, "only": true, "when": true,
	"search": true, "agents": true, "status": true, "record": true, "records": true,
	"memory": true, "metrics": true, "config": true, "files": true, "branch": true,
	"worker": true, "workers": true, "skills": true, "rules": true, "tests": true,
	"check": true, "index": true, "store": true, "hooks": true,
}
