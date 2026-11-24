package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

var (
	brainTurnQueryBudget = search.DefaultHookBrainBudget()
	loadSpecRecordsHook  = loadCurrentSpecsFromStoreCtx
)

type cachedTurnData struct {
	recs []brain.Record
	fts  map[string]bool
}

// turnBrainCache memoizes brain.List(dir) once per CLI invocation so
// BrainTurnContext, BrainRefs, and BrainRefsForDir never trigger N+1 SQLite
// or subprocess lookups across candidate paths (ADR-521). Guarded by mu so
// concurrent PreInvocation AST slicing and BrainTurnContext stay race-free (ADR-532).
type turnBrainCache struct {
	mu          sync.Mutex
	home        string
	global      bool
	byDir       map[string][]brain.Record
	specsByDir  map[string][]brain.Record
	turnByQuery map[string]cachedTurnData
	ftsByQuery  map[string]map[string]bool
	loadCount   int
}

func newTurnBrainCache(home string, global bool) *turnBrainCache {
	return &turnBrainCache{
		home:        home,
		global:      global,
		byDir:       map[string][]brain.Record{},
		specsByDir:  map[string][]brain.Record{},
		turnByQuery: map[string]cachedTurnData{},
		ftsByQuery:  map[string]map[string]bool{},
	}
}

func boundedBrainQueryCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDL := ctx.Deadline(); hasDL {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, brainTurnQueryBudget)
}

func (tc *turnBrainCache) records(dir, homeOverride string) []brain.Record {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.recordsLockedCtx(context.Background(), dir, homeOverride)
}

func (tc *turnBrainCache) recordsLockedCtx(ctx context.Context, dir, homeOverride string) []brain.Record {
	if ctx != nil && ctx.Err() != nil {
		return nil
	}
	home := tc.resolveHome(homeOverride)
	key := canonicalCacheDir(dir) + "|" + home
	if recs, ok := tc.byDir[key]; ok {
		return recs
	}
	tc.loadCount++
	qctx, cancel := boundedBrainQueryCtx(ctx)
	defer cancel()
	recs := loadTurnRecordsCtx(qctx, dir, home, tc.global)
	if qctx.Err() != nil {
		return nil
	}
	tc.byDir[key] = recs
	return recs
}

func (tc *turnBrainCache) resolveHome(homeOverride string) string {
	if tc.home != "" {
		return tc.home
	}
	return homeOverride
}

func canonicalCacheDir(dir string) string {
	if canon := search.CanonicalizePath("", dir); canon != "" {
		return canon
	}
	return dir
}

func (tc *turnBrainCache) ftsMatches(dir, homeOverride string, terms []string) map[string]bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.ftsMatchesLockedCtx(context.Background(), dir, homeOverride, terms)
}

func (tc *turnBrainCache) ftsMatchesLockedCtx(ctx context.Context, dir, homeOverride string, terms []string) map[string]bool {
	if ctx != nil && ctx.Err() != nil {
		return nil
	}
	home := tc.resolveHome(homeOverride)
	if home == "" || len(terms) == 0 {
		return nil
	}
	if _, err := os.Stat(brain.Path(home)); err != nil {
		return nil
	}
	key := canonicalCacheDir(dir) + "|" + home + "|" + strings.Join(terms, " ")
	if cached, ok := tc.ftsByQuery[key]; ok {
		return cached
	}
	qctx, cancel := boundedBrainQueryCtx(ctx)
	defer cancel()
	matched := queryBrainFTSRecordIDsCtx(qctx, brain.Options{Home: home, Dir: dir, Global: tc.global}, terms)
	if qctx.Err() != nil {
		return nil
	}
	tc.ftsByQuery[key] = matched
	return matched
}

func queryBrainFTSRecordIDsCtx(ctx context.Context, bo brain.Options, terms []string) map[string]bool {
	hits, err := brain.SearchTurnRecordsCtx(ctx, bo, terms, 16)
	if err != nil || len(hits) == 0 {
		return nil
	}
	return filterFTSHitsByPrecision(hits, terms)
}

func filterFTSHitsByPrecision(hits []brain.Hit, terms []string) map[string]bool {
	if len(hits) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, h := range hits {
		if ftsHitMeetsPrecision(h, terms) {
			out[h.Scope+"|"+h.ID] = true
			out[h.ID] = true
		}
	}
	return out
}

func (tc *turnBrainCache) loadTurnData(dir, homeOverride string, terms []string) ([]brain.Record, map[string]bool) {
	return tc.loadTurnDataCtx(context.Background(), dir, homeOverride, terms)
}

