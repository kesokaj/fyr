package search

import (
	"context"
	"database/sql"
	"io"
	"time"

	"github.com/kesokaj/fyr/internal/hooks/guard"
	"github.com/kesokaj/fyr/internal/hooks/telemetry"
	"github.com/kesokaj/fyr/internal/vet"
)

// Type aliases and constants forwarded from internal/hooks/guard and internal/hooks/telemetry.
type (
	gitIndexEntry            = guard.GitIndexEntry
	GoTreeState              = guard.GoTreeState
	LedgerNotice             = telemetry.LedgerNotice
	PreInvocationBanner      = telemetry.PreInvocationBanner
	PreInvocationEnrichment  = telemetry.PreInvocationEnrichment
	PreInvocationRecord      = telemetry.PreInvocationRecord
	PreInvocationSkillDigest = telemetry.PreInvocationSkillDigest
	TurnBannerMeta           = telemetry.TurnBannerMeta
	ProcessTelemetry         = telemetry.ProcessTelemetry
	ProcessModelRouting      = telemetry.ProcessModelRouting
	ProcessTurnTelemetry     = telemetry.ProcessTurnTelemetry
	ProcessWorkWave          = telemetry.ProcessWorkWave
	ProcessWorkWorker        = telemetry.ProcessWorkWorker
	modelsCatalogFile        = telemetry.ModelsCatalogFile
	resolvedModelInfo        = telemetry.ResolvedModelInfo
	sessionStartCapsule      = telemetry.SessionStartCapsule
	sessionGitSummary        = telemetry.SessionGitSummary
)

const (
	askTranscriptMaxCandidates = guard.AskTranscriptMaxCandidates
	askTranscriptTailBytes     = guard.AskTranscriptTailBytes
	contextKneeHandoffCue      = telemetry.ContextKneeHandoffCue
)

// --- guard leaf adapters ---

func openGuardLedgerDB(root string) (*sql.DB, func(), error) {
	st, _, err := openLedgerStore(root)
	if err != nil || st == nil {
		return nil, nil, err
	}
	return st.db, func() { st.close() }, nil
}

func storeSQLDB(st *store) *sql.DB {
	if st == nil {
		return nil
	}
	return st.db
}

func guardWatchdogRoleDeps() guard.WatchdogRoleDeps {
	return guard.WatchdogRoleDeps{RODSN: roDSN, OpenCopy: openCopy}
}

func guardAskInput(ctx context.Context, o Options, dir string, wd watchdogPreState, in HookInput) guard.AskInput {
	meta := hookSubagentMetaFromCtx(ctx)
	return guard.AskInput{
		Home: o.Home, Dir: dir, ConversationID: in.ConversationID,
		LastUserInput: in.LastUserInput, TranscriptPath: in.TranscriptPath,
		ToolCallArgs: in.ToolCall.Args, ParentConversationID: meta.ParentConversationID,
		AgentName: meta.AgentName, WatchdogRole: wd.role,
	}
}

func guardNudgeDeps() guard.NudgeDeps {
	return guard.NudgeDeps{
		HooksDisabled:     hooksDisabledForHome,
		NormalizeToolName: normalizeHookToolName,
		OpenLedgerDB:      openGuardLedgerDB,
		MarkSentinelOnce: func(db *sql.DB, convID string, genID, turnNum int, key string) bool {
			return markSessionSentinelOnce(&store{db: db}, convID, genID, turnNum, key)
		},
		WriteDecision: writeDecision,
	}
}

func ComputeGoTreeState(root string) (GoTreeState, error) {
	return guard.ComputeGoTreeState(root, readGitBranchReftable)
}

func checkInteractiveBashGuard(args map[string]any) (string, map[string]any) {
	return guard.CheckInteractiveBashGuard(args)
}

