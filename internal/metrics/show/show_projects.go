package show

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	r "github.com/kesokaj/fyr/internal/metrics/report"
)

type projAcc struct {
	root                                                    string
	sessions, sqliteVerbs, grepCalls, vetTurns, vetScoreSum int
	corrections, escapedBugs                                int
	activeMS, tokens, lastMS                                int64
}

func (d Deps) buildProjectRowsWithOpts(all []Sess, vetsByRoot map[string]*ProjVetAcc, stepsByID map[string][]Stp, eventsByID map[string][]SessEvent, o Options, home string) []r.ProjectRow {
	var byID, wtMap map[string]string
	if d.Roots.ResolveSessionRoots != nil {
		byID, wtMap = d.Roots.ResolveSessionRoots(all)
	}
	canon := BuildRootCanonicalizerWithRoots(all, vetsByRoot, byID, wtMap, d.Roots)
	accs := map[string]*projAcc{}
	getAcc := func(raw string) *projAcc {
		root := canon(raw)
		if root == "" || (!o.IncludeSandboxes && d.isSandboxRoot(root, home)) {
			return nil
		}
		if accs[root] == nil {
			accs[root] = &projAcc{root: root}
		}
		return accs[root]
	}
	for root, pv := range vetsByRoot {
		if p := getAcc(root); p != nil {
			p.vetTurns += pv.Turns
			p.vetScoreSum += pv.ScoreSum
			p.lastMS = max(p.lastMS, pv.LastMS)
		}
	}
	parentRoot := buildParentRootMap(all, byID, canon)
	seen := map[string]bool{}
	for _, x := range all {
		root := sessionProjectRoot(x, byID, parentRoot, d.Roots.IsAgyWorktreePath)
		if p := getAcc(root); p != nil {
			accumulateProjectSession(p, x, seen, stepsByID[x.ID], eventsByID[x.ID])
		}
	}
	return finalizeProjectRows(accs)
}

func buildParentRootMap(all []Sess, byID map[string]string, canon func(string) string) map[string]string {
	parentRoot := map[string]string{}
	for _, x := range all {
		root := x.Root
		if byID[x.ID] != "" {
			root = byID[x.ID]
		}
		if root != "" {
			parentRoot[x.ID] = canon(root)
		}
	}
	return parentRoot
}

func sessionProjectRoot(x Sess, byID, parentRoot map[string]string, isWorktree func(string) bool) string {
	root := x.Root
	if byID[x.ID] != "" {
		root = byID[x.ID]
	}
	isWT := isWorktree != nil && isWorktree(root)
	if (root == "" || isWT) && x.Parent != "" {
		return parentRoot[x.Parent]
	}
	return root
}

// BuildRootCanonicalizerWithRoots builds a cached project-root canonicalizer.
func BuildRootCanonicalizerWithRoots(all []Sess, vetsByRoot map[string]*ProjVetAcc, byID, wtMap map[string]string, rf RootFuncs) func(string) string {
	set := map[string]bool{}
	for _, x := range all {
		if x.Parent != "" {
			continue
		}
		root := strings.TrimSpace(x.Root)
		if byID[x.ID] != "" {
			root = byID[x.ID]
		}
		addCollapsibleRoot(set, root, rf)
	}
	for root := range vetsByRoot {
		addCollapsibleRoot(set, strings.TrimSpace(root), rf)
	}
	roots := sortedRootPrefixes(set)
	cache := map[string]string{}
	return func(raw string) string {
		if v, ok := cache[raw]; ok {
			return v
		}
		v := canonicalizeProjectRoot(raw, roots, wtMap, byID, rf)
		cache[raw] = v
		return v
	}
}

func addCollapsibleRoot(set map[string]bool, raw string, rf RootFuncs) {
	if raw == "" {
		return
	}
	clean := filepath.Clean(raw)
	if rf.IsNonCollapsibleRoot != nil && rf.IsNonCollapsibleRoot(clean) {
		return
	}
	if rf.IsAgyWorktreePath != nil && rf.IsAgyWorktreePath(clean) {
		return
	}
	set[clean] = true
}