func (tc *turnBrainCache) loadTurnDataCtx(ctx context.Context, dir, homeOverride string, terms []string) ([]brain.Record, map[string]bool) {
	if ctx != nil && ctx.Err() != nil {
		return nil, nil
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if ctx != nil && ctx.Err() != nil {
		return nil, nil
	}
	home := tc.resolveHome(homeOverride)
	recKey := canonicalCacheDir(dir) + "|" + home
	recs, hasRecs := tc.byDir[recKey]
	var fkey string
	var fts map[string]bool
	hasFTS := len(terms) == 0 || home == ""
	if !hasFTS {
		fkey = recKey + "|" + strings.Join(terms, " ")
		fts, hasFTS = tc.ftsByQuery[fkey]
	}
	if hasRecs && hasFTS {
		return recs, fts
	}
	if !hasRecs && fkey != "" {
		if cd, ok := tc.turnByQuery[fkey]; ok {
			return cd.recs, cd.fts
		}
	}
	if recs2, fts2, ok := tc.loadCombinedTurnDataCtx(ctx, dir, home, recKey, fkey, terms, hasRecs, hasFTS); ok {
		return recs2, fts2
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, nil
	}
	recs = tc.recordsLockedCtx(ctx, dir, homeOverride)
	if len(recs) == 0 || len(terms) == 0 || (ctx != nil && ctx.Err() != nil) {
		return recs, nil
	}
	return recs, tc.ftsMatchesLockedCtx(ctx, dir, homeOverride, terms)
}

func (tc *turnBrainCache) loadCombinedTurnDataCtx(ctx context.Context, dir, home, recKey, fkey string, terms []string, hasRecs, hasFTS bool) ([]brain.Record, map[string]bool, bool) {
	if hasRecs || hasFTS || home == "" || (ctx != nil && ctx.Err() != nil) {
		return nil, nil, false
	}
	if _, err := os.Stat(brain.Path(home)); err != nil {
		return nil, nil, false
	}
	qctx, cancel := boundedBrainQueryCtx(ctx)
	defer cancel()
	tc.loadCount++
	res, err := brain.LoadTurnDataCtx(qctx, brain.Options{Home: home, Dir: dir, Global: tc.global}, terms, 16)
	if qctx.Err() != nil {
		return nil, nil, true
	}
	if err != nil || len(res.Records) == 0 {
		return nil, nil, false
	}
	fts := filterFTSHitsByPrecision(res.Hits, terms)
	tc.ftsByQuery[fkey] = fts
	if res.Complete {
		tc.byDir[recKey] = res.Records
	} else {
		tc.turnByQuery[fkey] = cachedTurnData{recs: res.Records, fts: fts}
	}
	return res.Records, fts, true
}

func ftsHitMeetsPrecision(h brain.Hit, terms []string) bool {
	if len(terms) == 1 {
		return len(terms[0]) >= 5
	}
	return countFTSHitTerms(h, terms) >= 2
}

func countFTSHitTerms(h brain.Hit, terms []string) int {
	snipSpans := extractMarkedSnippetWords(h.Snippet)
	titleLow := strings.ToLower(h.ID + " " + h.Title)
	matched := 0
	for _, t := range terms {
		prefix := t
		if len(prefix) > 5 {
			prefix = prefix[:5]
		}
		if strings.Contains(titleLow, prefix) || hasStemPrefixInWords(snipSpans, prefix) {
			matched++
		}
	}
	return matched
}

func extractMarkedSnippetWords(snip string) []string {
	var out []string
	rest := snip
	for {
		i := strings.IndexRune(rest, '»')
		if i < 0 {
			return out
		}
		rest = rest[i+len("»"):]
		j := strings.IndexRune(rest, '«')
		if j < 0 {
			return out
		}
		if w := strings.ToLower(strings.TrimSpace(rest[:j])); w != "" {
			out = append(out, w)
		}
		rest = rest[j+len("«"):]
	}
}

func hasStemPrefixInWords(words []string, prefix string) bool {
	for _, w := range words {
		if strings.HasPrefix(w, prefix) || (len(w) >= 4 && strings.HasPrefix(prefix, w)) {
			return true
		}
	}
	return false
}

func loadTurnRecordsCtx(ctx context.Context, dir, home string, global bool) []brain.Record {
	if ctx != nil && ctx.Err() != nil {
		return nil
	}
	if home != "" {
		recs, err := brain.ListTurnRecordsCtx(ctx, brain.Options{Home: home, Dir: dir, Global: global})
		if err == nil && len(recs) > 0 {
			return recs
		}
		if ctx != nil && ctx.Err() != nil {
			return nil
		}
	}
	if trailRecs, ok, err := brain.ReadTrail(dir); err == nil && ok {
		return trailRecs
	}
	return nil
}

func makeBrainTurnContext(tc *turnBrainCache) func(dir, home, prompt string, files []string) search.TurnBrainContext {
	ctxFn := makeBrainTurnContextCtx(tc)
	return func(dir, home, prompt string, files []string) search.TurnBrainContext {
		return ctxFn(context.Background(), dir, home, prompt, files)
	}
}

func makeBrainTurnContextCtx(tc *turnBrainCache) func(ctx context.Context, dir, home, prompt string, files []string) search.TurnBrainContext {
	return func(ctx context.Context, dir, home, prompt string, files []string) search.TurnBrainContext {
		if ctx != nil && ctx.Err() != nil {
			return search.TurnBrainContext{}
		}
		terms := extractTurnQueryTerms(prompt)
		recs, ftsIDs := tc.loadTurnDataCtx(ctx, dir, home, terms)
		if len(recs) == 0 || (ctx != nil && ctx.Err() != nil) {
			return search.TurnBrainContext{}
		}
		var out search.TurnBrainContext
		for _, r := range recs {
			classifyTurnRecordWithFTS(&out, dir, prompt, files, terms, ftsIDs, r)
		}
		return out
	}
}

func totalMatchedRecords(ctx *search.TurnBrainContext) int {
	return len(ctx.Memories) + len(ctx.Bugs) + len(ctx.ADRs)
}

func classifyTurnRecord(ctx *search.TurnBrainContext, dir, prompt string, files, terms []string, r brain.Record) {
	classifyTurnRecordWithFTS(ctx, dir, prompt, files, terms, nil, r)
}

func classifyTurnRecordWithFTS(ctx *search.TurnBrainContext, dir, prompt string, files, terms []string, ftsIDs map[string]bool, r brain.Record) {
	switch r.Kind {
	case "plan":
		classifyPlanRecord(ctx, r)
	case "memory":
		classifyMemoryRecord(ctx, dir, prompt, files, terms, ftsIDs, r)
	case "bug":
		classifyBugRecord(ctx, prompt, files, terms, ftsIDs, r)
	case "adr":
		classifyADRRecord(ctx, prompt, files, terms, ftsIDs, r)
	}
}

func classifyPlanRecord(ctx *search.TurnBrainContext, r brain.Record) {
	if (r.Status == "open" || r.Status == "") && ctx.ActivePlan == nil {
		ctx.ActivePlan = &search.MatchedRecord{
			ID: r.ID, Kind: "plan", Status: r.Status, Title: r.Title,
			Provenance: r.Provenance, Body: summarizePlanReadyPhases(r.Body),
		}
	}
}

func classifyMemoryRecord(ctx *search.TurnBrainContext, dir, prompt string, files, terms []string, ftsIDs map[string]bool, r brain.Record) {
	if (r.Status != "closed" && r.Status != "") || !matchesRecordPrecisionWithFTS(r, prompt, files, terms, ftsIDs) {
		return
	}
	v := brain.ComputeMemoryValidity(dir, r, 0, time.Now())
	if v < brain.MinActiveValidity {
		return
	}
	m := search.MatchedRecord{
		ID: r.ID, Kind: "memory", Status: r.Status, Title: r.Title,
		Provenance: r.Provenance, Body: r.Body, StaleTag: brain.MemoryStalenessTag(dir, r),
		MemoryKind: r.MemoryKind, Validity: v, Global: r.Scope == "",
	}
	ctx.Memories = insertPrioritizedMemory(ctx.Memories, m, totalMatchedRecords(ctx))
}

func classifyBugRecord(ctx *search.TurnBrainContext, prompt string, files, terms []string, ftsIDs map[string]bool, r brain.Record) {
	if (r.Status == "open" || r.Status == "") && len(ctx.Bugs) < 2 && totalMatchedRecords(ctx) < 6 && matchesRecordPrecisionWithFTS(r, prompt, files, terms, ftsIDs) {
		ctx.Bugs = append(ctx.Bugs, search.MatchedRecord{
			ID: r.ID, Kind: "bug", Status: r.Status, Title: r.Title,
			Provenance: r.Provenance, Body: r.Body,
		})
	}
}

func classifyADRRecord(ctx *search.TurnBrainContext, prompt string, files, terms []string, ftsIDs map[string]bool, r brain.Record) {
	if r.Status != "accepted" && r.Status != "open" && r.Status != "" {
		return
	}
	rec := search.MatchedRecord{
		ID: r.ID, Kind: "adr", Status: r.Status, Title: r.Title,
		Provenance: r.Provenance, Body: r.Body,
	}
	conflict := search.CheckADRConflict(strings.ToLower(prompt), rec)
	if conflict != "" {
		ctx.Conflicts = append(ctx.Conflicts, conflict)
	}
	if (conflict != "" || (len(ctx.ADRs) < 2 && totalMatchedRecords(ctx) < 6 && matchesRecordPrecisionWithFTS(r, prompt, files, terms, ftsIDs))) && len(ctx.ADRs) < 3 {
		ctx.ADRs = append(ctx.ADRs, rec)
	}
}

func makeBrainReprime() func(dir, home string) string {
	return func(dir, home string) string {
		if home == "" {
			return ""
		}
		if _, err := os.Stat(brain.Path(home)); err != nil {
			return ""
		}
		bo := brain.Options{Home: home, Dir: dir}
		var parts []string
		if mem, err := brain.MemoryShow(bo); err == nil && strings.TrimSpace(mem) != "" {
			parts = append(parts, "# Re-primed Project Memory\n"+strings.TrimSpace(mem))
		}
		if plans, err := brain.List(bo, brain.ListFilter{Kind: "plan", Open: true}); err == nil && len(plans) > 0 {
			var lines []string
			for _, p := range plans {
				lines = append(lines, fmt.Sprintf("- %s: %s (%s)", p.ID, p.Title, summarizePlanReadyPhases(p.Body)))
			}
			parts = append(parts, "# Active Open Plans\n"+strings.Join(lines, "\n"))
		}
		return strings.Join(parts, "\n\n")
	}
}

func summarizePlanReadyPhases(body string) string {
	if line := brain.ReadyPhasesLine(body); line != "" {
		return line
	}
	return "open"
}

func makeBrainRefsForDir(tc *turnBrainCache) func(dir, target string) []search.LinkedRecord {
	return func(dir, target string) []search.LinkedRecord {
		return filterCachedBrainRefs(tc.records(dir, ""), target)
	}
}

func makeBrainRefs(tc *turnBrainCache, dir string) func(string) []search.LinkedRecord {
	return func(target string) []search.LinkedRecord {
		return filterCachedBrainRefs(tc.records(dir, ""), target)
	}
}

func makeBrainSpecBodies(tc *turnBrainCache) func(dir, home string, paths []string) []search.SpecContractSource {
	return func(dir, home string, paths []string) []search.SpecContractSource {
		if dir == "" || len(paths) == 0 {
			return nil
		}
		specs, ok := tc.currentSpecs(dir, home)
		if !ok {
			return nil
		}
		return buildSpecContractSources(specs, tc.records(dir, home), paths)
	}
}

func (tc *turnBrainCache) currentSpecs(dir, homeOverride string) ([]brain.Record, bool) {
	home := tc.resolveHome(homeOverride)
	key := canonicalCacheDir(dir) + "|" + home
	tc.mu.Lock()
	if cached, ok := tc.specsByDir[key]; ok {
		tc.mu.Unlock()
		return cached, true
	}
	tc.mu.Unlock()
	qctx, cancel := boundedBrainQueryCtx(context.Background())
	defer cancel()
	recs, err := loadSpecRecordsHook(qctx, dir, home)
	if qctx.Err() != nil || err != nil {
		return nil, false
	}
	tc.mu.Lock()
	tc.specsByDir[key] = recs
	tc.mu.Unlock()
	return recs, true
}

func loadCurrentSpecsFromStoreCtx(ctx context.Context, dir, home string) ([]brain.Record, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if home != "" {
		recs, err := brain.ListCurrentSpecsCtx(ctx, brain.Options{Home: home, Dir: dir})
		if err != nil {
			return nil, err
		}
		if len(recs) > 0 {
			return recs, nil
		}
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if trailRecs, ok, err := brain.ReadTrail(dir); err == nil && ok {
		return filterCurrentSpecs(trailRecs), nil
	}
	return nil, nil
}

func filterCurrentSpecs(recs []brain.Record) []brain.Record {
	var out []brain.Record
	for _, r := range recs {
		if r.Kind == "spec" && r.Status == "current" {
			out = append(out, r)
		}
	}
	return out
}
