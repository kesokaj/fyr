package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

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
	if home != "" {
		if _, err := os.Stat(brain.Path(home)); err == nil {
			if recs, err := brain.List(brain.Options{Home: home, Dir: dir, Global: global}, brain.ListFilter{}); err == nil && len(recs) > 0 {
				return recs
			}
		}
	}
	if recs, ok, err := brain.ReadTrail(dir); err == nil && ok {
		return recs
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
		if (r.Status == "open" || r.Status == "") && ctx.ActivePlan == nil {
			ctx.ActivePlan = &search.MatchedRecord{
				ID: r.ID, Kind: "plan", Status: r.Status, Title: r.Title,
				Provenance: r.Provenance, Body: summarizePlanReadyPhases(r.Body),
			}
		}
	case "memory":
		if (r.Status == "closed" || r.Status == "") && len(ctx.Memories) < 2 && totalMatchedRecords(ctx) < 4 && matchesRecordPrecision(r, prompt, files, terms) {
			ctx.Memories = append(ctx.Memories, search.MatchedRecord{
				ID: r.ID, Kind: "memory", Status: r.Status, Title: r.Title,
				Provenance: r.Provenance, Body: r.Body, StaleTag: brain.MemoryStalenessTag(dir, r),
			})
		}
	case "bug":
		if (r.Status == "open" || r.Status == "") && len(ctx.Bugs) < 2 && totalMatchedRecords(ctx) < 4 && matchesRecordPrecision(r, prompt, files, terms) {
			ctx.Bugs = append(ctx.Bugs, search.MatchedRecord{
				ID: r.ID, Kind: "bug", Status: r.Status, Title: r.Title,
				Provenance: r.Provenance, Body: r.Body,
			})
		}
	case "adr":
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
		if (conflict != "" || (len(ctx.ADRs) < 2 && totalMatchedRecords(ctx) < 4 && matchesRecordPrecision(r, prompt, files, terms))) && len(ctx.ADRs) < 3 {
			ctx.ADRs = append(ctx.ADRs, rec)
		}
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

func makeSkillCatalog() func(home string) []search.SkillMeta {
	return func(home string) []search.SkillMeta {
		if home == "" {
			return nil
		}
		skillsDir := filepath.Join(home, ".gemini", "config", "skills")
		entries, err := os.ReadDir(skillsDir)
		if err != nil {
			return nil
		}
		var out []search.SkillMeta
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(skillsDir, e.Name(), "SKILL.md")
			if meta, ok := parseSkillMetaFile(e.Name(), p); ok {
				out = append(out, meta)
			}
		}
		return out
	}
}

func parseSkillMetaFile(name, path string) (search.SkillMeta, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return search.SkillMeta{}, false
	}
	raw := string(data)
	desc := extractSkillDescription(raw)
	digest := extractActionableSkillDigest(raw)
	if digest == "" {
		digest = desc
	}
	if desc == "" && digest == "" {
		return search.SkillMeta{}, false
	}
	desc = truncateUTF8(desc, 600)
	digest = truncateUTF8(digest, 600)
	return search.SkillMeta{
		Name:        name,
		Description: desc,
		Path:        "skills/" + name + "/SKILL.md",
		Digest:      digest,
	}, true
}

func extractSkillDescription(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	var descLines []string
	inDesc := false
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "---" {
			break
		}
		if strings.HasPrefix(line, "description:") {
			inDesc = true
			rest := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			if rest != "" && rest != "|" && rest != ">" {
				descLines = append(descLines, strings.Trim(rest, `"'`))
			}
			continue
		}
		if inDesc {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				descLines = append(descLines, strings.TrimSpace(line))
			} else {
				break
			}
		}
	}
	return strings.Join(descLines, " ")
}

func extractActionableSkillDigest(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	start := 0
	if len(lines) >= 3 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				start = i + 1
				break
			}
		}
	}
	var parts []string
	inFence := false
	total := 0
	for i := start; i < len(lines) && total < 580; i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence || t == "" || strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "<!--") {
			continue
		}
		if len(t) > 180 {
			t = truncateUTF8(t, 180) + "..."
		}
		parts = append(parts, t)
		total += len(t) + 3
		if len(parts) >= 6 {
			break
		}
	}
	return truncateUTF8(strings.Join(parts, " | "), 600)
}

func truncateUTF8(s string, maxBytes int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxBytes {
		return s
	}
	i := maxBytes
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return strings.TrimSpace(s[:i])
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
