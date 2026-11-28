package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kesokaj/fyr/internal/hooks/guard"
	"github.com/kesokaj/fyr/internal/readledger"
	"github.com/kesokaj/fyr/internal/trail"
)

type watchdogPreState struct {
	active                             bool
	exempt                             bool
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
	norm := NormalizeHookToolName(raw)
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
	if (!isRelaySession() && HooksDisabledForHome(home, dir)) || strings.TrimSpace(in.ConversationID) == "" || !guard.EnsureExistingCheckoutIgnored(dir) {
		return true
	}
	target := ExtractToolTargetPath(in.ToolCall.Args)
	return target != "" && !isRelaySession() && HooksDisabledForHome(home, ResolveToolProjectDir(dir, in, target))
}

func evaluateWatchdogPreCtx(ctx context.Context, d Deps, o Options, dir string, in HookInput) (watchdogPreState, *Store, string) {
	if ctx.Err() != nil || watchdogShouldSkip(o.Home, dir, in) || d.OpenLedgerStore == nil {
		return watchdogPreState{}, nil, ""
	}
	st, root, err := d.OpenLedgerStore(dir)
	if err != nil || st == nil {
		return watchdogPreState{}, nil, ""
	}
	convID := strings.TrimSpace(in.ConversationID)
	normTool, ringTool, argHash := normalizeWatchdogToolName(in.ToolCall.Name), watchdogRingTool(in.ToolCall), watchdogArgHash(in.ToolCall.Args)
	var role, name string
	var toolCalls int
	_ = st.SQLDB.QueryRowContext(ctx, `SELECT COALESCE(agent_role, ''), COALESCE(agent_name, ''), COALESCE(tool_calls, 0) FROM session_meta WHERE conversation_id = ?`, convID).Scan(&role, &name, &toolCalls)
	if role == "" && ctx.Err() == nil {
		role, name = guard.ResolveWatchdogRoleCtx(ctx, o.Home, convID, in.LastUserInput, d.guardWatchdogRoleDeps())
	}
	exempt := isWatchdogExempt(dir, in, normTool)
	deny, warn := evaluateWatchdogBreach(ctx, st, convID, normTool, ringTool, argHash, role, toolCalls, exempt)
	return watchdogPreState{active: true, exempt: exempt, ringTool: ringTool, argHash: argHash, role: role, agentName: name, denyReason: deny, budgetWarn: warn}, st, root
}

func evaluateWatchdogBreach(ctx context.Context, st *Store, convID, normTool, ringTool, argHash, role string, toolCalls int, exempt bool) (string, string) {
	if ctx.Err() != nil {
		return "", ""
	}
	if deny := checkWatchdogPollLoop(ctx, st, convID, normTool, ringTool); deny != "" {
		return deny, ""
	}
	if exempt {
		return "", ""
	}
	if deny := checkWatchdogErrorLoop(ctx, st, convID, ringTool, argHash); deny != "" {
		return deny, ""
	}
	return checkWatchdogBudget(role, toolCalls)
}

// IsBuilderSubagentRole reports whether role is a general builder role
// (self, builder, coder — optionally prefixed with workflow-/workflow_ and
// suffixed with -*/_*) that gets the higher 120-call watchdog budget. Unknown
// custom roles (e.g. worker-*, implementer) default to the 40-call audit cap.
func IsBuilderSubagentRole(role string) bool {
	r := strings.ToLower(strings.TrimSpace(role))
	if strings.HasPrefix(r, "workflow-") {
		r = strings.TrimPrefix(r, "workflow-")
	} else if strings.HasPrefix(r, "workflow_") {
		r = strings.TrimPrefix(r, "workflow_")
	}
	if r == "self" {
		return true
	}
	for _, p := range []string{"builder", "coder"} {
		if r == p || strings.HasPrefix(r, p+"-") || strings.HasPrefix(r, p+"_") {
			return true
		}
	}
	return false
}

// SubagentBudgetCap returns (warnAt, capAt) tool-call thresholds for a subagent
// role. Builder/self roles get (90, 120); all audit, specialized, and unknown
// subagent roles default to (25, 40).
func SubagentBudgetCap(role string) (warnAt, capAt int) {
	if IsBuilderSubagentRole(role) {
		return 90, 120
	}
	return 25, 40
}

func checkWatchdogBudget(role string, toolCalls int) (string, string) {
	r := strings.TrimSpace(role)
	if r == "" || strings.EqualFold(r, "main") {
		return "", ""
	}
	warnAt, capAt := SubagentBudgetCap(r)
	if toolCalls >= capAt {
		return fmt.Sprintf("[SUBAGENT_BUDGET] %s reached %d/%d tool calls; write final artifact or finish now", role, toolCalls, capAt), ""
	}
	if toolCalls >= warnAt {
		return "", fmt.Sprintf("[SUBAGENT_BUDGET_WARN] %s at %d/%d tool calls; finish soon", role, toolCalls, capAt)
	}
	return "", ""
}

// IsAuditSubagentRole reports whether role is a non-main, non-builder subagent
// role subject to the stricter 40-call audit budget.
func IsAuditSubagentRole(role string) bool {
	r := strings.ToLower(strings.TrimSpace(role))
	return r != "" && r != "main" && !IsBuilderSubagentRole(r)
}

func isWatchdogExempt(dir string, in HookInput, normTool string) bool {
	if normTool == "finish" || normTool == "send_message" {
		return true
	}
	if normTool == "run_command" {
		return IsWatchdogExemptBash(in.ToolCall.Args)
	}
	target := ExtractToolTargetPath(in.ToolCall.Args)
	if normTool != "write_or_edit" || target == "" {
		return false
	}
	toolDir := ResolveToolProjectDir(dir, in, target)
	base := dir
	if root, _, ok := guard.FindGitRootFast(toolDir); ok {
		base = root
	} else if root, _, ok := guard.FindGitRootFast(dir); ok {
		base = root
	}
	if base == "" || trail.CheckScratchNoSymlinks(base) != nil {
		return false
	}
	resolved, canonBase := guard.ResolveTargetWithAncestors(toolDir, target), readledger.CanonicalizePath("", base)
	rel, err := filepath.Rel(filepath.FromSlash(canonBase), filepath.FromSlash(resolved))
	if err != nil || !strings.HasPrefix(filepath.ToSlash(filepath.Clean(rel)), ".agents/scratch/agents/") {
		return false
	}
	return !guard.HasSymlinkSegmentUnder(base, toolDir, target) && !guard.HasSymlinkSegmentUnder(canonBase, canonBase, filepath.FromSlash(resolved))
}

// IsWatchdogExemptBash reports whether a bash command is an allowed git/fyr commit-detach sequence.
func IsWatchdogExemptBash(args map[string]any) bool {
	_, cmd := guard.ExtractBashCmdArg(args)
	segs := guard.SplitShellSegments(cmd)
	if cmd == "" || len(segs) == 0 || guard.HasShellExpansionOutsideSingleQuotes(cmd) {
		return false
	}
	for _, seg := range segs {
		cmdTok, rest := guard.UnwrapSegmentCommand(guard.TokenizeShellSegment(seg))
		switch {
		case cmdTok == "git":
			sub, subArgs := guard.SkipGitGlobalFlags(rest)
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
