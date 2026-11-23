package search

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func handlePreInvocation(ctx context.Context, o Options, dir string, in HookInput, out io.Writer) error {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return err
	}
	rawPrompt := StripSandboxPreamble(strings.TrimSpace(in.LastUserInput))
	cleanPrompt := strings.TrimSpace(strings.TrimPrefix(rawPrompt, "!"))
	sc, state, subInfo, skip, err := preparePreInvocationFast(ctx, o, dir, in, rawPrompt)
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
		defer sc.close()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if subInfo.lightweight {
		sysMsg, banner := handleLightweightSubagentTurn(ctx, o, sc, dir, in, state, subInfo, rawPrompt, cleanPrompt, start)
		writePreInvocation(out, sysMsg, banner)
		return nil
	}
	sysMsg, banner := handleParallelHookTurn(ctx, o, sc, dir, in, state, rawPrompt, cleanPrompt, start)
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

func dynamicDirtyFastPathCap(ctx context.Context, existingFiles int) int {
	if !hasRemainingBudget(ctx, 60*time.Millisecond) {
		return 0
	}
	if existingFiles >= 2000 {
		return 3
	}
	return 8
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
	if existingFiles > 0 {
		if sc, summary, fast, handled, err := tryDirtyFastReconcile(ctx, root, st, existingFiles); handled {
			if err != nil {
				return nil, "", false, err
			}
			closeOnErr = false
			return sc, summary, fast, nil
		}
	}
	sc, summary, fast, err := reconcileHookScopeView(ctx, root, st, existingFiles)
	if err != nil {
		return nil, "", false, err
	}
	closeOnErr = false
	return sc, summary, fast, nil
}

func tryDirtyFastReconcile(ctx context.Context, root string, st *store, existingFiles int) (*scope, string, bool, bool, error) {
	dirtyEntries, dirtyLabels, ok := detectDirtyPathsFast(ctx, root, st)
	if !ok {
		return nil, "", false, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, "", false, true, err
	}
	if len(dirtyEntries) == 0 {
		return &scope{st: st, projects: []string{root}}, "", false, true, nil
	}
	capLimit := dynamicDirtyFastPathCap(ctx, existingFiles)
	if len(dirtyEntries) > capLimit {
		return &scope{st: st, projects: []string{root}}, formatDirtyFastPath(dirtyLabels, len(dirtyEntries), capLimit), true, true, nil
	}
	if _, err := st.applyDirtyPaths(ctx, root, dirtyEntries, existingFiles); err != nil {
		return nil, "", false, true, err
	}
	if existingFiles <= maxWarmSigEntries {
		st.saveGitWarmSig(root)
	}
	return &scope{st: st, projects: []string{root}}, "", false, true, nil
}

func detectDirtyPathsFast(ctx context.Context, root string, st *store) ([]entry, []string, bool) {
	if ctx.Err() != nil || !st.matchesGitIdxHeadSig(ctx, root) {
		return nil, nil, false
	}
	gitDir := resolveGitWorktreeDir(root)
	if gitDir == "" {
		return nil, nil, false
	}
	indexPath := filepath.Join(gitDir, "index")
	idxFi, err := os.Lstat(indexPath)
	if err != nil || !idxFi.Mode().IsRegular() {
		return nil, nil, false
	}
	entries, hasIndex, err := readGitIndex(indexPath)
	if !hasIndex || err != nil || len(entries) == 0 {
		return nil, nil, false
	}
	indexedFiles, nonTracked, ok := loadIndexedFilesCtx(ctx, st, root)
	if !ok {
		return nil, nil, false
	}
	fsRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, false
	}
	defer fsRoot.Close()
	dirtyEntries, dirtyLabels, ok := scanDirtyTrackedEntries(ctx, root, fsRoot, entries, idxFi.ModTime().Unix(), indexedFiles, nonTracked)
	if !ok {
		return nil, nil, false
	}
	return scanDirtyUntrackedEntries(ctx, root, fsRoot, nonTracked, dirtyEntries, dirtyLabels)
}

func loadIndexedFilesCtx(ctx context.Context, st *store, root string) (map[string]bool, map[string]entry, bool) {
	rows, err := st.db.QueryContext(ctx, `SELECT path, oid, state FROM files WHERE project = ?`, root)
	if err != nil {
		return nil, nil, false
	}
	defer rows.Close()
	indexedFiles := map[string]bool{}
	nonTracked := map[string]entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.path, &e.oid, &e.state); err != nil {
			return nil, nil, false
		}
		indexedFiles[e.path] = true
		if e.state != "tracked" {
			nonTracked[e.path] = e
		}
	}
	return indexedFiles, nonTracked, rows.Err() == nil
}

func scanDirtyTrackedEntries(ctx context.Context, root string, fsRoot *os.Root, entries []gitIndexEntry, idxSec int64, indexedFiles map[string]bool, nonTracked map[string]entry) ([]entry, []string, bool) {
	var dirty []entry
	var labels []string
	for _, e := range entries {
		if ctx.Err() != nil {
			return nil, nil, false
		}
		if e.Stage != 0 || (e.Mode&0o170000) == 0o160000 || (e.Mode&0o170000) == 0o120000 {
			continue
		}
		if !indexed(e.Path) || harness.SecretShaped(path.Base(e.Path)) {
			continue
		}
		if isNoIndexMarker(e.Path) || !filepath.IsLocal(filepath.FromSlash(e.Path)) {
			return nil, nil, false
		}
		ch, lbl, changed := checkDirtyTrackedEntry(root, fsRoot, e, idxSec, indexedFiles, nonTracked)
		if changed {
			dirty = append(dirty, ch)
			if lbl != "" {
				labels = append(labels, lbl)
			}
		}
	}
	return dirty, labels, true
}

