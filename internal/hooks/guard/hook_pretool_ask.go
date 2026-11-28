package guard

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/kesokaj/fyr/internal/readledger"
	"github.com/kesokaj/fyr/internal/trail"
	"github.com/kesokaj/fyr/internal/vet"
)

var (
	adrConflictIDRe = regexp.MustCompile(`(?i)\badr-\d+[a-z]?\b`)
	replanWordRe    = regexp.MustCompile(`\bre-?plan\b`)
	quotaWordRe     = regexp.MustCompile(`\bquotas?\b`)
	quotaCueRe      = regexp.MustCompile(`(?:\b(?:exhaust\w*|deplet\w*|empty|exceed\w*|floor|low|unavailable|blocked|out\s+of)\b|(?:^|[^0-9])0%)`)
	rateLimitWordRe = regexp.MustCompile(`\brate[\s-]+limits?\b`)
	rateLimitCueRe  = regexp.MustCompile(`\b(?:exhaust\w*|exceed\w*|blocked)\b`)
)

// AskInput carries the hook context fields checked by the ask_question guard.
type AskInput struct {
	Home                 string
	Dir                  string
	ConversationID       string
	LastUserInput        string
	TranscriptPath       string
	ToolCallArgs         map[string]any
	ParentConversationID string
	AgentName            string
	WatchdogRole         string
}

// AskDeps supplies bypass, ledger DB, and decision-writer callbacks for ask_question guarding.
type AskDeps struct {
	HookBypassed  func(home, dir, event string) bool
	OpenLedgerDB  func(root string) (*sql.DB, func(), error)
	WriteDecision func(out io.Writer, decision, reason string) error
}