func inspectBashCommandTree(cmd string) (string, bool) { return guard.InspectBashCommandTree(cmd) }
func extractBashCmdArg(args map[string]any) (string, string) {
	return guard.ExtractBashCmdArg(args)
}
func hasShellExpansionOutsideSingleQuotes(cmd string) bool {
	return guard.HasShellExpansionOutsideSingleQuotes(cmd)
}
func splitShellSegments(cmd string) []string   { return guard.SplitShellSegments(cmd) }
func tokenizeShellSegment(seg string) []string { return guard.TokenizeShellSegment(seg) }
func skipGitGlobalFlags(args []string) (string, []string) {
	return guard.SkipGitGlobalFlags(args)
}
func unwrapEnvOrExec(kind string, toks []string) []string { return guard.UnwrapEnvOrExec(kind, toks) }
func unwrapSegmentCommand(toks []string) (string, []string) {
	return guard.UnwrapSegmentCommand(toks)
}

func isRegularFileFast(path string) bool { return guard.IsRegularFileFast(path) }
func isCoordinatorOnlyMode(home, root string) bool {
	return guard.IsCoordinatorOnlyMode(home, root)
}
func resolveExplicitHookDirs(o Options, in HookInput) (string, string) {
	return guard.ResolveExplicitHookDirs(guard.CoordinatorInput{
		Home: o.Home, Dir: o.Dir, WorkspacePaths: in.WorkspacePaths, Cwd: in.Cwd,
	})
}
func parseWorkerFilesHeader(prompt string) ([]string, bool) {
	return guard.ParseWorkerFilesHeader(prompt)
}
func checkCoordinatorWriteGuard(o Options, dir, toolDir string, in HookInput, target string) string {
	return guard.CheckCoordinatorWriteGuard(guard.CoordinatorInput{
		Home: o.Home, Dir: o.Dir, ConversationID: in.ConversationID,
		WorkspacePaths: in.WorkspacePaths, Cwd: in.Cwd, LastUserInput: in.LastUserInput,
	}, dir, toolDir, target, openGuardLedgerDB)
}
func existingAncestorDir(path string) string { return guard.ExistingAncestorDir(path) }
func hasSymlinkSegmentUnder(root, relBase, target string) bool {
	return guard.HasSymlinkSegmentUnder(root, relBase, target)
}
func resolveTargetWithAncestors(toolDir, target string) string {
	return guard.ResolveTargetWithAncestors(toolDir, target)
}

func ensureHookProjectReady(dir string) error { return guard.EnsureHookProjectReady(dir) }
func ensureHookGitCheckout(absDir string) (string, []string, error) {
	return guard.EnsureHookGitCheckout(absDir)
}
func findGitRootFast(dir string) (string, string, bool) { return guard.FindGitRootFast(dir) }
func resolveGitCommonDirFast(dotGitFile, repoRoot string) (string, bool) {
	return guard.ResolveGitCommonDirFast(dotGitFile, repoRoot)
}
func guardLedgerFast(root, commonDir string) error {
	return guard.GuardLedgerFast(root, commonDir, guardProject)
}
func ensureHookScratchIgnored(root string, nested []string) error {
	return guard.EnsureHookScratchIgnored(root, nested)
}
func writePatternsToValidatedExclude(root, commonDir string, patterns []string) error {
	return guard.WritePatternsToValidatedExclude(root, commonDir, patterns)
}
func validateCommonDirForRoot(root, commonDir string) bool {
	return guard.ValidateCommonDirForRoot(root, commonDir)
}
func scratchIgnoredFast(root, commonDir string) bool {
	return guard.ScratchIgnoredFast(root, commonDir)
}
func ignoreFileScratchState(path string, coverFn func(string) bool) int {
	return guard.IgnoreFileScratchState(path, coverFn)
}
func patternCoversRootScratch(pat string) bool { return guard.PatternCoversRootScratch(pat) }
func ensureExistingCheckoutIgnored(dir string) bool {
	return guard.EnsureExistingCheckoutIgnored(dir)
}

func resolveGitWorktreeDir(root string) string { return guard.ResolveGitWorktreeDir(root) }
func checkTrackedGoEntries(root string, entries []gitIndexEntry, hasIndex bool) (map[string]bool, map[string]bool) {
	return guard.CheckTrackedGoEntries(root, entries, hasIndex)
}
func isTrackedEntryDirty(root string, e gitIndexEntry) bool {
	return guard.IsTrackedEntryDirty(root, e)
}
func readGitIndex(path string) ([]gitIndexEntry, bool, error) { return guard.ReadGitIndex(path) }
func parseGitIndex(data []byte) ([]gitIndexEntry, error)      { return guard.ParseGitIndex(data) }
func decodeGitIndexPath(data []byte, entryStart, nameStart int, version uint32, prevPath string) (string, int, error) {
	return guard.DecodeGitIndexPath(data, entryStart, nameStart, version, prevPath)
}

