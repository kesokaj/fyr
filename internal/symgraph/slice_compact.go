package symgraph

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/readledger"
)

const (
	// CompactHookMaxBytes caps the hook pre-invocation compact slice capsule size.
	CompactHookMaxBytes    = 950
	compactHop0FullMaxLine = 18
	compactHop0FullMaxSyms = 2
	compactHop1MaxMapSyms  = 3
)

// RenderCompactHookCapsule formats a compact <= 950 B hook capsule with Hop 0/1 snippets and graph footer.
func (sc *Scope) RenderCompactHookCapsule(o SliceOptions, g *Graph, tiers SliceTiers, maxBytes int) string {
	if maxBytes <= 0 || maxBytes > CompactHookMaxBytes {
		maxBytes = CompactHookMaxBytes
	}
	footer := sc.buildCompactGraphFooter(o, g, tiers, min(320, maxBytes/2))
	symCap := maxBytes - len(footer)
	var buf bytes.Buffer
	sc.writeCompactHop0(&buf, g, tiers.Hop0, SliceLineCache{}, symCap)
	writeCompactHop1(&buf, g, tiers.Hop1, symCap)
	if footer != "" && buf.Len()+len(footer) <= maxBytes {
		buf.WriteString(footer)
	}
	return strings.TrimRight(buf.String(), "\n")
}

func (sc *Scope) writeCompactHop0(buf *bytes.Buffer, g *Graph, hop0 []int, cache SliceLineCache, symCap int) {
	fullCount := 0
	for _, idx := range hop0 {
		s := g.Syms[idx]
		if sc.tryWriteFullHop0(buf, cache, s, symCap, fullCount) {
			fullCount++
			continue
		}
		line := fmt.Sprintf("[Hop 0] %s:%d-%d (%s) snippet — %s\n",
			s.Path, s.StartLine, s.EndLine, s.QualName(), CompactSig(s.Sig, 90))
		sc.appendWithinBudget(buf, line, symCap)
	}
}

func (sc *Scope) tryWriteFullHop0(buf *bytes.Buffer, cache SliceLineCache, s RankedSymbol, symCap, fullCount int) bool {
	span := s.EndLine - s.StartLine + 1
	if fullCount >= compactHop0FullMaxSyms || span > compactHop0FullMaxLine || symCap-buf.Len() <= 140 {
		return false
	}
	blk := sc.FormatHop0Symbol(cache, s, symCap-buf.Len())
	if blk == "" {
		return false
	}
	buf.WriteString(blk)
	return true
}

func writeCompactHop1(buf *bytes.Buffer, g *Graph, hop1 []Hop1Cand, symCap int) {
	hop1Count := 0
	for _, c := range hop1 {
		if hop1Count >= compactHop1MaxMapSyms {
			break
		}
		s := g.Syms[c.Idx]
		conf := c.Confidence
		if conf == "" {
			conf = ConfidenceExtracted
		}
		line := fmt.Sprintf("[Hop 1] %s:%d-%d (%s) snippet — %s [%s]\n",
			s.Path, s.StartLine, s.EndLine, s.QualName(), CompactSig(s.Sig, 72), conf)
		if buf.Len()+len(line) > symCap {
			break
		}
		buf.WriteString(line)
		hop1Count++
	}
}

func (sc *Scope) buildCompactGraphFooter(o SliceOptions, g *Graph, tiers SliceTiers, maxBytes int) string {
	if maxBytes <= 40 || len(tiers.Hop0) == 0 {
		return ""
	}
	callers, callees, tests := g.collectCompactEdges(tiers.Hop0)
	var buf bytes.Buffer
	if edgeLine := formatCompactEdgeLine(callers, callees); edgeLine != "" {
		sc.appendWithinBudget(&buf, edgeLine, maxBytes)
	}
	if len(tests) > 0 {
		sc.appendWithinBudget(&buf, "affected_tests: "+strings.Join(tests, ", ")+"\n", maxBytes)
	}
	sc.appendCompactLinkedRecords(&buf, o, g, tiers.Hop0, maxBytes)
	return buf.String()
}

func formatCompactEdgeLine(callers, callees []string) string {
	if len(callers) == 0 && len(callees) == 0 {
		return ""
	}
	cIn, cOut := "none", "none"
	if len(callers) > 0 {
		cIn = strings.Join(callers, ", ")
	}
	if len(callees) > 0 {
		cOut = strings.Join(callees, ", ")
	}
	return fmt.Sprintf("graph: callers: %s | callees: %s\n", cIn, cOut)
}