// HandlePreToolAskQuestionCtx enforces [KICKOFF_GUARD] on ask_question tool calls.
func HandlePreToolAskQuestionCtx(ctx context.Context, in AskInput, wdDB *sql.DB, wdRoot string, deps AskDeps, out io.Writer) error {
	if ctx.Err() != nil || (deps.HookBypassed != nil && deps.HookBypassed(in.Home, in.Dir, "pre-tool-use")) {
		return deps.WriteDecision(out, "allow", "")
	}
	isAuto, source := DetectKickoffOrWorkerSessionCtx(ctx, in, wdDB, wdRoot, deps.OpenLedgerDB)
	if !isAuto {
		return deps.WriteDecision(out, "allow", "")
	}
	if !isHeadlessAskSource(source) {
		payload := collectAskQuestionText(in.ToolCallArgs)
		if HasAskQuestionEscapeClause(payload) {
			return deps.WriteDecision(out, "allow", "")
		}
	}
	return deps.WriteDecision(out, "deny", fmt.Sprintf(
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

func detectStatelessHeadlessAsk(in AskInput) (bool, string) {
	if os.Getenv("FYR_RELAY") == "1" {
		return true, "relay"
	}
	if strings.TrimSpace(in.ParentConversationID) != "" {
		return true, "subagent"
	}
	if a := strings.TrimSpace(in.AgentName); a != "" && !strings.EqualFold(a, "main") {
		return true, "subagent:" + a
	}
	if r := strings.TrimSpace(in.WatchdogRole); r != "" && !strings.EqualFold(r, "main") {
		return true, "role:" + r
	}
	return false, ""
}

// DetectKickoffOrWorkerSessionCtx determines whether the active session is a headless worker or Kickoff turn.
func DetectKickoffOrWorkerSessionCtx(ctx context.Context, in AskInput, wdDB *sql.DB, wdRoot string, openLedgerDB func(root string) (*sql.DB, func(), error)) (bool, string) {
	if ok, src := detectStatelessHeadlessAsk(in); ok {
		return true, src
	}
	if strings.TrimSpace(in.LastUserInput) != "" {
		if ok, src := checkPersistedSessionRoleCtx(ctx, in.Dir, wdDB, wdRoot, in.ConversationID, openLedgerDB); ok {
			return true, src
		}
		if isKickoffOrWorkerPrompt(in.LastUserInput) {
			return true, "prompt"
		}
		return false, ""
	}
	if ok, src, decided := checkPersistedKickoffSessionCtx(ctx, in.Dir, wdDB, wdRoot, in.ConversationID, openLedgerDB); decided {
		return ok, src
	}
	if latestPrompt := readTranscriptLatestUserPrompt(in.TranscriptPath); isKickoffOrWorkerPrompt(latestPrompt) {
		return true, "transcript"
	}
	return false, ""
}

func isKickoffOrWorkerPrompt(raw string) bool {
	if vet.IsWorkerBriefPrompt(raw) {
		return true
	}
	s := vet.StripAnySandboxPreamble(raw)
	if s == "" {
		return false
	}
	low := strings.ToLower(strings.ReplaceAll(s, "\r\n", "\n"))
	if hasKickoffResumeHeader(strings.Split(low, "\n")) {
		return true
	}
	return vet.HasAutonomousWaiver(low)
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
		if !vet.IsKickoffHeaderMetaLine(line) {
			break
		}
	}
	return hasProject && hasCue
}

func checkPersistedSessionRoleCtx(ctx context.Context, dir string, wdDB *sql.DB, wdRoot, convID string, openLedgerDB func(root string) (*sql.DB, func(), error)) (bool, string) {
	if ctx.Err() != nil || strings.TrimSpace(convID) == "" {
		return false, ""
	}
	db, cleanup := openExistingAskDB(dir, wdDB, wdRoot, openLedgerDB)
	if db == nil {
		return false, ""
	}
	if cleanup != nil {
		defer cleanup()
	}
	return querySessionMetaRoleCtx(ctx, db, readledger.NormalizeConvID(convID))
}

func querySessionMetaRoleCtx(ctx context.Context, db *sql.DB, normConv string) (bool, string) {
	var role string
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(agent_role, '') FROM session_meta WHERE conversation_id = ?`, normConv).Scan(&role); err == nil {
		if r := strings.TrimSpace(role); r != "" && !strings.EqualFold(r, "main") {
			return true, "session_meta:" + r
		}
	}
	return false, ""
}

func checkPersistedKickoffSessionCtx(ctx context.Context, dir string, wdDB *sql.DB, wdRoot, convID string, openLedgerDB func(root string) (*sql.DB, func(), error)) (bool, string, bool) {
	if ctx.Err() != nil || strings.TrimSpace(convID) == "" {
		return false, "", false
	}
	db, cleanup := openExistingAskDB(dir, wdDB, wdRoot, openLedgerDB)
	if db == nil {
		return false, "", false
	}
	if cleanup != nil {
		defer cleanup()
	}
	normConv := readledger.NormalizeConvID(convID)
	if ok, src := querySessionMetaRoleCtx(ctx, db, normConv); ok {
		return true, src, true
	}
	var rawPrompt string
	err := db.QueryRowContext(ctx,
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

func openExistingAskDB(dir string, wdDB *sql.DB, wdRoot string, openLedgerDB func(root string) (*sql.DB, func(), error)) (*sql.DB, func()) {
	if wdDB != nil {
		return wdDB, nil
	}
	if openLedgerDB == nil {
		return nil, nil
	}
	root := wdRoot
	if root == "" {
		if found, _, ok := FindGitRootFast(dir); ok {
			root = found
		}
	}
	if root == "" || trail.CheckScratchNoSymlinks(root) != nil || !IsRegularFileFast(ProjectDB(root)) {
		return nil, nil
	}
	db, cleanup, err := openLedgerDB(root)
	if err != nil || db == nil {
		return nil, nil
	}
	return db, cleanup
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
		appendAskTextLeaf(b, x, limit)
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

func appendAskTextLeaf(b *strings.Builder, raw string, limit int) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	rem := limit - b.Len()
	if rem <= 0 {
		return
	}
	if len(s) > rem {
		s = s[:rem]
	}
	b.WriteString(s)
}

// HasAskQuestionEscapeClause reports whether text cites a PRD/ADR conflict, Re-plan trigger, or quota exhaustion.
func HasAskQuestionEscapeClause(text string) bool {
	low := strings.ToLower(text)
	if replanWordRe.MatchString(low) {
		return true
	}
	if hasQuotaExhaustionCue(low) {
		return true
	}
	hasConflictCue := vet.ContainsAnySubstr(low,
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
