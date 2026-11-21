package search

import (
	"context"
	"path"
	"strings"
)

const (
	maxBoundedQueryTerms     = 12
	maxBoundedSeedFiles      = 12  // hook 1-hop seed cap (ADR-532)
	maxBoundedCLISeedFiles   = 40  // multi-hop CLI --slice seed cap
	maxBoundedSeedNames      = 80  // multi-hop CLI --slice symbol cap
	maxBoundedHookSeedNames  = 24  // hook 1-hop symbol cap
	maxBoundedInboundRefs    = 100 // hook 1-hop inbound ref cap (ADR-532)
	maxBoundedCLIInboundRefs = 400 // multi-hop CLI --slice inbound ref cap
	maxBoundedTotalFiles     = 96
	maxBoundedHookFiles      = 16
	maxBoundedHop2Files      = 32
)

type boundedGraphLimits struct {
	seedFiles   int
	seedNames   int
	inboundRefs int
	totalFiles  int
}

func boundedGraphLimitsForHops(maxHops int) boundedGraphLimits {
	if maxHops <= 1 {
		return boundedGraphLimits{
			seedFiles:   maxBoundedSeedFiles,
			seedNames:   maxBoundedHookSeedNames,
			inboundRefs: maxBoundedInboundRefs,
			totalFiles:  maxBoundedHookFiles,
		}
	}
	return boundedGraphLimits{
		seedFiles:   maxBoundedCLISeedFiles,
		seedNames:   maxBoundedSeedNames,
		inboundRefs: maxBoundedCLIInboundRefs,
		totalFiles:  maxBoundedTotalFiles,
	}
}

// loadBoundedSymGraph loads only the query-matched seed symbols/files, optional
// FTS5 fallback files, and 1-2 hop neighbors instead of reading all symbols and
// sym_refs across a large repository.
func (sc *scope) loadBoundedSymGraph(prompt string, active map[string]bool, includeDocs bool, maxHops int) (*symGraph, error) {
	return sc.loadBoundedSymGraphCtx(context.Background(), prompt, active, includeDocs, maxHops)
}

func (sc *scope) loadBoundedSymGraphCtx(ctx context.Context, prompt string, active map[string]bool, includeDocs bool, maxHops int) (*symGraph, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sc == nil || sc.st == nil || len(sc.projects) == 0 {
		return newSymGraphWithMeta(nil, nil, nil), nil
	}
	if strings.TrimSpace(prompt) == "" && len(active) == 0 {
		return sc.loadSymGraphOpts(includeDocs)
	}
	lim := boundedGraphLimitsForHops(maxHops)
	terms := parseQueryTerms(prompt)
	seedOIDs, hasExplicitFileSeed, err := querySeedFileOIDsDBCtx(ctx, sc.st.db, sc.projects, includeDocs, active, terms, lim.seedFiles)
	if err != nil {
		return newSymGraphWithMeta(nil, nil, nil), err
	}
	seedSyms, err := querySeedSymbolsDBCtx(ctx, sc.st.db, sc.projects, includeDocs, seedOIDs, hasExplicitFileSeed, terms, lim.seedNames)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seedOIDs, seedSyms, err = sc.mergeBoundedFTSSeedsCtx(ctx, prompt, active, includeDocs, seedOIDs, seedSyms, terms, lim.seedFiles)
	if err != nil {
		return nil, err
	}
	if len(seedOIDs) == 0 && len(seedSyms) == 0 {
		return newSymGraphWithMeta(nil, nil, nil), nil
	}
	allOIDs, inRefs, err := sc.expandBoundedNeighborhoodCtx(ctx, includeDocs, maxHops, lim, seedOIDs, seedSyms, terms)
	if err != nil {
		return nil, err
	}
	return sc.assembleBoundedSymGraphCtx(ctx, allOIDs, inRefs, includeDocs)
}

func fileMatchesBoundedTerms(filePath string, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	pLow := strings.ToLower(filePath)
	baseLow := strings.ToLower(path.Base(filePath))
	stemLow := strings.TrimSuffix(baseLow, strings.ToLower(path.Ext(baseLow)))
	for _, t := range terms {
		tl := strings.ToLower(t)
		if pLow == tl || baseLow == tl || (len(tl) >= 4 && stemLow == tl) {
			return true
		}
	}
	return false
}

