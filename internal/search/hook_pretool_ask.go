package search

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
)

var (
	adrConflictIDRe = regexp.MustCompile(`(?i)\badr-\d+[a-z]?\b`)
	replanWordRe    = regexp.MustCompile(`\bre-?plan\b`)
	quotaWordRe     = regexp.MustCompile(`\bquotas?\b`)
	quotaCueRe      = regexp.MustCompile(`(?:\b(?:exhaust\w*|deplet\w*|empty|exceed\w*|floor|low|unavailable|blocked|out\s+of)\b|(?:^|[^0-9])0%)`)
	rateLimitWordRe = regexp.MustCompile(`\brate[\s-]+limits?\b`)
	rateLimitCueRe  = regexp.MustCompile(`\b(?:exhaust\w*|exceed\w*|blocked)\b`)
)

func handlePreToolAskQuestionCtx(ctx context.Context, o Options, dir string, wd watchdogPreState, wdSt *store, wdRoot string, in HookInput, out io.Writer) error {
	if ctx.Err() != nil || hookBypassedForEvent(o.Home, dir, "pre-tool-use") {
		return writeDecision(out, "allow", "")
	}
	isAuto, source := detectKickoffOrWorkerSessionCtx(ctx, dir, wd, wdSt, wdRoot, in)
	if !isAuto {
		return writeDecision(out, "allow", "")
	}
	if !isHeadlessAskSource(source) {
		payload := collectAskQuestionText(in.ToolCall.Args)
		if hasAskQuestionEscapeClause(payload) {
			return writeDecision(out, "allow", "")
		}
	}
	return writeDecision(out, "deny", fmt.Sprintf(
		"[KICKOFF_GUARD] ask_question is blocked during autonomous Kickoff/worker turns (%s); settle implementation choices autonomously in the Autonomous Decisions Log, or cite an explicit PRD.md/ADR conflict, Re-plan trigger, or quota exhaustion.",
		source,
	))
}

func isHeadlessAskSource(source string) bool {
	return source == "relay" ||
		strings.HasPrefix(source, "subagent") ||
		strings.HasPrefix(source, "role:") ||
		strings.HasPrefix(source, "session_meta:")
}

func detectStatelessHeadlessAsk(ctx context.Context, wd watchdogPreState) (bool, string) {
	if os.Getenv("FYR_RELAY") == "1" {
		return true, "relay"
	}
	meta := hookSubagentMetaFromCtx(ctx)
	if strings.TrimSpace(meta.ParentConversationID) != "" {
		return true, "subagent"
	}
	if a := strings.TrimSpace(meta.AgentName); a != "" && !strings.EqualFold(a, "main") {
		return true, "subagent:" + a
	}
	if r := strings.TrimSpace(wd.role); r != "" && !strings.EqualFold(r, "main") {
		return true, "role:" + r
	}
	return false, ""
}

func detectKickoffOrWorkerSessionCtx(ctx context.Context, dir string, wd watchdogPreState, wdSt *store, wdRoot string, in HookInput) (bool, string) {
	if ok, src := detectStatelessHeadlessAsk(ctx, wd); ok {
		return true, src
	}
	if strings.TrimSpace(in.LastUserInput) != "" {
		if ok, src := checkPersistedSessionRoleCtx(ctx, dir, wdSt, wdRoot, in.ConversationID); ok {
			return true, src
		}
		if isKickoffOrWorkerPrompt(in.LastUserInput) {
			return true, "prompt"
		}
		return false, ""
	}
	if ok, src, decided := checkPersistedKickoffSessionCtx(ctx, dir, wdSt, wdRoot, in.ConversationID); decided {
		return ok, src
	}
	if latestPrompt := readTranscriptLatestUserPrompt(in.TranscriptPath); isKickoffOrWorkerPrompt(latestPrompt) {
		return true, "transcript"
	}
	return false, ""
}

func isKickoffOrWorkerPrompt(raw string) bool {
	if isWorkerBriefPrompt(raw) {
		return true
	}
	s := stripAnySandboxPreamble(raw)
	if s == "" {
		return false
	}
	low := strings.ToLower(strings.ReplaceAll(s, "\r\n", "\n"))
	if hasKickoffResumeHeader(strings.Split(low, "\n")) {
		return true
	}
	return hasAutonomousWaiver(low)
}

func hasKickoffResumeHeader(lines []string) bool {
	hasProject, hasCue := false, false
	limit := min(len(lines), 8)
	for i := 0; i < limit; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "project:") {
			hasProject = true
			if strings.Contains(line, "run fyr resume") || strings.Contains(line, "handoff ") {
				hasCue = true
			}
			continue
		}
		if strings.HasPrefix(line, "done when:") || strings.HasPrefix(line, "standing approvals") || strings.HasPrefix(line, "never:") {
			hasCue = true
			continue
		}
		if !isKickoffHeaderMetaLine(line) {
			break
		}
	}
	return hasProject && hasCue
}

