package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

const askTranscriptScanBytes = 32 << 10

var (
	adrConflictIDRe = regexp.MustCompile(`(?i)\badr-\d+[a-z]?\b`)
	quotaWordRe     = regexp.MustCompile(`\bquotas?\b`)
	quotaCueRe      = regexp.MustCompile(`(?:\b(?:exhaust\w*|deplet\w*|empty|exceed\w*|floor|low|unavailable|blocked|out\s+of)\b|(?:^|[^0-9])0%)`)
	rateLimitWordRe = regexp.MustCompile(`\brate[\s-]+limits?\b`)
	rateLimitCueRe  = regexp.MustCompile(`\b(?:exhaust\w*|exceed\w*|blocked)\b`)
)

func handlePreToolAskQuestionCtx(ctx context.Context, o Options, dir string, wd watchdogPreState, wdSt *store, wdRoot string, in HookInput, out io.Writer) error {
	if ctx.Err() != nil || hooksDisabledForHome(o.Home, dir) {
		return writeDecision(out, "allow", "")
	}
	isAuto, source := detectKickoffOrWorkerSessionCtx(ctx, o, dir, wd, wdSt, wdRoot, in)
	if !isAuto {
		return writeDecision(out, "allow", "")
	}
	payload := collectAskQuestionText(in.ToolCall.Args)
	if hasAskQuestionEscapeClause(payload) {
		return writeDecision(out, "allow", "")
	}
	return writeDecision(out, "deny", fmt.Sprintf(
		"[KICKOFF_GUARD] ask_question is blocked during autonomous Kickoff/worker turns (%s); settle implementation choices autonomously in the Autonomous Decisions Log, or cite an explicit PRD.md/ADR conflict, Re-plan trigger, or quota exhaustion.",
		source,
	))
}

func detectKickoffOrWorkerSessionCtx(ctx context.Context, o Options, dir string, wd watchdogPreState, wdSt *store, wdRoot string, in HookInput) (bool, string) {
	if os.Getenv("FYR_RELAY") == "1" {
		return true, "relay"
	}
	if isKickoffOrWorkerPrompt(in.LastUserInput) {
		return true, "prompt"
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
	if ok, src := checkPersistedKickoffSessionCtx(ctx, dir, wdSt, wdRoot, in.ConversationID); ok {
		return true, src
	}
	if firstPrompt := readTranscriptFirstUserPrompt(in.TranscriptPath); isKickoffOrWorkerPrompt(firstPrompt) {
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

func checkPersistedKickoffSessionCtx(ctx context.Context, dir string, wdSt *store, wdRoot, convID string) (bool, string) {
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
	normConv := normalizeLedgerConvID(convID)
	var role string
	if err := st.db.QueryRowContext(ctx, `SELECT COALESCE(agent_role, '') FROM session_meta WHERE conversation_id = ?`, normConv).Scan(&role); err == nil {
		if r := strings.TrimSpace(role); r != "" && !strings.EqualFold(r, "main") {
			return true, "session_meta:" + r
		}
	}
	for _, q := range []string{
		`SELECT COALESCE(raw_prompt, '') FROM vet_history WHERE conversation_id = ? ORDER BY turn_num DESC LIMIT 1`,
		`SELECT COALESCE(raw_prompt, '') FROM vet_history WHERE conversation_id = ? ORDER BY turn_num ASC LIMIT 1`,
	} {
		var rawPrompt string
		if err := st.db.QueryRowContext(ctx, q, normConv).Scan(&rawPrompt); err == nil && isKickoffOrWorkerPrompt(rawPrompt) {
			return true, "vet_history"
		}
	}
	return false, ""
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

func readTranscriptFirstUserPrompt(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	fi, err := os.Lstat(p)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return ""
	}
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, askTranscriptScanBytes))
	if err != nil || len(data) == 0 {
		return ""
	}
	seen := 0
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if seen++; seen > 20 {
			break
		}
		if text := extractTranscriptUserText(line); text != "" {
			return text
		}
	}
	return ""
}

func extractTranscriptUserText(line string) string {
	var row map[string]any
	if json.Unmarshal([]byte(line), &row) != nil {
		return ""
	}
	if s := extractStringArg(row, "userInput", "user_input", "lastUserInput", "last_user_input", "prompt"); s != "" {
		return s
	}
	if isUserTranscriptRole(row) {
		if s := extractContentText(row["content"]); s != "" {
			return s
		}
		if s := extractStringArg(row, "text", "body"); s != "" {
			return s
		}
	}
	if msg, ok := row["message"].(map[string]any); ok && isUserTranscriptRole(msg) {
		return extractContentText(msg["content"])
	}
	return ""
}

func isUserTranscriptRole(row map[string]any) bool {
	for _, k := range []string{"type", "role", "author"} {
		if s, ok := row[k].(string); ok {
			switch strings.ToLower(strings.TrimSpace(s)) {
			case "user", "user_input", "human":
				return true
			}
		}
	}
	return false
}

func extractContentText(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	arr, ok := v.([]any)
	if !ok {
		return ""
	}
	var out strings.Builder
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if s := extractStringArg(m, "text", "content"); s != "" {
			if out.Len() > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(s)
		}
	}
	return strings.TrimSpace(out.String())
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
			b.WriteString(s)
		}
	case []any:
		for _, elem := range x {
			appendAskValueStrings(b, elem, limit)
		}
	case map[string]any:
		for _, val := range x {
			appendAskValueStrings(b, val, limit)
		}
	}
}

func hasAskQuestionEscapeClause(text string) bool {
	low := strings.ToLower(text)
	if strings.Contains(low, "re-plan") || strings.Contains(low, "replan") {
		return true
	}
	if hasQuotaExhaustionCue(low) {
		return true
	}
	hasConflictCue := containsAnySubstr(low,
		"conflict", "contradict", "violat", "amend", "override", "supersede",
		"incompatib", "mismatch", "diverg", "deviat", "block", "cannot satisfy", "breaks ",
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
