package hooks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/hooks/guard"
	"github.com/kesokaj/fyr/internal/hooks/telemetry"
	"github.com/kesokaj/fyr/internal/readledger"
	"github.com/kesokaj/fyr/internal/vet"
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

func resolvePreInvocationSubagentCtx(ctx context.Context, d Deps, home string, st *Store, convID, rawPrompt string) preInvocationSubagentInfo {
	var role, name string
	var toolCalls int
	if st != nil && st.SQLDB != nil {
		_ = st.SQLDB.QueryRowContext(ctx,
			`SELECT COALESCE(agent_role, ''), COALESCE(agent_name, ''), COALESCE(tool_calls, 0) FROM session_meta WHERE conversation_id = ?`,
			convID,
		).Scan(&role, &name, &toolCalls)
	}
	meta := hookSubagentMetaFromCtx(ctx)
	hasParent := strings.TrimSpace(meta.ParentConversationID) != ""
	if role == "" {
		role, name, hasParent = inferPreInvocationSubagentRole(ctx, d, home, convID, rawPrompt, meta, hasParent)
		if role != "" && st != nil && st.SQLDB != nil && ctx.Err() == nil {
			_, _ = st.SQLDB.ExecContext(ctx,
				`UPDATE session_meta SET agent_role = CASE WHEN agent_role = '' THEN ? ELSE agent_role END, agent_name = CASE WHEN agent_role = '' THEN ? ELSE agent_name END WHERE conversation_id = ?`,
				role, name, convID,
			)
		}
	}
	light := IsAuditSubagentRole(role) || (hasParent && role != "" && !strings.EqualFold(role, "main"))
	return preInvocationSubagentInfo{role: role, name: name, toolCalls: toolCalls, lightweight: light}
}

func inferPreInvocationSubagentRole(ctx context.Context, d Deps, home, convID, rawPrompt string, meta hookSubagentMeta, hasParent bool) (string, string, bool) {
	if a := strings.TrimSpace(meta.AgentName); a != "" && !strings.EqualFold(a, "main") {
		return a, a, hasParent || IsAuditSubagentRole(a)
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
		r, n, found := guard.LookupWatchdogSummaryRoleCtx(sctx, home, convID, d.guardWatchdogRoleDeps())
		if found && r != "" && !strings.EqualFold(r, "main") {
			return r, n, true
		}
	}
	return "", "", false
}

func matchPreInvocationRolePreamble(rawPrompt string) string {
	if r := guard.MatchWatchdogPromptPreamble(rawPrompt); r != "" {
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

func handleLightweightSubagentTurn(ctx context.Context, d Deps, o Options, sc *Scope, dir string, in HookInput, state readledger.SessionState, sub preInvocationSubagentInfo, rawPrompt, cleanPrompt string, start time.Time) (string, string) {
	if ctx != nil && ctx.Err() != nil {
		return "", ""
	}
	cat, hasCat := telemetry.LoadModelsCatalog(o.Home)
	var modelInfo telemetry.ResolvedModelInfo
	if raw := telemetry.CleanRawModelID(in.ModelName); raw != "" {
		id, disp, think := telemetry.ResolveModelDetailsWithCatalog(cat, hasCat, raw)
		modelInfo = telemetry.ResolvedModelInfo{ID: id, Display: disp, Thinking: think}
	}
	sections, meta := buildLightweightSubagentSections(o, sc, dir, state, sub, modelInfo.ID)
	bp := strings.HasPrefix(strings.TrimSpace(rawPrompt), "!")
	intent := vet.ClassifyIntent(cleanPrompt, 0)
	conflicts := vet.DetectTurnConflicts(cleanPrompt, vet.TurnBrainContext{})
	vetRes := vet.Result{
		Intent:              intent,
		Gate:                lightweightSubagentGate(state.Compacted, bp, intent, conflicts),
		Score:               100,
		Bypassed:            bp,
		SessionModel:        modelInfo.ID,
		SessionModelDisplay: modelInfo.Display,
		SessionThinking:     modelInfo.Thinking,
		Conflicts:           conflicts,
	}
	return telemetry.FinalizeHookEnvelope(ctx, o.Home, cat, hasCat, d.telemetryArtifactDeps(sc), dir, state.ConversationID, state.TurnCount, rawPrompt, cleanPrompt, toTelemetryConvInput(in), state, vetRes, vet.SliceResult{}, vet.TurnBrainContext{}, meta, sections, start)
}

func buildLightweightSubagentSections(o Options, sc *Scope, dir string, state readledger.SessionState, sub preInvocationSubagentInfo, modelID string) ([]string, telemetry.TurnBannerMeta) {
	var sections []string
	var meta telemetry.TurnBannerMeta
	meta.SessionModelResolved = true
	if state.Compacted {
		if o.BrainReprime != nil {
			if rep := strings.TrimSpace(o.BrainReprime(dir, o.Home)); rep != "" {
				sections = append(sections, rep)
			}
		}
		meta.CompactedGeneration = max(1, state.GenerationID)
	}
	if warnFull, warnShort := dedupContextKnee(o.Home, sc, state.ConversationID, modelID, state); warnFull != "" {
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

func lightweightSubagentGate(compacted, bp bool, intent vet.IntentMode, conflicts []string) vet.VetGate {
	if len(conflicts) > 0 && !bp {
		return vet.GateVetFirst
	}
	if !compacted && !bp && (intent == vet.IntentApproval || intent == vet.IntentTrivial) {
		return vet.GatePassThrough
	}
	return vet.GateEnrichAndProceed
}
