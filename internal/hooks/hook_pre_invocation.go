package hooks

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/hooks/guard"
	"github.com/kesokaj/fyr/internal/hooks/telemetry"
	"github.com/kesokaj/fyr/internal/readledger"
	"github.com/kesokaj/fyr/internal/symgraph"
	"github.com/kesokaj/fyr/internal/vet"
)

func handlePreInvocation(ctx context.Context, d Deps, o Options, dir string, in HookInput, out io.Writer) error {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return err
	}
	rawPrompt := StripSandboxPreamble(strings.TrimSpace(in.LastUserInput))
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(rawPrompt, "!"))
	sc, state, subInfo, skip, err := preparePreInvocationFast(ctx, d, o, dir, in, rawPrompt)
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
	if ref := hookTimeoutStoreRefFromCtx(ctx); ref != nil {
		defer ref.release()
	} else {
		defer sc.Close()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if subInfo.lightweight {
		sysMsg, banner := handleLightweightSubagentTurn(ctx, d, o, sc, dir, in, state, subInfo, rawPrompt, cleanPrompt, start)
		writePreInvocation(out, sysMsg, banner)
		return nil
	}
	sysMsg, banner := HandleParallelHookTurn(ctx, d, o, sc, dir, in, state, rawPrompt, cleanPrompt, start)
	writePreInvocation(out, sysMsg, banner)
	return nil
}

// PrepareHookScope ensures dir is ready and reconciles its hook scope.
func PrepareHookScope(d Deps, dir string) (*Scope, error) {
	if err := guard.EnsureHookProjectReady(dir); err != nil {
		return nil, err
	}
	root, _, ok := guard.FindGitRootFast(dir)
	if !ok {
		return nil, guard.ErrNotCheckout
	}
	sc, _, _, err := prepareHookScopeReady(context.Background(), d, root, nil)
	return sc, err
}

func openHookScopeStore(d Deps, root string) (*Store, error) {
	if d.GuardProject != nil {
		if err := d.GuardProject(root); err != nil {
			return nil, err
		}
	}
	return d.OpenStore(d.ProjectDB(root))
}

func prepareHookScopeReady(ctx context.Context, d Deps, root string, st *Store) (*Scope, string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	owned := st == nil
	if owned {
		opened, err := openHookScopeStore(d, root)
		if err != nil {
			return nil, "", false, err
		}
		st = opened
	}
	sc, summary, fast, err := reconcileReadyStore(ctx, d, root, st)
	if err != nil {
		if owned {
			st.Close()
		}
		return nil, "", false, err
	}
	return sc, summary, fast, nil
}