func handlePreToolAskQuestionCtx(ctx context.Context, o Options, dir string, wd watchdogPreState, wdSt *store, wdRoot string, in HookInput, out io.Writer) error {
	hb := hookBypassedForEvent
	deps := guard.AskDeps{
		HookBypassed:  hb,
		OpenLedgerDB:  openGuardLedgerDB,
		WriteDecision: writeDecision,
	}
	return guard.HandlePreToolAskQuestionCtx(ctx, guardAskInput(ctx, o, dir, wd, in), storeSQLDB(wdSt), wdRoot, deps, out)
}

func detectKickoffOrWorkerSessionCtx(ctx context.Context, dir string, wd watchdogPreState, wdSt *store, wdRoot string, in HookInput) (bool, string) {
	return guard.DetectKickoffOrWorkerSessionCtx(ctx, guardAskInput(ctx, Options{}, dir, wd, in), storeSQLDB(wdSt), wdRoot, openGuardLedgerDB)
}

func hasAskQuestionEscapeClause(text string) bool { return guard.HasAskQuestionEscapeClause(text) }

func handlePreToolGrepNudgeCtx(ctx context.Context, home, dir string, wdSt *store, wdRoot string, in HookInput, out io.Writer) error {
	ni := guard.NudgeInput{Home: home, Dir: dir, ConversationID: in.ConversationID, ToolName: in.ToolCall.Name, ToolArgs: in.ToolCall.Args}
	return guard.HandlePreToolGrepNudgeCtx(ctx, ni, storeSQLDB(wdSt), wdRoot, guardNudgeDeps(), out)
}

func checkBashGrepNudgeCtx(ctx context.Context, home, dir string, wdSt *store, wdRoot string, in HookInput, cmd string) string {
	ni := guard.NudgeInput{Home: home, Dir: dir, ConversationID: in.ConversationID, ToolName: in.ToolCall.Name, ToolArgs: in.ToolCall.Args}
	return guard.CheckBashGrepNudgeCtx(ctx, ni, storeSQLDB(wdSt), wdRoot, guardNudgeDeps(), cmd)
}

func resolveWatchdogRoleCtx(ctx context.Context, home, convID, lastInput string) (string, string) {
	return guard.ResolveWatchdogRoleCtx(ctx, home, convID, lastInput, guardWatchdogRoleDeps())
}
func matchWatchdogPromptPreamble(lastInput string) string {
	return guard.MatchWatchdogPromptPreamble(lastInput)
}
func lookupWatchdogSummaryRoleCtx(ctx context.Context, home, convID string) (string, string, bool) {
	return guard.LookupWatchdogSummaryRoleCtx(ctx, home, convID, guardWatchdogRoleDeps())
}

func handlePreToolWorkflow(home, dir string, in HookInput, out io.Writer) error {
	deps := guard.WorkflowDeps{
		EffectiveSessionModel:  func() string { return effectiveHookSessionModel(home, in) },
		NeedsGuardedProUpgrade: needsGuardedProUpgradeForHome,
		IsModelGuardedSubagent: isModelGuardedSubagent,
		FormatModelTag:         formatModelTag,
		SanitizePrintable:      printable,
		WriteDecision:          writeDecision,
		WriteDecisionOverwrite: writeDecisionWithOverwrite,
	}
	return guard.HandlePreToolWorkflow(guard.WorkflowInput{Home: home, Dir: dir, ToolArgs: in.ToolCall.Args}, deps, out)
}

func skipPythonWSAndComments(src string, i int) int { return guard.SkipPythonWSAndComments(src, i) }

// --- telemetry leaf adapters ---

func toTelemetryConvInput(in HookInput) telemetry.ConvInput {
	return telemetry.ConvInput{
		ConversationID:        in.ConversationID,
		ArtifactDirectoryPath: in.ArtifactDirectoryPath,
		TranscriptPath:        in.TranscriptPath,
		ModelName:             in.ModelName,
	}
}

