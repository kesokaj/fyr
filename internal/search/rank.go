package search

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
)

const (
	multInPrompt   = 10.0
	multLongIdent  = 10.0
	multUnexported = 0.1
	multManyFiles  = 0.1
	multActiveFile = 50.0
	pprIterations  = 20
	pprDamping     = 0.85
)

func (sc *scope) sessionActiveFiles(sessionID string, extra map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range extra {
		if v {
			out[strings.TrimPrefix(filepath.ToSlash(k), "./")] = true
		}
	}
	if sessionID == "" || sc.st == nil {
		return out
	}
	rows, err := sc.st.db.Query(`SELECT DISTINCT resolved_path FROM read_ledger WHERE conversation_id = ? AND start_line >= -1`, sessionID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			out[filepath.ToSlash(p)] = true
			for _, root := range sc.projects {
				if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
					out[filepath.ToSlash(rel)] = true
				}
			}
		}
	}
	return out
}

func (g *symGraph) rank(query string, activeFiles map[string]bool) []RankedSymbol {
	if len(g.syms) == 0 {
		return nil
	}
	terms := parseQueryTerms(query)
	mul, matched := g.computeMultipliers(terms, activeFiles)
	pers := g.buildPersonalization(mul, matched, activeFiles)
	scores := g.runPowerMethod(mul, pers, len(terms) > 0 || len(activeFiles) > 0)
	for i := range g.syms {
		g.syms[i].Score = scores[i]
	}
	out := make([]RankedSymbol, len(g.syms))
	copy(out, g.syms)
	sort.Slice(out, func(i, j int) bool {
		if math.Abs(out[i].Score-out[j].Score) > 1e-12 {
			return out[i].Score > out[j].Score
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].StartLine < out[j].StartLine
	})
	return out
}

func (g *symGraph) computeMultipliers(terms []string, activeFiles map[string]bool) ([]float64, []bool) {
	mul, matched := make([]float64, len(g.syms)), make([]bool, len(g.syms))
	for i, s := range g.syms {
		m := 1.0
		if matched[i] = matchesPromptTerms(s, terms); matched[i] {
			m *= multInPrompt
		}
		if len(s.Name) >= 8 {
			m *= multLongIdent
		}
		if !s.Exported {
			m *= multUnexported
		}
		if g.defFile[s.Name] > 5 {
			m *= multManyFiles
		}
		if activeFiles[s.Path] {
			m *= multActiveFile
		}
		if !matched[i] && strings.HasSuffix(s.Path, "_test.go") {
			m *= multUnexported
		}
		mul[i] = m
	}
	return mul, matched
}

func (g *symGraph) buildPersonalization(mul []float64, matched []bool, activeFiles map[string]bool) []float64 {
	pers, hasSeed, sum := make([]float64, len(g.syms)), false, 0.0
	for i, s := range g.syms {
		if matched[i] || activeFiles[s.Path] {
			hasSeed = true
			break
		}
	}
	for i, s := range g.syms {
		w := mul[i] / math.Sqrt(float64(max(1, s.InRefs)))
		if hasSeed {
			if matched[i] || activeFiles[s.Path] {
				w = mul[i]
			} else {
				w *= 1e-3
			}
		}
		pers[i] = w
		sum += w
	}
	if sum > 0 {
		for i := range pers {
			pers[i] /= sum
		}
	}
	return pers
}

func (g *symGraph) runPowerMethod(mul, pers []float64, personalized bool) []float64 {
	n := len(g.syms)
	scores, next, nodeW := make([]float64, n), make([]float64, n), make([]float64, n)
	for i, s := range g.syms {
		nodeW[i] = mul[i] / math.Sqrt(float64(max(1, s.InRefs)))
	}
	copy(scores, pers)
	revFactor := 0.0
	if personalized {
		revFactor = 0.35
	}
	for iter := 0; iter < pprIterations; iter++ {
		for i := range next {
			next[i] = (1.0 - pprDamping) * pers[i]
		}
		for u := 0; u < n; u++ {
			g.stepNodePower(u, mul[u], nodeW, scores[u], revFactor, next)
		}
		copy(scores, next)
	}
	return scores
}

func (g *symGraph) stepNodePower(u int, selfW float64, nodeW []float64, scoreU, revFactor float64, next []float64) {
	denom := selfW
	for _, e := range g.out[u] {
		denom += e.weight * nodeW[e.toIdx]
	}
	for _, e := range g.in[u] {
		if e.fromIdx >= 0 && revFactor > 0 {
			denom += revFactor * e.weight * nodeW[e.fromIdx]
		}
	}
	if denom <= 0 {
		return
	}
	mass := pprDamping * scoreU / denom
	next[u] += mass * selfW
	for _, e := range g.out[u] {
		next[e.toIdx] += mass * e.weight * nodeW[e.toIdx]
	}
	for _, e := range g.in[u] {
		if e.fromIdx >= 0 && revFactor > 0 {
			next[e.fromIdx] += mass * revFactor * e.weight * nodeW[e.fromIdx]
		}
	}
}

func parseQueryTerms(query string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(query, func(r rune) bool {
		return !isWordRune(r) && r != '.' && r != '/' && r != '-'
	}) {
		if f = strings.Trim(f, "./-"); f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func matchesPromptTerms(s RankedSymbol, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	nameLow, qualLow, pathLow := strings.ToLower(s.Name), strings.ToLower(s.QualName()), strings.ToLower(s.Path)
	baseLow := strings.ToLower(strings.TrimSuffix(filepath.Base(s.Path), filepath.Ext(s.Path)))
	shortShellSym := strings.HasSuffix(pathLow, ".sh") && len(nameLow) < hookMinIdentLen
	for _, t := range terms {
		tl := strings.ToLower(t)
		if pathLow == tl || (strings.HasSuffix(tl, ".sh") && filepath.Base(pathLow) == filepath.Base(tl)) {
			return true
		}
		if shortShellSym {
			continue
		}
		if nameLow == tl || qualLow == tl || baseLow == tl {
			return true
		}
		if len(tl) >= 4 && (strings.Contains(nameLow, tl) || strings.Contains(qualLow, tl)) {
			return true
		}
	}
	return false
}