func (sc *Scope) appendCompactLinkedRecords(buf *bytes.Buffer, o SliceOptions, g *Graph, hop0 []int, maxBytes int) {
	hop0Paths := make(map[string]bool, len(hop0))
	for _, idx := range hop0 {
		hop0Paths[g.Syms[idx].Path] = true
	}
	recs := sc.linkedRecords(hop0Paths, o.BrainRefs)
	if len(recs) == 0 {
		return
	}
	parts := make([]string, 0, min(3, len(recs)))
	for i, r := range recs {
		if i >= 3 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s [%s] %s", r.ID, r.Status, CompactSig(r.Title, 48)))
	}
	sc.appendWithinBudget(buf, "linked_records: "+strings.Join(parts, "; ")+"\n", maxBytes)
}

func (g *Graph) collectCompactEdges(hop0 []int) (callers, callees, tests []string) {
	seenIn, seenOut, seenTests := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, u := range hop0 {
		callers, tests = g.collectCompactInEdges(u, seenIn, seenTests, callers, tests)
		callees = g.collectCompactOutEdges(u, seenOut, callees)
	}
	sort.Strings(callers)
	sort.Strings(callees)
	sort.Strings(tests)
	return callers, callees, tests
}

func (g *Graph) collectCompactInEdges(u int, seenIn, seenTests map[string]bool, callers, tests []string) ([]string, []string) {
	for _, e := range g.In[u] {
		if strings.HasSuffix(e.FromPath, "_test.go") && !seenTests[e.FromPath] && len(tests) < 4 {
			seenTests[e.FromPath] = true
			tests = append(tests, e.FromPath)
		}
		if e.FromIdx < 0 || len(callers) >= 4 {
			continue
		}
		if q := g.Syms[e.FromIdx].QualName(); !seenIn[q] {
			seenIn[q] = true
			callers = append(callers, q)
		}
	}
	return callers, tests
}

func (g *Graph) collectCompactOutEdges(u int, seenOut map[string]bool, callees []string) []string {
	for _, e := range g.Out[u] {
		if e.ToIdx < 0 || len(callees) >= 4 {
			continue
		}
		if q := g.Syms[e.ToIdx].QualName(); !seenOut[q] {
			seenOut[q] = true
			callees = append(callees, q)
		}
	}
	return callees
}

// CompactSig collapses whitespace in s and truncates to maxLen bytes on a UTF-8 boundary.
func CompactSig(s string, maxLen int) string {
	fields := strings.Fields(s)
	out := strings.Join(fields, " ")
	if maxLen > 3 && len(out) > maxLen {
		cut := maxLen - 3
		for cut > 0 && !utf8.RuneStart(out[cut]) {
			cut--
		}
		return strings.TrimSpace(out[:cut]) + "..."
	}
	return out
}

// ExtractCompactCapsuleMeta extracts capsule pivots, prioritized hop file paths, and symbol count.
func ExtractCompactCapsuleMeta(capsule string) (pivots []readledger.CapsulePivot, files []string, symCount int) {
	pivots = readledger.ParseCapsulePivots(capsule)
	hop0Set := map[string]bool{}
	hop1Set := map[string]bool{}
	for _, line := range strings.Split(capsule, "\n") {
		p, isHop0, ok := parseCompactHopLine(line)
		if !ok {
			continue
		}
		symCount++
		if p == "" {
			continue
		}
		if isHop0 {
			hop0Set[p] = true
		} else {
			hop1Set[p] = true
		}
	}
	if len(hop0Set) > 0 {
		return pivots, SortedMapKeys(hop0Set), symCount
	}
	return pivots, SortedMapKeys(hop1Set), symCount
}

func parseCompactHopLine(line string) (filePath string, isHop0, ok bool) {
	switch {
	case strings.HasPrefix(line, "[Hop 0] "):
		isHop0 = true
	case strings.HasPrefix(line, "[Hop 1] "):
	default:
		return "", false, false
	}
	rest := line[strings.IndexByte(line, ']')+2:]
	spec, _, hasSpace := strings.Cut(rest, " ")
	if !hasSpace {
		return "", isHop0, true
	}
	colon := strings.LastIndexByte(spec, ':')
	if colon <= 0 {
		return "", isHop0, true
	}
	return spec[:colon], isHop0, true
}
