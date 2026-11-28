package search

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func preparePreInvocationFast(ctx context.Context, o Options, dir string, in HookInput, rawPrompt string) (*scope, SessionState, preInvocationSubagentInfo, bool, error) {
	if err := ensureHookProjectReady(dir); err != nil {
		return nil, SessionState{}, preInvocationSubagentInfo{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, SessionState{}, preInvocationSubagentInfo{}, false, err
	}
	st, root, err := openLedgerStore(dir)
	if err != nil {
		return nil, SessionState{}, preInvocationSubagentInfo{}, false, err
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
				st.close()
			}
		}
	}()
	state, skip, err := checkTurnOrMidTurnWithStore(ctx, st, in, rawPrompt)
	if err != nil || skip {
		return nil, state, preInvocationSubagentInfo{}, skip, err
	}
	subInfo := resolvePreInvocationSubagentCtx(ctx, o.Home, st, state.ConversationID, rawPrompt)
	ownSt = false
	return &scope{st: st, projects: []string{root}}, state, subInfo, false, nil
}

func hookSliceDeadline(ctx context.Context, o Options) time.Duration {
	limit := defaultHookSliceDeadline
	if o.hookDeadline > 0 {
		if scaled := o.hookDeadline * 60 / 100; scaled > 0 {
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
	if defaultHookDeadline > 200*time.Millisecond {
		return min(500*time.Millisecond, defaultHookDeadline/4)
	}
	return 50 * time.Millisecond
}

func hookBrainDeadline(ctx context.Context, o Options) time.Duration {
	limit := 50 * time.Millisecond
	if o.hookDeadline == 0 {
		limit = DefaultHookBrainBudget()
	}
	if o.hookDeadline > 0 {
		if scaled := o.hookDeadline / 4; scaled > 0 && scaled < limit {
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
	slice        SliceResult
	pivots       []CapsulePivot
	dirtySummary string
	fastPath     bool
	leftOut      []string
}

type parallelTurnHandles struct {
	sliceCh     chan parallelSliceOutcome
	sliceCtx    context.Context
	sliceCancel context.CancelFunc
	brainCh     chan TurnBrainContext
	brainCtx    context.Context
	brainCancel context.CancelFunc
	skillCh     chan []SkillMeta
}

func (h parallelTurnHandles) cancel() {
	if h.sliceCancel != nil {
		h.sliceCancel()
	}
	if h.brainCancel != nil {
		h.brainCancel()
	}
}

func handleParallelHookTurn(ctx context.Context, o Options, sc *scope, dir string, in HookInput, state SessionState, rawPrompt, cleanPrompt string, start time.Time) (string, string) {
	bypassed := strings.HasPrefix(strings.TrimSpace(rawPrompt), "!")
	initIntent := ClassifyIntent(cleanPrompt, 1)
	skipHeavy := !state.Compacted && !bypassed && (initIntent == IntentApproval || initIntent == IntentTrivial)
	promptFiles := extractPromptAndSliceFiles(cleanPrompt, nil)

	var handles parallelTurnHandles
	if !skipHeavy {
		handles = startParallelTurnWork(ctx, o, sc, dir, in.ConversationID, cleanPrompt, state, promptFiles)
		defer handles.cancel()
	} else if sc != nil && sc.st != nil {
		handles = startParallelScopeOnly(ctx, o, sc, dir, in.ConversationID, state)
		defer handles.cancel()
	}
	cat, hasCat := loadModelsCatalog(o.Home)
	modelInfo := resolvePreInvocationModelInfo(ctx, o.Home, cat, hasCat, sc, in)
	prev, recSk, recPiv := loadTurnHistoryCtx(ctx, sc, in.ConversationID, state)

	sliceOut, brainCtx, catalog, brainTimedOut, ok := collectParallelTurnWork(ctx, handles)
	if !ok || ctx.Err() != nil {
		return "", ""
	}
	if sc != nil && len(sliceOut.leftOut) > 0 {
		sc.leftOut = sliceOut.leftOut
	}
	sections, meta := buildParallelTurnSections(o, sc, dir, in, state, modelInfo.ID, sliceOut.dirtySummary, sliceOut.fastPath)
	if ctx.Err() != nil {
		return "", ""
	}
	slice, pivots := sliceOut.slice, sliceOut.pivots
	if slice.Capsule != "" {
		meta.PivotCount = slice.Pivots
		preseedHookSlicePivots(sc, o, dir, in.ConversationID, state.TurnCount, pivots)
	}
	if !skipHeavy && !brainTimedOut && hasBrainTurnContext(o) && hasNewSliceFiles(promptFiles, slice.Files) && hasRemainingBudget(ctx, 15*time.Millisecond) {
		brainCtx = callBrainTurnContextBounded(ctx, o, dir, cleanPrompt, slice.Files, brainCtx)
	}
	if rawPrompt == "" && len(sections) == 0 && slice.Capsule == "" && !meta.HasSignals() {
		return "", ""
	}
	vetRes := vetPromptCoreWithHistory(rawPrompt, ClassifyIntent(cleanPrompt, slice.SymbolHits), slice, brainCtx, catalog, prev, recSk, recPiv)
	vetRes.SessionModel, vetRes.SessionModelDisplay, vetRes.SessionThinking = modelInfo.ID, modelInfo.Display, modelInfo.Thinking
	finalizeParallelMeta(o.Home, sc, in.ConversationID, state.GenerationID, state.TurnCount, modelInfo.ID, &vetRes, &brainCtx, &meta, sections)
	return finalizeHookEnvelope(ctx, o.Home, cat, hasCat, sc, dir, in.ConversationID, state.TurnCount, rawPrompt, cleanPrompt, in, state, vetRes, slice, brainCtx, meta, sections, start)
}

func hasBrainTurnContext(o Options) bool {
	return o.BrainTurnContextCtx != nil || o.BrainTurnContext != nil
}

func invokeBrainTurnContext(ctx context.Context, o Options, dir, prompt string, files []string) TurnBrainContext {
	if o.BrainTurnContextCtx != nil {
		return o.BrainTurnContextCtx(ctx, dir, o.Home, prompt, files)
	}
	return o.BrainTurnContext(dir, o.Home, prompt, files)
}

func callBrainTurnContextBounded(ctx context.Context, o Options, dir, cleanPrompt string, files []string, fallback TurnBrainContext) TurnBrainContext {
	bctx, cancel := context.WithTimeout(ctx, min(hookBrainDeadline(ctx, o), 35*time.Millisecond))
	defer cancel()
	ch := make(chan TurnBrainContext, 1)
	go func() { ch <- invokeBrainTurnContext(bctx, o, dir, cleanPrompt, files) }()
	select {
	case res := <-ch:
		return res
	case <-bctx.Done():
		return fallback
	}
}

func buildParallelTurnSections(o Options, sc *scope, dir string, in HookInput, state SessionState, modelID, dirtySummary string, fastPath bool) ([]string, TurnBannerMeta) {
	var sections []string
	var meta TurnBannerMeta
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
	if warnFull, warnShort := dedupContextKneeWarning(o.Home, sc, in.ConversationID, modelID, state); warnFull != "" {
		sections = append(sections, warnFull)
		meta.ContextKneeWarning = warnShort
	}
	return sections, meta
}

func startParallelScopeOnly(ctx context.Context, o Options, sc *scope, dir, convID string, state SessionState) parallelTurnHandles {
	sliceCtx, sliceCancel := context.WithTimeout(ctx, hookSliceDeadline(ctx, o))
	sliceOpts := o
	sliceOpts.Dir, sliceOpts.Session = dir, convID
	return parallelTurnHandles{
		sliceCh:     launchParallelScopeAndVetSlice(sliceCtx, sliceOpts, sc, "", state),
		sliceCtx:    sliceCtx,
		sliceCancel: sliceCancel,
	}
}

func startParallelTurnWork(ctx context.Context, o Options, sc *scope, dir, convID, cleanPrompt string, state SessionState, promptFiles []string) parallelTurnHandles {
	sliceCtx, sliceCancel := context.WithTimeout(ctx, hookSliceDeadline(ctx, o))
	sliceOpts := o
	sliceOpts.Dir = dir
	sliceOpts.Session = convID
	h := parallelTurnHandles{
		sliceCh:     launchParallelScopeAndVetSlice(sliceCtx, sliceOpts, sc, cleanPrompt, state),
		sliceCtx:    sliceCtx,
		sliceCancel: sliceCancel,
	}
	if hasBrainTurnContext(o) {
		brainCtx, brainCancel := context.WithTimeout(ctx, hookBrainDeadline(ctx, o))
		h.brainCtx, h.brainCancel = brainCtx, brainCancel
		h.brainCh = make(chan TurnBrainContext, 1)
		go func() {
			h.brainCh <- invokeBrainTurnContext(brainCtx, o, dir, cleanPrompt, promptFiles)
		}()
	}
	if o.SkillCatalog != nil {
		h.skillCh = make(chan []SkillMeta, 1)
		go func() {
			h.skillCh <- o.SkillCatalog(o.Home)
		}()
	}
	return h
}

func loadTurnHistoryCtx(ctx context.Context, sc *scope, convID string, state SessionState) (*VetHistoryRow, map[string]bool, []string) {
	if sc == nil || sc.st == nil {
		return nil, nil, nil
	}
	prev, _ := sc.st.lastVetHistoryCtx(ctx, convID, state.TurnCount)
	if state.Compacted {
		return prev, nil, nil
	}
	recSk, recPiv, _ := sc.st.recentSuggestedSkills(convID, state.TurnCount)
	return prev, recSk, recPiv
}

func collectParallelTurnWork(ctx context.Context, h parallelTurnHandles) (parallelSliceOutcome, TurnBrainContext, []SkillMeta, bool, bool) {
	var (
		sliceOut      parallelSliceOutcome
		brainCtx      TurnBrainContext
		catalog       []SkillMeta
		brainTimedOut bool
	)
	if h.brainCh != nil {
		select {
		case brainCtx = <-h.brainCh:
		default:
			select {
			case brainCtx = <-h.brainCh:
			case <-h.brainCtx.Done():
				if ctx.Err() != nil {
					return parallelSliceOutcome{}, TurnBrainContext{}, nil, true, false
				}
				brainTimedOut = true
			case <-ctx.Done():
				return parallelSliceOutcome{}, TurnBrainContext{}, nil, true, false
			}
		}
	}
	if h.sliceCh != nil {
		select {
		case sliceOut = <-h.sliceCh:
		default:
			select {
			case sliceOut = <-h.sliceCh:
			case <-h.sliceCtx.Done():
				if h.sliceCancel != nil {
					h.sliceCancel()
				}
			}
		}
	}
	if h.skillCh != nil {
		select {
		case catalog = <-h.skillCh:
		default:
			select {
			case catalog = <-h.skillCh:
			case <-ctx.Done():
				return parallelSliceOutcome{}, TurnBrainContext{}, nil, brainTimedOut, false
			}
		}
	}
	return sliceOut, brainCtx, catalog, brainTimedOut, true
}

func preseedHookSlicePivots(sc *scope, o Options, dir, convID string, turnNum int, pivots []CapsulePivot) {
	if len(pivots) == 0 {
		return
	}
	sliceOpts := o
	sliceOpts.Dir = dir
	sliceOpts.Session = convID
	sc.preseedCapsulePivots(sliceOpts, turnNum, pivots)
}

func finalizeParallelMeta(home string, sc *scope, convID string, genID, turnNum int, modelID string, vetRes *VetResult, brainCtx *TurnBrainContext, meta *TurnBannerMeta, sections []string) {
	if sc != nil && sc.st != nil {
		filterSessionDedupTurn(sc.st, convID, genID, turnNum, vetRes, brainCtx)
	}
	if nudge := suggestSessionModelNudgeForHome(home, sc, convID, genID, turnNum, modelID, *vetRes); nudge != "" {
		meta.ModelTip = strings.TrimSpace(strings.TrimPrefix(nudge, "model tip: "))
	}
	meta.ExtraSections = sections
	meta.SessionModelResolved = true
}

func launchParallelScopeAndVetSlice(sliceCtx context.Context, sliceOpts Options, sc *scope, prompt string, state SessionState) chan parallelSliceOutcome {
	ch := make(chan parallelSliceOutcome, 1)
	if sc == nil || sc.st == nil || len(sc.projects) == 0 {
		ch <- parallelSliceOutcome{}
		return ch
	}
	root := sc.projects[0]
	projects := append([]string(nil), sc.projects...)
	dbPath := sc.st.path
	go func() {
		var out parallelSliceOutcome
		if sliceCtx.Err() != nil {
			ch <- out
			return
		}
		if prepSt := openShortTimeoutStoreCtx(sliceCtx, dbPath); prepSt != nil {
			prepSc, dirtySummary, fast, err := prepareHookScopeReady(sliceCtx, root, prepSt)
			if err == nil && prepSc != nil {
				out.dirtySummary = dirtySummary
				out.fastPath = fast
				out.leftOut = append([]string(nil), prepSc.leftOut...)
			}
			_ = prepSt.close()
		}
		if prompt == "" || sliceCtx.Err() != nil {
			ch <- out
			return
		}
		roSt := openSliceReadOnlyStore(dbPath)
		if roSt == nil {
			ch <- out
			return
		}
		defer roSt.close()
		registerFTSTurnCache(roSt.db)
		defer clearFTSTurnCache(roSt.db)
		roScope := &scope{st: roSt, projects: projects, leftOut: out.leftOut}
		out.slice, out.pivots = roScope.buildVetSliceCtx(sliceCtx, sliceOpts, prompt, state, false)
		ch <- out
	}()
	return ch
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
