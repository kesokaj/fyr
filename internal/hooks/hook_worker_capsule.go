package hooks

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/hooks/guard"
	"github.com/kesokaj/fyr/internal/hooks/telemetry"
	"github.com/kesokaj/fyr/internal/symgraph"
	"github.com/kesokaj/fyr/internal/trail"
	"github.com/kesokaj/fyr/internal/vet"
)

func lookupWorkerCapsuleContext(d Deps, o Options, explicitRoot, explicitDir, rawPrompt string, headerFiles []string) ([]string, []string, []vet.MatchedRecord, *vet.SkillMeta, string) {
	promptFiles := vet.ExtractPromptAndSliceFiles(rawPrompt, headerFiles)
	astLines, astFiles := lookupWorkerHop0AST(d, explicitRoot, rawPrompt, promptFiles)
	allFiles := vet.ExtractPromptAndSliceFiles(rawPrompt, append(append([]string{}, promptFiles...), astFiles...))
	recs := LookupWorkerRecords(o, explicitDir, rawPrompt, allFiles)
	skill := lookupWorkerTopSkill(o, rawPrompt, allFiles, len(astLines))
	contracts := lookupWorkerContractDigest(d, o, explicitDir, allFiles)
	return astLines, allFiles, recs, skill, contracts
}

func lookupWorkerContractDigest(d Deps, o Options, explicitDir string, files []string) string {
	if explicitDir == "" || o.FormatContractDigest == nil || len(files) == 0 {
		return ""
	}
	return o.FormatContractDigest(explicitDir, o.Home, files, d.defaultContractDigestBytes())
}

func lookupWorkerHop0AST(d Deps, explicitRoot, prompt string, files []string) ([]string, []string) {
	if explicitRoot == "" || trail.CheckScratchNoSymlinks(explicitRoot) != nil || d.ProjectDB == nil || !guard.IsRegularFileFast(d.ProjectDB(explicitRoot)) {
		return nil, nil
	}
	if d.OpenLedgerStore == nil || d.LoadBoundedSymGraphCtx == nil {
		return nil, nil
	}
	st, _, err := d.OpenLedgerStore(explicitRoot)
	if err != nil || st == nil {
		return nil, nil
	}
	defer st.Close()
	sc := &Scope{St: st, Projects: []string{explicitRoot}}
	active := resolveWorkerSeedPaths(sc, files)
	g, err := d.LoadBoundedSymGraphCtx(context.Background(), sc, prompt, active, false, 1)
	if err != nil || len(g.Syms) == 0 {
		return nil, nil
	}
	hop0Idxs := selectWorkerHop0Idxs(d, sc, g, prompt, matchGraphFiles(g, files))
	var lines, astFiles []string
	seen := map[string]bool{}
	for _, idx := range hop0Idxs {
		s := g.Syms[idx]
		if clean := d.printable(fmt.Sprintf("[Hop 0] %s:%d-%d (%s) — %s", s.Path, s.StartLine, s.EndLine, s.QualName(), symgraph.CompactSig(s.Sig, 90))); clean != "" {
			lines = append(lines, clean)
		}
		if s.Path != "" && !seen[s.Path] {
			seen[s.Path] = true
			astFiles = append(astFiles, s.Path)
		}
	}
	return lines, astFiles
}

func resolveWorkerSeedPaths(sc *Scope, files []string) map[string]bool {
	if sc == nil || sc.St == nil || sc.St.SQLDB == nil || len(files) == 0 {
		return nil
	}
	indexed, err := symgraph.QueryIndexedFilesDB(sc.St.SQLDB, sc.Projects, false)
	if err != nil || len(indexed) == 0 {
		return nil
	}
	allPaths := make([]string, 0, len(indexed))
	hasPath := make(map[string]bool, len(indexed))
	for _, f := range indexed {
		allPaths = append(allPaths, f.Path)
		hasPath[f.Path] = true
	}
	return matchPathList(allPaths, hasPath, files)
}