func checkPersistedSessionRoleCtx(ctx context.Context, dir string, wdSt *store, wdRoot, convID string) (bool, string) {
	if ctx.Err() != nil || strings.TrimSpace(convID) == "" {
		return false, ""
	}
	st, ownSt := openExistingAskStore(dir, wdSt, wdRoot)
	if st == nil {
		return false, ""
	}
	if ownSt {
		defer st.close()
	}
	return querySessionMetaRoleCtx(ctx, st, normalizeLedgerConvID(convID))
}

func querySessionMetaRoleCtx(ctx context.Context, st *store, normConv string) (bool, string) {
	var role string
	if err := st.db.QueryRowContext(ctx, `SELECT COALESCE(agent_role, '') FROM session_meta WHERE conversation_id = ?`, normConv).Scan(&role); err == nil {
		if r := strings.TrimSpace(role); r != "" && !strings.EqualFold(r, "main") {
			return true, "session_meta:" + r
		}
	}
	return false, ""
}

func checkPersistedKickoffSessionCtx(ctx context.Context, dir string, wdSt *store, wdRoot, convID string) (bool, string, bool) {
	if ctx.Err() != nil || strings.TrimSpace(convID) == "" {
		return false, "", false
	}
	st, ownSt := openExistingAskStore(dir, wdSt, wdRoot)
	if st == nil {
		return false, "", false
	}
	if ownSt {
		defer st.close()
	}
	normConv := normalizeLedgerConvID(convID)
	if ok, src := querySessionMetaRoleCtx(ctx, st, normConv); ok {
		return true, src, true
	}
	var rawPrompt string
	err := st.db.QueryRowContext(ctx,
		`SELECT COALESCE(raw_prompt, '') FROM vet_history WHERE conversation_id = ? ORDER BY turn_num DESC LIMIT 1`,
		normConv,
	).Scan(&rawPrompt)
	if err != nil || strings.TrimSpace(rawPrompt) == "" {
		return false, "", false
	}
	if isKickoffOrWorkerPrompt(rawPrompt) {
		return true, "vet_history", true
	}
	return false, "", true
}

func openExistingAskStore(dir string, wdSt *store, wdRoot string) (*store, bool) {
	if wdSt != nil {
		return wdSt, false
	}
	root := wdRoot
	if root == "" {
		if found, _, ok := findGitRootFast(dir); ok {
			root = found
		}
	}
	if root == "" || checkScratchNoSymlinks(root) != nil || !isRegularFileFast(projectDB(root)) {
		return nil, false
	}
	st, _, err := openLedgerStore(root)
	if err != nil || st == nil {
		return nil, false
	}
	return st, true
}

func collectAskQuestionText(args map[string]any) string {
	var b strings.Builder
	appendAskValueStrings(&b, args, 16<<10)
	return b.String()
}

func appendAskValueStrings(b *strings.Builder, v any, limit int) {
	if b.Len() >= limit || v == nil {
		return
	}
	switch x := v.(type) {
	case string:
		if s := strings.TrimSpace(x); s != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			rem := limit - b.Len()
			if rem > 0 {
				if len(s) > rem {
					s = s[:rem]
				}
				b.WriteString(s)
			}
		}
	case []any:
		for _, elem := range x {
			appendAskValueStrings(b, elem, limit)
		}
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) {
			appendAskValueStrings(b, x[k], limit)
		}
	}
}

func hasAskQuestionEscapeClause(text string) bool {
	low := strings.ToLower(text)
	if replanWordRe.MatchString(low) {
		return true
	}
	if hasQuotaExhaustionCue(low) {
		return true
	}
	hasConflictCue := containsAnySubstr(low,
		"conflict", "contradict", "violat", "amend", "override", "supersede",
		"incompatib", "mismatch", "diverg", "deviat", "cannot satisfy", "breaks ",
	)
	if strings.Contains(low, "prd.md") && hasConflictCue {
		return true
	}
	if (adrConflictIDRe.MatchString(low) || strings.Contains(low, "adr ")) && hasConflictCue {
		return true
	}
	return false
}

func hasQuotaExhaustionCue(low string) bool {
	if quotaWordRe.MatchString(low) && quotaCueRe.MatchString(low) {
		return true
	}
	return rateLimitWordRe.MatchString(low) && rateLimitCueRe.MatchString(low)
}
