package search

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type preInvocationSubagentInfo struct {
	role        string
	name        string
	toolCalls   int
	lightweight bool
}

var preInvocationVerifierPreambles = []string{
	"you run **live runtime e2e",
	"you run live runtime e2e",
}

func resolvePreInvocationSubagentCtx(ctx context.Context, home string, st *store, convID, rawPrompt string) preInvocationSubagentInfo {
	var role, name string
	var toolCalls int
	if st != nil && st.db != nil {
		_ = st.db.QueryRowContext(ctx,
			`SELECT COALESCE(agent_role, ''), COALESCE(agent_name, ''), COALESCE(tool_calls, 0) FROM session_meta WHERE conversation_id = ?`,
			convID,
		).Scan(&role, &name, &toolCalls)
	}
	meta := hookSubagentMetaFromCtx(ctx)
	hasParent := strings.TrimSpace(meta.ParentConversationID) != ""
	if role == "" {
		role, name, hasParent = inferPreInvocationSubagentRole(ctx, home, convID, rawPrompt, meta, hasParent)
		if role != "" && st != nil && st.db != nil && ctx.Err() == nil {
			_, _ = st.db.ExecContext(ctx,
				`UPDATE session_meta SET agent_role = CASE WHEN agent_role = '' THEN ? ELSE agent_role END, agent_name = CASE WHEN agent_role = '' THEN ? ELSE agent_name END WHERE conversation_id = ?`,
				role, name, convID,
			)
		}
	}
	light := isAuditSubagentRole(role) || (hasParent && role != "" && !strings.EqualFold(role, "main"))
	return preInvocationSubagentInfo{role: role, name: name, toolCalls: toolCalls, lightweight: light}
}

func inferPreInvocationSubagentRole(ctx context.Context, home, convID, rawPrompt string, meta hookSubagentMeta, hasParent bool) (string, string, bool) {
	if a := strings.TrimSpace(meta.AgentName); a != "" && !strings.EqualFold(a, "main") {
		return a, a, hasParent || isAuditSubagentRole(a)
	}
	if r := matchPreInvocationRolePreamble(rawPrompt); r != "" {
		return r, r, true
	}
	if hasParent {
		return "self", "self", true
	}
	if home != "" && convID != "" && convID != "default" && ctx.Err() == nil {
		sctx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
		defer cancel()
		r, n, found := lookupWatchdogSummaryRoleCtx(sctx, home, convID)
		if found && r != "" && !strings.EqualFold(r, "main") {
			return r, n, true
		}
	}
	return "", "", false
}

func matchPreInvocationRolePreamble(rawPrompt string) string {
	if r := matchWatchdogPromptPreamble(rawPrompt); r != "" {
		return r
	}
	stripped := strings.TrimSpace(StripSandboxPreamble(rawPrompt))
	if stripped == "" {
		return ""
	}
	lines := strings.Split(strings.ToLower(strings.ReplaceAll(stripped, "\r\n", "\n")), "\n")
	checked := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		for _, p := range preInvocationVerifierPreambles {
			if strings.HasPrefix(t, p) {
				return "verifier"
			}
		}
		checked++
		if checked >= 2 {
			break
		}
	}
	return ""
}

func handleLightweightSubagentTurn(ctx context.Context, o Options, sc *scope, dir string, in HookInput, state SessionState, sub preInvocationSubagentInfo, rawPrompt, cleanPrompt string, start time.Time) (string, string) {
	if ctx != nil && ctx.Err() != nil {
		return "", ""
	}
	cat, hasCat := loadModelsCatalog(o.Home)
	var modelInfo resolvedModelInfo
	if raw := cleanRawModelID(in.ModelName); raw != "" {
		id, disp, think := resolveModelDetailsWithCatalog(cat, hasCat, raw)
		modelInfo = resolvedModelInfo{ID: id, Display: disp, Thinking: think}
	}
	sections, meta := buildLightweightSubagentSections(o, sc, dir, state, sub, modelInfo.ID)
	bp := strings.HasPrefix(strings.TrimSpace(rawPrompt), "!")
	intent := ClassifyIntent(cleanPrompt, 0)
	conflicts := detectTurnConflicts(cleanPrompt, TurnBrainContext{})
	vetRes := VetResult{
		Intent:              intent,
		Gate:                lightweightSubagentGate(state.Compacted, bp, intent, conflicts),
		Score:               100,
		Bypassed:            bp,
		SessionModel:        modelInfo.ID,
		SessionModelDisplay: modelInfo.Display,
		SessionThinking:     modelInfo.Thinking,
		Conflicts:           conflicts,
	}
	return finalizeHookEnvelope(ctx, o.Home, cat, hasCat, sc, dir, state.ConversationID, state.TurnCount, rawPrompt, cleanPrompt, in, state, vetRes, SliceResult{}, TurnBrainContext{}, meta, sections, start)
}

func buildLightweightSubagentSections(o Options, sc *scope, dir string, state SessionState, sub preInvocationSubagentInfo, modelID string) ([]string, TurnBannerMeta) {
	var sections []string
	var meta TurnBannerMeta
	meta.SessionModelResolved = true
	if state.Compacted {
		if o.BrainReprime != nil {
			if rep := strings.TrimSpace(o.BrainReprime(dir, o.Home)); rep != "" {
				sections = append(sections, rep)
			}
		}
		meta.CompactedGeneration = max(1, state.GenerationID)
	}
	if warnFull, warnShort := dedupContextKneeWarning(o.Home, sc, state.ConversationID, modelID, state); warnFull != "" {
		sections = append(sections, warnFull)
		meta.ContextKneeWarning = warnShort
	}
	if deny, warn := checkWatchdogBudget(sub.role, sub.toolCalls); deny != "" {
		sections = append(sections, deny)
		meta.Notices = append(meta.Notices, deny)
	} else if warn != "" {
		sections = append(sections, warn)
		meta.Notices = append(meta.Notices, warn)
	}
	_, capAt := SubagentBudgetCap(sub.role)
	meta.Notices = append(meta.Notices, fmt.Sprintf("subagent fast-path: %s (budget %d/%d)", sub.role, sub.toolCalls, capAt))
	meta.ExtraSections = sections
	return sections, meta
}

func lightweightSubagentGate(compacted, bypassed bool, intent IntentMode, conflicts []string) VetGate {
	if len(conflicts) > 0 && !bypassed {
		return GateVetFirst
	}
	if !compacted && !bypassed && (intent == IntentApproval || intent == IntentTrivial) {
		return GatePassThrough
	}
	return GateEnrichAndProceed
}