func selectWorkerHop0Idxs(d Deps, sc *Scope, g *symgraph.Graph, prompt string, active map[string]bool) []int {
	ranked, confident := rankAndCheckConfidence(g, prompt, active)
	if !confident {
		ranked, confident = FTSFallbackRanked(d, sc, g, prompt, active)
	}
	if !confident && len(active) == 0 {
		return nil
	}
	if !confident {
		ranked = g.Rank(prompt, active)
	}
	low := strings.ToLower(prompt)
	sort.SliceStable(ranked, func(i, j int) bool {
		mi := len(ranked[i].Name) >= 4 && strings.Contains(low, strings.ToLower(ranked[i].Name))
		mj := len(ranked[j].Name) >= 4 && strings.Contains(low, strings.ToLower(ranked[j].Name))
		return mi && !mj
	})
	if confident {
		return g.SelectSliceTiers(prompt, ranked, 0).Hop0
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

func matchGraphFiles(g *symgraph.Graph, files []string) map[string]bool {
	if len(files) == 0 {
		return map[string]bool{}
	}
	allPaths := make([]string, 0, len(g.ByPath))
	hasPath := make(map[string]bool, len(g.ByPath))
	for p := range g.ByPath {
		allPaths = append(allPaths, p)
		hasPath[p] = true
	}
	sort.Strings(allPaths)
	return matchPathList(allPaths, hasPath, files)
}

func matchPathList(allPaths []string, hasPath map[string]bool, files []string) map[string]bool {
	active := map[string]bool{}
	for _, raw := range files {
		n := vet.NormalizeSlashPath(raw)
		switch {
		case n == "":
		case hasPath[n]:
			active[n] = true
		case vet.IsDirPathToken(n):
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

// LookupWorkerRecords collects up to 3 relevant brain records for a worker capsule.
func LookupWorkerRecords(o Options, explicitDir, prompt string, files []string) []vet.MatchedRecord {
	var out []vet.MatchedRecord
	seen := map[string]bool{}
	add := func(r vet.MatchedRecord) {
		if r.ID == "" || seen[r.ID] || (r.Kind == "memory" && r.Validity > 0 && r.Validity < 0.40) {
			return
		}
		seen[r.ID] = true
		out = append(out, r)
	}
	if explicitDir != "" && hasBrainTurnContext(o) {
		bc := invokeBrainTurnContext(context.Background(), o, explicitDir, prompt, files)
		appendWorkerBrainTurnRecords(bc, add)
	}
	if len(out) < 3 && explicitDir != "" && o.BrainRefsForDir != nil {
		appendWorkerBrainRefRecords(o.BrainRefsForDir, explicitDir, files, add)
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func appendWorkerBrainTurnRecords(bc vet.TurnBrainContext, add func(vet.MatchedRecord)) {
	for _, r := range bc.Bugs {
		add(r)
	}
	for _, r := range bc.ADRs {
		add(r)
	}
	for _, r := range bc.Memories {
		add(r)
	}
}

func appendWorkerBrainRefRecords(refsFn func(string, string) []symgraph.LinkedRecord, explicitDir string, files []string, add func(vet.MatchedRecord)) {
	for _, f := range files {
		for _, lr := range refsFn(explicitDir, f) {
			add(vet.MatchedRecord{ID: lr.ID, Kind: linkedRecordKind(lr.ID), Status: lr.Status, Title: lr.Title})
		}
	}
}

func linkedRecordKind(id string) string {
	if strings.HasPrefix(id, "BUG-") {
		return "bug"
	}
	if strings.HasPrefix(id, "ADR-") {
		return "adr"
	}
	return "record"
}

func lookupWorkerTopSkill(o Options, prompt string, files []string, symbolHits int) *vet.SkillMeta {
	var catalog []vet.SkillMeta
	if o.SkillCatalog != nil {
		catalog = o.SkillCatalog(o.Home)
	}
	skills, _ := vet.MatchSkills(prompt, vet.ClassifyIntent(prompt, symbolHits), files, catalog)
	for i := range skills {
		if strings.TrimSpace(skills[i].Digest) != "" || strings.TrimSpace(skills[i].Description) != "" {
			sk := skills[i]
			return &sk
		}
	}
	return nil
}

func capLineUTF8(d Deps, s string, maxB int) string {
	return telemetry.TruncateBytesUTF8(strings.Join(strings.Fields(d.printable(s)), " "), maxB)
}

func gatherWorkerFenceTexts(rawPrompt string, inferred []string, recs []vet.MatchedRecord, skill *vet.SkillMeta, astLines []string, contractDigest string) []string {
	texts := make([]string, 0, 3+len(inferred)+len(recs)*3+len(astLines))
	texts = append(texts, rawPrompt)
	texts = append(texts, inferred...)
	for _, r := range recs {
		texts = append(texts, r.ID, r.Title, r.Body)
	}
	if skill != nil {
		texts = append(texts, skill.Name, skill.Digest, skill.Description)
	}
	if contractDigest != "" {
		texts = append(texts, contractDigest)
	}
	return append(texts, astLines...)
}