func telemetryConvDeps(sc *scope) telemetry.ConvDeps {
	var metaDB *sql.DB
	if sc != nil && sc.st != nil {
		metaDB = sc.st.db
	}
	return telemetry.ConvDeps{
		MetaDB: metaDB,
		CopyDB: copyDB,
		RODSN:  roDSN,
		ProtoFields: func(msg []byte) []telemetry.ProtoField {
			raw := ProtoFields(msg)
			out := make([]telemetry.ProtoField, len(raw))
			for i, f := range raw {
				out[i] = telemetry.ProtoField{Path: f.Path, Wire: f.Wire, Varint: f.Varint, Bytes: f.Bytes}
			}
			return out
		},
	}
}

func telemetryArtifactDeps(sc *scope) telemetry.ArtifactDeps {
	return telemetry.ArtifactDeps{
		HooksDisabled: hooksDisabledForHome,
		RecordVetHistory: func(ctx context.Context, convID string, turnNum int, rawPrompt string, res vet.Result, pivotFiles []string, m vet.TurnMetrics) error {
			if sc == nil || sc.st == nil {
				return nil
			}
			return sc.st.recordVetHistoryWithMetricsCtx(ctx, convID, turnNum, rawPrompt, res, pivotFiles, vetTurnMetrics{
				durationMS:    m.DurationMS,
				timedOut:      m.TimedOut,
				injectedBytes: m.InjectedBytes,
			})
		},
		ListVetHistory: func(dir, convID string, limit int) ([]vet.HistoryRow, bool) {
			if sc != nil && sc.st != nil {
				rows, _ := sc.st.listConversationVetHistory(convID, limit)
				return rows, true
			}
			root, _, ok := findGitRootFast(dir)
			if !ok {
				return nil, false
			}
			st, err := openStore(projectDB(root), io.Discard)
			if err != nil {
				return nil, false
			}
			defer st.close()
			rows, _ := st.listConversationVetHistory(convID, limit)
			return rows, true
		},
	}
}

func ResolveModelDetails(home, rawModel string) (string, string, string) {
	return telemetry.ResolveModelDetails(home, rawModel)
}
func resolveModelDetailsWithCatalog(cat modelsCatalogFile, hasCat bool, rawModel string) (string, string, string) {
	return telemetry.ResolveModelDetailsWithCatalog(cat, hasCat, rawModel)
}
func cleanRawModelID(raw string) string { return telemetry.CleanRawModelID(raw) }
func loadModelsCatalog(home string) (modelsCatalogFile, bool) {
	return telemetry.LoadModelsCatalog(home)
}
func normalizeThinking(effort, id string) string { return telemetry.NormalizeThinking(effort, id) }

func effectiveHookSessionModel(home string, in HookInput) string {
	return telemetry.EffectiveHookSessionModel(home, toTelemetryConvInput(in), telemetryConvDeps(nil))
}
func resolveHookSessionModelForHome(home string, in HookInput) string {
	return telemetry.ResolveHookSessionModelForHome(home, toTelemetryConvInput(in), telemetryConvDeps(nil))
}
func resolvePreInvocationModelInfo(ctx context.Context, home string, cat modelsCatalogFile, hasCat bool, sc *scope, in HookInput) resolvedModelInfo {
	return telemetry.ResolvePreInvocationModelInfo(ctx, home, cat, hasCat, toTelemetryConvInput(in), telemetryConvDeps(sc))
}
func resolveHookConvDBPathForHome(home string, in HookInput) (string, string) {
	return telemetry.ResolveHookConvDBPathForHome(home, toTelemetryConvInput(in))
}

func dedupContextKneeWarning(home string, sc *scope, convID, sessionModel string, state SessionState) (string, string) {
	var st *store
	if sc != nil {
		st = sc.st
	}
	return telemetry.DedupContextKneeWarning(home, convID, sessionModel, state, func(cID string, genID, turnNum int, key string) bool {
		return markSessionSentinelOnce(st, cID, genID, turnNum, key)
	})
}

