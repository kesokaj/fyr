package search

import (
	"path"
	"strings"
)

const (
	maxBoundedQueryTerms  = 12
	maxBoundedSeedFiles   = 40
	maxBoundedSeedNames   = 80
	maxBoundedInboundRefs = 400
	maxBoundedTotalFiles  = 96
	maxBoundedHop2Files   = 32
)

// loadBoundedSymGraph loads only the query-matched seed symbols/files, optional
// FTS5 fallback files, and 1-2 hop neighbors instead of reading all symbols and
// sym_refs across a large repository.
func (sc *scope) loadBoundedSymGraph(prompt string, active map[string]bool, includeDocs bool, maxHops int) (*symGraph, error) {
	if sc == nil || sc.st == nil || len(sc.projects) == 0 {
		return newSymGraphWithMeta(nil, nil, nil), nil
	}
	if strings.TrimSpace(prompt) == "" && len(active) == 0 {
		return sc.loadSymGraphOpts(includeDocs)
	}
	files, err := queryIndexedFilesDB(sc.st.db, sc.projects, includeDocs)
	if err != nil || len(files) == 0 {
		return newSymGraphWithMeta(nil, nil, nil), err
	}
	terms := parseQueryTerms(prompt)
	seedOIDs := collectSeedFileOIDs(files, active, terms)
	seedSyms, err := querySeedSymbolsDB(sc.st.db, sc.projects, includeDocs, seedOIDs, terms)
	if err != nil {
		return nil, err
	}
	seedOIDs, seedSyms, err = sc.mergeBoundedFTSSeeds(files, prompt, active, includeDocs, seedOIDs, seedSyms, terms)
	if err != nil {
		return nil, err
	}
	if len(seedOIDs) == 0 && len(seedSyms) == 0 {
		return newSymGraphWithMeta(nil, nil, nil), nil
	}
	allOIDs, inRefs, err := sc.expandBoundedNeighborhood(includeDocs, maxHops, seedOIDs, seedSyms, terms)
	if err != nil {
		return nil, err
	}
	return sc.assembleBoundedSymGraph(files, allOIDs, inRefs, includeDocs)
}

