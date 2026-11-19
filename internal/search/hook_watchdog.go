package search

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

type watchdogPreState struct {
	active                             bool
	ringTool, argHash, role, agentName string
	denyReason, budgetWarn             string
}

type watchdogRingRow struct {
	tool, argHash string
	isError       int
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

func evaluateWatchdogPre(o Options, dir string, in HookInput) watchdogPreState {
	if watchdogShouldSkip(o.Home, dir, in) {
		return watchdogPreState{}
	}
	st, _, err := openLedgerStore(dir)
	if err != nil || st == nil {
		return watchdogPreState{}
	}
	defer st.close()
	convID := strings.TrimSpace(in.ConversationID)
	normTool, ringTool, argHash := normalizeWatchdogToolName(in.ToolCall.Name), watchdogRingTool(in.ToolCall), watchdogArgHash(in.ToolCall.Args)
	var role, name string
	var toolCalls int
	_ = st.db.QueryRow(`SELECT COALESCE(agent_role, ''), COALESCE(agent_name, ''), COALESCE(tool_calls, 0) FROM session_meta WHERE conversation_id = ?`, convID).Scan(&role, &name, &toolCalls)
	if role == "" {
		role, name = resolveWatchdogRole(o.Home, convID, in.LastUserInput)
	}
	deny, warn := "", ""
	if deny = checkWatchdogPollLoop(st, convID, normTool, ringTool); deny == "" && !isWatchdogExempt(dir, in, normTool) {
		if deny = checkWatchdogErrorLoop(st, convID, ringTool, argHash); deny == "" {
			deny, warn = checkWatchdogBudget(role, toolCalls)
		}
	}
	return watchdogPreState{active: true, ringTool: ringTool, argHash: argHash, role: role, agentName: name, denyReason: deny, budgetWarn: warn}
}

func checkWatchdogPollLoop(st *store, convID, normTool, ringTool string) string {
	if (normTool != "manage_task" && normTool != "manage_subagents") || ringTool != normTool {
		return ""
	}
	var cnt, allPoll int
	q := `SELECT COUNT(*), COALESCE(MIN(CASE WHEN tool IN ('manage_task','manage_subagents') THEN 1 ELSE 0 END), 0) FROM (SELECT tool FROM watchdog_ring WHERE conversation_id = ? ORDER BY seq DESC LIMIT 2)`
	if st.db.QueryRow(q, convID).Scan(&cnt, &allPoll) == nil && cnt == 2 && allPoll == 1 {
		return "[POLL_LOOP] >= 3 consecutive status/list polls; end turn to await wakeup or run fyr watch --check."
	}
	return ""
}

func checkWatchdogErrorLoop(st *store, convID, ringTool, argHash string) string {
	rows, err := st.db.Query(`SELECT tool, arg_hash, is_error FROM watchdog_ring WHERE conversation_id = ? ORDER BY seq DESC LIMIT 4`, convID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var ring []watchdogRingRow
	for rows.Next() {
		var r watchdogRingRow
		if rows.Scan(&r.tool, &r.argHash, &r.isError) == nil {
			ring = append(ring, r)
		}
	}
	if len(ring) >= 3 && ring[0].isError == 1 && ring[1].isError == 1 && ring[2].isError == 1 &&
		ring[0].tool == ringTool && ring[1].tool == ringTool && ring[2].tool == ringTool &&
		ring[0].argHash == argHash && ring[1].argHash == argHash && ring[2].argHash == argHash {
		return "[TOKEN_LOOP] >= 3 consecutive identical failing tool calls"
	}
	key := func(r watchdogRingRow) string { return r.tool + ":" + r.argHash }
	incoming := ringTool + ":" + argHash
	if len(ring) >= 4 && ring[0].isError == 1 && ring[1].isError == 1 && ring[2].isError == 1 && ring[3].isError == 1 &&
		key(ring[0]) == key(ring[2]) && key(ring[1]) == key(ring[3]) && key(ring[0]) != key(ring[1]) &&
		incoming == key(ring[1]) {
		return "[TOKEN_LOOP] repeating A-B-A-B error loop"
	}
	return ""
}

func checkWatchdogBudget(role string, toolCalls int) (string, string) {
	if role == "" || role == "main" {
		return "", ""
	}
	warnAt, capAt := 25, 40
	if strings.EqualFold(role, "self") {
		warnAt, capAt = 90, 120
	}
	if toolCalls >= capAt {
		return fmt.Sprintf("[SUBAGENT_BUDGET] %s reached %d/%d tool calls; write final artifact or finish now", role, toolCalls, capAt), ""
	}
	if toolCalls >= warnAt {
		return "", fmt.Sprintf("[SUBAGENT_BUDGET_WARN] %s at %d/%d tool calls; finish soon", role, toolCalls, capAt)
	}
	return "", ""
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
	if cmd == "" || len(segs) == 0 {
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

var watchdogPromptRoles = [][2]string{
	{"you are a senior code reviewer", "reviewer"}, {"you are the security reviewer", "security"},
	{"you run runtime e2e", "verifier"}, {"you are a verifier", "verifier"}, {"you are the verifier", "verifier"},
	{"you are the recon unit", "explorer"}, {"you are an explorer", "explorer"}, {"you are the explorer", "explorer"},
	{"you read what the project has learned", "dreamer"}, {"you are a dreamer", "dreamer"}, {"you are the dreamer", "dreamer"},
	{"you maintain explanatory documentation", "documentation"}, {"you are the documentation", "documentation"}, {"you are a documentation", "documentation"},
}

func resolveWatchdogRole(home, convID, lastInput string) (string, string) {
	low := strings.ToLower(lastInput)
	for _, p := range watchdogPromptRoles {
		if strings.Contains(low, p[0]) {
			return p[1], p[1]
		}
	}
	h := strings.TrimSpace(home)
	agyDir := filepath.Join(h, ".gemini", "antigravity-cli")
	if _, err := os.Stat(filepath.Join(agyDir, "conversation_summaries.db")); h == "" || err != nil {
		return "main", ""
	}
	conf := filepath.Join(h, harness.StateDir)
	_, stErr := os.Stat(conf)
	created := errors.Is(stErr, os.ErrNotExist)
	if os.MkdirAll(conf, 0o700) != nil {
		return "main", ""
	}
	tmp, err := os.MkdirTemp(conf, "watchdog-agy-")
	if err != nil {
		if created {
			_ = os.Remove(conf)
		}
		return "main", ""
	}
	defer func() {
		_ = os.RemoveAll(tmp)
		if created {
			_ = os.Remove(conf)
		}
	}()
	return queryWatchdogSummaryRole(agyDir, tmp, convID)
}

func queryWatchdogSummaryRole(agyDir, tmp, convID string) (string, string) {
	db, err := openCopy(agyDir, "conversation_summaries.db", tmp)
	if err != nil {
		return "main", ""
	}
	defer db.Close()
	var agentName, parentID string
	err = db.QueryRow(`SELECT COALESCE(agent_name, ''), COALESCE(parent_conversation_id, '') FROM conversation_summaries WHERE conversation_id = ? LIMIT 1`, convID).Scan(&agentName, &parentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "main", ""
	}
	if a := strings.TrimSpace(agentName); a != "" {
		return a, a
	}
	if strings.TrimSpace(parentID) != "" {
		return "self", "self"
	}
	return "main", ""
}

func finalizeWatchdogPre(dir string, in HookInput, wd watchdogPreState, raw []byte, out io.Writer) error {
	var dec HookDecisionOutput
	if json.Unmarshal(bytes.TrimSpace(raw), &dec) != nil || dec.Decision != "allow" {
		_, _ = out.Write(raw)
		return nil
	}
	if wd.active {
		recordWatchdogAllow(dir, in, wd)
		if wd.budgetWarn != "" && strings.TrimSpace(dec.Reason) == "" {
			return writeDecisionWithOverwrite(out, "allow", wd.budgetWarn, dec.Overwrite)
		}
	}
	_, _ = out.Write(raw)
	return nil
}

func recordWatchdogAllow(dir string, in HookInput, wd watchdogPreState) {
	st, _, err := openLedgerStore(dir)
	if err != nil || st == nil {
		return
	}
	defer st.close()
	convID := strings.TrimSpace(in.ConversationID)
	_, _ = st.db.Exec(
		`INSERT INTO session_meta(conversation_id, generation_id, last_transcript_bytes, turn_count, est_tokens, agent_role, agent_name, tool_calls) VALUES (?, 0, 0, 0, 0, ?, ?, 1) ON CONFLICT(conversation_id) DO UPDATE SET agent_role = CASE WHEN session_meta.agent_role = '' THEN excluded.agent_role ELSE session_meta.agent_role END, agent_name = CASE WHEN session_meta.agent_role = '' THEN excluded.agent_name ELSE session_meta.agent_name END, tool_calls = session_meta.tool_calls + 1`,
		convID, wd.role, wd.agentName,
	)
	_, _ = st.db.Exec(`INSERT INTO watchdog_ring(conversation_id, tool_call_id, tool, arg_hash, is_error, ts) VALUES (?, ?, ?, ?, 0, ?)`, convID, strings.TrimSpace(in.ToolCall.ID), wd.ringTool, wd.argHash, nowFunc().Unix())
	_, _ = st.db.Exec(`DELETE FROM watchdog_ring WHERE conversation_id = ? AND seq NOT IN (SELECT seq FROM watchdog_ring WHERE conversation_id = ? ORDER BY seq DESC LIMIT 64)`, convID, convID)
}

func handlePostToolUse(o Options, dir string, in HookInput, out io.Writer) error {
	if strings.TrimSpace(in.Error) != "" && !watchdogShouldSkip(o.Home, dir, in) {
		markWatchdogPostError(dir, in)
	}
	_, _ = out.Write([]byte("{}\n"))
	return nil
}

func markWatchdogPostError(dir string, in HookInput) {
	st, _, err := openLedgerStore(dir)
	if err != nil || st == nil {
		return
	}
	defer st.close()
	convID := strings.TrimSpace(in.ConversationID)
	if callID := strings.TrimSpace(in.ToolCall.ID); callID != "" && execRowsAffected(st, `UPDATE watchdog_ring SET is_error = 1 WHERE conversation_id = ? AND tool_call_id = ?`, convID, callID) > 0 {
		return
	}
	ringTool := watchdogRingTool(in.ToolCall)
	if execRowsAffected(st, `UPDATE watchdog_ring SET is_error = 1 WHERE seq = (SELECT seq FROM watchdog_ring WHERE conversation_id = ? AND tool = ? ORDER BY seq DESC LIMIT 1)`, convID, ringTool) > 0 {
		return
	}
	if norm := normalizeWatchdogToolName(in.ToolCall.Name); norm != "" {
		_, _ = st.db.Exec(`UPDATE watchdog_ring SET is_error = 1 WHERE seq = (SELECT seq FROM watchdog_ring WHERE conversation_id = ? AND (tool = ? OR tool LIKE ?) ORDER BY seq DESC LIMIT 1)`, convID, norm, norm+":%")
	}
}

func execRowsAffected(st *store, query string, args ...any) int64 {
	res, err := st.db.Exec(query, args...)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}
