package hooks

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/hooks/guard"
	"github.com/kesokaj/fyr/internal/hooks/telemetry"
	"github.com/kesokaj/fyr/internal/readledger"
	"github.com/kesokaj/fyr/internal/vet"
)

func preparePreInvocationFast(ctx context.Context, d Deps, o Options, dir string, in HookInput, rawPrompt string) (*Scope, readledger.SessionState, preInvocationSubagentInfo, bool, error) {
	if err := guard.EnsureHookProjectReady(dir); err != nil {
		return nil, readledger.SessionState{}, preInvocationSubagentInfo{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, readledger.SessionState{}, preInvocationSubagentInfo{}, false, err
	}
	st, root, err := d.OpenLedgerStore(dir)
	if err != nil {
		return nil, readledger.SessionState{}, preInvocationSubagentInfo{}, false, err
	}
	ref := hookTimeoutStoreRefFromCtx(ctx)
	if ref != nil {
		ref.attach(st, root)
	}
	ownSt := true
	defer func() {
		if ownSt && st != nil {
			if ref != nil {
				ref.release()
			} else {
				st.Close()
			}
		}
	}()
	state, skip, err := CheckTurnOrMidTurnWithStore(ctx, d, st, in, rawPrompt)
	if err != nil || skip {
		return nil, state, preInvocationSubagentInfo{}, skip, err
	}
	subInfo := resolvePreInvocationSubagentCtx(ctx, d, o.Home, st, state.ConversationID, rawPrompt)
	ownSt = false
	return &Scope{St: st, Projects: []string{root}}, state, subInfo, false, nil
}

// HookSliceDeadline returns the bounded AST slice budget for a PreInvocation hook.
func HookSliceDeadline(ctx context.Context, o Options) time.Duration {
	limit := defaultHookSliceDeadline
	if o.HookDeadline > 0 {
		if scaled := o.HookDeadline * 60 / 100; scaled > 0 {
			limit = scaled
		}
	}
	if ctx != nil {
		if dl, ok := ctx.Deadline(); ok {
			rem := time.Until(dl)
			reserve := min(40*time.Millisecond, rem/4)
			if capDur := rem - reserve; capDur < limit {
				limit = capDur
			}
		}
	}
	if limit < 5*time.Millisecond {
		return 5 * time.Millisecond
	}
	return limit
}

// DefaultHookBrainBudget returns the bounded brain context budget (50ms under
// the static 200ms production hook deadline; proportionally scaled under -race).
func DefaultHookBrainBudget() time.Duration {
	if DefaultHookDeadline > 200*time.Millisecond {
		return min(500*time.Millisecond, DefaultHookDeadline/4)
	}
	return 50 * time.Millisecond
}

// HookBrainDeadline returns the bounded brain-query deadline for a PreInvocation hook.
func HookBrainDeadline(ctx context.Context, o Options) time.Duration {
	limit := 50 * time.Millisecond
	if o.HookDeadline == 0 {
		limit = DefaultHookBrainBudget()
	}
	if o.HookDeadline > 0 {
		if scaled := o.HookDeadline / 4; scaled > 0 && scaled < limit {
			limit = scaled
		}
	}
	if ctx != nil {
		if dl, ok := ctx.Deadline(); ok {
			rem := time.Until(dl)
			reserve := min(40*time.Millisecond, rem/4)
			if capDur := rem - reserve; capDur < limit {
				limit = capDur
			}
		}
	}
	if limit < 5*time.Millisecond {
		return 5 * time.Millisecond
	}
	return limit
}

type parallelSliceOutcome struct {
	slice        vet.SliceResult
	pivots       []readledger.CapsulePivot
	dirtySummary string
	fastPath     bool
	leftOut      []string
}

type parallelTurnHandles struct {
	sliceCh     chan parallelSliceOutcome
	sliceCtx    context.Context
	sliceCancel context.CancelFunc
	brainCh     chan vet.TurnBrainContext
	brainCtx    context.Context
	brainCancel context.CancelFunc
	skillCh     chan []vet.SkillMeta
}

func (h parallelTurnHandles) cancel() {
	if h.sliceCancel != nil {
		h.sliceCancel()
	}
	if h.brainCancel != nil {
		h.brainCancel()
	}
}

// HandleParallelHookTurn orchestrates parallel AST slice, brain context, and skill lookup for PreInvocation.
func HandleParallelHookTurn(ctx context.Context, d Deps, o Options, sc *Scope, dir string, in HookInput, state readledger.SessionState, rawPrompt, cleanPrompt string, start time.Time) (string, string) {
	bp := strings.HasPrefix(strings.TrimSpace(rawPrompt), "!")
	initIntent := vet.ClassifyIntent(cleanPrompt, 1)
	skipHeavy := !state.Compacted && !bp && (initIntent == vet.IntentApproval || initIntent == vet.IntentTrivial)
	promptFiles := vet.ExtractPromptAndSliceFiles(cleanPrompt, nil)

	var handles parallelTurnHandles
	if !skipHeavy {
		handles = startParallelTurnWork(ctx, d, o, sc, dir, in.ConversationID, cleanPrompt, state, promptFiles)
		defer handles.cancel()
	} else if sc != nil && sc.St != nil {
		handles = startParallelScopeOnly(ctx, d, o, sc, dir, in.ConversationID, state)
		defer handles.cancel()
	}
	cat, hasCat := telemetry.LoadModelsCatalog(o.Home)
	modelInfo := telemetry.ResolvePreInvocationModelInfo(ctx, o.Home, cat, hasCat, toTelemetryConvInput(in), d.telemetryConvDeps(sc))
	prev, recSk, recPiv := loadTurnHistoryCtx(ctx, sc, in.ConversationID, state)

	sliceOut, brainCtx, catalog, brainTimedOut, ok := collectParallelTurnWork(ctx, handles)
	if !ok || ctx.Err() != nil {
		return "", ""
	}
	if sc != nil && len(sliceOut.leftOut) > 0 {
		sc.LeftOut = sliceOut.leftOut
	}
	sections, meta := buildParallelTurnSections(o, sc, dir, in, state, modelInfo.ID, sliceOut.dirtySummary, sliceOut.fastPath)
	if ctx.Err() != nil {
		return "", ""
	}
	slice, pivots := sliceOut.slice, sliceOut.pivots
	if slice.Capsule != "" {
		meta.PivotCount = slice.Pivots
		preseedHookSlicePivots(d, sc, o, dir, in.ConversationID, state.TurnCount, pivots)
	}
	if !skipHeavy && !brainTimedOut && hasBrainTurnContext(o) && hasNewSliceFiles(promptFiles, slice.Files) && hasRemainingBudget(ctx, 15*time.Millisecond) {
		brainCtx = callBrainTurnContextBounded(ctx, o, dir, cleanPrompt, slice.Files, brainCtx)
	}
	if rawPrompt == "" && len(sections) == 0 && slice.Capsule == "" && !meta.HasSignals() {
		return "", ""
	}
	vetRes := vet.VetPromptCoreWithHistory(rawPrompt, vet.ClassifyIntent(cleanPrompt, slice.SymbolHits), slice, brainCtx, catalog, prev, recSk, recPiv)
	vetRes.SessionModel, vetRes.SessionModelDisplay, vetRes.SessionThinking = modelInfo.ID, modelInfo.Display, modelInfo.Thinking
	finalizeParallelMeta(o.Home, sc, in.ConversationID, state.GenerationID, state.TurnCount, modelInfo.ID, &vetRes, &brainCtx, &meta, sections)
	return telemetry.FinalizeHookEnvelope(ctx, o.Home, cat, hasCat, d.telemetryArtifactDeps(sc), dir, in.ConversationID, state.TurnCount, rawPrompt, cleanPrompt, toTelemetryConvInput(in), state, vetRes, slice, brainCtx, meta, sections, start)
}

func hasBrainTurnContext(o Options) bool {
	return o.BrainTurnContextCtx != nil || o.BrainTurnContext != nil
}

func invokeBrainTurnContext(ctx context.Context, o Options, dir, prompt string, files []string) vet.TurnBrainContext {
	if o.BrainTurnContextCtx != nil {
		return o.BrainTurnContextCtx(ctx, dir, o.Home, prompt, files)
	}
	return o.BrainTurnContext(dir, o.Home, prompt, files)
}

func callBrainTurnContextBounded(ctx context.Context, o Options, dir, cleanPrompt string, files []string, fallback vet.TurnBrainContext) vet.TurnBrainContext {
	bctx, cancel := context.WithTimeout(ctx, min(HookBrainDeadline(ctx, o), 35*time.Millisecond))
	defer cancel()
	ch := make(chan vet.TurnBrainContext, 1)
	go func() { ch <- invokeBrainTurnContext(bctx, o, dir, cleanPrompt, files) }()
	select {
	case res := <-ch:
		return res
	case <-bctx.Done():
		return fallback
	}
}

func dedupContextKnee(home string, sc *Scope, convID, modelID string, state readledger.SessionState) (string, string) {
	var db *sql.DB
	if sc != nil {
		db = sc.St.DB()
	}
	return telemetry.DedupContextKneeWarning(home, convID, modelID, state, func(cID string, genID, turnNum int, sent string) bool {
		return MarkSessionSentinelOnce(db, cID, genID, turnNum, sent)
	})
}

func buildParallelTurnSections(o Options, sc *Scope, dir string, in HookInput, state readledger.SessionState, modelID, dirtySummary string, fastPath bool) ([]string, telemetry.TurnBannerMeta) {
	var sections []string
	var meta telemetry.TurnBannerMeta
	if state.Compacted {
		if o.BrainReprime != nil {
			if rep := strings.TrimSpace(o.BrainReprime(dir, o.Home)); rep != "" {
				sections = append(sections, rep)
			}
		}
		meta.CompactedGeneration = max(1, state.GenerationID)
	}
	if fastPath && dirtySummary != "" {
		sections = append(sections, dirtySummary)
		meta.DirtyFastPath = true
	}
	if warnFull, warnShort := dedupContextKnee(o.Home, sc, in.ConversationID, modelID, state); warnFull != "" {
		sections = append(sections, warnFull)
		meta.ContextKneeWarning = warnShort
	}
	return sections, meta
}

func startParallelScopeOnly(ctx context.Context, d Deps, o Options, sc *Scope, dir, convID string, state readledger.SessionState) parallelTurnHandles {
	sliceCtx, sliceCancel := context.WithTimeout(ctx, HookSliceDeadline(ctx, o))
	sliceOpts := o
	sliceOpts.Dir, sliceOpts.Session = dir, convID
	return parallelTurnHandles{
		sliceCh:     launchParallelScopeAndVetSlice(sliceCtx, d, sliceOpts, sc, "", state),
		sliceCtx:    sliceCtx,
		sliceCancel: sliceCancel,
	}
}

func startParallelTurnWork(ctx context.Context, d Deps, o Options, sc *Scope, dir, convID, cleanPrompt string, state readledger.SessionState, promptFiles []string) parallelTurnHandles {
	sliceCtx, sliceCancel := context.WithTimeout(ctx, HookSliceDeadline(ctx, o))
	sliceOpts := o
	sliceOpts.Dir = dir
	sliceOpts.Session = convID
	h := parallelTurnHandles{
		sliceCh:     launchParallelScopeAndVetSlice(sliceCtx, d, sliceOpts, sc, cleanPrompt, state),
		sliceCtx:    sliceCtx,
		sliceCancel: sliceCancel,
	}
	if hasBrainTurnContext(o) {
		brainCtx, brainCancel := context.WithTimeout(ctx, HookBrainDeadline(ctx, o))
		h.brainCtx, h.brainCancel = brainCtx, brainCancel
		h.brainCh = make(chan vet.TurnBrainContext, 1)
		go func() {
			h.brainCh <- invokeBrainTurnContext(brainCtx, o, dir, cleanPrompt, promptFiles)
		}()
	}
	if o.SkillCatalog != nil {
		h.skillCh = make(chan []vet.SkillMeta, 1)
		go func() {
			h.skillCh <- o.SkillCatalog(o.Home)
		}()
	}
	return h
}

func loadTurnHistoryCtx(ctx context.Context, sc *Scope, convID string, state readledger.SessionState) (*vet.HistoryRow, map[string]bool, []string) {
	if sc == nil || sc.St == nil || sc.St.SQLDB == nil {
		return nil, nil, nil
	}
	prev, _ := vet.LastHistoryCtx(ctx, sc.St.SQLDB, convID, state.TurnCount)
	if state.Compacted {
		return prev, nil, nil
	}
	recSk, recPiv, _ := vet.RecentSuggestedSkills(sc.St.SQLDB, convID, state.TurnCount)
	return prev, recSk, recPiv
}

func collectParallelTurnWork(ctx context.Context, h parallelTurnHandles) (parallelSliceOutcome, vet.TurnBrainContext, []vet.SkillMeta, bool, bool) {
	brainCtx, brainTimedOut, ok := awaitParallelBrain(ctx, h)
	if !ok {
		return parallelSliceOutcome{}, vet.TurnBrainContext{}, nil, true, false
	}
	sliceOut := awaitParallelSlice(h)
	catalog, ok := awaitParallelSkills(ctx, h.skillCh)
	if !ok {
		return parallelSliceOutcome{}, vet.TurnBrainContext{}, nil, brainTimedOut, false
	}
	return sliceOut, brainCtx, catalog, brainTimedOut, true
}

func awaitParallelBrain(ctx context.Context, h parallelTurnHandles) (vet.TurnBrainContext, bool, bool) {
	if h.brainCh == nil {
		return vet.TurnBrainContext{}, false, true
	}
	select {
	case res := <-h.brainCh:
		return res, false, true
	default:
	}
	select {
	case res := <-h.brainCh:
		return res, false, true
	case <-h.brainCtx.Done():
		return vet.TurnBrainContext{}, true, ctx.Err() == nil
	case <-ctx.Done():
		return vet.TurnBrainContext{}, true, false
	}
}

func awaitParallelSlice(h parallelTurnHandles) parallelSliceOutcome {
	if h.sliceCh == nil {
		return parallelSliceOutcome{}
	}
	select {
	case res := <-h.sliceCh:
		return res
	default:
	}
	select {
	case res := <-h.sliceCh:
		return res
	case <-h.sliceCtx.Done():
		if h.sliceCancel != nil {
			h.sliceCancel()
		}
		return parallelSliceOutcome{}
	}
}

func awaitParallelSkills(ctx context.Context, ch <-chan []vet.SkillMeta) ([]vet.SkillMeta, bool) {
	if ch == nil {
		return nil, true
	}
	select {
	case catalog := <-ch:
		return catalog, true
	default:
	}
	select {
	case catalog := <-ch:
		return catalog, true
	case <-ctx.Done():
		return nil, false
	}
}

func preseedHookSlicePivots(d Deps, sc *Scope, o Options, dir, convID string, turnNum int, pivots []readledger.CapsulePivot) {
	if len(pivots) == 0 {
		return
	}
	sliceOpts := o
	sliceOpts.Dir = dir
	sliceOpts.Session = convID
	preseedCapsulePivots(d, sc, sliceOpts, turnNum, pivots)
}

func finalizeParallelMeta(home string, sc *Scope, convID string, genID, turnNum int, modelID string, vetRes *vet.Result, brainCtx *vet.TurnBrainContext, meta *telemetry.TurnBannerMeta, sections []string) {
	if sc != nil && sc.St != nil && sc.St.SQLDB != nil {
		filterSessionDedupTurn(sc.St.SQLDB, convID, genID, turnNum, vetRes, brainCtx)
	}
	if nudge := suggestSessionModelNudgeForHome(home, sc, convID, genID, turnNum, modelID, *vetRes); nudge != "" {
		meta.ModelTip = strings.TrimSpace(strings.TrimPrefix(nudge, "model tip: "))
	}
	meta.ExtraSections = sections
	meta.SessionModelResolved = true
}

func launchParallelScopeAndVetSlice(sliceCtx context.Context, d Deps, sliceOpts Options, sc *Scope, prompt string, state readledger.SessionState) chan parallelSliceOutcome {
	ch := make(chan parallelSliceOutcome, 1)
	if sc == nil || sc.St == nil || len(sc.Projects) == 0 {
		ch <- parallelSliceOutcome{}
		return ch
	}
	root := sc.Projects[0]
	projects := append([]string(nil), sc.Projects...)
	dbPath := sc.St.Path
	go func() {
		ch <- runParallelScopeAndVetSlice(sliceCtx, d, sliceOpts, root, projects, dbPath, prompt, state)
	}()
	return ch
}

func runParallelScopeAndVetSlice(sliceCtx context.Context, d Deps, sliceOpts Options, root string, projects []string, dbPath, prompt string, state readledger.SessionState) parallelSliceOutcome {
	if sliceCtx.Err() != nil {
		return parallelSliceOutcome{}
	}
	out := reconcileParallelScopeSnapshot(sliceCtx, d, root, dbPath)
	if prompt == "" || sliceCtx.Err() != nil || d.OpenSliceReadOnlyStore == nil {
		return out
	}
	roSt := d.OpenSliceReadOnlyStore(dbPath)
	if roSt == nil {
		return out
	}
	defer roSt.Close()
	roScope := &Scope{St: roSt, Projects: projects, LeftOut: out.leftOut}
	out.slice, out.pivots = BuildVetSliceCtx(sliceCtx, d, roScope, sliceOpts, prompt, state, false)
	return out
}

func reconcileParallelScopeSnapshot(sliceCtx context.Context, d Deps, root, dbPath string) parallelSliceOutcome {
	if d.OpenShortTimeoutStoreCtx == nil {
		return parallelSliceOutcome{}
	}
	prepSt := d.OpenShortTimeoutStoreCtx(sliceCtx, dbPath)
	if prepSt == nil {
		return parallelSliceOutcome{}
	}
	defer func() { _ = prepSt.CloseErr() }()
	prepSc, dirtySummary, fast, err := prepareHookScopeReady(sliceCtx, d, root, prepSt)
	if err != nil || prepSc == nil {
		return parallelSliceOutcome{}
	}
	return parallelSliceOutcome{
		dirtySummary: dirtySummary,
		fastPath:     fast,
		leftOut:      append([]string(nil), prepSc.LeftOut...),
	}
}

func hasNewSliceFiles(promptFiles, sliceFiles []string) bool {
	if len(sliceFiles) == 0 {
		return false
	}
	for _, sf := range sliceFiles {
		if !slices.Contains(promptFiles, sf) {
			return true
		}
	}
	return false
}

func hasRemainingBudget(ctx context.Context, minRem time.Duration) bool {
	if ctx == nil {
		return true
	}
	if ctx.Err() != nil {
		return false
	}
	if dl, ok := ctx.Deadline(); ok {
		return time.Until(dl) >= minRem
	}
	return true
}
