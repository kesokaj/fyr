package search

import (
	"context"
	"database/sql"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/hooks"
	"github.com/kesokaj/fyr/internal/hooks/guard"
	"github.com/kesokaj/fyr/internal/hooks/telemetry"
	"github.com/kesokaj/fyr/internal/readledger"
	"github.com/kesokaj/fyr/internal/symgraph"
	"github.com/kesokaj/fyr/internal/trail"
	"github.com/kesokaj/fyr/internal/vet"
)

// Exported hook payload and response types forwarded from internal/hooks.
type (
	HookInput           = hooks.HookInput
	HookToolCall        = hooks.HookToolCall
	PreInvocationOutput = hooks.PreInvocationOutput
	HookInjectStep      = hooks.HookInjectStep
	HookSystemMessage   = hooks.HookSystemMessage
	HookDecisionOutput  = hooks.HookDecisionOutput
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
	sessionStartCapsule      = telemetry.SessionStartCapsule
	sessionGitSummary        = telemetry.SessionGitSummary
)

type watchdogPreState struct{ role string }

const (
	askTranscriptMaxCandidates = guard.AskTranscriptMaxCandidates
	askTranscriptTailBytes     = guard.AskTranscriptTailBytes
	contextKneeHandoffCue      = telemetry.ContextKneeHandoffCue
	clampOnceSentinelLine      = hooks.ClampOnceSentinelLine
	defaultHookDeadline        = hooks.DefaultHookDeadline
	ftsFallbackLimit           = hooks.FTSFallbackLimit
	maxCheckpointScanBytes     = hooks.MaxCheckpointScanBytes
	maxReadFileBytes           = hooks.MaxReadFileBytes
	preInvocationSentinel      = hooks.PreInvocationSentinel
	sessionStartMaxBytes       = hooks.SessionStartMaxBytes
	sessionStartMaxFileRead    = hooks.SessionStartMaxFileRead
	verifyStampRelPath         = hooks.VerifyStampRelPath
	workerCapsuleTag           = hooks.WorkerCapsuleTag
)

// --- hooks.Store / hooks.Scope / hooks.Deps adapters ---

func wrapHookStore(st *store) *hooks.Store {
	if st == nil {
		return nil
	}
	return &hooks.Store{
		SQLDB:             st.db,
		Path:              st.path,
		Raw:               st,
		CloseFn:           st.close,
		WithImmediateTxFn: st.withImmediateTx,
		LookupFTSFn:       st.lookupFTSTurnCache,
		StoreFTSFn:        st.storeFTSTurnCache,
	}
}

func unwrapHookStore(hs *hooks.Store) *store {
	if hs == nil {
		return nil
	}
	if st, ok := hs.Raw.(*store); ok && st != nil {
		return st
	}
	return &store{db: hs.SQLDB, path: hs.Path}
}

func wrapHookScope(sc *scope) *hooks.Scope {
	if sc == nil {
		return nil
	}
	return &hooks.Scope{St: wrapHookStore(sc.st), Projects: sc.projects, LeftOut: sc.leftOut}
}

func unwrapHookScope(hsc *hooks.Scope) *scope {
	if hsc == nil {
		return nil
	}
	return &scope{st: unwrapHookStore(hsc.St), projects: hsc.Projects, leftOut: hsc.LeftOut}
}

func toHooksOptions(o Options) hooks.Options {
	ho := hooks.Options{
		Dir: o.Dir, Home: o.Home, Session: o.Session, Vet: o.Vet, Hook: o.Hook,
		ActiveFiles: o.ActiveFiles, BrainRefsForDir: o.BrainRefsForDir, BrainReprime: o.BrainReprime,
		BrainTurnContext: o.BrainTurnContext, BrainTurnContextCtx: o.BrainTurnContextCtx,
		SkillCatalog: o.SkillCatalog, HookDeadline: o.hookDeadline, NowFunc: o.nowFunc,
	}
	if o.BrainSpecBodies != nil {
		ho.FormatContractDigest = func(dir, home string, paths []string, maxBytes int) string {
			return FormatContractDigest(o.BrainSpecBodies(dir, home, paths), maxBytes)
		}
	}
	if o.hookDispatch != nil {
		ho.HookDispatch = func(ctx context.Context, _ hooks.Options, event, dir string, in hooks.HookInput, out io.Writer) error {
			return o.hookDispatch(ctx, o, event, dir, in, out)
		}
	}
	return ho
}

