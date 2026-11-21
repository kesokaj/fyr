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

func preparePreInvocationFast(ctx context.Context, o Options, dir string, in HookInput, rawPrompt string) (*scope, SessionState, preInvocationSubagentInfo, bool, string, bool, error) {
	if err := ensureHookProjectReady(dir); err != nil {
		return nil, SessionState{}, preInvocationSubagentInfo{}, false, "", false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, SessionState{}, preInvocationSubagentInfo{}, false, "", false, err
	}
	st, root, err := openLedgerStore(dir)
	if err != nil {
		return nil, SessionState{}, preInvocationSubagentInfo{}, false, "", false, err
	}
	ownSt := true
	defer func() {
		if ownSt && st != nil {
			st.close()
		}
	}()
	state, skip, err := checkTurnOrMidTurnWithStore(ctx, st, in, rawPrompt)
	if err != nil || skip {
		return nil, state, preInvocationSubagentInfo{}, skip, "", false, err
	}
	subInfo := resolvePreInvocationSubagentCtx(ctx, o.Home, st, state.ConversationID, rawPrompt)
	if subInfo.lightweight {
		ownSt = false
		return &scope{st: st, projects: []string{root}}, state, subInfo, false, "", false, nil
	}
	sc, dirtySummary, fast, err := prepareHookScopeReady(ctx, root, st)
	if err != nil {
		return nil, SessionState{}, preInvocationSubagentInfo{}, false, "", false, err
	}
	ownSt = false
	return sc, state, subInfo, false, dirtySummary, fast, nil
}