func collectSeedFileOIDs(files []indexedFileMeta, active map[string]bool, terms []string) map[string]bool {
	oids := map[string]bool{}
	for _, f := range files {
		if active[f.path] {
			oids[f.oid] = true
		}
	}
	terms = filterBoundedQueryTerms(terms)
	for _, f := range files {
		if len(oids) >= maxBoundedSeedFiles {
			break
		}
		if fileMatchesBoundedTerms(f.path, terms) {
			oids[f.oid] = true
		}
	}
	return oids
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

func (sc *scope) mergeBoundedFTSSeeds(files []indexedFileMeta, prompt string, active map[string]bool, includeDocs bool, seedOIDs map[string]bool, seedSyms []RankedSymbol, terms []string) (map[string]bool, []RankedSymbol, error) {
	if len(active) > 0 || hasStrongIdentSeed(seedSyms, terms) {
		return seedOIDs, seedSyms, nil
	}
	words := extractFTSFallbackWords(prompt)
	minWords := 2
	if len(seedOIDs) == 0 && len(seedSyms) == 0 {
		minWords = 1
	}
	if len(words) < minWords {
		return seedOIDs, seedSyms, nil
	}
	oidByPath := make(map[string]string, len(files))
	for _, f := range files {
		oidByPath[f.path] = f.oid
	}
	var extraOIDs []string
	for _, h := range sc.queryFTSFallbackChunks(words, strings.ToLower(prompt)) {
		if oid := oidByPath[h.path]; oid != "" && !seedOIDs[oid] && len(seedOIDs) < maxBoundedSeedFiles {
			seedOIDs[oid] = true
			extraOIDs = append(extraOIDs, oid)
		}
	}
	if len(extraOIDs) == 0 {
		return seedOIDs, seedSyms, nil
	}
	extraSyms, err := querySymbolsByOIDsDB(sc.st.db, sc.projects, includeDocs, extraOIDs)
	if err != nil {
		return nil, nil, err
	}
	return seedOIDs, append(seedSyms, extraSyms...), nil
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

func (sc *scope) expandBoundedNeighborhood(includeDocs bool, maxHops int, seedOIDs map[string]bool, seedSyms []RankedSymbol, terms []string) (map[string]bool, []rawSymRef, error) {
	allOIDs := cloneBoolMap(seedOIDs)
	priorityNames, otherNames := partitionPrioritySymNames(seedSyms, terms, maxBoundedSeedNames)
	for _, s := range seedSyms {
		addBoundedOID(allOIDs, s.OID, maxBoundedTotalFiles)
	}
	frontierOIDs := sortedMapKeys(allOIDs)
	hop1New, inRefs, err := sc.expandSingleHop(includeDocs, frontierOIDs, priorityNames, otherNames, allOIDs)
	if err != nil {
		return nil, nil, err
	}
	if maxHops >= 2 && len(hop1New) > 0 && len(allOIDs) < maxBoundedTotalFiles {
		if len(hop1New) > maxBoundedHop2Files {
			hop1New = hop1New[:maxBoundedHop2Files]
		}
		hop1Syms, err := querySymbolsByOIDsDB(sc.st.db, sc.projects, includeDocs, hop1New)
		if err != nil {
			return nil, nil, err
		}
		pNames, oNames := partitionPrioritySymNames(hop1Syms, terms, 24)
		if _, extraIn, err := sc.expandSingleHop(includeDocs, hop1New, pNames, oNames, allOIDs); err == nil {
			inRefs = append(inRefs, extraIn...)
		} else {
			return nil, nil, err
		}
	}
	return allOIDs, inRefs, nil
}

func (sc *scope) expandSingleHop(includeDocs bool, fromOIDs, priorityNames, otherNames []string, allOIDs map[string]bool) ([]string, []rawSymRef, error) {
	outRefs, err := querySymRefsByOIDsDB(sc.st.db, sc.projects, includeDocs, fromOIDs)
	if err != nil {
		return nil, nil, err
	}
	inRefs, err := sc.queryPrioritizedInboundRefs(includeDocs, priorityNames, otherNames)
	if err != nil {
		return nil, nil, err
	}
	outTargets := collectDistinctOutTargetNames(outRefs, maxBoundedSeedNames)
	outOIDs, err := queryOIDsBySymbolNamesDB(sc.st.db, sc.projects, includeDocs, outTargets)
	if err != nil {
		return nil, nil, err
	}
	var added []string
	for _, oid := range outOIDs {
		if !allOIDs[oid] && addBoundedOID(allOIDs, oid, maxBoundedTotalFiles) {
			added = append(added, oid)
		}
	}
	for _, r := range inRefs {
		if !allOIDs[r.oid] && addBoundedOID(allOIDs, r.oid, maxBoundedTotalFiles) {
			added = append(added, r.oid)
		}
	}
	return added, inRefs, nil
}

func (sc *scope) queryPrioritizedInboundRefs(includeDocs bool, priorityNames, otherNames []string) ([]rawSymRef, error) {
	var inRefs []rawSymRef
	if len(priorityNames) > 0 {
		refs, err := queryInboundSymRefsDB(sc.st.db, sc.projects, includeDocs, priorityNames, maxBoundedInboundRefs)
		if err != nil {
			return nil, err
		}
		inRefs = append(inRefs, refs...)
	}
	rem := maxBoundedInboundRefs - len(inRefs)
	if rem > 0 && len(otherNames) > 0 {
		refs, err := queryInboundSymRefsDB(sc.st.db, sc.projects, includeDocs, otherNames, rem)
		if err != nil {
			return nil, err
		}
		inRefs = append(inRefs, refs...)
	}
	return inRefs, nil
}

func (sc *scope) assembleBoundedSymGraph(files []indexedFileMeta, allOIDs map[string]bool, inRefs []rawSymRef, includeDocs bool) (*symGraph, error) {
	oidList := sortedMapKeys(allOIDs)
	if len(oidList) == 0 {
		return newSymGraphWithMeta(nil, nil, nil), nil
	}
	var boundedFiles []indexedFileMeta
	for _, f := range files {
		if allOIDs[f.oid] {
			boundedFiles = append(boundedFiles, f)
		}
	}
	syms, err := querySymbolsByOIDsDB(sc.st.db, sc.projects, includeDocs, oidList)
	if err != nil {
		return nil, err
	}
	outRefs, err := querySymRefsByOIDsDB(sc.st.db, sc.projects, includeDocs, oidList)
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
