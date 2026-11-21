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
	if err := dispatchPreToolUseCtx(ctx, o, dir, wdSt, wdRoot, in, &buf); err != nil {
		return err
	}
	return finalizeWatchdogPreCtx(ctx, wdSt, dir, in, wd, buf.Bytes(), out)
}

func dispatchPreToolUseCtx(ctx context.Context, o Options, dir string, wdSt *store, wdRoot string, in HookInput, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch normalizeHookToolName(in.ToolCall.Name) {
	case "read_file":
		return handlePreToolReadCtx(ctx, o.Home, dir, wdSt, wdRoot, in, out)
	case "run_command":
		return handlePreToolBashCtx(ctx, o.Home, dir, in, out)
	case "write_or_edit":
		return handlePreToolWriteCtx(ctx, o, dir, in, out)
	case "invoke_subagent", "invokesubagent":
		return handlePreToolSubagent(o, dir, in, out)
	case "swarm":
		return handlePreToolSwarm(o, dir, in, out)
	case "run_workflow":
		return handlePreToolWorkflow(o.Home, dir, in, out)
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
		dec, err := ledgerSt.checkAndRecordResolvedRead(ledgerRoot, normalizeLedgerConvID(in.ConversationID), resolved, start, end, 0)
		if err == nil && !dec.Allowed {
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
	st, root, err := openSessionLedgerStore(ledgerDir, home)
	if err != nil {
		return nil, "", false
	}
	return st, root, true
}

func handlePreToolBash(home, dir string, in HookInput, out io.Writer) error {
	return handlePreToolBashCtx(context.Background(), home, dir, in, out)
}

func handlePreToolBashCtx(ctx context.Context, home, dir string, in HookInput, out io.Writer) error {
	cmd := extractStringArg(in.ToolCall.Args, "CommandLine", "commandLine", "command_line", "Command", "command", "cmd")
	bfr, ok := ParseBashFileRead(cmd)
	toolDir := resolveToolProjectDir(dir, in, bfr.Path)
	if hooksDisabledForHome(home, toolDir) {
		return writeDecision(out, "allow", "")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	denyReason, overwrite := checkInteractiveBashGuard(in.ToolCall.Args)
	if denyReason != "" {
		return writeDecision(out, "deny", denyReason)
	}
	if ok {
		dec, err := CheckAndRecordReadInSession(dir, home, toolDir, in.ConversationID, bfr.Path, bfr.StartLine, bfr.EndLine, 0)
		if err == nil && !dec.Allowed {
			return writeDecision(out, "deny", dec.Reason)
		}
	}
	if overwrite != nil {
		writeDecisionWithOverwrite(out, "allow", "[AUTO_GIT_TERMINAL_PROMPT] prepended export GIT_TERMINAL_PROMPT=0", overwrite)
		return nil
	}
	return writeDecision(out, "allow", "")
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