func hookSliceDeadline(ctx context.Context, o Options) time.Duration {
	limit := defaultHookSliceDeadline
	if o.hookDeadline > 0 {
		if scaled := o.hookDeadline * 60 / 100; scaled > 0 && scaled < limit {
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
	slice  SliceResult
	pivots []CapsulePivot
}

type parallelTurnHandles struct {
	sliceCh     chan parallelSliceOutcome
	sliceCtx    context.Context
	sliceCancel context.CancelFunc
	brainCh     chan TurnBrainContext
	skillCh     chan []SkillMeta
}

func (h parallelTurnHandles) cancel() {
	if h.sliceCancel != nil {
		h.sliceCancel()
	}
}

func handleParallelHookTurn(ctx context.Context, o Options, sc *scope, dir string, in HookInput, state SessionState, rawPrompt, cleanPrompt, dirtySummary string, fastPath bool, start time.Time) (string, string) {
	cat, hasCat := loadModelsCatalog(o.Home)
	modelInfo := resolvePreInvocationModelInfo(ctx, o.Home, cat, hasCat, sc, in)
	sections, meta := buildParallelTurnSections(o, sc, dir, in, state, modelInfo.ID, dirtySummary, fastPath)
	if ctx.Err() != nil {
		return "", ""
	}
	bypassed := strings.HasPrefix(strings.TrimSpace(rawPrompt), "!")
	initIntent := ClassifyIntent(cleanPrompt, 1)
	skipHeavy := !state.Compacted && !bypassed && (initIntent == IntentApproval || initIntent == IntentTrivial)
	promptFiles := extractPromptAndSliceFiles(cleanPrompt, nil)

	var handles parallelTurnHandles
	if !skipHeavy {
		handles = startParallelTurnWork(ctx, o, sc, dir, in.ConversationID, cleanPrompt, state, promptFiles)
		defer handles.cancel()
	}
	prev, recSk, recPiv := loadTurnHistoryCtx(ctx, sc, in.ConversationID, state)
	slice, pivots, brainCtx, catalog, ok := collectParallelTurnWork(ctx, handles)
	if !ok || ctx.Err() != nil {
		return "", ""
	}
	if slice.Capsule != "" {
		meta.PivotCount = slice.Pivots
		preseedHookSlicePivots(sc, o, dir, in.ConversationID, state.TurnCount, pivots)
	}
	if !skipHeavy && o.BrainTurnContext != nil && hasNewSliceFiles(promptFiles, slice.Files) && hasRemainingBudget(ctx, 15*time.Millisecond) {
		brainCtx = o.BrainTurnContext(dir, o.Home, cleanPrompt, slice.Files)
	}
	if rawPrompt == "" && len(sections) == 0 && slice.Capsule == "" && !meta.hasSignals() {
		return "", ""
	}
	vetRes := vetPromptCoreWithHistory(rawPrompt, ClassifyIntent(cleanPrompt, slice.SymbolHits), slice, brainCtx, catalog, prev, recSk, recPiv)
	vetRes.SessionModel, vetRes.SessionModelDisplay, vetRes.SessionThinking = modelInfo.ID, modelInfo.Display, modelInfo.Thinking
	finalizeParallelMeta(o.Home, sc, in.ConversationID, state.GenerationID, state.TurnCount, modelInfo.ID, &vetRes, &brainCtx, &meta, sections)
	return finalizeHookEnvelope(ctx, o.Home, cat, hasCat, sc, dir, in.ConversationID, state.TurnCount, rawPrompt, cleanPrompt, in, state, vetRes, slice, brainCtx, meta, sections, start)
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

func startParallelTurnWork(ctx context.Context, o Options, sc *scope, dir, convID, cleanPrompt string, state SessionState, promptFiles []string) parallelTurnHandles {
	sliceCtx, sliceCancel := context.WithTimeout(ctx, hookSliceDeadline(ctx, o))
	sliceOpts := o
	sliceOpts.Dir = dir
	sliceOpts.Session = convID
	h := parallelTurnHandles{
		sliceCh:     launchParallelVetSlice(sliceCtx, sliceOpts, sc, cleanPrompt, state),
		sliceCtx:    sliceCtx,
		sliceCancel: sliceCancel,
	}
	if o.BrainTurnContext != nil {
		h.brainCh = make(chan TurnBrainContext, 1)
		go func() {
			h.brainCh <- o.BrainTurnContext(dir, o.Home, cleanPrompt, promptFiles)
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
	recSk, recPiv, _ := sc.st.recentSuggestedSkills(convID, state.TurnCount, 3)
	return prev, recSk, recPiv
}

func collectParallelTurnWork(ctx context.Context, h parallelTurnHandles) (SliceResult, []CapsulePivot, TurnBrainContext, []SkillMeta, bool) {
	var (
		slice    SliceResult
		pivots   []CapsulePivot
		brainCtx TurnBrainContext
		catalog  []SkillMeta
	)
	if h.sliceCh != nil {
		select {
		case res := <-h.sliceCh:
			slice, pivots = res.slice, res.pivots
		case <-h.sliceCtx.Done():
			h.cancel()
		}
	}
	if h.brainCh != nil {
		select {
		case brainCtx = <-h.brainCh:
		case <-ctx.Done():
			return SliceResult{}, nil, TurnBrainContext{}, nil, false
		}
	}
	if h.skillCh != nil {
		select {
		case catalog = <-h.skillCh:
		case <-ctx.Done():
			return SliceResult{}, nil, TurnBrainContext{}, nil, false
		}
	}
	return slice, pivots, brainCtx, catalog, true
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

func launchParallelVetSlice(sliceCtx context.Context, sliceOpts Options, sc *scope, prompt string, state SessionState) chan parallelSliceOutcome {
	ch := make(chan parallelSliceOutcome, 1)
	if sc == nil || sc.st == nil || prompt == "" {
		ch <- parallelSliceOutcome{}
		return ch
	}
	projects := append([]string(nil), sc.projects...)
	leftOut := append([]string(nil), sc.leftOut...)
	dbPath := sc.st.path
	go func() {
		roSt := openSliceReadOnlyStore(dbPath)
		if roSt == nil {
			ch <- parallelSliceOutcome{}
			return
		}
		defer roSt.close()
		roScope := &scope{st: roSt, projects: projects, leftOut: leftOut}
		s, piv := roScope.buildVetSliceCtx(sliceCtx, sliceOpts, prompt, state, false)
		ch <- parallelSliceOutcome{slice: s, pivots: piv}
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
