package symgraph

import (
	"context"
	"path"
	"strings"
)

const (
	// MaxBoundedQueryTerms caps filtered query terms considered by bounded symbol lookup.
	MaxBoundedQueryTerms = 12
	// MaxBoundedSeedNames caps multi-hop CLI --slice seed symbol names.
	MaxBoundedSeedNames      = 80
	maxBoundedSeedFiles      = 12  // hook 1-hop seed cap (ADR-532)
	maxBoundedCLISeedFiles   = 40  // multi-hop CLI --slice seed cap
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
		seedNames:   MaxBoundedSeedNames,
		inboundRefs: maxBoundedCLIInboundRefs,
		totalFiles:  maxBoundedTotalFiles,
	}
}

// LoadBoundedSymGraph loads only the query-matched seed symbols/files, optional
// FTS5 fallback files, and 1-2 hop neighbors instead of reading all symbols and
// sym_refs across a large repository.
func (sc *Scope) LoadBoundedSymGraph(prompt string, active map[string]bool, includeDocs bool, maxHops int) (*Graph, error) {
	return sc.LoadBoundedSymGraphCtx(context.Background(), prompt, active, includeDocs, maxHops)
}

// LoadBoundedSymGraphCtx is the context-aware bounded symbol graph loader.
func (sc *Scope) LoadBoundedSymGraphCtx(ctx context.Context, prompt string, active map[string]bool, includeDocs bool, maxHops int) (*Graph, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sc == nil || sc.DB == nil || len(sc.Projects) == 0 {
		return NewGraphWithMeta(nil, nil, nil), nil
	}
	if strings.TrimSpace(prompt) == "" && len(active) == 0 {
		return sc.LoadSymGraphOpts(includeDocs)
	}
	lim := boundedGraphLimitsForHops(maxHops)
	terms := ParseQueryTerms(prompt)
	seedOIDs, hasExplicitFileSeed, err := querySeedFileOIDsDBCtx(ctx, sc.DB, sc.Projects, includeDocs, active, terms, lim.seedFiles)
	if err != nil {
		return NewGraphWithMeta(nil, nil, nil), err
	}
	seedSyms, err := querySeedSymbolsDBCtx(ctx, sc.DB, sc.Projects, includeDocs, seedOIDs, hasExplicitFileSeed, terms, lim.seedNames)
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
		return NewGraphWithMeta(nil, nil, nil), nil
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

func (sc *Scope) mergeBoundedFTSSeedsCtx(ctx context.Context, prompt string, active map[string]bool, includeDocs bool, seedOIDs map[string]bool, seedSyms []RankedSymbol, terms []string, maxSeedFiles int) (map[string]bool, []RankedSymbol, error) {
	if len(active) > 0 || len(seedOIDs) > 0 || hasStrongIdentSeed(seedSyms, terms) {
		return seedOIDs, seedSyms, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if sc.ExtractFTSWords == nil || sc.FTSFallbackChunks == nil {
		return seedOIDs, seedSyms, nil
	}
	words := sc.ExtractFTSWords(prompt)
	minWords := 2
	if len(seedOIDs) == 0 && len(seedSyms) == 0 {
		minWords = 1
	}
	if len(words) < minWords {
		return seedOIDs, seedSyms, nil
	}
	chunks := sc.FTSFallbackChunks(words, strings.ToLower(prompt))
	if len(chunks) == 0 {
		return seedOIDs, seedSyms, nil
	}
	matchedFiles, err := queryFilesByPathsDBCtx(ctx, sc.DB, sc.Projects, includeDocs, collectUniqueChunkPaths(chunks))
	if err != nil {
		return nil, nil, err
	}
	extraOIDs := addFTSChunkOIDs(chunks, matchedFiles, seedOIDs, maxSeedFiles)
	if len(extraOIDs) == 0 {
		return seedOIDs, seedSyms, nil
	}
	extraSyms, err := querySymbolsByOIDsDBCtx(ctx, sc.DB, sc.Projects, includeDocs, extraOIDs)
	if err != nil {
		return nil, nil, err
	}
	return seedOIDs, append(seedSyms, extraSyms...), nil
}

func collectUniqueChunkPaths(chunks []FTSChunkHit) []string {
	var paths []string
	seen := map[string]bool{}
	for _, h := range chunks {
		if h.Path != "" && !seen[h.Path] {
			seen[h.Path] = true
			paths = append(paths, h.Path)
		}
	}
	return paths
}

func addFTSChunkOIDs(chunks []FTSChunkHit, matchedFiles []FileMeta, seedOIDs map[string]bool, maxSeedFiles int) []string {
	oidByPath := make(map[string]string, len(matchedFiles))
	for _, f := range matchedFiles {
		oidByPath[f.Path] = f.OID
	}
	var extraOIDs []string
	for _, h := range chunks {
		if oid := oidByPath[h.Path]; oid != "" && !seedOIDs[oid] && len(seedOIDs) < maxSeedFiles {
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
		if len(t) < HookMinIdentLen {
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

func (sc *Scope) expandBoundedNeighborhoodCtx(ctx context.Context, includeDocs bool, maxHops int, lim boundedGraphLimits, seedOIDs map[string]bool, seedSyms []RankedSymbol, terms []string) (map[string]bool, []RawSymRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	allOIDs := cloneBoolMap(seedOIDs)
	priorityNames, otherNames := partitionPrioritySymNames(seedSyms, terms, lim.seedNames)
	for _, s := range seedSyms {
		addBoundedOID(allOIDs, s.OID, lim.totalFiles)
	}
	if maxHops <= 0 {
		return allOIDs, nil, nil
	}
	frontierOIDs := SortedMapKeys(allOIDs)
	hop1New, inRefs, err := sc.expandSingleHopCtx(ctx, includeDocs, frontierOIDs, priorityNames, otherNames, allOIDs, lim)
	if err != nil {
		return nil, nil, err
	}
	if maxHops < 2 || len(hop1New) == 0 || len(allOIDs) >= lim.totalFiles {
		return allOIDs, inRefs, nil
	}
	extraIn, err := sc.expandSecondHopCtx(ctx, includeDocs, hop1New, terms, allOIDs, lim)
	if err != nil {
		return nil, nil, err
	}
	return allOIDs, append(inRefs, extraIn...), nil
}

func (sc *Scope) expandSecondHopCtx(ctx context.Context, includeDocs bool, hop1New, terms []string, allOIDs map[string]bool, lim boundedGraphLimits) ([]RawSymRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(hop1New) > maxBoundedHop2Files {
		hop1New = hop1New[:maxBoundedHop2Files]
	}
	hop1Syms, err := querySymbolsByOIDsDBCtx(ctx, sc.DB, sc.Projects, includeDocs, hop1New)
	if err != nil {
		return nil, err
	}
	pNames, oNames := partitionPrioritySymNames(hop1Syms, terms, 24)
	_, extraIn, err := sc.expandSingleHopCtx(ctx, includeDocs, hop1New, pNames, oNames, allOIDs, lim)
	return extraIn, err
}

func (sc *Scope) expandSingleHopCtx(ctx context.Context, includeDocs bool, fromOIDs, priorityNames, otherNames []string, allOIDs map[string]bool, lim boundedGraphLimits) ([]string, []RawSymRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	outRefs, err := querySymRefsByOIDsDBCtx(ctx, sc.DB, sc.Projects, includeDocs, fromOIDs)
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
	outOIDs, err := queryOIDsBySymbolNamesDBCtx(ctx, sc.DB, sc.Projects, includeDocs, outTargets)
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
		if !allOIDs[r.OID] && addBoundedOID(allOIDs, r.OID, lim.totalFiles) {
			added = append(added, r.OID)
		}
	}
	return added, inRefs, nil
}

func filterPriorityOutRefs(outRefs []RawSymRef, priorityNames []string) []RawSymRef {
	if len(priorityNames) == 0 {
		return outRefs
	}
	pSet := make(map[string]bool, len(priorityNames))
	for _, n := range priorityNames {
		pSet[n] = true
	}
	out := make([]RawSymRef, 0, len(outRefs))
	for _, r := range outRefs {
		if pSet[r.FromSym] {
			out = append(out, r)
			continue
		}
		if dot := strings.LastIndexByte(r.FromSym, '.'); dot >= 0 && pSet[r.FromSym[dot+1:]] {
			out = append(out, r)
		}
	}
	return out
}

func (sc *Scope) queryPrioritizedInboundRefsCtx(ctx context.Context, includeDocs bool, priorityNames, otherNames []string, maxInbound int) ([]RawSymRef, error) {
	var inRefs []RawSymRef
	if len(priorityNames) > 0 {
		refs, err := queryInboundSymRefsDBCtx(ctx, sc.DB, sc.Projects, includeDocs, priorityNames, maxInbound)
		if err != nil {
			return nil, err
		}
		inRefs = append(inRefs, refs...)
	}
	rem := maxInbound - len(inRefs)
	if rem > 0 && len(otherNames) > 0 {
		refs, err := queryInboundSymRefsDBCtx(ctx, sc.DB, sc.Projects, includeDocs, otherNames, rem)
		if err != nil {
			return nil, err
		}
		inRefs = append(inRefs, refs...)
	}
	return inRefs, nil
}

func (sc *Scope) assembleBoundedSymGraphCtx(ctx context.Context, allOIDs map[string]bool, inRefs []RawSymRef, includeDocs bool) (*Graph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	oidList := SortedMapKeys(allOIDs)
	if len(oidList) == 0 {
		return NewGraphWithMeta(nil, nil, nil), nil
	}
	boundedFiles, err := queryFilesByOIDsDBCtx(ctx, sc.DB, sc.Projects, includeDocs, oidList)
	if err != nil {
		return nil, err
	}
	syms, err := querySymbolsByOIDsDBCtx(ctx, sc.DB, sc.Projects, includeDocs, oidList)
	if err != nil {
		return nil, err
	}
	outRefs, err := querySymRefsByOIDsDBCtx(ctx, sc.DB, sc.Projects, includeDocs, oidList)
	if err != nil {
		return nil, err
	}
	rawRefs := mergeUniqueRawRefs(outRefs, inRefs)
	g := NewGraphWithMeta(syms, boundedFiles, LoadGoModules(sc))
	populateSymGraphRefs(g, rawRefs)
	return g, nil
}
