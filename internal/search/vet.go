package search

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
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
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Status     string `json:"status,omitempty"`
	Title      string `json:"title"`
	Provenance string `json:"provenance,omitempty"`
	Body       string `json:"body,omitempty"`
	StaleTag   string `json:"staleTag,omitempty"`
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
	Capsule    string   `json:"capsule,omitempty"`
	Pivots     int      `json:"pivots,omitempty"`
	SymbolHits int      `json:"symbolHits,omitempty"`
	Files      []string `json:"files,omitempty"`
	Confident  bool     `json:"confident,omitempty"`
}

// VetResult is the structured output of VetPrompt and BuildTurnEnvelope.
type VetResult struct {
	Intent             IntentMode      `json:"intent"`
	Gate               VetGate         `json:"gate"`
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
	fileTokenRe  = regexp.MustCompile(`(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+|\b[A-Za-z0-9_-]+\.(?:go|ts|tsx|js|py|sh|md|sql|json|yaml|yml|toml)\b`)
	identTokenRe = regexp.MustCompile("`[^`]+`|\\b[A-Z][A-Za-z0-9]+\\.[A-Za-z0-9_]+\\b|\\b[a-z]+[A-Z][A-Za-z0-9_]*\\b|\\b[A-Z][a-z0-9]+[A-Z][A-Za-z0-9_]*\\b|\\b[a-z0-9]+_[a-z0-9_]+\\b")
	flagOrRange  = regexp.MustCompile(`--[a-z][a-z0-9-]*|:\d+(?:-\d+)?\b|\b(?:func|type|struct|interface|return|error|panic|nil|exit)\b`)
)

// ClassifyIntent deterministically classifies prompt into one of the 9 IntentModes.
func ClassifyIntent(prompt string, symbolHits int) IntentMode {
	p := strings.TrimSpace(prompt)
	low := strings.ToLower(p)
	if p == "" || isApprovalPrompt(low) {
		return IntentApproval
	}
	if isKickoffPrompt(p) {
		return IntentTask
	}
	if mode, ok := classifySlashPrefix(low); ok {
		return mode
	}
	switch {
	case isCorrectionPrompt(low):
		return IntentCorrection
	case isReleasePrompt(low):
		return IntentRelease
	case isReviewPrompt(low):
		return IntentReview
	case isTrivialPrompt(low, symbolHits):
		return IntentTrivial
	case isQuestionPrompt(p, low):
		return IntentQuestion
	case isBugPrompt(low):
		return IntentBug
	case isFeaturePrompt(low, symbolHits):
		return IntentFeature
	default:
		return IntentTask
	}
}

func isApprovalPrompt(low string) bool {
	trimmed := strings.Trim(low, ".! \t\n\r")
	if strings.HasPrefix(trimmed, "go approved") || strings.HasPrefix(trimmed, "go all approved") {
		return true
	}
	switch trimmed {
	case "yes", "y", "ok", "okay", "yes ok", "ok yes", "proceed", "continue", "go", "go ahead", "do it", "lgtm", "sgtm", "approved", "ship", "ack":
		return true
	}
	return false
}

func isKickoffPrompt(p string) bool {
	return strings.Contains(p, "Standing approvals (closed set):") ||
		strings.Contains(p, "Standing approvals:") ||
		(strings.HasPrefix(p, "Project:") && strings.Contains(p, "Files:"))
}

func classifySlashPrefix(low string) (IntentMode, bool) {
	s := strings.TrimSpace(strings.TrimPrefix(low, "run "))
	if !strings.HasPrefix(s, "/") {
		return "", false
	}
	first, _, _ := strings.Cut(s, " ")
	switch first {
	case "/fyr-preflight", "/preflight":
		return IntentRelease, true
	case "/fyr-review", "/review", "/fyr-harness-audit", "/harness-audit":
		return IntentReview, true
	case "/fyr-bug", "/bug":
		return IntentBug, true
	case "/fyr-feature", "/feature", "/fyr-spec", "/spec", "/fyr-requirements", "/requirements", "/fyr-init-project", "/init-project":
		return IntentFeature, true
	case "/fyr-explain-project", "/explain-project", "/explain", "/fyr-research", "/research":
		return IntentQuestion, true
	default:
		return IntentTask, true
	}
}

