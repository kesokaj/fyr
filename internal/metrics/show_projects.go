package metrics

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type projAcc struct {
	root                                                    string
	sessions, sqliteVerbs, grepCalls, vetTurns, vetScoreSum int
	corrections, escapedBugs                                int
	activeMS, tokens, lastMS                                int64
}

func buildProjectRows(all []sess, vetsByRoot map[string]*projVetAcc, stepsByID map[string][]stp, eventsByID map[string][]sessEvent) []ProjectRow {
	byID, wtMap, _ := resolveSessionRoots(all)
	canon := buildRootCanonicalizerWithRoots(all, vetsByRoot, byID, wtMap)
	accs := map[string]*projAcc{}
	getAcc := func(raw string) *projAcc {
		r := canon(raw)
		if r == "" || isProbeBreakdownRoot(r) {
			return nil
		}
		if accs[r] == nil {
			accs[r] = &projAcc{root: r}
		}
		return accs[r]
	}
	for r, pv := range vetsByRoot {
		if p := getAcc(r); p != nil {
			p.vetTurns += pv.turns
			p.vetScoreSum += pv.scoreSum
			p.lastMS = max(p.lastMS, pv.lastMS)
		}
	}
	parentRoot := buildParentRootMap(all, byID, canon)
	seen := map[string]bool{}
	for _, x := range all {
		root := sessionProjectRoot(x, byID, parentRoot)
		if p := getAcc(root); p != nil {
			accumulateProjectSession(p, x, seen, stepsByID[x.id], eventsByID[x.id])
		}
	}
	return finalizeProjectRows(accs)
}

func buildParentRootMap(all []sess, byID map[string]string, canon func(string) string) map[string]string {
	parentRoot := map[string]string{}
	for _, x := range all {
		r := x.root
		if byID[x.id] != "" {
			r = byID[x.id]
		}
		if r != "" {
			parentRoot[x.id] = canon(r)
		}
	}
	return parentRoot
}

func sessionProjectRoot(x sess, byID, parentRoot map[string]string) string {
	root := x.root
	if byID[x.id] != "" {
		root = byID[x.id]
	}
	if (root == "" || isAgyWorktreePath(root)) && x.parent != "" {
		return parentRoot[x.parent]
	}
	return root
}

func buildRootCanonicalizer(all []sess, vetsByRoot map[string]*projVetAcc) func(string) string {
	byID, wtMap, _ := resolveSessionRoots(all)
	return buildRootCanonicalizerWithRoots(all, vetsByRoot, byID, wtMap)
}

func buildRootCanonicalizerWithRoots(all []sess, vetsByRoot map[string]*projVetAcc, byID, wtMap map[string]string) func(string) string {
	set := map[string]bool{}
	for _, x := range all {
		r := strings.TrimSpace(x.root)
		if byID[x.id] != "" {
			r = byID[x.id]
		}
		if x.parent == "" && r != "" {
			clean := filepath.Clean(r)
			if !isNonCollapsibleRoot(clean) && !isAgyWorktreePath(clean) {
				set[clean] = true
			}
		}
	}
	for r := range vetsByRoot {
		if strings.TrimSpace(r) != "" {
			clean := filepath.Clean(r)
			if !isNonCollapsibleRoot(clean) && !isAgyWorktreePath(clean) {
				set[clean] = true
			}
		}
	}
	roots := sortedRootPrefixes(set)
	cache := map[string]string{}
	return func(raw string) string {
		if v, ok := cache[raw]; ok {
			return v
		}
		v := canonicalizeProjectRoot(raw, roots, wtMap, byID)
		cache[raw] = v
		return v
	}
}

func sortedRootPrefixes(set map[string]bool) []string {
	roots := make([]string, 0, len(set))
	for r := range set {
		roots = append(roots, r)
	}
	sort.Slice(roots, func(i, j int) bool { return len(roots[i]) < len(roots[j]) })
	return roots
}

func canonicalizeProjectRoot(raw string, roots []string, wtMap, byID map[string]string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	clean := filepath.Clean(raw)
	if mapped := wtMap[clean]; mapped != "" {
		clean = mapped
	} else if main, ok := resolveGitWorktreeOnDisk(clean); ok {
		clean = main
	} else if isAgyWorktreePath(clean) {
		if fb := resolveFallbackRoot("", clean, "", byID, nil, roots); fb != "" {
			clean = fb
		}
	}
	for _, r := range roots {
		if clean == r || strings.HasPrefix(clean, r+"/") {
			return r
		}
	}
	return clean
}

func accumulateProjectSession(p *projAcc, x sess, seen map[string]bool, steps []stp, events []sessEvent) {
	main := x.parent == ""
	if main {
		p.sessions++
		p.lastMS = max(p.lastMS, max(x.start, x.end))
		addProjectEventsFromSlice(p, events)
	}
	var last int64
	for _, st := range steps {
		if st.kind == "tool" || st.kind == "assistant" || st.kind == "generation" {
			key := x.vendor + "\x00" + x.label + "\x00" + st.ext
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		if st.ts > 0 {
			if main && last > 0 && st.ts-last < activeGap.Milliseconds() {
				p.activeMS += st.ts - last
			}
			last = st.ts
			p.lastMS = max(p.lastMS, st.ts)
		}
		p.tokens += st.in + st.out + st.cr + st.cw
		addProjectStepVerbs(p, st)
	}
}

func addProjectStepVerbs(p *projAcc, st stp) {
	if st.kind != "tool" || st.tool == "" {
		return
	}
	if st.tool == "Grep" {
		p.grepCalls++
	}
	for _, verb := range st.verbs {
		switch {
		case isSQLiteVerb(verb):
			p.sqliteVerbs++
		case isGrepVerb(verb):
			p.grepCalls++
		}
	}
}

func addProjectEventsFromSlice(p *projAcc, events []sessEvent) {
	for _, ev := range events {
		switch ev.kind {
		case "bug":
			p.escapedBugs++
		case "correction":
			p.corrections++
		}
	}
}

func finalizeProjectRows(accs map[string]*projAcc) []ProjectRow {
	if len(accs) == 0 {
		return nil
	}
	out := make([]ProjectRow, 0, len(accs))
	for _, p := range accs {
		tok := p.tokens
		row := ProjectRow{
			Project: p.root, Sessions: p.sessions, ActiveMin: round4(float64(p.activeMS) / 60000),
			Tokens: tok, Corrections: p.corrections, EscapedBugs: p.escapedBugs,
		}
		if p.grepCalls > 0 {
			r := round4(float64(p.sqliteVerbs) / float64(p.grepCalls))
			row.SearchGrepRatio = &r
		}
		if p.vetTurns > 0 {
			m := round4(float64(p.vetScoreSum) / float64(p.vetTurns))
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
