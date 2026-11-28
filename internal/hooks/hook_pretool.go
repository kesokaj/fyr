package hooks

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/hooks/guard"
	"github.com/kesokaj/fyr/internal/hooks/telemetry"
	"github.com/kesokaj/fyr/internal/readledger"
)

func handlePreToolUse(ctx context.Context, d Deps, o Options, dir string, in HookInput, out io.Writer) error {
	if deny := checkStatelessPreToolSafety(ctx, o, dir, in); deny != "" {
		return writeDecision(out, "deny", deny)
	}
	wd, wdSt, wdRoot := evaluateWatchdogPreCtx(ctx, d, o, dir, in)
	if wdSt != nil {
		defer wdSt.Close()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if wd.denyReason != "" {
		return writeDecision(out, "deny", wd.denyReason)
	}
	var buf bytes.Buffer
	if err := dispatchPreToolUseCtx(ctx, d, o, dir, wd, wdSt, wdRoot, in, &buf); err != nil {
		return err
	}
	return finalizeWatchdogPreCtx(ctx, d, wdSt, dir, in, wd, buf.Bytes(), out)
}

func checkStatelessPreToolSafety(ctx context.Context, o Options, dir string, in HookInput) string {
	switch NormalizeHookToolName(in.ToolCall.Name) {
	case "run_command":
		return checkStatelessBashSafety(dir, in)
	case "invoke_subagent", "invokesubagent":
		return checkStatelessSubagentSafety(in.ToolCall.Args)
	case "swarm":
		return checkStatelessSwarmSafety(o.Home, in.ToolCall.Args)
	case "ask_question", "askquestion":
		return checkStatelessAskQuestionSafety(ctx)
	}
	return ""
}

func checkStatelessBashSafety(dir string, in HookInput) string {
	if !isRelaySession() {
		if toolDir := bashToolProjectDir(dir, in); toolDir != "" && !sameCleanPath(dir, toolDir) && HooksDisabled(toolDir) {
			return ""
		}
	}
	denyReason, _ := guard.CheckInteractiveBashGuard(in.ToolCall.Args)
	return denyReason
}

func checkStatelessSubagentSafety(args map[string]any) string {
	subagent, guarded := IsGuardedSubagentCall(args)
	if !guarded {
		return ""
	}
	if model, blocked := nonInheritModelOverride(args); blocked {
		return "[MODEL_OVERRIDE] subagent \"" + subagent + "\" forbids Model=\"" + model + "\" override; omit Model or pass \"inherit\" (rules/agy-dispatch.md)."
	}
	return ""
}

func checkStatelessSwarmSafety(home string, args map[string]any) string {
	if !strings.EqualFold(stringArgCI(args, "action"), "add") {
		return ""
	}
	role := InferSwarmRole(args)
	rawModel := stringArgCI(args, "model")
	m := strings.ToLower(rawModel)
	if m != "" && m != "auto" && role == "judge" && isFastExecutionModelForHome(home, rawModel) {
		return "[MODEL_OVERRIDE] swarm review/security worker forbids weak model=\"" + rawModel + "\" override"
	}
	return ""
}

func checkStatelessAskQuestionSafety(ctx context.Context) string {
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
	return ""
}

func dispatchPreToolUseCtx(ctx context.Context, d Deps, o Options, dir string, wd watchdogPreState, wdSt *Store, wdRoot string, in HookInput, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch NormalizeHookToolName(in.ToolCall.Name) {
	case "read_file":
		return handlePreToolReadCtx(ctx, d, o.Home, dir, wdSt, wdRoot, in, out)
	case "run_command":
		return HandlePreToolBashCtx(ctx, d, o.Home, dir, wdSt, wdRoot, in, out)
	case "write_or_edit":
		return handlePreToolWriteCtx(ctx, d, o, dir, in, out)
	case "invoke_subagent", "invokesubagent":
		return handlePreToolSubagent(d, o, dir, in, out)
	case "swarm":
		return handlePreToolSwarm(d, o, dir, in, out)
	case "run_workflow":
		return handlePreToolWorkflowGuard(d, o.Home, dir, in, out)
	case "grep_search", "grepsearch", "grep":
		ni := guard.NudgeInput{Home: o.Home, Dir: dir, ConversationID: in.ConversationID, ToolName: in.ToolCall.Name, ToolArgs: in.ToolCall.Args}
		return guard.HandlePreToolGrepNudgeCtx(ctx, ni, wdSt.DB(), wdRoot, d.guardNudgeDeps(), out)
	case "ask_question", "askquestion":
		return handlePreToolAskGuard(ctx, d, o, dir, wd, wdSt, wdRoot, in, out)
	default:
		return writeDecision(out, "allow", "")
	}
}

func handlePreToolAskGuard(ctx context.Context, d Deps, o Options, dir string, wd watchdogPreState, wdSt *Store, wdRoot string, in HookInput, out io.Writer) error {
	hb := HookBypassedForEvent
	deps := guard.AskDeps{
		HookBypassed:  hb,
		OpenLedgerDB:  d.openGuardLedgerDB,
		WriteDecision: writeDecision,
	}
	return guard.HandlePreToolAskQuestionCtx(ctx, guardAskInput(ctx, o, dir, wd, in), wdSt.DB(), wdRoot, deps, out)
}

func handlePreToolWorkflowGuard(d Deps, home, dir string, in HookInput, out io.Writer) error {
	deps := guard.WorkflowDeps{
		EffectiveSessionModel: func() string {
			return telemetry.EffectiveHookSessionModel(home, toTelemetryConvInput(in), d.telemetryConvDeps(nil))
		},
		NeedsGuardedProUpgrade: NeedsGuardedProUpgradeForHome,
		IsModelGuardedSubagent: IsModelGuardedSubagent,
		FormatModelTag:         FormatModelTag,
		SanitizePrintable:      d.printable,
		WriteDecision:          writeDecision,
		WriteDecisionOverwrite: writeDecisionWithOverwrite,
	}
	return guard.HandlePreToolWorkflow(guard.WorkflowInput{Home: home, Dir: dir, ToolArgs: in.ToolCall.Args}, deps, out)
}

func handlePreToolReadCtx(ctx context.Context, d Deps, home, dir string, wdSt *Store, wdRoot string, in HookInput, out io.Writer) error {
	args := in.ToolCall.Args
	target := ExtractToolTargetPath(args)
	toolDir := ResolveToolProjectDir(dir, in, target)
	if target == "" || HooksDisabledForHome(home, toolDir) {
		return writeDecision(out, "allow", "")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	resolved := readledger.CanonicalizePath(toolDir, target)
	candRoot := resolveReadGuardRoot(d, dir, toolDir, wdRoot)
	if !shouldGuardReadInRoot(d, candRoot, resolved) {
		return writeDecision(out, "allow", "")
	}
	snap := loadReadFileSnapshot(resolved)
	ledgerSt, ledgerRoot, ownSt := resolveReusableReadLedgerStore(d, dir, home, toolDir, wdSt, wdRoot)
	if ownSt && ledgerSt != nil {
		defer ledgerSt.Close()
	}
	if !shouldGuardReadInRoot(d, ledgerRoot, resolved) {
		return writeDecision(out, "allow", "")
	}
	if clampDec, clamped := checkUnrangedClampWithStore(ctx, d, ledgerSt, ledgerRoot, in.ConversationID, snap, args); clamped {
		return writeDecision(out, "deny", clampDec.Reason)
	}
	start, end, record := clampReadRangeWithSnapshot(args, snap)
	if !record {
		return writeDecision(out, "allow", "")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if ledgerSt != nil && d.CheckAndRecordResolvedRead != nil {
		if dec, err := d.CheckAndRecordResolvedRead(ledgerSt, ledgerRoot, readledger.NormalizeConvID(in.ConversationID), resolved, start, end, 0); err == nil && !dec.Allowed {
			return writeDecision(out, "deny", dec.Reason)
		}
	}
	return writeDecision(out, "allow", "")
}

func resolveReadGuardRoot(d Deps, sessionDir, toolDir, wdRoot string) string {
	if wdRoot != "" {
		return wdRoot
	}
	if root, _, ok := guard.FindGitRootFast(sessionDir); ok {
		return root
	}
	if toolRoot, _, ok := guard.FindGitRootFast(toolDir); ok && hasIndexedOrAgentsDir(d, toolRoot) {
		return toolRoot
	}
	return ""
}

func shouldGuardReadInRoot(d Deps, ledgerRoot, resolved string) bool {
	if ledgerRoot == "" || resolved == "" || !hasIndexedOrAgentsDir(d, ledgerRoot) {
		return false
	}
	canonRoot := readledger.CanonicalizePath("", ledgerRoot)
	if canonRoot == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.FromSlash(canonRoot), filepath.FromSlash(resolved))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}

func hasIndexedOrAgentsDir(d Deps, root string) bool {
	if d.ProjectDB != nil && guard.IsRegularFileFast(d.ProjectDB(root)) {
		return true
	}
	fi, err := os.Lstat(filepath.Join(root, ".agents"))
	return err == nil && fi.Mode()&os.ModeSymlink == 0 && fi.IsDir()
}

func resolveReusableReadLedgerStore(d Deps, sessionDir, home, toolDir string, wdSt *Store, wdRoot string) (*Store, string, bool) {
	ledgerDir := d.ResolveSessionLedgerDir(sessionDir, toolDir)
	if wdSt != nil && wdRoot != "" {
		if sameAutoInitPath(ledgerDir, wdRoot) {
			return wdSt, wdRoot, false
		}
		if root, _, ok := guard.FindGitRootFast(ledgerDir); ok && sameAutoInitPath(root, wdRoot) {
			return wdSt, wdRoot, false
		}
	}
	if d.OpenSessionLedgerStore != nil {
		if st, root, err := d.OpenSessionLedgerStore(ledgerDir, home); err == nil {
			return st, root, true
		}
	}
	return nil, "", false
}

// HandlePreToolBashCtx handles PreToolUse enforcement for run_command / bash calls.
func HandlePreToolBashCtx(ctx context.Context, d Deps, home, dir string, wdSt *Store, wdRoot string, in HookInput, out io.Writer) error {
	if !isRelaySession() && (HooksDisabledGlobal(home) || bashDirHooksDisabled(dir, in)) {
		return writeDecision(out, "allow", "")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	denyReason, overwrite := guard.CheckInteractiveBashGuard(in.ToolCall.Args)
	if denyReason != "" {
		return writeDecision(out, "deny", denyReason)
	}
	if HooksDisabledGlobal(home) || bashDirHooksDisabled(dir, in) {
		return writeBashAllowDecision(out, "", overwrite)
	}
	cmd := extractStringArg(in.ToolCall.Args, "CommandLine", "commandLine", "command_line", "Command", "command", "cmd")
	if reads := readledger.ParseBashFileReads(cmd); len(reads) > 0 {
		if deny, err := enforceBashFileReads(ctx, d, home, dir, wdSt, wdRoot, in, reads); err != nil {
			return err
		} else if deny != "" {
			return writeDecision(out, "deny", deny)
		}
	}
	ni := guard.NudgeInput{Home: home, Dir: dir, ConversationID: in.ConversationID, ToolName: in.ToolCall.Name, ToolArgs: in.ToolCall.Args}
	nudge := guard.CheckBashGrepNudgeCtx(ctx, ni, wdSt.DB(), wdRoot, d.guardNudgeDeps(), cmd)
	return writeBashAllowDecision(out, nudge, overwrite)
}

func bashDirHooksDisabled(dir string, in HookInput) bool {
	if HooksDisabled(dir) {
		return true
	}
	toolDir := bashToolProjectDir(dir, in)
	return toolDir != "" && !sameCleanPath(dir, toolDir) && HooksDisabled(toolDir)
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

func enforceBashFileReads(ctx context.Context, d Deps, home, dir string, wdSt *Store, wdRoot string, in HookInput, reads []readledger.BashFileRead) (string, error) {
	cache := bashLedgerCache{byRoot: make(map[string]bashLedgerHandle, 1)}
	defer cache.close()
	items := resolveBashReadTargets(ctx, d, home, dir, wdSt, wdRoot, in, reads, &cache)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if deny := checkBashUnrangedClamps(ctx, d, in.ConversationID, items); deny != "" {
		return deny, nil
	}
	convID := readledger.NormalizeConvID(in.ConversationID)
	if deny, err := checkBashUnchangedHits(ctx, d, convID, items); err != nil || deny != "" {
		return deny, err
	}
	return "", recordBashFreshReads(ctx, d, convID, items)
}

func checkBashUnrangedClamps(ctx context.Context, d Deps, rawConvID string, items []resolvedBashRead) string {
	for _, it := range items {
		if !it.unranged {
			continue
		}
		if dec, clamped := checkUnrangedClampWithStore(ctx, d, it.st, it.root, rawConvID, it.snap, nil); clamped {
			return dec.Reason
		}
	}
	return ""
}

func checkBashUnchangedHits(ctx context.Context, d Deps, convID string, items []resolvedBashRead) (string, error) {
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		reqStart, reqEnd := readledger.NormalizeLineRange(it.read.StartLine, it.read.EndLine, it.snap.totalLines)
		if !hasUnchangedReadHit(ctx, it.st.SQLDB, convID, it.snap.resolved, reqStart, reqEnd, it.snap.sumHex) {
			continue
		}
		if dec, err := d.CheckAndRecordResolvedRead(it.st, it.root, convID, it.snap.resolved, it.read.StartLine, it.read.EndLine, 0); err == nil && !dec.Allowed {
			return dec.Reason, nil
		}
	}
	return "", nil
}

func recordBashFreshReads(ctx context.Context, d Deps, convID string, items []resolvedBashRead) error {
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		reqStart, reqEnd := readledger.NormalizeLineRange(it.read.StartLine, it.read.EndLine, it.snap.totalLines)
		if !hasUnchangedReadHit(ctx, it.st.SQLDB, convID, it.snap.resolved, reqStart, reqEnd, it.snap.sumHex) {
			_, _ = d.CheckAndRecordResolvedRead(it.st, it.root, convID, it.snap.resolved, it.read.StartLine, it.read.EndLine, 0)
		}
	}
	return nil
}

func hasUnchangedReadHit(ctx context.Context, db *sql.DB, convID, resolved string, reqStart, reqEnd int, sumHex string) bool {
	if db == nil {
		return false
	}
	genID, _ := readledger.CurrentGeneration(db, convID)
	var hit int
	err := db.QueryRowContext(ctx,
		`SELECT 1 FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND sha256 = ? AND start_line > 0 AND start_line <= ? AND end_line >= ? LIMIT 1`,
		convID, genID, resolved, sumHex, reqStart, reqEnd,
	).Scan(&hit)
	return err == nil && hit == 1
}

func handlePreToolWriteCtx(ctx context.Context, d Deps, o Options, dir string, in HookInput, out io.Writer) error {
	target := ExtractToolTargetPath(in.ToolCall.Args)
	toolDir := ResolveToolProjectDir(dir, in, target)
	if target == "" || HooksDisabledForHome(o.Home, toolDir) {
		return writeDecision(out, "allow", "")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	coordIn := guard.CoordinatorInput{
		Home: o.Home, Dir: o.Dir, ConversationID: in.ConversationID,
		WorkspacePaths: in.WorkspacePaths, Cwd: in.Cwd, LastUserInput: in.LastUserInput,
	}
	if reason := guard.CheckCoordinatorWriteGuard(coordIn, dir, toolDir, target, d.openGuardLedgerDB); reason != "" {
		return writeDecision(out, "deny", reason)
	}
	dec, err := d.CheckPreWriteHashWithEdit(dir, o.Home, toolDir, in.ConversationID, target, in.ToolCall.Args)
	if err == nil && !dec.Allowed {
		return writeDecision(out, "deny", dec.Reason)
	}
	if err == nil && toolDir == dir && ctx.Err() == nil && d.MarkPreWriteInLedger != nil {
		if mErr := d.MarkPreWriteInLedger(dir, in.ConversationID, dec.ResolvedPath); mErr != nil && !errors.Is(mErr, guard.ErrNotCheckout) {
			return writeDecision(out, "allow", "fyr brain: pre-write ledger unavailable ("+oneLineErr(mErr)+")")
		}
	}
	return writeDecision(out, "allow", "")
}

func handleStopHook(ctx context.Context, d Deps, o Options, dir string, in HookInput, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	telemetry.WriteHookProcessArtifact(o.Home, d.telemetryArtifactDeps(nil), dir, toTelemetryConvInput(in), "Verify", nil)
	if err := ctx.Err(); err != nil {
		return err
	}
	var coErr error
	if d.RecordStopCoActivity != nil {
		coErr = d.RecordStopCoActivity(dir, in.ConversationID)
	}
	if shouldContinue, reason := evaluateStopStamp(d, o.Home, dir, in); shouldContinue {
		return writeDecision(out, "continue", reason)
	}
	if coErr != nil && !errors.Is(coErr, guard.ErrNotCheckout) {
		return writeDecision(out, "allow", "fyr brain: co-activity store unavailable ("+oneLineErr(coErr)+")")
	}
	return writeDecision(out, "allow", "")
}