func reconcileReadyStore(ctx context.Context, d Deps, root string, st *Store) (*Scope, string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	var existingFiles int
	_ = st.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM files WHERE project = ?`, root).Scan(&existingFiles)
	if existingFiles > 0 && d.MatchesGitWarmSig != nil && d.MatchesGitWarmSig(ctx, st, root) {
		return &Scope{St: st, Projects: []string{root}}, "", false, nil
	}
	if existingFiles > 0 {
		if sc, summary, fast, handled, err := TryDirtyFastReconcile(ctx, d, root, st, existingFiles); handled {
			return sc, summary, fast, err
		}
	}
	return reconcileHookScopeView(ctx, d, root, st, existingFiles)
}

// RunVet executes `fyr search --vet` / `fyr vet` against sc and writes the formatted result.
func RunVet(d Deps, sc *Scope, o Options, out io.Writer) error {
	prompt := StripSandboxPreamble(strings.TrimSpace(o.Vet))
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(prompt, "!"))
	slice := BuildVetSlice(d, sc, o, cleanPrompt, readledger.SessionState{}, false)
	var prev *vet.HistoryRow
	var recentSkills map[string]bool
	var recentPivots []string
	if sc != nil && sc.St != nil && sc.St.SQLDB != nil && o.Session != "" {
		prev, _ = vet.LastHistoryCtx(context.Background(), sc.St.SQLDB, o.Session, 0)
		recentSkills, recentPivots, _ = vet.RecentSuggestedSkills(sc.St.SQLDB, o.Session, 0)
	}
	intent := vet.ClassifyIntent(cleanPrompt, slice.SymbolHits)
	var brainCtx vet.TurnBrainContext
	if hasBrainTurnContext(o) {
		brainCtx = invokeBrainTurnContext(context.Background(), o, o.Dir, cleanPrompt, slice.Files)
	}
	var catalog []vet.SkillMeta
	if o.SkillCatalog != nil {
		catalog = o.SkillCatalog(o.Home)
	}
	res := vet.VetPromptCoreWithHistory(prompt, intent, slice, brainCtx, catalog, prev, recentSkills, recentPivots)
	sysMsg, banner := telemetry.BuildTurnEnvelopeWithMeta(&res, slice, brainCtx, telemetry.TurnBannerMeta{})
	res.SystemMessage = sysMsg
	res.UserMarkdownBanner = banner
	if sc != nil && sc.St != nil && sc.St.SQLDB != nil && o.Session != "" && prompt != "" {
		nextTurn := 1
		if prev != nil {
			nextTurn = prev.TurnNum + 1
		}
		pivotFiles := vet.ExtractPromptAndSliceFiles(cleanPrompt, slice.Files)
		_ = vet.RecordHistoryCtx(context.Background(), sc.St.SQLDB, d.now().Unix(), o.Session, nextTurn, prompt, res, pivotFiles, vet.TurnMetrics{})
	}
	_, err := io.WriteString(out, vet.FormatResult(res))
	return err
}

// BuildVetSlice builds a compact AST slice for prompt vetting.
func BuildVetSlice(d Deps, sc *Scope, o Options, prompt string, state readledger.SessionState, preseedLedger bool) vet.SliceResult {
	res, _ := BuildVetSliceCtx(context.Background(), d, sc, o, prompt, state, preseedLedger)
	return res
}

// BuildVetSliceCtx builds a bounded, context-aware AST slice and returns its capsule pivots.
func BuildVetSliceCtx(ctx context.Context, d Deps, sc *Scope, o Options, prompt string, state readledger.SessionState, preseedLedger bool) (vet.SliceResult, []readledger.CapsulePivot) {
	if sc == nil || sc.St == nil || prompt == "" || (ctx != nil && ctx.Err() != nil) {
		return vet.SliceResult{}, nil
	}
	active := d.SessionActiveFiles(sc, o.Session, o.ActiveFiles)
	g, err := d.LoadBoundedSymGraphCtx(ctx, sc, prompt, active, false, 1)
	if err != nil || len(g.Syms) == 0 || (ctx != nil && ctx.Err() != nil) {
		return vet.SliceResult{}, nil
	}
	hits, _ := countMatchingSymbols(g, prompt)
	ranked, confident := rankAndCheckConfidence(g, prompt, active)
	if !confident && (ctx == nil || ctx.Err() == nil) {
		ranked, confident = FTSFallbackRanked(d, sc, g, prompt, active)
	}
	if ctx != nil && ctx.Err() != nil {
		return vet.SliceResult{}, nil
	}
	pkgComms := g.PackageCommunities()
	if !confident && !state.Compacted {
		return vet.SliceResult{SymbolHits: hits, Files: nil, PkgCommunities: pkgComms}, nil
	}
	capsule, pivots, symFiles, symCount := renderHookOrVetCapsule(d, sc, o, g, prompt, ranked)
	if capsule != "" && preseedLedger && len(pivots) > 0 {
		preseedCapsulePivots(d, sc, o, state.TurnCount, pivots)
	}
	return vet.SliceResult{
		Capsule:        capsule,
		Pivots:         symCount,
		SymbolHits:     hits,
		Files:          symFiles,
		Confident:      confident,
		PkgCommunities: pkgComms,
	}, pivots
}

func preseedCapsulePivots(d Deps, sc *Scope, o Options, turnNum int, pivots []readledger.CapsulePivot) {
	if sc != nil && sc.St != nil && len(sc.Projects) == 1 && sc.Projects[0] != "" {
		root := sc.Projects[0]
		resolved := make([]readledger.ResolvedCapsulePivot, 0, len(pivots))
		for _, p := range pivots {
			rp := readledger.CanonicalizePath(root, p.Path)
			if sumHex, _, totalLines, ok := readledger.ReadFileDigestAndLines(rp); ok {
				s, e := readledger.NormalizeLineRange(p.StartLine, p.EndLine, totalLines)
				resolved = append(resolved, readledger.ResolvedCapsulePivot{Resolved: rp, SumHex: sumHex, Start: s, End: e})
			}
		}
		if len(resolved) > 0 {
			_ = readledger.UpsertCapsulePivotsTx(sc.St, d.now, o.Session, turnNum, resolved)
		}
		return
	}
	if d.RegisterCapsulePivotsWithHome != nil {
		_ = d.RegisterCapsulePivotsWithHome(o.Dir, o.Home, o.Session, turnNum, pivots)
	}
}

func countMatchingSymbols(g *symgraph.Graph, prompt string) (int, map[string]bool) {
	terms := symgraph.ParseQueryTerms(prompt)
	hits := 0
	fileSet := map[string]bool{}
	for _, s := range g.Syms {
		if symgraph.MatchesPromptTerms(s, terms) {
			hits++
			fileSet[s.Path] = true
		}
	}
	return hits, fileSet
}

func renderHookOrVetCapsule(d Deps, sc *Scope, o Options, g *symgraph.Graph, prompt string, ranked []symgraph.RankedSymbol) (string, []readledger.CapsulePivot, []string, int) {
	tiers := g.SelectSliceTiers(prompt, ranked, 1)
	capsule := strings.TrimSpace(d.RenderCompactHookCapsule(sc, o, prompt, g, tiers, symgraph.CompactHookMaxBytes))
	if capsule == "" {
		return "", nil, nil, 0
	}
	pivots, files, symCount := symgraph.ExtractCompactCapsuleMeta(capsule)
	return capsule, pivots, files, symCount
}

func rankAndCheckConfidence(g *symgraph.Graph, prompt string, active map[string]bool) ([]symgraph.RankedSymbol, bool) {
	if prompt == "" || len(g.Syms) == 0 {
		return nil, false
	}
	var longTerms []string
	for _, t := range symgraph.ParseQueryTerms(prompt) {
		if len(t) >= hookMinIdentLen {
			longTerms = append(longTerms, t)
		}
	}
	if len(longTerms) == 0 {
		return nil, false
	}
	matched := map[int]bool{}
	for i, s := range g.Syms {
		if s.Kind == "heading" {
			continue
		}
		if symgraph.MatchesPromptTerms(s, longTerms) {
			matched[i] = true
		}
	}
	if len(matched) == 0 {
		return nil, false
	}
	ranked := g.Rank(prompt, active)
	if len(ranked) == 0 {
		return nil, false
	}
	median := medianRankScore(g, ranked, matched)
	return ranked, median > 0 && ranked[0].Score >= hookConfidenceRatio*median
}

func medianRankScore(g *symgraph.Graph, ranked []symgraph.RankedSymbol, matched map[int]bool) float64 {
	if len(ranked) >= 4 {
		m := ranked[len(ranked)/2].Score
		if ranked[0].Score >= hookConfidenceRatio*m {
			return m
		}
	}
	neighbors := map[int]bool{}
	for idx := range matched {
		neighbors[idx] = true
		for _, e := range g.Out[idx] {
			neighbors[e.ToIdx] = true
		}
		for _, e := range g.In[idx] {
			if e.FromIdx >= 0 {
				neighbors[e.FromIdx] = true
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