func (sc *scope) mergeBoundedFTSSeedsCtx(ctx context.Context, prompt string, active map[string]bool, includeDocs bool, seedOIDs map[string]bool, seedSyms []RankedSymbol, terms []string, maxSeedFiles int) (map[string]bool, []RankedSymbol, error) {
	if len(active) > 0 || len(seedOIDs) > 0 || hasStrongIdentSeed(seedSyms, terms) {
		return seedOIDs, seedSyms, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	words := extractFTSFallbackWords(prompt)
	minWords := 2
	if len(seedOIDs) == 0 && len(seedSyms) == 0 {
		minWords = 1
	}
	if len(words) < minWords {
		return seedOIDs, seedSyms, nil
	}
	chunks := sc.queryFTSFallbackChunks(words, strings.ToLower(prompt))
	if len(chunks) == 0 {
		return seedOIDs, seedSyms, nil
	}
	matchedFiles, err := queryFilesByPathsDBCtx(ctx, sc.st.db, sc.projects, includeDocs, collectUniqueChunkPaths(chunks))
	if err != nil {
		return nil, nil, err
	}
	extraOIDs := addFTSChunkOIDs(chunks, matchedFiles, seedOIDs, maxSeedFiles)
	if len(extraOIDs) == 0 {
		return seedOIDs, seedSyms, nil
	}
	extraSyms, err := querySymbolsByOIDsDBCtx(ctx, sc.st.db, sc.projects, includeDocs, extraOIDs)
	if err != nil {
		return nil, nil, err
	}
	return seedOIDs, append(seedSyms, extraSyms...), nil
}

func collectUniqueChunkPaths(chunks []ftsChunkHit) []string {
	var paths []string
	seen := map[string]bool{}
	for _, h := range chunks {
		if h.path != "" && !seen[h.path] {
			seen[h.path] = true
			paths = append(paths, h.path)
		}
	}
	return paths
}

func addFTSChunkOIDs(chunks []ftsChunkHit, matchedFiles []indexedFileMeta, seedOIDs map[string]bool, maxSeedFiles int) []string {
	oidByPath := make(map[string]string, len(matchedFiles))
	for _, f := range matchedFiles {
		oidByPath[f.path] = f.oid
	}
	var extraOIDs []string
	for _, h := range chunks {
		if oid := oidByPath[h.path]; oid != "" && !seedOIDs[oid] && len(seedOIDs) < maxSeedFiles {
			seedOIDs[oid] = true
			extraOIDs = append(extraOIDs, oid)
		}
	}
	return extraOIDs
}

func hasStrongIdentSeed(seedSyms []RankedSymbol, terms []string) bool {
	if len(seedSyms) == 0 {
		return false
	}
	for _, t := range terms {
		if len(t) < hookMinIdentLen {
			continue
		}
		tl := strings.ToLower(t)
		for _, s := range seedSyms {
			if strings.Contains(strings.ToLower(s.Name), tl) || strings.Contains(strings.ToLower(s.QualName()), tl) {
				return true
			}
		}
	}
	return false
}

func (sc *scope) expandBoundedNeighborhoodCtx(ctx context.Context, includeDocs bool, maxHops int, lim boundedGraphLimits, seedOIDs map[string]bool, seedSyms []RankedSymbol, terms []string) (map[string]bool, []rawSymRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	allOIDs := cloneBoolMap(seedOIDs)
	maxFiles := lim.totalFiles
	priorityNames, otherNames := partitionPrioritySymNames(seedSyms, terms, lim.seedNames)
	for _, s := range seedSyms {
		addBoundedOID(allOIDs, s.OID, maxFiles)
	}
	if maxHops <= 0 {
		return allOIDs, nil, nil
	}
	frontierOIDs := sortedMapKeys(allOIDs)
	hop1New, inRefs, err := sc.expandSingleHopCtx(ctx, includeDocs, frontierOIDs, priorityNames, otherNames, allOIDs, lim)
	if err != nil {
		return nil, nil, err
	}
	if maxHops >= 2 && len(hop1New) > 0 && len(allOIDs) < maxFiles {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if len(hop1New) > maxBoundedHop2Files {
			hop1New = hop1New[:maxBoundedHop2Files]
		}
		hop1Syms, err := querySymbolsByOIDsDBCtx(ctx, sc.st.db, sc.projects, includeDocs, hop1New)
		if err != nil {
			return nil, nil, err
		}
		pNames, oNames := partitionPrioritySymNames(hop1Syms, terms, 24)
		if _, extraIn, err := sc.expandSingleHopCtx(ctx, includeDocs, hop1New, pNames, oNames, allOIDs, lim); err == nil {
			inRefs = append(inRefs, extraIn...)
		} else {
			return nil, nil, err
		}
	}
	return allOIDs, inRefs, nil
}

func (sc *scope) expandSingleHopCtx(ctx context.Context, includeDocs bool, fromOIDs, priorityNames, otherNames []string, allOIDs map[string]bool, lim boundedGraphLimits) ([]string, []rawSymRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	outRefs, err := querySymRefsByOIDsDBCtx(ctx, sc.st.db, sc.projects, includeDocs, fromOIDs)
	if err != nil {
		return nil, nil, err
	}
	inOtherNames := otherNames
	if len(priorityNames) > 0 {
		inOtherNames = nil
		outRefs = filterPriorityOutRefs(outRefs, priorityNames)
	}
	inRefs, err := sc.queryPrioritizedInboundRefsCtx(ctx, includeDocs, priorityNames, inOtherNames, lim.inboundRefs)
	if err != nil {
		return nil, nil, err
	}
	outTargets := collectDistinctOutTargetNames(outRefs, lim.seedNames)
	outOIDs, err := queryOIDsBySymbolNamesDBCtx(ctx, sc.st.db, sc.projects, includeDocs, outTargets)
	if err != nil {
		return nil, nil, err
	}
	var added []string
	for _, oid := range outOIDs {
		if !allOIDs[oid] && addBoundedOID(allOIDs, oid, lim.totalFiles) {
			added = append(added, oid)
		}
	}
	for _, r := range inRefs {
		if !allOIDs[r.oid] && addBoundedOID(allOIDs, r.oid, lim.totalFiles) {
			added = append(added, r.oid)
		}
	}
	return added, inRefs, nil
}

func filterPriorityOutRefs(outRefs []rawSymRef, priorityNames []string) []rawSymRef {
	if len(priorityNames) == 0 {
		return outRefs
	}
	pSet := make(map[string]bool, len(priorityNames))
	for _, n := range priorityNames {
		pSet[n] = true
	}
	out := make([]rawSymRef, 0, len(outRefs))
	for _, r := range outRefs {
		if pSet[r.fromSym] {
			out = append(out, r)
			continue
		}
		if dot := strings.LastIndexByte(r.fromSym, '.'); dot >= 0 && pSet[r.fromSym[dot+1:]] {
			out = append(out, r)
		}
	}
	return out
}

func (sc *scope) queryPrioritizedInboundRefsCtx(ctx context.Context, includeDocs bool, priorityNames, otherNames []string, maxInbound int) ([]rawSymRef, error) {
	var inRefs []rawSymRef
	if len(priorityNames) > 0 {
		refs, err := queryInboundSymRefsDBCtx(ctx, sc.st.db, sc.projects, includeDocs, priorityNames, maxInbound)
		if err != nil {
			return nil, err
		}
		inRefs = append(inRefs, refs...)
	}
	rem := maxInbound - len(inRefs)
	if rem > 0 && len(otherNames) > 0 {
		refs, err := queryInboundSymRefsDBCtx(ctx, sc.st.db, sc.projects, includeDocs, otherNames, rem)
		if err != nil {
			return nil, err
		}
		inRefs = append(inRefs, refs...)
	}
	return inRefs, nil
}

func (sc *scope) assembleBoundedSymGraphCtx(ctx context.Context, allOIDs map[string]bool, inRefs []rawSymRef, includeDocs bool) (*symGraph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	oidList := sortedMapKeys(allOIDs)
	if len(oidList) == 0 {
		return newSymGraphWithMeta(nil, nil, nil), nil
	}
	boundedFiles, err := queryFilesByOIDsDBCtx(ctx, sc.st.db, sc.projects, includeDocs, oidList)
	if err != nil {
		return nil, err
	}
	syms, err := querySymbolsByOIDsDBCtx(ctx, sc.st.db, sc.projects, includeDocs, oidList)
	if err != nil {
		return nil, err
	}
	outRefs, err := querySymRefsByOIDsDBCtx(ctx, sc.st.db, sc.projects, includeDocs, oidList)
	if err != nil {
		return nil, err
	}
	rawRefs := mergeUniqueRawRefs(outRefs, inRefs)
	g := newSymGraphWithMeta(syms, boundedFiles, loadGoModules(sc))
	for _, r := range rawRefs {
		if r.kind == "import" {
			g.addImportRef(r)
		}
	}
	for _, r := range rawRefs {
		if r.kind != "import" {
			g.addRawRef(r)
		}
	}
	return g, nil
}