func checkDirtyTrackedEntry(root string, fsRoot *os.Root, e gitIndexEntry, idxSec int64, indexedFiles map[string]bool, nonTracked map[string]entry) (entry, string, bool) {
	local := filepath.FromSlash(e.Path)
	inIndex := indexedFiles[e.Path]
	prev, wasDirty := nonTracked[e.Path]
	delete(nonTracked, e.Path)
	fi, err := os.Lstat(filepath.Join(root, local))
	if err != nil || !fi.Mode().IsRegular() {
		if inIndex {
			return entry{path: e.Path, state: "deleted"}, "D " + e.Path, true
		}
		return entry{}, "", false
	}
	if fi.Size() > maxFileBytes {
		if inIndex {
			return entry{path: e.Path, state: "deleted"}, "", true
		}
		return entry{}, "", false
	}
	mt := fi.ModTime()
	statClean := fi.Size() == int64(e.Size) && mt.Unix() == int64(e.MTimeSec) &&
		uint32(mt.Nanosecond()) == e.MTimeNsec && e.MTimeSec != 0 &&
		(e.MTimeNsec != 0 || int64(e.MTimeSec) < idxSec)
	idxOID := fmt.Sprintf("%x", e.SHA1)
	if statClean {
		if !inIndex || (wasDirty && prev.oid != idxOID) {
			return entry{path: e.Path, oid: idxOID, state: "tracked"}, "", true
		}
		return entry{}, "", false
	}
	data, err := readAll(fsRoot, local)
	if err != nil {
		if inIndex {
			return entry{path: e.Path, state: "deleted"}, "", true
		}
		return entry{}, "", false
	}
	oid := blobID(data)
	if oid == idxOID {
		if !inIndex || (wasDirty && prev.oid != idxOID) {
			return entry{path: e.Path, oid: idxOID, state: "tracked"}, "", true
		}
		return entry{}, "", false
	}
	if !inIndex || !wasDirty || prev.oid != oid {
		return entry{path: e.Path, oid: oid, state: "modified"}, "M " + e.Path, true
	}
	return entry{}, "", false
}

func scanDirtyUntrackedEntries(ctx context.Context, root string, fsRoot *os.Root, remaining map[string]entry, dirty []entry, labels []string) ([]entry, []string, bool) {
	if ctx.Err() != nil {
		return nil, nil, false
	}
	out, err := gitCtx(ctx, root, nil, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, nil, false
	}
	seenUntracked := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		if isNoIndexMarker(p) {
			return nil, nil, false
		}
		if !indexed(p) || harness.SecretShaped(path.Base(p)) {
			continue
		}
		seenUntracked[p] = true
		fi, err := fsRoot.Lstat(filepath.FromSlash(p))
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFileBytes {
			continue
		}
		data, err := readAll(fsRoot, filepath.FromSlash(p))
		if err != nil {
			continue
		}
		oid := blobID(data)
		if prev, ok := remaining[p]; ok && prev.oid == oid {
			continue
		}
		dirty = append(dirty, entry{path: p, oid: oid, state: "untracked"})
		labels = append(labels, "?? "+p)
	}
	for p, prev := range remaining {
		if prev.state == "untracked" && !seenUntracked[p] {
			dirty = append(dirty, entry{path: p, state: "deleted"})
			labels = append(labels, "D "+p)
		}
	}
	return dirty, labels, true
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
	capLimit := hookMaxDirtyFastPath
	if existingFiles > 0 {
		capLimit = dynamicDirtyFastPathCap(ctx, existingFiles)
	}
	if existingFiles > 0 && diffN > capLimit {
		return &scope{st: st, projects: []string{root}, leftOut: leftOut}, formatDirtyFastPath(dirtyPaths, diffN, capLimit), true, nil
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

func formatDirtyFastPath(dirtyPaths []string, diffN, threshold int) string {
	var b strings.Builder
	count := max(len(dirtyPaths), diffN)
	fmt.Fprintf(&b, "# fast-path: %d modified/untracked files (> %d threshold; serving cached AST snapshot)\n", count, threshold)
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
	if hasBrainTurnContext(o) {
		brainCtx = invokeBrainTurnContext(context.Background(), o, o.Dir, cleanPrompt, slice.Files)
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
	res, _ := sc.buildVetSliceCtx(context.Background(), o, prompt, state, preseedLedger)
	return res
}

func (sc *scope) buildVetSliceCtx(ctx context.Context, o Options, prompt string, state SessionState, preseedLedger bool) (SliceResult, []CapsulePivot) {
	if sc == nil || sc.st == nil || prompt == "" || (ctx != nil && ctx.Err() != nil) {
		return SliceResult{}, nil
	}
	active := sc.sessionActiveFiles(o.Session, o.ActiveFiles)
	g, err := sc.loadBoundedSymGraphCtx(ctx, prompt, active, false, 1)
	if err != nil || len(g.syms) == 0 || (ctx != nil && ctx.Err() != nil) {
		return SliceResult{}, nil
	}
	hits, _ := countMatchingSymbols(g, prompt)
	ranked, confident := rankAndCheckConfidence(g, prompt, active)
	if !confident && (ctx == nil || ctx.Err() == nil) {
		ranked, confident = sc.ftsFallbackRanked(g, prompt, active)
	}
	if ctx != nil && ctx.Err() != nil {
		return SliceResult{}, nil
	}
	pkgComms := g.packageCommunities()
	if !confident && !state.Compacted {
		return SliceResult{SymbolHits: hits, Files: nil, PkgCommunities: pkgComms}, nil
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
	}, pivots
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
	tiers := g.selectSliceTiers(prompt, ranked, 1)
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
