package search

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

func handlePreInvocation(ctx context.Context, o Options, dir string, in HookInput, out io.Writer) error {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return err
	}
	rawPrompt := StripSandboxPreamble(strings.TrimSpace(in.LastUserInput))
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(rawPrompt, "!"))
	sc, state, skip, dirtySummary, fast, err := preparePreInvocationState(ctx, dir, in, rawPrompt)
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		if in.InvocationNum > 1 {
			writePreInvocation(out, "", "")
			return nil
		}
		writeStoreDegraded(out, err)
		return nil
	}
	if skip {
		writePreInvocation(out, "", "")
		return nil
	}
	defer sc.close()
	if err := ctx.Err(); err != nil {
		return err
	}
	cat, hasCat := loadModelsCatalog(o.Home)
	modelInfo := resolvePreInvocationModelInfo(ctx, o.Home, cat, hasCat, sc, in)
	sections, meta, slice := buildHealthyHookTurn(o, sc, dir, in.ConversationID, modelInfo.ID, rawPrompt, cleanPrompt, state, dirtySummary, fast)
	if err := ctx.Err(); err != nil {
		return err
	}
	sysMsg, banner := buildHookEnvelopeWithModelTimed(ctx, o, cat, hasCat, sc, dir, in.ConversationID, modelInfo, in, state, rawPrompt, cleanPrompt, slice, sections, meta, start)
	writePreInvocation(out, sysMsg, banner)
	return nil
}

func preparePreInvocationState(ctx context.Context, dir string, in HookInput, rawPrompt string) (*scope, SessionState, bool, string, bool, error) {
	if err := ensureHookProjectReady(dir); err != nil {
		return nil, SessionState{}, false, "", false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, SessionState{}, false, "", false, err
	}
	st, root, err := openLedgerStore(dir)
	if err != nil {
		return nil, SessionState{}, false, "", false, err
	}
	ownSt := true
	defer func() {
		if ownSt && st != nil {
			st.close()
		}
	}()
	state, skip, err := checkTurnOrMidTurnWithStore(ctx, st, in, rawPrompt)
	if err != nil || skip {
		return nil, state, skip, "", false, err
	}
	sc, dirtySummary, fast, err := prepareHookScopeReady(ctx, root, st)
	if err != nil {
		return nil, SessionState{}, false, "", false, err
	}
	ownSt = false
	return sc, state, false, dirtySummary, fast, nil
}

func prepareHookScope(dir string) (*scope, string, bool, error) {
	if err := ensureHookProjectReady(dir); err != nil {
		return nil, "", false, err
	}
	root, _, ok := findGitRootFast(dir)
	if !ok {
		return nil, "", false, errNotCheckout
	}
	return prepareHookScopeReady(context.Background(), root, nil)
}

