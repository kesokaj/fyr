package search

import "strings"

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
	s := stripAnySandboxPreamble(p)
	if s == "" {
		return false
	}
	low := strings.ToLower(strings.ReplaceAll(s, "\r\n", "\n"))
	return hasWorkerTopHeader(strings.Split(low, "\n"))
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
		containsAnyWord(low, "add", "create", "extend", "fix", "refactor", "implement", "build", "update")
}

func isReleasePrompt(low string) bool {
	if containsAnySubstr(low, "write me a prompt", "give me the prompt", "when this is done", "once this is done", "after phase", "when phase") {
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
		!containsAnyWord(low, "refactor", "redesign", "architect", "migrate", "add", "create", "extend", "support", "build", "implement", "split", "extract", "replace") {
		return true
	}
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
	return containsAnyWord(low, "fix") && !strings.Contains(low, "fix typo") && !strings.Contains(low, "fix comment") && !strings.Contains(low, "fix plan")
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
