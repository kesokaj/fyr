package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

type watchdogPreState struct {
	active                             bool
	ringTool, argHash, role, agentName string
	denyReason, budgetWarn             string
}

var watchdogToolMap = map[string]string{
	"managetask": "manage_task", "manage_task": "manage_task",
	"managesubagents": "manage_subagents", "manage_subagents": "manage_subagents",
	"sendmessage": "send_message", "send_message": "send_message",
	"invokesubagent": "invoke_subagent", "invoke_subagent": "invoke_subagent",
}

func normalizeWatchdogToolName(raw string) string {
	norm := normalizeHookToolName(raw)
	if mapped, ok := watchdogToolMap[norm]; ok {
		return mapped
	}
	return norm
}

func watchdogRingTool(tc HookToolCall) string {
	norm := normalizeWatchdogToolName(tc.Name)
	if norm != "manage_task" && norm != "manage_subagents" {
		return norm
	}
	if act := strings.ToLower(strings.TrimSpace(extractStringArg(tc.Args, "Action", "action"))); act != "" && act != "list" && act != "status" {
		return norm + ":" + act
	}
	return norm
}

func watchdogArgHash(args map[string]any) string {
	raw, err := json.Marshal(args)
	if err != nil || args == nil {
		raw = []byte("{}")
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:16]
}

func watchdogShouldSkip(home, dir string, in HookInput) bool {
	if hooksDisabledForHome(home, dir) || strings.TrimSpace(in.ConversationID) == "" || !ensureExistingCheckoutIgnored(dir) {
		return true
	}
	target := extractToolTargetPath(in.ToolCall.Args)
	return target != "" && hooksDisabledForHome(home, resolveToolProjectDir(dir, in, target))
}

func evaluateWatchdogPreCtx(ctx context.Context, o Options, dir string, in HookInput) (watchdogPreState, *store, string) {
	if ctx.Err() != nil || watchdogShouldSkip(o.Home, dir, in) {
		return watchdogPreState{}, nil, ""
	}
	st, root, err := openLedgerStore(dir)
	if err != nil || st == nil {
		return watchdogPreState{}, nil, ""
	}
	convID := strings.TrimSpace(in.ConversationID)
	normTool, ringTool, argHash := normalizeWatchdogToolName(in.ToolCall.Name), watchdogRingTool(in.ToolCall), watchdogArgHash(in.ToolCall.Args)
	var role, name string
	var toolCalls int
	_ = st.db.QueryRowContext(ctx, `SELECT COALESCE(agent_role, ''), COALESCE(agent_name, ''), COALESCE(tool_calls, 0) FROM session_meta WHERE conversation_id = ?`, convID).Scan(&role, &name, &toolCalls)
	if role == "" && ctx.Err() == nil {
		role, name = resolveWatchdogRoleCtx(ctx, o.Home, convID, in.LastUserInput)
	}
	deny, warn := "", ""
	if ctx.Err() == nil {
		if deny = checkWatchdogPollLoop(ctx, st, convID, normTool, ringTool); deny == "" && !isWatchdogExempt(dir, in, normTool) {
			if deny = checkWatchdogErrorLoop(ctx, st, convID, ringTool, argHash); deny == "" {
				deny, warn = checkWatchdogBudget(role, toolCalls)
			}
		}
	}
	return watchdogPreState{active: true, ringTool: ringTool, argHash: argHash, role: role, agentName: name, denyReason: deny, budgetWarn: warn}, st, root
}

func checkWatchdogBudget(role string, toolCalls int) (string, string) {
	if role == "" || strings.EqualFold(role, "main") {
		return "", ""
	}
	warnAt, capAt := 90, 120
	if isAuditSubagentRole(role) {
		warnAt, capAt = 25, 40
	}
	if toolCalls >= capAt {
		return fmt.Sprintf("[SUBAGENT_BUDGET] %s reached %d/%d tool calls; write final artifact or finish now", role, toolCalls, capAt), ""
	}
	if toolCalls >= warnAt {
		return "", fmt.Sprintf("[SUBAGENT_BUDGET_WARN] %s at %d/%d tool calls; finish soon", role, toolCalls, capAt)
	}
	return "", ""
}

func isAuditSubagentRole(role string) bool {
	r := strings.ToLower(strings.TrimSpace(role))
	for _, p := range []string{"reviewer", "security", "verifier", "explorer", "dreamer", "documentation", "research", "subagent"} {
		if r == p || strings.HasPrefix(r, p+"-") || strings.HasPrefix(r, p+"_") {
			return true
		}
	}
	return strings.HasPrefix(r, "sub-")
}

func isWatchdogExempt(dir string, in HookInput, normTool string) bool {
	if normTool == "finish" || normTool == "send_message" {
		return true
	}
	if normTool == "run_command" {
		return isWatchdogExemptBash(in.ToolCall.Args)
	}
	target := extractToolTargetPath(in.ToolCall.Args)
	if normTool != "write_or_edit" || target == "" {
		return false
	}
	toolDir := resolveToolProjectDir(dir, in, target)
	base := dir
	if root, _, ok := findGitRootFast(toolDir); ok {
		base = root
	} else if root, _, ok := findGitRootFast(dir); ok {
		base = root
	}
	if base == "" || checkScratchNoSymlinks(base) != nil {
		return false
	}
	resolved, canonBase := resolveTargetWithAncestors(toolDir, target), CanonicalizePath("", base)
	rel, err := filepath.Rel(filepath.FromSlash(canonBase), filepath.FromSlash(resolved))
	if err != nil || !strings.HasPrefix(filepath.ToSlash(filepath.Clean(rel)), ".agents/scratch/agents/") {
		return false
	}
	return !hasSymlinkSegmentUnder(base, toolDir, target) && !hasSymlinkSegmentUnder(canonBase, canonBase, filepath.FromSlash(resolved))
}

func isWatchdogExemptBash(args map[string]any) bool {
	_, cmd := extractBashCmdArg(args)
	segs := splitShellSegments(cmd)
	if cmd == "" || len(segs) == 0 || hasShellExpansionOutsideSingleQuotes(cmd) {
		return false
	}
	for _, seg := range segs {
		cmdTok, rest := unwrapSegmentCommand(tokenizeShellSegment(seg))
		switch {
		case cmdTok == "git":
			sub, subArgs := skipGitGlobalFlags(rest)
			if !slices.Contains([]string{"status", "diff", "add", "commit"}, sub) && (sub != "checkout" || !slices.Contains(subArgs, "--detach")) {
				return false
			}
		case cmdTok == "fyr" && isExemptFyrGitCheck(rest):
		default:
			return false
		}
	}
	return true
}

func isExemptFyrGitCheck(args []string) bool {
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			return args[i] == "git-check"
		}
		if slices.Contains([]string{"--home", "--repo", "--dir", "-C"}, args[i]) && i+1 < len(args) {
			i++
		}
	}
	return false
}