func isCorrectionPrompt(low string) bool {
	for _, p := range []string{
		"no,", "no —", "no -", "no:", "wrong", "actually,", "actually ",
		"always do ", "never do ", "we don't use", "we do not use",
		"don't use ", "do not use ", "stop doing", "from now on",
		"instead of that", "that's wrong", "that is wrong", "you missed",
	} {
		if strings.HasPrefix(low, p) || strings.Contains(low, " "+p) {
			return true
		}
	}
	return false
}

func isReleasePrompt(low string) bool {
	for _, p := range []string{"ship it", "release this", "cut v", "deploy v", "tag v", "new version", "cut a release"} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

func isReviewPrompt(low string) bool {
	for _, p := range []string{"review this", "review the ", "code review", "security review", "review diff", "review pr", "audit the "} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

func isTrivialPrompt(low string, symbolHits int) bool {
	for _, p := range []string{"typo", "fix typo", "fix comment", "one-line", "one line", "rename "} {
		if strings.Contains(low, p) {
			return true
		}
	}
	words := strings.Fields(low)
	return len(words) <= 3 && symbolHits == 0 && !strings.HasSuffix(low, "?") &&
		!isBugPrompt(low) && !isFeatureKeyword(low)
}

func isQuestionPrompt(orig, low string) bool {
	hasEditVerb := containsAnyWord(low, "fix", "implement", "add", "build", "create", "refactor", "update", "write", "change", "remove", "delete")
	if strings.HasSuffix(strings.TrimSpace(orig), "?") && (!hasEditVerb || isDeliberativeQuestion(low)) {
		return true
	}
	for _, q := range []string{"what ", "why ", "how ", "where ", "when ", "who ", "which ", "explain ", "walk me through", "can you explain", "tell me "} {
		if strings.HasPrefix(low, q) && !hasEditVerb {
			return true
		}
	}
	return false
}

func isDeliberativeQuestion(low string) bool {
	return !containsAnySubstr(low, "can you ", "could you ", "would you ") &&
		containsAnySubstr(low, "do we ", "should we ", "did we ", "did you ", "have we ", "have you ", "are we ")
}

func isBugPrompt(low string) bool {
	if containsAnySubstr(low, "bug", "broken", "failing", "fails", "failure", "crash", "crashes", "panic", "regression", "debug", "investigate", "doesn't work", "does not work", "nil pointer", "race condition", "deadlock", "error in", "wrong output") {
		return true
	}
	return containsAnyWord(low, "fix") && !strings.Contains(low, "fix typo") && !strings.Contains(low, "fix comment")
}

func isFeaturePrompt(low string, symbolHits int) bool {
	if isFeatureKeyword(low) {
		return true
	}
	return containsAnyWord(low, "add", "create", "extend", "support") && (symbolHits == 0 || strings.Contains(low, "new "))
}

func isFeatureKeyword(low string) bool {
	for _, kw := range []string{"build ", "implement ", "design ", "architect ", "new feature", "support for", "add support", "integrate ", "scaffold ", "introduce "} {
		if strings.HasPrefix(low, kw) || strings.Contains(low, " "+kw) {
			return true
		}
	}
	return false
}

// EvaluateAnchors checks crew's 6 prompt anchors (target, goal, scope,
// inputs_outputs, constraints, verification) and returns present and missing lists.
func EvaluateAnchors(prompt string, slice SliceResult, brainCtx TurnBrainContext) ([]string, []string) {
	p := strings.TrimSpace(prompt)
	low := strings.ToLower(p)
	hasTarget := slice.SymbolHits > 0 || slice.Pivots > 0 || len(slice.Files) > 0 ||
		fileTokenRe.MatchString(p) || identTokenRe.MatchString(p)
	hasGoal := len(strings.Fields(p)) >= 3
	hasScope := len(slice.Files) > 0 || fileTokenRe.MatchString(p) ||
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

// VetPrompt runs the 6-Anchor Vetter, ADR/PRD conflict check, Skill Prefetcher,
// and TurnEnvelope builder in < 10 ms.
func VetPrompt(prompt string, intent IntentMode, slice SliceResult, brainCtx TurnBrainContext, catalog ...[]SkillMeta) VetResult {
	var cat []SkillMeta
	if len(catalog) > 0 {
		cat = catalog[0]
	}
	res := vetPromptCore(prompt, intent, slice, brainCtx, cat)
	sysMsg, banner := BuildTurnEnvelope(&res, slice, brainCtx, nil, nil)
	res.SystemMessage = sysMsg
	res.UserMarkdownBanner = banner
	return res
}

func vetPromptCore(prompt string, intent IntentMode, slice SliceResult, brainCtx TurnBrainContext, catalog []SkillMeta) VetResult {
	trimmed := StripSandboxPreamble(strings.TrimSpace(prompt))
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(trimmed, "!"))
	if intent == "" {
		intent = ClassifyIntent(cleanPrompt, slice.SymbolHits)
	}
	conflicts := detectTurnConflicts(cleanPrompt, brainCtx)
	ctxWithConflicts := brainCtx
	ctxWithConflicts.Conflicts = conflicts
	present, missing := EvaluateAnchors(cleanPrompt, slice, ctxWithConflicts)
	files := extractPromptAndSliceFiles(cleanPrompt, slice.Files)
	skills, secReq := MatchSkills(cleanPrompt, intent, files, catalog)
	return VetResult{
		Intent:           intent,
		Gate:             decideVetGate(trimmed, intent, present, conflicts),
		PresentAnchors:   present,
		MissingAnchors:   missing,
		Skills:           skills,
		SecurityRequired: secReq,
		MatchedRecords:   collectMatchedRecords(brainCtx),
		Conflicts:        conflicts,
	}
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

// MatchSkills selects the top 1-2 skills (<= 600 B digest + SKILL.md path)
// and sets the mandatory security subagent flag on auth/crypto/secret/frontmatter surfaces.
func MatchSkills(prompt string, intent IntentMode, files []string, catalog []SkillMeta) ([]SkillMeta, bool) {
	low := strings.ToLower(prompt)
	secReq := touchesSecuritySurface(low, files)
	if intent == IntentApproval || intent == IntentTrivial {
		return nil, false
	}
	byName := mergeSkillCatalog(catalog)
	if intent == IntentCorrection {
		return selectNamedSkills(byName, "operator correction", "fyr-feedback-log", "fyr-project-memory"), secReq
	}
	type scoredSkill struct {
		meta  SkillMeta
		score int
	}
	var scored []scoredSkill
	for _, s := range byName {
		if sc, reason := scoreSkillCandidate(s, low, intent, files); sc > 0 {
			cand := s
			cand.Digest = truncateBytes(cand.Digest, maxSkillDigestBytes)
			cand.Reason = reason
			scored = append(scored, scoredSkill{meta: cand, score: sc})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].meta.Name < scored[j].meta.Name
	})
	var out []SkillMeta
	for i := 0; i < len(scored) && len(out) < 2; i++ {
		out = append(out, scored[i].meta)
	}
	return out, secReq
}

func selectNamedSkills(byName map[string]SkillMeta, reason string, names ...string) []SkillMeta {
	var out []SkillMeta
	for _, name := range names {
		if s, ok := byName[name]; ok {
			s.Digest = truncateBytes(s.Digest, maxSkillDigestBytes)
			s.Reason = reason
			out = append(out, s)
		}
	}
	return out
}

var intentPrimarySkill = map[IntentMode]string{
	IntentBug:     "fyr-bug",
	IntentFeature: "fyr-feature",
	IntentReview:  "fyr-review",
	IntentRelease: "fyr-preflight",
}

func scoreSkillCandidate(s SkillMeta, lowPrompt string, intent IntentMode, files []string) (int, string) {
	score, reason := scoreSkillSurface(s.Name, lowPrompt, intent, files)
	if primary, ok := intentPrimarySkill[intent]; ok && s.Name == primary {
		score, reason = 100, "intent="+string(intent)
	}
	if strings.Contains(lowPrompt, s.Name) {
		score += 40
		if reason == "" {
			reason = "named in prompt"
		}
	}
	return score, reason
}

func scoreSkillSurface(name, lowPrompt string, intent IntentMode, files []string) (int, string) {
	switch name {
	case "fyr-explain-project", "explain-project":
		if intent == IntentQuestion && (strings.Contains(lowPrompt, "explain") || strings.Contains(lowPrompt, "walk me through")) {
			return 95, "intent=QUESTION (onboarding)"
		}
	case "golang":
		if hasExt(files, ".go") || containsAnyWord(lowPrompt, "go", "golang", "goroutine", "slog", "httptest") {
			return 70, "Go code surface"
		}
	case "typescript":
		if hasExt(files, ".ts", ".tsx") || containsAnyWord(lowPrompt, "typescript", "tsconfig", "vitest") {
			return 70, "TypeScript surface"
		}
	case "bash":
		if hasExt(files, ".sh") || containsAnyWord(lowPrompt, "bash", "shell", "script", "jq", "rg") {
			return 70, "shell script surface"
		}
	case "testing-strategy":
		if containsAnyWord(lowPrompt, "test", "tests", "coverage", "verifier", "e2e") {
			return 60, "verification/testing surface"
		}
	case "fyr-spec", "spec":
		if containsAnyWord(lowPrompt, "spec", "specify", "contract", "interface") {
			return 65, "specification surface"
		}
	case "fyr-project-memory", "project-memory":
		if containsAnyWord(lowPrompt, "remember", "memory", "forget") {
			return 85, "memory directive"
		}
	case "fyr-index-project":
		if strings.Contains(lowPrompt, "fyr-index-project") || (containsAnyWord(lowPrompt, "index", "indexing", "search.db") && !containsAnyWord(lowPrompt, "refresh", "rebuild")) {
			return 90, "project indexing surface"
		}
	case "fyr-refresh-project-index":
		if strings.Contains(lowPrompt, "fyr-refresh-project-index") || (containsAnyWord(lowPrompt, "index", "search.db") && containsAnyWord(lowPrompt, "refresh", "rebuild", "stale")) {
			return 92, "project index refresh surface"
		}
	}
	return 0, ""
}

func touchesSecuritySurface(lowPrompt string, files []string) bool {
	if containsAnySubstr(lowPrompt, "auth", "authn", "authz", "oauth", "oidc", "jwt", "iap", "token", "password", "crypto", "cipher", "hmac", "sha256", "tls", "secret", "redact", "credential", "iam", "permission", "frontmatter", "allowlist", "allow-list", "skillkeys", "agentkeys", "knowntools", "inject", "injection", "sanitize", "fence") {
		return true
	}
	for _, f := range files {
		if containsAnySubstr(strings.ToLower(f), "redact", "security", "auth", "guard", "inventory.go", "fence.go", "go.mod") {
			return true
		}
	}
	return false
}

func containsAnySubstr(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func extractPromptAndSliceFiles(prompt string, sliceFiles []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range sliceFiles {
		if f = strings.TrimSpace(f); f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for _, m := range fileTokenRe.FindAllString(prompt, -1) {
		if m = strings.Trim(m, "`'\".,;:()"); m != "" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

func containsAnyWord(low string, words ...string) bool {
	tokens := strings.FieldsFunc(low, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-'
	})
	for _, t := range tokens {
		if slices.Contains(words, t) {
			return true
		}
	}
	return false
}

func hasExt(files []string, exts ...string) bool {
	for _, f := range files {
		if slices.Contains(exts, strings.ToLower(filepath.Ext(f))) {
			return true
		}
	}
	return false
}