func finalizeHookEnvelope(ctx context.Context, home string, cat modelsCatalogFile, hasCat bool, sc *scope, dir, convID string, turnNum int, rawPrompt, cleanPrompt string, in HookInput, state SessionState, vetRes VetResult, slice SliceResult, brainCtx TurnBrainContext, meta TurnBannerMeta, sections []string, start time.Time) (string, string) {
	return telemetry.FinalizeHookEnvelope(ctx, home, cat, hasCat, telemetryArtifactDeps(sc), dir, convID, turnNum, rawPrompt, cleanPrompt, toTelemetryConvInput(in), state, vetRes, slice, brainCtx, meta, sections, start)
}

func writeHookProcessArtifact(home string, sc *scope, dir string, in HookInput, stage string, records []MatchedRecord) {
	telemetry.WriteHookProcessArtifact(home, telemetryArtifactDeps(sc), dir, toTelemetryConvInput(in), stage, records)
}

func loadLatestWorkStatus(home string) (telemetry.WorkStatusSnapshot, bool) {
	return telemetry.LoadLatestWorkStatus(home)
}
func resolveProcessArtifactPathForHome(home string, in HookInput) string {
	return telemetry.ResolveProcessArtifactPathForHome(home, toTelemetryConvInput(in))
}
func isUnderBrainDirForHome(home, dir string) bool {
	return telemetry.IsUnderBrainDirForHome(home, dir)
}
func isRegularDirOrAbsent(p string) bool { return telemetry.IsRegularDirOrAbsent(p) }
func writeProcessArtifactAtomic(target string, data []byte) error {
	return telemetry.WriteProcessArtifactAtomic(target, data)
}

func safeBacktickTextFence(text string) string { return telemetry.SafeBacktickTextFence(text) }
func turnBannerMetaFromExtras(extraSections, extraBanners []string) TurnBannerMeta {
	return telemetry.TurnBannerMetaFromExtras(extraSections, extraBanners)
}
func truncateBytesUTF8(s string, maxB int) string { return telemetry.TruncateBytesUTF8(s, maxB) }
func compactRecordNote(r MatchedRecord, title string) string {
	return telemetry.CompactRecordNote(r, title)
}
func cleanPrintableSlice(items []string, maxLen int) []string {
	return telemetry.CleanPrintableSlice(items, maxLen)
}
func applyExtraBanner(b *PreInvocationBanner, raw string) { telemetry.ApplyExtraBanner(b, raw) }

func preInvocationDirective(res VetResult) string { return telemetry.PreInvocationDirective(res) }
func buildPreInvocationBannerBaseWithMeta(res VetResult, meta TurnBannerMeta) PreInvocationBanner {
	return telemetry.BuildPreInvocationBannerBaseWithMeta(res, meta)
}
func buildPreInvocationEnrichment(res VetResult, slice SliceResult, brainCtx TurnBrainContext, extraSections []string, compactedGen int) *PreInvocationEnrichment {
	return telemetry.BuildPreInvocationEnrichment(res, slice, brainCtx, extraSections, compactedGen)
}
func formatStoreDegradedBanner(err error) string { return telemetry.FormatStoreDegradedBanner(err) }

func renderLedgerNotice(n LedgerNotice) string { return telemetry.RenderLedgerNotice(n) }
func formatUnchangedLedgerNotice(relPath string, start, end int, sumHex string, hitCount int, skel string) string {
	return telemetry.FormatUnchangedLedgerNotice(relPath, start, end, sumHex, hitCount, skel)
}
func formatClampedLedgerNotice(relPath string, totalLines int, sumHex, skel string) string {
	return telemetry.FormatClampedLedgerNotice(relPath, totalLines, sumHex, skel)
}
func formatModifiedLedgerNotice(relPath, prevSHA, curSHA, skel string) string {
	return telemetry.FormatModifiedLedgerNotice(relPath, prevSHA, curSHA, skel)
}

func renderProcessTelemetryMarkdownWithCatalog(home string, cat modelsCatalogFile, hasCat bool, convID, stage string, rows []VetHistoryRow, records []MatchedRecord) string {
	return telemetry.RenderProcessTelemetryMarkdownWithCatalog(home, cat, hasCat, convID, stage, rows, records)
}
func renderSessionStartCapsule(cap *sessionStartCapsule) string {
	return telemetry.RenderSessionStartCapsule(cap)
}
func parseFenceHexToken(tok string) (string, bool) { return telemetry.ParseFenceHexToken(tok) }