func hookDeps() hooks.Deps {
	d := hooks.Deps{
		Now: func() time.Time { return nowFunc() }, ProjectDB: projectDB, GuardProject: guardProject,
		ResolveSessionLedgerDir: resolveSessionLedgerDir, CheckPreWriteHashWithEdit: CheckPreWriteHashWithEdit,
		RegisterCapsulePivotsWithHome: RegisterCapsulePivotsWithHome, MarkPreWriteInLedger: markPreWriteInLedger,
		RecordStopCoActivity: RecordStopCoActivity, RelPathInRepo: relPathInRepo,
		FTSQuery: ftsQuery, Printable: printable, TruncateMultilineUTF8: truncateMultilineUTF8,
		DefaultContractDigestBytes: defaultContractDigestBytes, MaxFileBytes: maxFileBytes, MaxWarmSigEntries: maxWarmSigEntries,
		CopyDB: copyDB, OpenCopy: openCopy, RODSN: roDSN, ProtoFields: searchProtoFields,
		UsageError: func(msg string) error { return usageError{msg} },
	}
	fillHookStoreDeps(&d)
	fillHookGitDeps(&d)
	return d
}

func fillHookStoreDeps(d *hooks.Deps) {
	d.OpenStore = func(path string) (*hooks.Store, error) {
		st, err := openStore(path, io.Discard)
		return wrapHookStore(st), err
	}
	d.OpenLedgerStore = func(dir string) (*hooks.Store, string, error) {
		st, root, err := openLedgerStore(dir)
		return wrapHookStore(st), root, err
	}
	d.OpenSessionLedgerStore = func(sessionDir, home string) (*hooks.Store, string, error) {
		st, root, err := openSessionLedgerStore(sessionDir, home)
		return wrapHookStore(st), root, err
	}
	d.OpenShortTimeoutStoreCtx = func(ctx context.Context, path string) *hooks.Store {
		return wrapHookStore(openShortTimeoutStoreCtx(ctx, path))
	}
	d.OpenSliceReadOnlyStore = func(path string) *hooks.Store {
		return wrapHookStore(openSliceReadOnlyStore(path))
	}
	d.CheckAndRecordResolvedRead = func(st *hooks.Store, root, convID, resolved string, startLine, endLine, turnNum int) (readledger.ReadDecision, error) {
		return unwrapHookStore(st).checkAndRecordResolvedRead(root, convID, resolved, startLine, endLine, turnNum)
	}
	d.SessionActiveFiles = func(sc *hooks.Scope, sessionID string, extra map[string]bool) map[string]bool {
		return unwrapHookScope(sc).sessionActiveFiles(sessionID, extra)
	}
	d.LoadBoundedSymGraphCtx = func(ctx context.Context, sc *hooks.Scope, prompt string, active map[string]bool, includeDocs bool, maxHops int) (*symgraph.Graph, error) {
		return unwrapHookScope(sc).loadBoundedSymGraphCtx(ctx, prompt, active, includeDocs, maxHops)
	}
	d.RenderCompactHookCapsule = func(sc *hooks.Scope, o hooks.Options, prompt string, g *symgraph.Graph, tiers symgraph.SliceTiers, maxBytes int) string {
		sopts := symgraph.SliceOptions{Slice: prompt, Session: o.Session, Hook: o.Hook, ActiveFiles: o.ActiveFiles}
		if o.BrainRefsForDir != nil && o.Dir != "" {
			sopts.BrainRefs = func(target string) []LinkedRecord { return o.BrainRefsForDir(o.Dir, target) }
		}
		if o.FormatContractDigest != nil {
			sopts.FormatSpecDigest = func(paths []string, maxB int) string {
				return o.FormatContractDigest(o.Dir, o.Home, paths, maxB)
			}
		}
		return unwrapHookScope(sc).symScope().RenderCompactHookCapsule(sopts, g, tiers, maxBytes)
	}
}