func sortedRootPrefixes(set map[string]bool) []string {
	roots := make([]string, 0, len(set))
	for root := range set {
		roots = append(roots, root)
	}
	sort.Slice(roots, func(i, j int) bool { return len(roots[i]) < len(roots[j]) })
	return roots
}

func canonicalizeProjectRoot(raw string, roots []string, wtMap, byID map[string]string, rf RootFuncs) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	clean := resolveCleanWorktree(filepath.Clean(raw), roots, wtMap, byID, rf)
	for _, root := range roots {
		if clean == root || strings.HasPrefix(clean, root+"/") {
			return root
		}
	}
	return clean
}

func resolveCleanWorktree(clean string, roots []string, wtMap, byID map[string]string, rf RootFuncs) string {
	if mapped := wtMap[clean]; mapped != "" {
		return mapped
	}
	if rf.ResolveGitWorktreeOnDisk != nil {
		if main, ok := rf.ResolveGitWorktreeOnDisk(clean); ok {
			return main
		}
	}
	if rf.IsAgyWorktreePath != nil && rf.IsAgyWorktreePath(clean) && rf.ResolveFallbackRoot != nil {
		if fb := rf.ResolveFallbackRoot("", clean, "", byID, nil, roots); fb != "" {
			return fb
		}
	}
	return clean
}

func accumulateProjectSession(p *projAcc, x Sess, seen map[string]bool, steps []Stp, events []SessEvent) {
	main := x.Parent == ""
	if main {
		p.sessions++
		p.lastMS = max(p.lastMS, max(x.Start, x.End))
		addProjectEventsFromSlice(p, events)
	}
	var last int64
	for _, st := range steps {
		if isReplayedStep(x, st, seen) {
			continue
		}
		if st.TS > 0 {
			if main && last > 0 && st.TS-last < ActiveGap.Milliseconds() {
				p.activeMS += st.TS - last
			}
			last = st.TS
			p.lastMS = max(p.lastMS, st.TS)
		}
		p.tokens += st.In + st.Out + st.CR + st.CW
		addProjectStepVerbs(p, st)
	}
}

func addProjectStepVerbs(p *projAcc, st Stp) {
	if st.Kind != "tool" || st.Tool == "" {
		return
	}
	sq, gr := countStepSearchVerbs(st)
	p.sqliteVerbs += sq
	p.grepCalls += gr
}

func addProjectEventsFromSlice(p *projAcc, events []SessEvent) {
	for _, ev := range events {
		switch ev.Kind {
		case "bug":
			p.escapedBugs++
		case "correction":
			p.corrections++
		}
	}
}

func finalizeProjectRows(accs map[string]*projAcc) []r.ProjectRow {
	if len(accs) == 0 {
		return nil
	}
	out := make([]r.ProjectRow, 0, len(accs))
	for _, p := range accs {
		tok := p.tokens
		row := r.ProjectRow{
			Project: p.root, Sessions: p.sessions, ActiveMin: r.Round4(float64(p.activeMS) / 60000),
			Tokens: tok, Corrections: p.corrections, EscapedBugs: p.escapedBugs,
		}
		if p.grepCalls > 0 {
			ratio := r.Round4(float64(p.sqliteVerbs) / float64(p.grepCalls))
			row.SearchGrepRatio = &ratio
		}
		if p.vetTurns > 0 {
			m := r.Round4(float64(p.vetScoreSum) / float64(p.vetTurns))
			row.MeanVetScore = &m
		}
		if p.lastMS > 0 {
			row.LastActive = time.UnixMilli(p.lastMS).UTC().Format("2006-01-02")
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		return out[i].Project < out[j].Project
	})
	return out
}
