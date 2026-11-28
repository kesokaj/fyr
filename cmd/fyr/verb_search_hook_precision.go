package main

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

var turnQueryStopwords = map[string]bool{
	"want": true, "have": true, "some": true, "sort": true, "this": true,
	"that": true, "with": true, "from": true, "what": true, "when": true,
	"where": true, "which": true, "there": true, "their": true, "would": true,
	"could": true, "should": true, "about": true, "into": true, "more": true,
	"make": true, "need": true, "like": true, "just": true, "also": true,
	"only": true, "very": true, "good": true, "best": true, "work": true,
	"used": true, "using": true, "models": true, "model": true, "file": true,
	"files": true, "code": true, "plan": true, "test": true, "tests": true,
	"check": true, "update": true, "change": true, "then": true, "will": true,
	"they": true, "them": true, "your": true, "here": true, "does": true,
	"these": true, "those": true, "during": true,
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

func extractTurnQueryTerms(prompt string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.FieldsFunc(prompt, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' && r != '/' && r != '.'
	}) {
		t := strings.ToLower(strings.Trim(w, ".-_/"))
		if len(t) >= 4 && !turnQueryStopwords[t] && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func matchesRecordPrecisionWithFTS(r brain.Record, prompt string, files, terms []string, ftsIDs map[string]bool) bool {
	if r.ID != "" && strings.Contains(strings.ToLower(prompt), strings.ToLower(r.ID)) {
		return true
	}
	if len(ftsIDs) > 0 && (ftsIDs[r.Scope+"|"+r.ID] || ftsIDs[r.ID]) {
		return true
	}
	titleHay := strings.ToLower(r.ID + "\n" + r.Title)
	fullHay := titleHay + "\n" + strings.ToLower(r.Body)
	for _, f := range files {
		lf := strings.ToLower(strings.TrimSpace(f))
		base := strings.ToLower(filepath.Base(f))
		if (lf != "" && strings.Contains(fullHay, lf)) || (len(base) >= 5 && strings.Contains(fullHay, base)) {
			return true
		}
	}
	titleHits := 0
	for _, t := range terms {
		if len(t) >= 7 && strings.ContainsAny(t, "_-./") && strings.Contains(fullHay, t) {
			return true
		}
		if strings.Contains(titleHay, t) {
			titleHits++
			if titleHits >= 2 {
				return true
			}
		}
	}
	return false
}

func totalMatchedRecords(ctx *search.TurnBrainContext) int {
	return len(ctx.Memories) + len(ctx.Bugs) + len(ctx.ADRs)
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

func filterCurrentSpecs(recs []brain.Record) []brain.Record {
	var out []brain.Record
	for _, r := range recs {
		if r.Kind == "spec" && r.Status == "current" {
			out = append(out, r)
		}
	}
	return out
}

func filterCachedBrainRefs(recs []brain.Record, target string) []search.LinkedRecord {
	target = strings.TrimSpace(target)
	if len(recs) == 0 || target == "" {
		return nil
	}
	lowTarget := strings.ToLower(target)
	var out []search.LinkedRecord
	for _, r := range recs {
		isOpenBug := r.Kind == "bug" && (r.Status == "open" || r.Status == "")
		isActiveADR := r.Kind == "adr" && (r.Status == "accepted" || r.Status == "open" || r.Status == "")
		if !isOpenBug && !isActiveADR {
			continue
		}
		if strings.EqualFold(r.ID, target) || strings.Contains(strings.ToLower(r.ID+"\n"+r.Title+"\n"+r.Body), lowTarget) {
			out = append(out, search.LinkedRecord{ID: r.ID, Status: r.Status, Title: r.Title})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func insertPrioritizedMemory(mems []search.MatchedRecord, m search.MatchedRecord, totalRecs int) []search.MatchedRecord {
	if len(mems) < 4 && totalRecs < 6 {
		mems = append(mems, m)
		sort.Slice(mems, func(i, j int) bool { return memoryPriorityLess(mems[i], mems[j]) })
		return mems
	}
	if len(mems) > 0 && memoryPriorityLess(m, mems[len(mems)-1]) {
		mems[len(mems)-1] = m
		sort.Slice(mems, func(i, j int) bool { return memoryPriorityLess(mems[i], mems[j]) })
	}
	return mems
}

func memoryPriorityLess(a, b search.MatchedRecord) bool {
	aPE := a.MemoryKind == "procedural" || a.MemoryKind == "episodic"
	bPE := b.MemoryKind == "procedural" || b.MemoryKind == "episodic"
	if aPE != bPE {
		return aPE
	}
	if a.Validity != b.Validity {
		return a.Validity > b.Validity
	}
	if a.Global != b.Global {
		return !a.Global
	}
	return a.ID < b.ID
}
