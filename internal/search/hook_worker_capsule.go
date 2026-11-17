package search

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

func lookupWorkerCapsuleContext(o Options, explicitRoot, explicitDir, rawPrompt string, headerFiles []string) ([]string, []string, []MatchedRecord, *SkillMeta) {
	promptFiles := extractPromptAndSliceFiles(rawPrompt, headerFiles)
	astLines, astFiles := lookupWorkerHop0AST(explicitRoot, rawPrompt, promptFiles)
	allFiles := extractPromptAndSliceFiles(rawPrompt, append(append([]string{}, promptFiles...), astFiles...))
	recs := lookupWorkerRecords(o, explicitDir, rawPrompt, allFiles)
	skill := lookupWorkerTopSkill(o, rawPrompt, allFiles, len(astLines))
	return astLines, allFiles, recs, skill
}

func lookupWorkerHop0AST(explicitRoot, prompt string, files []string) ([]string, []string) {
	if explicitRoot == "" || checkScratchNoSymlinks(explicitRoot) != nil || !isRegularFileFast(projectDB(explicitRoot)) {
		return nil, nil
	}
	st, _, err := openLedgerStore(explicitRoot)
	if err != nil || st == nil {
		return nil, nil
	}
	defer st.close()
	sc := &scope{st: st, projects: []string{explicitRoot}}
	g, err := sc.loadSymGraph()
	if err != nil || len(g.syms) == 0 {
		return nil, nil
	}
	hop0Idxs := selectWorkerHop0Idxs(sc, g, prompt, matchGraphFiles(g, files))
	var lines, astFiles []string
	seen := map[string]bool{}
	for _, idx := range hop0Idxs {
		s := g.syms[idx]
		if clean := printable(fmt.Sprintf("[Hop 0] %s:%d-%d (%s) — %s", s.Path, s.StartLine, s.EndLine, s.QualName(), compactSig(s.Sig, 90))); clean != "" {
			lines = append(lines, clean)
		}
		if s.Path != "" && !seen[s.Path] {
			seen[s.Path] = true
			astFiles = append(astFiles, s.Path)
		}
	}
	return lines, astFiles
}

func selectWorkerHop0Idxs(sc *scope, g *symGraph, prompt string, active map[string]bool) []int {
	ranked, confident := rankAndCheckConfidence(g, prompt, active)
	if !confident {
		ranked, confident = sc.ftsFallbackRanked(g, prompt, active)
	}
	if !confident && len(active) == 0 {
		return nil
	}
	if !confident {
		ranked = g.rank(prompt, active)
	}
	low := strings.ToLower(prompt)
	sort.SliceStable(ranked, func(i, j int) bool {
		mi := len(ranked[i].Name) >= 4 && strings.Contains(low, strings.ToLower(ranked[i].Name))
		mj := len(ranked[j].Name) >= 4 && strings.Contains(low, strings.ToLower(ranked[j].Name))
		return mi && !mj
	})
	if confident {
		return g.selectSliceTiers(prompt, ranked, 0).hop0
	}
	var idxs []int
	for _, s := range ranked {
		if active[s.Path] {
			if idxs = append(idxs, s.Idx); len(idxs) >= 5 {
				break
			}
		}
	}
	return idxs
}

func matchGraphFiles(g *symGraph, files []string) map[string]bool {
	active := map[string]bool{}
	if len(files) == 0 {
		return active
	}
	allPaths := make([]string, 0, len(g.byPath))
	for p := range g.byPath {
		allPaths = append(allPaths, p)
	}
	sort.Strings(allPaths)
	for _, raw := range files {
		n := normalizeSlashPath(raw)
		switch {
		case n == "":
		case len(g.byPath[n]) > 0:
			active[n] = true
		case isDirPathToken(n):
			for _, p := range allPaths {
				if strings.HasPrefix(p, n+"/") {
					active[p] = true
				}
			}
		case !strings.Contains(n, "/"):
			if uniq := uniqueGraphBaseMatch(allPaths, n); uniq != "" {
				active[uniq] = true
			}
		}
	}
	return active
}

func uniqueGraphBaseMatch(allPaths []string, base string) string {
	var uniq string
	for _, p := range allPaths {
		if filepath.Base(p) == base {
			if uniq != "" && uniq != p {
				return ""
			}
			uniq = p
		}
	}
	return uniq
}

func lookupWorkerRecords(o Options, explicitDir, prompt string, files []string) []MatchedRecord {
	var out []MatchedRecord
	seen := map[string]bool{}
	add := func(r MatchedRecord) {
		if r.ID == "" || seen[r.ID] || (r.Kind == "memory" && r.Validity > 0 && r.Validity < 0.40) {
			return
		}
		seen[r.ID] = true
		out = append(out, r)
	}
	if explicitDir != "" && o.BrainTurnContext != nil {
		bc := o.BrainTurnContext(explicitDir, o.Home, prompt, files)
		for _, r := range append(append(append([]MatchedRecord{}, bc.Bugs...), bc.ADRs...), bc.Memories...) {
			add(r)
		}
	}
	if len(out) < 3 && explicitDir != "" && o.BrainRefsForDir != nil {
		for _, f := range files {
			for _, lr := range o.BrainRefsForDir(explicitDir, f) {
				kind := "record"
				if strings.HasPrefix(lr.ID, "BUG-") {
					kind = "bug"
				} else if strings.HasPrefix(lr.ID, "ADR-") {
					kind = "adr"
				}
				add(MatchedRecord{ID: lr.ID, Kind: kind, Status: lr.Status, Title: lr.Title})
			}
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func lookupWorkerTopSkill(o Options, prompt string, files []string, symbolHits int) *SkillMeta {
	var catalog []SkillMeta
	if o.SkillCatalog != nil {
		catalog = o.SkillCatalog(o.Home)
	}
	skills, _ := MatchSkills(prompt, ClassifyIntent(prompt, symbolHits), files, catalog)
	for i := range skills {
		if strings.TrimSpace(skills[i].Digest) != "" || strings.TrimSpace(skills[i].Description) != "" {
			sk := skills[i]
			return &sk
		}
	}
	return nil
}

func capLineUTF8(s string, maxB int) string {
	return truncateBytesUTF8(strings.Join(strings.Fields(printable(s)), " "), maxB)
}

func gatherWorkerFenceTexts(rawPrompt string, inferred []string, recs []MatchedRecord, skill *SkillMeta, astLines []string) []string {
	texts := make([]string, 0, 2+len(inferred)+len(recs)*3+len(astLines))
	texts = append(texts, rawPrompt)
	texts = append(texts, inferred...)
	for _, r := range recs {
		texts = append(texts, r.ID, r.Title, r.Body)
	}
	if skill != nil {
		texts = append(texts, skill.Name, skill.Digest, skill.Description)
	}
	return append(texts, astLines...)
}