func fillHookGitDeps(d *hooks.Deps) {
	d.MatchesGitWarmSig = func(ctx context.Context, st *hooks.Store, root string) bool {
		return unwrapHookStore(st).matchesGitWarmSig(ctx, root)
	}
	d.MatchesGitIdxHeadSig = func(ctx context.Context, st *hooks.Store, root string) bool {
		return unwrapHookStore(st).matchesGitIdxHeadSig(ctx, root)
	}
	d.SaveGitWarmSig = func(st *hooks.Store, root string) { unwrapHookStore(st).saveGitWarmSig(root) }
	d.ApplyDirtyPaths = func(ctx context.Context, st *hooks.Store, root string, dirty []hooks.Entry, existingFiles int) error {
		return unwrapHookStore(st).applyDirtyPaths(ctx, root, fromHookEntries(dirty), existingFiles)
	}
	d.GitView = func(root string) ([]hooks.Entry, []string, error) {
		view, leftOut, err := gitView(root)
		return toHookEntries(view), leftOut, err
	}
	d.DiffCount = func(st *hooks.Store, root string, view []hooks.Entry) (int, error) {
		return unwrapHookStore(st).diffCount(source{project: root, dir: root, view: fromHookEntries(view)})
	}
	d.ApplyView = func(st *hooks.Store, root string, view []hooks.Entry) (int, error) {
		return unwrapHookStore(st).apply(source{project: root, dir: root, view: fromHookEntries(view)})
	}
	d.GitCtx, d.BlobID, d.Indexed, d.IsNoIndexMarker, d.ReadAll = gitCtx, blobID, indexed, isNoIndexMarker, readAll
}

func toHookEntries(in []entry) []hooks.Entry {
	out := make([]hooks.Entry, len(in))
	for i, e := range in {
		out[i] = hooks.Entry{Path: e.path, OID: e.oid, State: e.state}
	}
	return out
}

func fromHookEntries(in []hooks.Entry) []entry {
	out := make([]entry, len(in))
	for i, e := range in {
		out[i] = entry{path: e.Path, oid: e.OID, state: e.State}
	}
	return out
}

func searchProtoFields(msg []byte) []telemetry.ProtoField {
	raw := ProtoFields(msg)
	out := make([]telemetry.ProtoField, len(raw))
	for i, f := range raw {
		out[i] = telemetry.ProtoField{Path: f.Path, Wire: f.Wire, Varint: f.Varint, Bytes: f.Bytes}
	}
	return out
}

// --- store FTS turn cache & short-timeout SQLite helpers ---

func (s *store) clearFTSTurnCache() {
	if s == nil {
		return
	}
	s.ftsTurnCacheMu.Lock()
	s.ftsTurnCache = nil
	s.ftsTurnCacheMu.Unlock()
}

func (s *store) lookupFTSTurnCache(k string) ([]ftsChunkHit, bool) {
	if s == nil {
		return nil, false
	}
	s.ftsTurnCacheMu.Lock()
	defer s.ftsTurnCacheMu.Unlock()
	if s.ftsTurnCache == nil {
		return nil, false
	}
	hits, ok := s.ftsTurnCache[k]
	return hits, ok
}

func (s *store) storeFTSTurnCache(k string, hits []ftsChunkHit) {
	if s == nil {
		return
	}
	s.ftsTurnCacheMu.Lock()
	if s.ftsTurnCache == nil {
		s.ftsTurnCache = map[string][]ftsChunkHit{}
	}
	s.ftsTurnCache[k] = hits
	s.ftsTurnCacheMu.Unlock()
}

