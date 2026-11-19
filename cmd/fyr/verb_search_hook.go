package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

// turnBrainCache memoizes brain.List(dir) once per CLI invocation so
// BrainTurnContext, BrainRefs, and BrainRefsForDir never trigger N+1 SQLite
// or subprocess lookups across candidate paths (ADR-521).
type turnBrainCache struct {
	home      string
	global    bool
	byDir     map[string][]brain.Record
	loadCount int
}

func newTurnBrainCache(home string, global bool) *turnBrainCache {
	return &turnBrainCache{
		home:   home,
		global: global,
		byDir:  map[string][]brain.Record{},
	}
}

func (tc *turnBrainCache) records(dir, homeOverride string) []brain.Record {
	home := tc.home
	if home == "" {
		home = homeOverride
	}
	canon := search.CanonicalizePath("", dir)
	if canon == "" {
		canon = dir
	}
	key := canon + "|" + home
	if recs, ok := tc.byDir[key]; ok {
		return recs
	}
	tc.loadCount++
	recs := loadTurnRecords(dir, home, tc.global)
	tc.byDir[key] = recs
	return recs
}

func loadTurnRecords(dir, home string, global bool) []brain.Record {
	var recs []brain.Record
	if home != "" {
		if _, err := os.Stat(brain.Path(home)); err == nil {
			if prj, err := brain.List(brain.Options{Home: home, Dir: dir, Global: global}, brain.ListFilter{}); err == nil {
				recs = append(recs, prj...)
			}
			if !global {
				if globals, err := brain.List(brain.Options{Home: home, Dir: dir, Global: true}, brain.ListFilter{Kind: "memory"}); err == nil {
					recs = append(recs, globals...)
				}
			}
			if len(recs) > 0 {
				return recs
			}
		}
	}
	if trailRecs, ok, err := brain.ReadTrail(dir); err == nil && ok {
		return trailRecs
	}
	return nil
}

func makeBrainTurnContext(tc *turnBrainCache) func(dir, home, prompt string, files []string) search.TurnBrainContext {
	return func(dir, home, prompt string, files []string) search.TurnBrainContext {
		recs := tc.records(dir, home)
		if len(recs) == 0 {
			return search.TurnBrainContext{}
		}
		terms := extractTurnQueryTerms(prompt)
		var out search.TurnBrainContext
		for _, r := range recs {
			classifyTurnRecord(&out, dir, prompt, files, terms, r)
		}
		return out
	}
}

func totalMatchedRecords(ctx *search.TurnBrainContext) int {
	return len(ctx.Memories) + len(ctx.Bugs) + len(ctx.ADRs)
}

func classifyTurnRecord(ctx *search.TurnBrainContext, dir, prompt string, files, terms []string, r brain.Record) {
	switch r.Kind {
	case "plan":
		classifyPlanRecord(ctx, r)
	case "memory":
		classifyMemoryRecord(ctx, dir, prompt, files, terms, r)
	case "bug":
		classifyBugRecord(ctx, prompt, files, terms, r)
	case "adr":
		classifyADRRecord(ctx, prompt, files, terms, r)
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

func classifyMemoryRecord(ctx *search.TurnBrainContext, dir, prompt string, files, terms []string, r brain.Record) {
	if (r.Status != "closed" && r.Status != "") || !matchesRecordPrecision(r, prompt, files, terms) {
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

func classifyBugRecord(ctx *search.TurnBrainContext, prompt string, files, terms []string, r brain.Record) {
	if (r.Status == "open" || r.Status == "") && len(ctx.Bugs) < 2 && totalMatchedRecords(ctx) < 6 && matchesRecordPrecision(r, prompt, files, terms) {
		ctx.Bugs = append(ctx.Bugs, search.MatchedRecord{
			ID: r.ID, Kind: "bug", Status: r.Status, Title: r.Title,
			Provenance: r.Provenance, Body: r.Body,
		})
	}
}

func classifyADRRecord(ctx *search.TurnBrainContext, prompt string, files, terms []string, r brain.Record) {
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
	if (conflict != "" || (len(ctx.ADRs) < 2 && totalMatchedRecords(ctx) < 6 && matchesRecordPrecision(r, prompt, files, terms))) && len(ctx.ADRs) < 3 {
		ctx.ADRs = append(ctx.ADRs, rec)
	}
}

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

func matchesRecordPrecision(r brain.Record, prompt string, files, terms []string) bool {
	if r.ID != "" && strings.Contains(strings.ToLower(prompt), strings.ToLower(r.ID)) {
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
