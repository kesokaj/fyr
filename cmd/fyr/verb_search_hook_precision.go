package main

import (
	"path/filepath"
	"sort"
	"strings"

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