func openShortTimeoutStoreCtx(ctx context.Context, path string) *store {
	if ctx.Err() != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil || prepareFiles(path) != nil {
		return nil
	}
	absPath := path
	if abs, err := filepath.Abs(path); err == nil {
		absPath = abs
	}
	u := url.URL{
		Scheme:   "file",
		Path:     filepath.ToSlash(absPath),
		RawQuery: "_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(25)&_pragma=foreign_keys(0)",
	}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil
	}
	db.SetMaxOpenConns(1)
	s := &store{db: db, path: path}
	var v string
	if err := db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'schema'`).Scan(&v); err == nil && v == schemaVersion {
		return s
	}
	if ctx.Err() != nil || s.ensureSchema() != nil {
		_ = db.Close()
		return nil
	}
	return s
}

func openSliceReadOnlyStore(path string) *store {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	absPath := path
	if abs, err := filepath.Abs(path); err == nil {
		absPath = abs
	}
	u := url.URL{
		Scheme:   "file",
		Path:     filepath.ToSlash(absPath),
		RawQuery: "mode=ro&_pragma=busy_timeout(15)&_pragma=foreign_keys(0)",
	}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil
	}
	db.SetMaxOpenConns(1)
	return &store{db: db, path: path}
}

// --- Exported hook APIs ---

func RunHook(o Options, in io.Reader, out io.Writer) error {
	return hooks.RunHook(hookDeps(), toHooksOptions(o), in, out)
}

func DefaultHookBrainBudget() time.Duration     { return hooks.DefaultHookBrainBudget() }
func IsBuilderSubagentRole(role string) bool    { return hooks.IsBuilderSubagentRole(role) }
func SubagentBudgetCap(role string) (int, int)  { return hooks.SubagentBudgetCap(role) }
func IsAuditSubagentRole(role string) bool      { return hooks.IsAuditSubagentRole(role) }
func StripSandboxPreamble(prompt string) string { return hooks.StripSandboxPreamble(prompt) }
func ComputeGoTreeState(root string) (GoTreeState, error) {
	return guard.ComputeGoTreeState(root, hooks.ReadGitBranchReftable)
}
func ResolveModelDetails(home, rawModel string) (string, string, string) {
	return telemetry.ResolveModelDetails(home, rawModel)
}

// --- Thin package-private forwarding shims for internal/search callers & tests ---

func normalizeHookEvent(raw string) string      { return hooks.NormalizeHookEvent(raw) }
func checkScratchNoSymlinks(root string) error  { return trail.CheckScratchNoSymlinks(root) }
func extractFTSFallbackWords(p string) []string { return hooks.ExtractFTSFallbackWords(p) }
func prepareHookScope(dir string) (*scope, error) {
	hsc, err := hooks.PrepareHookScope(hookDeps(), dir)
	return unwrapHookScope(hsc), err
}
func (sc *scope) runVet(o Options, out io.Writer) error {
	return hooks.RunVet(hookDeps(), wrapHookScope(sc), toHooksOptions(o), out)
}
func (sc *scope) buildVetSlice(o Options, prompt string, state SessionState, preseedLedger bool) SliceResult {
	return hooks.BuildVetSlice(hookDeps(), wrapHookScope(sc), toHooksOptions(o), prompt, state, preseedLedger)
}
func (sc *scope) queryFTSFallbackChunks(words []string, promptLower string) []ftsChunkHit {
	return hooks.QueryFTSFallbackChunks(hookDeps(), wrapHookScope(sc), words, promptLower)
}

func checkTurnOrMidTurnWithStore(ctx context.Context, st *store, in HookInput, rawPrompt string) (SessionState, bool, error) {
	return hooks.CheckTurnOrMidTurnWithStore(ctx, hookDeps(), wrapHookStore(st), in, rawPrompt)
}
func collectSessionGitSummary(root string) sessionGitSummary {
	return hooks.CollectSessionGitSummary(hookDeps(), root)
}

//nolint:unparam // test shim forwarding full hooks.DetectDirtyPathsFast signature
func detectDirtyPathsFast(ctx context.Context, root string, st *store) ([]entry, []string, bool) {
	es, lbls, ok := hooks.DetectDirtyPathsFast(ctx, hookDeps(), root, wrapHookStore(st))
	return fromHookEntries(es), lbls, ok
}
func dynamicDirtyFastPathCap(ctx context.Context, n int) int {
	return hooks.DynamicDirtyFastPathCap(ctx, n)
}
func extractLineRangeArgs(args map[string]any) (int, int) { return hooks.ExtractLineRangeArgs(args) }
func extractSessionDailyAlert(home string) string {
	return hooks.ExtractSessionDailyAlert(hookDeps(), home)
}
func extractToolTargetPath(args map[string]any) string { return hooks.ExtractToolTargetPath(args) }
func fitSessionStartCapsule(cap *sessionStartCapsule, maxB int) string {
	return hooks.FitSessionStartCapsule(cap, maxB)
}
func formatDirtyFastPath(p []string, d, th int) string { return hooks.FormatDirtyFastPath(p, d, th) }
func formatModelTag(home, raw string) string           { return hooks.FormatModelTag(home, raw) }
func handleParallelHookTurn(ctx context.Context, o Options, sc *scope, dir string, in HookInput, state SessionState, rawPrompt, cleanPrompt string, start time.Time) (string, string) {
	return hooks.HandleParallelHookTurn(ctx, hookDeps(), toHooksOptions(o), wrapHookScope(sc), dir, in, state, rawPrompt, cleanPrompt, start)
}
func handlePreToolBashCtx(ctx context.Context, home, dir string, wdSt *store, wdRoot string, in HookInput, out io.Writer) error {
	return hooks.HandlePreToolBashCtx(ctx, hookDeps(), home, dir, wrapHookStore(wdSt), wdRoot, in, out)
}
func handleSessionStart(ctx context.Context, o Options, dir string, in HookInput, out io.Writer) error {
	return hooks.HandleSessionStart(ctx, hookDeps(), toHooksOptions(o), dir, in, out)
}
func hookBrainDeadline(ctx context.Context, o Options) time.Duration {
	return hooks.HookBrainDeadline(ctx, toHooksOptions(o))
}
func hookBypassedForEvent(home, dir, event string) bool {
	return hooks.HookBypassedForEvent(home, dir, event)
}
func hookSliceDeadline(ctx context.Context, o Options) time.Duration {
	return hooks.HookSliceDeadline(ctx, toHooksOptions(o))
}
func hooksDisabled(dir string) bool             { return hooks.HooksDisabled(dir) }
func hooksDisabledGlobal(home string) bool      { return hooks.HooksDisabledGlobal(home) }
func inferSwarmRole(args map[string]any) string { return hooks.InferSwarmRole(args) }
func isGuardedSubagentCall(args map[string]any) (string, bool) {
	return hooks.IsGuardedSubagentCall(args)
}
func isWatchdogExemptBash(args map[string]any) bool { return hooks.IsWatchdogExemptBash(args) }
func lookupWorkerRecords(o Options, explicitDir, prompt string, files []string) []MatchedRecord {
	return hooks.LookupWorkerRecords(toHooksOptions(o), explicitDir, prompt, files)
}
func normalizeHookToolName(raw string) string { return hooks.NormalizeHookToolName(raw) }
func parseAndNormalizeHookInput(o Options, data []byte) (HookInput, error) {
	return hooks.ParseAndNormalizeHookInput(toHooksOptions(o), data)
}
func pickSwarmModel(home, role string) (string, string) { return hooks.PickSwarmModel(home, role) }
func resolveToolProjectDir(dir string, in HookInput, target string) string {
	return hooks.ResolveToolProjectDir(dir, in, target)
}

//nolint:unparam // test shim forwarding full hooks.ScanTranscriptCheckpoint signature
func scanTranscriptCheckpoint(path string, fromOffset int64) (int64, int64, int, bool) {
	return hooks.ScanTranscriptCheckpoint(path, fromOffset)
}
func sha256PromptHex(rawPrompt string) string { return hooks.SHA256PromptHex(rawPrompt) }
func tryDirtyFastReconcile(ctx context.Context, root string, st *store, existingFiles int) (*scope, string, bool, bool, error) {
	hsc, summary, fast, handled, err := hooks.TryDirtyFastReconcile(ctx, hookDeps(), root, wrapHookStore(st), existingFiles)
	return unwrapHookScope(hsc), summary, fast, handled, err
}
func writeScratchStampAtomic(target string, data []byte) error {
	return hooks.WriteScratchStampAtomic(target, data)
}

// --- guard & telemetry leaf shims still referenced by internal/search prod & tests ---

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
func searchTelemetryConvInput(in HookInput) telemetry.ConvInput {
	return telemetry.ConvInput{ConversationID: in.ConversationID, ArtifactDirectoryPath: in.ArtifactDirectoryPath, TranscriptPath: in.TranscriptPath, ModelName: in.ModelName}
}
func telemetryConvDeps(sc *scope) telemetry.ConvDeps {
	var metaDB *sql.DB
	if sc != nil && sc.st != nil {
		metaDB = sc.st.db
	}
	return telemetry.ConvDeps{MetaDB: metaDB, CopyDB: copyDB, RODSN: roDSN, ProtoFields: searchProtoFields}
}
func telemetryArtifactDeps(sc *scope) telemetry.ArtifactDeps {
	return telemetry.ArtifactDeps{
		HooksDisabled: hooks.HooksDisabledForHome,
		RecordVetHistory: func(ctx context.Context, convID string, turnNum int, rawPrompt string, res vet.Result, pivotFiles []string, m vet.TurnMetrics) error {
			if sc == nil || sc.st == nil {
				return nil
			}
			return sc.st.recordVetHistoryWithMetricsCtx(ctx, convID, turnNum, rawPrompt, res, pivotFiles, vetTurnMetrics{durationMS: m.DurationMS, timedOut: m.TimedOut, injectedBytes: m.InjectedBytes})
		},
		ListVetHistory: func(dir, convID string, limit int) ([]vet.HistoryRow, bool) {
			if sc != nil && sc.st != nil {
				rows, _ := sc.st.listConversationVetHistory(convID, limit)
				return rows, true
			}
			root, _, ok := guard.FindGitRootFast(dir)
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

func checkInteractiveBashGuard(args map[string]any) (string, map[string]any) {
	return guard.CheckInteractiveBashGuard(args)
}
func inspectBashCommandTree(cmd string) (string, bool)    { return guard.InspectBashCommandTree(cmd) }
func unwrapEnvOrExec(kind string, toks []string) []string { return guard.UnwrapEnvOrExec(kind, toks) }
func ensureHookProjectReady(dir string) error             { return guard.EnsureHookProjectReady(dir) }
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
func patternCoversRootScratch(pat string) bool      { return guard.PatternCoversRootScratch(pat) }
func ensureExistingCheckoutIgnored(dir string) bool { return guard.EnsureExistingCheckoutIgnored(dir) }
func resolveGitWorktreeDir(root string) string      { return guard.ResolveGitWorktreeDir(root) }
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
func detectKickoffOrWorkerSessionCtx(ctx context.Context, dir string, wd watchdogPreState, wdSt *store, wdRoot string, in HookInput) (bool, string) {
	ai := guard.AskInput{Dir: dir, ConversationID: in.ConversationID, LastUserInput: in.LastUserInput, TranscriptPath: in.TranscriptPath, ToolCallArgs: in.ToolCall.Args, WatchdogRole: wd.role}
	return guard.DetectKickoffOrWorkerSessionCtx(ctx, ai, storeSQLDB(wdSt), wdRoot, openGuardLedgerDB)
}
func hasAskQuestionEscapeClause(text string) bool { return guard.HasAskQuestionEscapeClause(text) }
func lookupWatchdogSummaryRoleCtx(ctx context.Context, home, convID string) (string, string, bool) {
	return guard.LookupWatchdogSummaryRoleCtx(ctx, home, convID, guard.WatchdogRoleDeps{RODSN: roDSN, OpenCopy: openCopy})
}
func skipPythonWSAndComments(src string, i int) int { return guard.SkipPythonWSAndComments(src, i) }
func loadModelsCatalog(home string) (modelsCatalogFile, bool) {
	return telemetry.LoadModelsCatalog(home)
}
func resolveHookSessionModelForHome(home string, in HookInput) string {
	return telemetry.ResolveHookSessionModelForHome(home, searchTelemetryConvInput(in), telemetryConvDeps(nil))
}
func resolveHookConvDBPathForHome(home string, in HookInput) (string, string) {
	return telemetry.ResolveHookConvDBPathForHome(home, searchTelemetryConvInput(in))
}
func writeHookProcessArtifact(home string, sc *scope, dir string, in HookInput, stage string, records []MatchedRecord) {
	telemetry.WriteHookProcessArtifact(home, telemetryArtifactDeps(sc), dir, searchTelemetryConvInput(in), stage, records)
}
func loadLatestWorkStatus(home string) (telemetry.WorkStatusSnapshot, bool) {
	return telemetry.LoadLatestWorkStatus(home)
}
func resolveProcessArtifactPathForHome(home string, in HookInput) string {
	return telemetry.ResolveProcessArtifactPathForHome(home, searchTelemetryConvInput(in))
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
func truncateBytesUTF8(s string, maxB int) string         { return telemetry.TruncateBytesUTF8(s, maxB) }
func applyExtraBanner(b *PreInvocationBanner, raw string) { telemetry.ApplyExtraBanner(b, raw) }
func preInvocationDirective(res VetResult) string         { return telemetry.PreInvocationDirective(res) }
func buildPreInvocationBannerBaseWithMeta(res VetResult, meta TurnBannerMeta) PreInvocationBanner {
	return telemetry.BuildPreInvocationBannerBaseWithMeta(res, meta)
}
func buildPreInvocationEnrichment(res VetResult, slice SliceResult, brainCtx TurnBrainContext, extraSections []string, compactedGen int) *PreInvocationEnrichment {
	return telemetry.BuildPreInvocationEnrichment(res, slice, brainCtx, extraSections, compactedGen)
}
func formatStoreDegradedBanner(err error) string { return telemetry.FormatStoreDegradedBanner(err) }
func renderLedgerNotice(n LedgerNotice) string   { return telemetry.RenderLedgerNotice(n) }
func formatUnchangedLedgerNotice(relPath string, start, end int, sumHex string, hitCount int, skel string) string {
	return telemetry.FormatUnchangedLedgerNotice(relPath, start, end, sumHex, hitCount, skel)
}
func formatModifiedLedgerNotice(relPath, prevSHA, curSHA, skel string) string {
	return telemetry.FormatModifiedLedgerNotice(relPath, prevSHA, curSHA, skel)
}
func renderProcessTelemetryMarkdownWithCatalog(home string, cat modelsCatalogFile, hasCat bool, convID, stage string, rows []VetHistoryRow, records []MatchedRecord) string {
	return telemetry.RenderProcessTelemetryMarkdownWithCatalog(home, cat, hasCat, convID, stage, rows, records)
}
func parseFenceHexToken(tok string) (string, bool) { return telemetry.ParseFenceHexToken(tok) }
