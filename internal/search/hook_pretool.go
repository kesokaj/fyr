package search

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func handlePreToolUse(ctx context.Context, o Options, dir string, in HookInput, out io.Writer) error {
	if deny := checkStatelessPreToolSafety(ctx, o, dir, in); deny != "" {
		return writeDecision(out, "deny", deny)
	}
	wd, wdSt, wdRoot := evaluateWatchdogPreCtx(ctx, o, dir, in)
	if wdSt != nil {
		defer wdSt.close()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if wd.denyReason != "" {
		return writeDecision(out, "deny", wd.denyReason)
	}
	var buf bytes.Buffer
	if err := dispatchPreToolUseCtx(ctx, o, dir, wd, wdSt, wdRoot, in, &buf); err != nil {
		return err
	}
	return finalizeWatchdogPreCtx(ctx, wdSt, dir, in, wd, buf.Bytes(), out)
}

func checkStatelessPreToolSafety(ctx context.Context, o Options, dir string, in HookInput) string {
	switch normalizeHookToolName(in.ToolCall.Name) {
	case "run_command":
		if !isRelaySession() {
			if toolDir := bashToolProjectDir(dir, in); toolDir != "" && !sameCleanPath(dir, toolDir) && hooksDisabled(toolDir) {
				return ""
			}
		}
		denyReason, _ := checkInteractiveBashGuard(in.ToolCall.Args)
		return denyReason
	case "invoke_subagent", "invokesubagent":
		if subagent, guarded := isGuardedSubagentCall(in.ToolCall.Args); guarded {
			if model, blocked := nonInheritModelOverride(in.ToolCall.Args); blocked {
				return "[MODEL_OVERRIDE] subagent \"" + subagent + "\" forbids Model=\"" + model + "\" override; omit Model or pass \"inherit\" (rules/agy-dispatch.md)."
			}
		}
	case "swarm":
		if strings.EqualFold(stringArgCI(in.ToolCall.Args, "action"), "add") {
			role := inferSwarmRole(in.ToolCall.Args)
			rawModel := stringArgCI(in.ToolCall.Args, "model")
			if m := strings.ToLower(rawModel); m != "" && m != "auto" && role == "judge" && isFastExecutionModelForHome(o.Home, rawModel) {
				return "[MODEL_OVERRIDE] swarm review/security worker forbids weak model=\"" + rawModel + "\" override"
			}
		}
	case "ask_question", "askquestion":
		if isRelaySession() {
			return "[KICKOFF_GUARD] ask_question is blocked during autonomous Kickoff/worker turns (relay); no interactive operator is available in fyr relay sessions."
		}
		meta := hookSubagentMetaFromCtx(ctx)
		if strings.TrimSpace(meta.ParentConversationID) != "" {
			return "[KICKOFF_GUARD] ask_question is blocked during autonomous Kickoff/worker turns (subagent); subagents cannot prompt an interactive operator."
		}
		if a := strings.TrimSpace(meta.AgentName); a != "" && !strings.EqualFold(a, "main") {
			return "[KICKOFF_GUARD] ask_question is blocked during autonomous Kickoff/worker turns (subagent:" + a + "); subagents cannot prompt an interactive operator."
		}
	}
	return ""
}

func dispatchPreToolUseCtx(ctx context.Context, o Options, dir string, wd watchdogPreState, wdSt *store, wdRoot string, in HookInput, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch normalizeHookToolName(in.ToolCall.Name) {
	case "read_file":
		return handlePreToolReadCtx(ctx, o.Home, dir, wdSt, wdRoot, in, out)
	case "run_command":
		return handlePreToolBashCtx(ctx, o.Home, dir, wdSt, wdRoot, in, out)
	case "write_or_edit":
		return handlePreToolWriteCtx(ctx, o, dir, in, out)
	case "invoke_subagent", "invokesubagent":
		return handlePreToolSubagent(o, dir, in, out)
	case "swarm":
		return handlePreToolSwarm(o, dir, in, out)
	case "run_workflow":
		return handlePreToolWorkflow(o.Home, dir, in, out)
	case "grep_search", "grepsearch", "grep":
		return handlePreToolGrepNudgeCtx(ctx, o.Home, dir, wdSt, wdRoot, in, out)
	case "ask_question", "askquestion":
		return handlePreToolAskQuestionCtx(ctx, o, dir, wd, wdSt, wdRoot, in, out)
	default:
		return writeDecision(out, "allow", "")
	}
}

func handlePreToolReadCtx(ctx context.Context, home, dir string, wdSt *store, wdRoot string, in HookInput, out io.Writer) error {
	args := in.ToolCall.Args
	target := extractToolTargetPath(args)
	toolDir := resolveToolProjectDir(dir, in, target)
	if target == "" || hooksDisabledForHome(home, toolDir) {
		return writeDecision(out, "allow", "")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	resolved := CanonicalizePath(toolDir, target)
	candRoot := resolveReadGuardRoot(dir, toolDir, wdRoot)
	if !shouldGuardReadInRoot(candRoot, resolved) {
		return writeDecision(out, "allow", "")
	}
	snap := loadReadFileSnapshot(resolved)
	ledgerSt, ledgerRoot, ownSt := resolveReusableReadLedgerStore(dir, home, toolDir, wdSt, wdRoot)
	if ownSt && ledgerSt != nil {
		defer ledgerSt.close()
	}
	if !shouldGuardReadInRoot(ledgerRoot, resolved) {
		return writeDecision(out, "allow", "")
	}
	if clampDec, clamped := checkUnrangedClampWithStore(ctx, ledgerSt, ledgerRoot, in.ConversationID, snap, args); clamped {
		return writeDecision(out, "deny", clampDec.Reason)
	}
	start, end, record := clampReadRangeWithSnapshot(args, snap)
	if !record {
		return writeDecision(out, "allow", "")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if ledgerSt != nil {
		if dec, err := ledgerSt.checkAndRecordResolvedRead(ledgerRoot, normalizeLedgerConvID(in.ConversationID), resolved, start, end, 0); err == nil && !dec.Allowed {
			return writeDecision(out, "deny", dec.Reason)
		}
	}
	return writeDecision(out, "allow", "")
}

func resolveReadGuardRoot(sessionDir, toolDir, wdRoot string) string {
	if wdRoot != "" {
		return wdRoot
	}
	if root, _, ok := findGitRootFast(sessionDir); ok {
		return root
	}
	if toolRoot, _, ok := findGitRootFast(toolDir); ok && hasIndexedOrAgentsDir(toolRoot) {
		return toolRoot
	}
	return ""
}

func shouldGuardReadInRoot(ledgerRoot, resolved string) bool {
	if ledgerRoot == "" || resolved == "" || !hasIndexedOrAgentsDir(ledgerRoot) {
		return false
	}
	canonRoot := CanonicalizePath("", ledgerRoot)
	if canonRoot == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.FromSlash(canonRoot), filepath.FromSlash(resolved))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}

func hasIndexedOrAgentsDir(root string) bool {
	if isRegularFileFast(projectDB(root)) {
		return true
	}
	fi, err := os.Lstat(filepath.Join(root, ".agents"))
	return err == nil && fi.Mode()&os.ModeSymlink == 0 && fi.IsDir()
}

func resolveReusableReadLedgerStore(sessionDir, home, toolDir string, wdSt *store, wdRoot string) (*store, string, bool) {
	ledgerDir := resolveSessionLedgerDir(sessionDir, toolDir)
	if wdSt != nil && wdRoot != "" {
		if sameAutoInitPath(ledgerDir, wdRoot) {
			return wdSt, wdRoot, false
		}
		if root, _, ok := findGitRootFast(ledgerDir); ok && sameAutoInitPath(root, wdRoot) {
			return wdSt, wdRoot, false
		}
	}
	if st, root, err := openSessionLedgerStore(ledgerDir, home); err == nil {
		return st, root, true
	}
	return nil, "", false
}

func handlePreToolBash(home, dir string, in HookInput, out io.Writer) error {
	return handlePreToolBashCtx(context.Background(), home, dir, nil, "", in, out)
}

func handlePreToolBashCtx(ctx context.Context, home, dir string, wdSt *store, wdRoot string, in HookInput, out io.Writer) error {
	if !isRelaySession() && (hooksDisabledGlobal(home) || bashDirHooksDisabled(dir, in)) {
		return writeDecision(out, "allow", "")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	denyReason, overwrite := checkInteractiveBashGuard(in.ToolCall.Args)
	if denyReason != "" {
		return writeDecision(out, "deny", denyReason)
	}
	if hooksDisabledGlobal(home) || bashDirHooksDisabled(dir, in) {
		return writeBashAllowDecision(out, "", overwrite)
	}
	cmd := extractStringArg(in.ToolCall.Args, "CommandLine", "commandLine", "command_line", "Command", "command", "cmd")
	if reads := ParseBashFileReads(cmd); len(reads) > 0 {
		if deny, err := enforceBashFileReads(ctx, home, dir, wdSt, wdRoot, in, reads); err != nil {
			return err
		} else if deny != "" {
			return writeDecision(out, "deny", deny)
		}
	}
	nudge := checkBashGrepNudgeCtx(ctx, home, dir, wdSt, wdRoot, in, cmd)
	return writeBashAllowDecision(out, nudge, overwrite)
}

func bashDirHooksDisabled(dir string, in HookInput) bool {
	if hooksDisabled(dir) {
		return true
	}
	toolDir := bashToolProjectDir(dir, in)
	return toolDir != "" && !sameCleanPath(dir, toolDir) && hooksDisabled(toolDir)
}

func writeBashAllowDecision(out io.Writer, nudge string, overwrite map[string]any) error {
	if overwrite != nil {
		reason := "[AUTO_GIT_TERMINAL_PROMPT] prepended export GIT_TERMINAL_PROMPT=0"
		if nudge != "" {
			reason += "; " + nudge
		}
		writeDecisionWithOverwrite(out, "allow", reason, overwrite)
		return nil
	}
	return writeDecision(out, "allow", nudge)
}

func enforceBashFileReads(ctx context.Context, home, dir string, wdSt *store, wdRoot string, in HookInput, reads []BashFileRead) (string, error) {
	cache := bashLedgerCache{byRoot: make(map[string]bashLedgerHandle, 1)}
	defer cache.close()
	items := resolveBashReadTargets(ctx, home, dir, wdSt, wdRoot, in, reads, &cache)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	convID := normalizeLedgerConvID(in.ConversationID)
	for _, it := range items {
		if it.unranged {
			if dec, clamped := checkUnrangedClampWithStore(ctx, it.st, it.root, in.ConversationID, it.snap, nil); clamped {
				return dec.Reason, nil
			}
		}
	}
	for _, it := range items {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		reqStart, reqEnd := normalizeLineRange(it.read.StartLine, it.read.EndLine, it.snap.totalLines)
		if it.st.hasUnchangedReadHit(ctx, convID, it.snap.resolved, reqStart, reqEnd, it.snap.sumHex) {
			if dec, err := it.st.checkAndRecordResolvedRead(it.root, convID, it.snap.resolved, it.read.StartLine, it.read.EndLine, 0); err == nil && !dec.Allowed {
				return dec.Reason, nil
			}
		}
	}
	for _, it := range items {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		reqStart, reqEnd := normalizeLineRange(it.read.StartLine, it.read.EndLine, it.snap.totalLines)
		if !it.st.hasUnchangedReadHit(ctx, convID, it.snap.resolved, reqStart, reqEnd, it.snap.sumHex) {
			_, _ = it.st.checkAndRecordResolvedRead(it.root, convID, it.snap.resolved, it.read.StartLine, it.read.EndLine, 0)
		}
	}
	return "", nil
}

func (s *store) hasUnchangedReadHit(ctx context.Context, convID, resolved string, reqStart, reqEnd int, sumHex string) bool {
	genID, _ := s.currentGeneration(convID)
	var hit int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND sha256 = ? AND start_line > 0 AND start_line <= ? AND end_line >= ? LIMIT 1`,
		convID, genID, resolved, sumHex, reqStart, reqEnd,
	).Scan(&hit)
	return err == nil && hit == 1
}

func handlePreToolWriteCtx(ctx context.Context, o Options, dir string, in HookInput, out io.Writer) error {
	target := extractToolTargetPath(in.ToolCall.Args)
	toolDir := resolveToolProjectDir(dir, in, target)
	if target == "" || hooksDisabledForHome(o.Home, toolDir) {
		return writeDecision(out, "allow", "")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if reason := checkCoordinatorWriteGuard(o, dir, toolDir, in, target); reason != "" {
		return writeDecision(out, "deny", reason)
	}
	dec, err := CheckPreWriteHashWithEdit(dir, o.Home, toolDir, in.ConversationID, target, in.ToolCall.Args)
	if err == nil && !dec.Allowed {
		return writeDecision(out, "deny", dec.Reason)
	}
	if err == nil && toolDir == dir && ctx.Err() == nil {
		if mErr := markPreWriteInLedger(dir, in.ConversationID, dec.ResolvedPath); mErr != nil && !errors.Is(mErr, errNotCheckout) {
			return writeDecision(out, "allow", "fyr brain: pre-write ledger unavailable ("+oneLineErr(mErr)+")")
		}
	}
	return writeDecision(out, "allow", "")
}

func handleStopHook(ctx context.Context, o Options, dir string, in HookInput, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	writeHookProcessArtifact(o.Home, nil, dir, in, "Verify", nil)
	if err := ctx.Err(); err != nil {
		return err
	}
	coErr := RecordStopCoActivity(dir, in.ConversationID)
	if shouldContinue, reason := evaluateStopStamp(o.Home, dir, in); shouldContinue {
		return writeDecision(out, "continue", reason)
	}
	if coErr != nil && !errors.Is(coErr, errNotCheckout) {
		return writeDecision(out, "allow", "fyr brain: co-activity store unavailable ("+oneLineErr(coErr)+")")
	}
	return writeDecision(out, "allow", "")
}