func prepareHookScopeReady(ctx context.Context, root string, st *store) (*scope, string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	closeOnErr := false
	if st == nil {
		if err := guardProject(root); err != nil {
			return nil, "", false, err
		}
		opened, err := openStore(projectDB(root), io.Discard)
		if err != nil {
			return nil, "", false, err
		}
		st = opened
		closeOnErr = true
	}
	defer func() {
		if closeOnErr && st != nil {
			st.close()
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	var existingFiles int
	_ = st.db.QueryRowContext(ctx, `SELECT count(*) FROM files WHERE project = ?`, root).Scan(&existingFiles)
	if existingFiles > 0 && st.matchesGitWarmSig(ctx, root) {
		closeOnErr = false
		return &scope{st: st, projects: []string{root}}, "", false, nil
	}
	sc, summary, fast, err := reconcileHookScopeView(ctx, root, st, existingFiles)
	if err != nil {
		return nil, "", false, err
	}
	closeOnErr = false
	return sc, summary, fast, nil
}

func reconcileHookScopeView(ctx context.Context, root string, st *store, existingFiles int) (*scope, string, bool, error) {
	view, leftOut, err := gitView(root)
	if err != nil {
		return nil, "", false, err
	}
	dirtyPaths := collectDirtyPaths(view)
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	diffN, _ := st.diffCount(source{project: root, dir: root, view: view})
	if existingFiles > 0 && diffN > hookMaxDirtyFastPath {
		return &scope{st: st, projects: []string{root}, leftOut: leftOut}, formatDirtyFastPath(dirtyPaths, diffN), true, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	if _, err := st.apply(source{project: root, dir: root, view: view}); err != nil {
		return nil, "", false, err
	}
	if len(leftOut) == 0 {
		st.saveGitWarmSig(root)
	}
	return &scope{st: st, projects: []string{root}, leftOut: leftOut}, "", false, nil
}

func collectDirtyPaths(view []entry) []string {
	var out []string
	for _, e := range view {
		if e.state == "modified" {
			out = append(out, "M "+e.path)
		} else if e.state == "untracked" {
			out = append(out, "?? "+e.path)
		}
	}
	return out
}

func formatDirtyFastPath(dirtyPaths []string, diffN int) string {
	var b strings.Builder
	count := max(len(dirtyPaths), diffN)
	fmt.Fprintf(&b, "# fast-path: %d modified/untracked files (> %d threshold; serving cached AST snapshot)\n", count, hookMaxDirtyFastPath)
	limit := min(20, len(dirtyPaths))
	for i := 0; i < limit; i++ {
		b.WriteString("  " + dirtyPaths[i] + "\n")
	}
	if len(dirtyPaths) > limit {
		fmt.Fprintf(&b, "  ... and %d more\n", len(dirtyPaths)-limit)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (sc *scope) runVet(o Options, out io.Writer) error {
	prompt := StripSandboxPreamble(strings.TrimSpace(o.Vet))
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(prompt, "!"))
	slice := sc.buildVetSlice(o, cleanPrompt, SessionState{}, false)
	var prev *VetHistoryRow
	var recentSkills map[string]bool
	var recentPivots []string
	if sc != nil && sc.st != nil && o.Session != "" {
		prev, _ = sc.st.lastVetHistory(o.Session, 0)
		recentSkills, recentPivots, _ = sc.st.recentSuggestedSkills(o.Session, 0, 3)
	}
	intent := ClassifyIntent(cleanPrompt, slice.SymbolHits)
	var brainCtx TurnBrainContext
	if o.BrainTurnContext != nil {
		brainCtx = o.BrainTurnContext(o.Dir, o.Home, cleanPrompt, slice.Files)
	}
	var catalog []SkillMeta
	if o.SkillCatalog != nil {
		catalog = o.SkillCatalog(o.Home)
	}
	res := vetPromptCoreWithHistory(prompt, intent, slice, brainCtx, catalog, prev, recentSkills, recentPivots)
	sysMsg, banner := BuildTurnEnvelope(&res, slice, brainCtx, nil, nil)
	res.SystemMessage = sysMsg
	res.UserMarkdownBanner = banner
	if sc != nil && sc.st != nil && o.Session != "" && prompt != "" {
		nextTurn := 1
		if prev != nil {
			nextTurn = prev.TurnNum + 1
		}
		pivotFiles := extractPromptAndSliceFiles(cleanPrompt, slice.Files)
		_ = sc.st.recordVetHistory(o.Session, nextTurn, prompt, res, pivotFiles)
	}
	_, err := io.WriteString(out, FormatVetResult(res))
	return err
}

func (sc *scope) buildVetSlice(o Options, prompt string, state SessionState, preseedLedger bool) SliceResult {
	if sc == nil || sc.st == nil || prompt == "" {
		return SliceResult{}
	}
	active := sc.sessionActiveFiles(o.Session, o.ActiveFiles)
	g, err := sc.loadBoundedSymGraph(prompt, active, false, 1)
	if err != nil || len(g.syms) == 0 {
		return SliceResult{}
	}
	hits, _ := countMatchingSymbols(g, prompt)
	ranked, confident := rankAndCheckConfidence(g, prompt, active)
	if !confident {
		ranked, confident = sc.ftsFallbackRanked(g, prompt, active)
	}
	pkgComms := g.packageCommunities()
	if !confident && !state.Compacted {
		return SliceResult{SymbolHits: hits, Files: nil, PkgCommunities: pkgComms}
	}
	capsule, pivots, symFiles, symCount := sc.renderHookOrVetCapsule(o, g, prompt, ranked)
	if capsule != "" && preseedLedger && len(pivots) > 0 {
		sc.preseedCapsulePivots(o, state.TurnCount, pivots)
	}
	return SliceResult{
		Capsule:        capsule,
		Pivots:         symCount,
		SymbolHits:     hits,
		Files:          symFiles,
		Confident:      confident,
		PkgCommunities: pkgComms,
	}
}

func (sc *scope) preseedCapsulePivots(o Options, turnNum int, pivots []CapsulePivot) {
	if sc != nil && sc.st != nil && len(sc.projects) == 1 && sc.projects[0] != "" {
		root := sc.projects[0]
		resolved := make([]resolvedCapsulePivot, 0, len(pivots))
		for _, p := range pivots {
			rp := CanonicalizePath(root, p.Path)
			if sumHex, _, totalLines, ok := readFileDigestAndLines(rp); ok {
				s, e := normalizeLineRange(p.StartLine, p.EndLine, totalLines)
				resolved = append(resolved, resolvedCapsulePivot{resolved: rp, sumHex: sumHex, start: s, end: e})
			}
		}
		if len(resolved) > 0 {
			_ = sc.st.upsertCapsulePivotsTx(o.Session, turnNum, resolved)
		}
		return
	}
	_ = RegisterCapsulePivotsWithHome(o.Dir, o.Home, o.Session, turnNum, pivots)
}

func countMatchingSymbols(g *symGraph, prompt string) (int, map[string]bool) {
	terms := parseQueryTerms(prompt)
	hits := 0
	fileSet := map[string]bool{}
	for _, s := range g.syms {
		if matchesPromptTerms(s, terms) {
			hits++
			fileSet[s.Path] = true
		}
	}
	return hits, fileSet
}

func (sc *scope) renderHookOrVetCapsule(o Options, g *symGraph, prompt string, ranked []RankedSymbol) (string, []CapsulePivot, []string, int) {
	sliceOpts := o
	sliceOpts.Slice = prompt
	if o.BrainRefsForDir != nil && o.Dir != "" {
		sliceOpts.BrainRefs = func(target string) []LinkedRecord { return o.BrainRefsForDir(o.Dir, target) }
	}
	tiers := g.selectSliceTiers(prompt, ranked, defaultSliceHops)
	capsule := strings.TrimSpace(sc.renderCompactHookCapsule(sliceOpts, g, tiers, compactHookMaxBytes))
	if capsule == "" {
		return "", nil, nil, 0
	}
	pivots, files, symCount := extractCompactCapsuleMeta(capsule)
	return capsule, pivots, files, symCount
}

func rankAndCheckConfidence(g *symGraph, prompt string, active map[string]bool) ([]RankedSymbol, bool) {
	if prompt == "" || len(g.syms) == 0 {
		return nil, false
	}
	var longTerms []string
	for _, t := range parseQueryTerms(prompt) {
		if len(t) >= hookMinIdentLen {
			longTerms = append(longTerms, t)
		}
	}
	if len(longTerms) == 0 {
		return nil, false
	}
	matched := map[int]bool{}
	for i, s := range g.syms {
		if s.Kind == "heading" {
			continue
		}
		if matchesPromptTerms(s, longTerms) {
			matched[i] = true
		}
	}
	if len(matched) == 0 {
		return nil, false
	}
	ranked := g.rank(prompt, active)
	if len(ranked) == 0 {
		return nil, false
	}
	median := medianRankScore(g, ranked, matched)
	return ranked, median > 0 && ranked[0].Score >= hookConfidenceRatio*median
}

func medianRankScore(g *symGraph, ranked []RankedSymbol, matched map[int]bool) float64 {
	if len(ranked) >= 4 {
		m := ranked[len(ranked)/2].Score
		if ranked[0].Score >= hookConfidenceRatio*m {
			return m
		}
	}
	neighbors := map[int]bool{}
	for idx := range matched {
		neighbors[idx] = true
		for _, e := range g.out[idx] {
			neighbors[e.toIdx] = true
		}
		for _, e := range g.in[idx] {
			if e.fromIdx >= 0 {
				neighbors[e.fromIdx] = true
			}
		}
	}
	var bg []float64
	for _, s := range ranked {
		if !neighbors[s.Idx] {
			bg = append(bg, s.Score)
		}
	}
	if len(bg) > 0 {
		return bg[len(bg)/2]
	}
	return ranked[0].Score / (hookConfidenceRatio + 1.0)
}
