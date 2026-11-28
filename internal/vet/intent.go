package vet

import "strings"

// ClassifyIntent deterministically classifies prompt into one of the 9 IntentModes.
func ClassifyIntent(prompt string, symbolHits int) IntentMode {
	p := strings.TrimSpace(prompt)
	low := strings.ToLower(p)
	if p == "" || isApprovalPrompt(low) {
		return IntentApproval
	}
	if IsKickoffPrompt(p) {
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

// IsKickoffPrompt reports whether p starts with an autonomous Kickoff or worker header.
func IsKickoffPrompt(p string) bool {
	s := StripAnySandboxPreamble(p)
	if s == "" {
		return false
	}
	low := strings.ToLower(strings.ReplaceAll(s, "\r\n", "\n"))
	return HasWorkerTopHeader(strings.Split(low, "\n"))
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
	case "/fyr-research", "/research":
		return IntentQuestion, true
	default:
		return IntentTask, true
	}
}

func isCorrectionPrompt(low string) bool {
	turnPrefixes := []string{
		"no,", "no —", "no -", "no:", "actually,", "actually ",
		"always do ", "never do ", "we don't use", "we do not use",
		"don't use ", "do not use ", "stop doing", "from now on",
		"instead of that", "that's wrong", "that is wrong", "you missed",
	}
	for _, p := range turnPrefixes {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	if strings.HasPrefix(low, "wrong") && !strings.Contains(low, "wrong output") {
		return true
	}
	if hasEditOrFeatureVerb(low) {
		return false
	}
	return hasInlineCorrectionCue(low, turnPrefixes)
}

func hasInlineCorrectionCue(low string, turnPrefixes []string) bool {
	for _, p := range turnPrefixes {
		if hasSentenceStart(low, p) {
			return true
		}
	}
	for _, p := range []string{"we don't use", "we do not use", "stop doing", "from now on", "instead of that", "that's wrong", "that is wrong", "you missed"} {
		if strings.Contains(low, ", "+p) {
			return true
		}
	}
	return hasSentenceStart(low, "wrong") && !strings.Contains(low, "wrong output")
}

func hasSentenceStart(low, phrase string) bool {
	for _, sep := range []string{". ", "! ", "? ", "\n"} {
		if strings.Contains(low, sep+phrase) {
			return true
		}
	}
	return false
}

func hasEditOrFeatureVerb(low string) bool {
	return isBugPrompt(low) || isFeatureKeyword(low) ||
		ContainsAnyWord(low, "add", "create", "extend", "fix", "refactor", "implement", "update") ||
		hasImperativeVerb(low, "build", buildNounPrevs, buildNounNexts)
}

func isReleasePrompt(low string) bool {
	if ContainsAnySubstr(low, "write me a prompt", "give me the prompt", "when this is done", "once this is done", "after phase", "when phase") {
		return false
	}
	for _, p := range []string{"ship it", "release this", "cut v", "deploy v", "tag v", "new version", "cut a release"} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

func isReviewPrompt(low string) bool {
	if strings.HasPrefix(low, "review ") {
		return true
	}
	for _, p := range []string{"review this", "review the ", "code review", "security review", "review diff", "review pr", "audit the "} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

func isTrivialPrompt(low string, symbolHits int) bool {
	for _, p := range []string{"typo", "fix typo", "fix comment", "one-line", "one line"} {
		if strings.Contains(low, p) {
			return true
		}
	}
	words := strings.Fields(low)
	if strings.Contains(low, "rename ") && len(words) <= 10 &&
		!isBugPrompt(low) && !isFeatureKeyword(low) &&
		!ContainsAnyWord(low, "refactor", "redesign", "architect", "migrate", "add", "create", "extend", "support", "build", "implement", "split", "extract", "replace") {
		return true
	}
	return len(words) <= 3 && symbolHits == 0 && !strings.HasSuffix(low, "?") &&
		!isBugPrompt(low) && !isFeaturePrompt(low, symbolHits)
}

func isQuestionPrompt(orig, low string) bool {
	hasEditVerb := ContainsAnyWord(low, "fix", "implement", "add", "create", "refactor", "update", "write", "change", "remove", "delete") ||
		hasImperativeVerb(low, "build", buildNounPrevs, buildNounNexts)
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
	return !ContainsAnySubstr(low, "can you ", "could you ", "would you ") &&
		ContainsAnySubstr(low, "do we ", "should we ", "did we ", "did you ", "have we ", "have you ", "are we ")
}

func isBugPrompt(low string) bool {
	if ContainsAnySubstr(low, "bug", "broken", "failing", "fails", "failure", "crash", "crashes", "panic", "regression", "debug", "investigate", "doesn't work", "does not work", "nil pointer", "race condition", "deadlock", "error in", "wrong output") {
		return true
	}
	return ContainsAnyWord(low, "fix") && !strings.Contains(low, "fix typo") && !strings.Contains(low, "fix comment") && !strings.Contains(low, "fix plan")
}

func isFeaturePrompt(low string, symbolHits int) bool {
	if isFeatureKeyword(low) {
		return true
	}
	return ContainsAnyWord(low, "add", "create", "extend", "support") && (symbolHits == 0 || strings.Contains(low, "new "))
}

func wordSet(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

var (
	buildNounPrevs = wordSet(
		"the", "a", "an", "this", "that", "our", "my", "your", "its", "their", "each", "every",
		"last", "next", "latest", "current", "local", "clean", "full", "ci", "go", "docker", "npm",
		"make", "during", "after", "before", "on", "in", "of", "for", "from", "failed", "failing", "broken",
	)
	buildNounNexts = wordSet(
		"counter", "number", "id", "version", "status", "log", "logs", "output", "step", "steps",
		"time", "duration", "failure", "failures", "error", "errors", "warning", "warnings", "cache",
		"artifact", "artifacts", "dir", "directory", "tag", "tags", "flag", "flags", "target", "targets",
		"matrix", "info", "metadata", "system", "pipeline", "script", "scripts", "tool", "tools",
		"command", "failed", "fails", "broke", "broken", "succeeded", "passed",
	)
	designNounPrevs = wordSet(
		"the", "a", "an", "this", "that", "our", "my", "your", "its", "their", "api", "schema",
		"system", "ui", "ux", "database", "db", "current", "existing", "proposed", "of", "in", "for", "with",
	)
	designNounNexts = wordSet(
		"doc", "docs", "document", "pattern", "patterns", "review", "choice", "choices",
		"decision", "decisions", "question", "questions", "tradeoff", "tradeoffs", "of",
	)
)

func hasImperativeVerb(low, verb string, nounPrevs, nounNexts map[string]bool) bool {
	tokens := strings.FieldsFunc(low, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-'
	})
	for i, tok := range tokens {
		if tok != verb {
			continue
		}
		prev, next := adjacentTokens(tokens, i)
		if !nounPrevs[prev] && !nounNexts[next] {
			return true
		}
	}
	return false
}

func adjacentTokens(tokens []string, i int) (string, string) {
	prev, next := "", ""
	if i > 0 {
		prev = tokens[i-1]
	}
	if i+1 < len(tokens) {
		next = tokens[i+1]
	}
	return prev, next
}

func isFeatureKeyword(low string) bool {
	if hasImperativeVerb(low, "build", buildNounPrevs, buildNounNexts) ||
		hasImperativeVerb(low, "design", designNounPrevs, designNounNexts) {
		return true
	}
	if ContainsAnyWord(low, "implement", "architect", "integrate", "scaffold", "introduce") {
		return true
	}
	return ContainsAnySubstr(low, "new feature", "support for", "add support")
}
