package search

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	compactHookMaxBytes    = 950
	compactHop0FullMaxLine = 18
	compactHop0FullMaxSyms = 2
	compactHop1MaxMapSyms  = 3
)

func (sc *scope) renderCompactHookCapsule(o Options, g *symGraph, tiers sliceTiers, maxBytes int) string {
	if maxBytes <= 0 || maxBytes > compactHookMaxBytes {
		maxBytes = compactHookMaxBytes
	}
	footer := sc.buildCompactGraphFooter(o, g, tiers, min(320, maxBytes/2))
	symCap := maxBytes - len(footer)
	var buf bytes.Buffer
	fullCount := 0
	for _, idx := range tiers.hop0 {
		s := g.syms[idx]
		span := s.EndLine - s.StartLine + 1
		wroteFull := false
		if fullCount < compactHop0FullMaxSyms && span <= compactHop0FullMaxLine && symCap-buf.Len() > 140 {
			if blk := sc.formatHop0Symbol(s, symCap-buf.Len()); blk != "" {
				buf.WriteString(blk)
				fullCount++
				wroteFull = true
			}
		}
		if !wroteFull {
			line := fmt.Sprintf("[Hop 0] %s:%d-%d (%s) snippet — %s\n",
				s.Path, s.StartLine, s.EndLine, s.QualName(), compactSig(s.Sig, 90))
			sc.appendWithinBudget(&buf, line, symCap)
		}
	}
	hop1Count := 0
	for _, c := range tiers.hop1 {
		if hop1Count >= compactHop1MaxMapSyms {
			break
		}
		s := g.syms[c.idx]
		conf := c.confidence
		if conf == "" {
			conf = "EXTRACTED"
		}
		line := fmt.Sprintf("[Hop 1] %s:%d-%d (%s) snippet — %s [%s]\n",
			s.Path, s.StartLine, s.EndLine, s.QualName(), compactSig(s.Sig, 72), conf)
		if buf.Len()+len(line) > symCap {
			break
		}
		buf.WriteString(line)
		hop1Count++
	}
	if footer != "" && buf.Len()+len(footer) <= maxBytes {
		buf.WriteString(footer)
	}
	return strings.TrimRight(buf.String(), "\n")
}

func (sc *scope) buildCompactGraphFooter(o Options, g *symGraph, tiers sliceTiers, maxBytes int) string {
	if maxBytes <= 40 || len(tiers.hop0) == 0 {
		return ""
	}
	callers, callees, tests := g.collectCompactEdges(tiers.hop0)
	var buf bytes.Buffer
	if len(callers) > 0 || len(callees) > 0 {
		cIn := "none"
		if len(callers) > 0 {
			cIn = strings.Join(callers, ", ")
		}
		cOut := "none"
		if len(callees) > 0 {
			cOut = strings.Join(callees, ", ")
		}
		sc.appendWithinBudget(&buf, fmt.Sprintf("graph: callers: %s | callees: %s\n", cIn, cOut), maxBytes)
	}
	if len(tests) > 0 {
		sc.appendWithinBudget(&buf, "affected_tests: "+strings.Join(tests, ", ")+"\n", maxBytes)
	}
	hop0Paths := map[string]bool{}
	for _, idx := range tiers.hop0 {
		hop0Paths[g.syms[idx].Path] = true
	}
	if recs := sc.activeLinkedRecords(hop0Paths, o.BrainRefs); len(recs) > 0 {
		var parts []string
		for i, r := range recs {
			if i >= 3 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s [%s] %s", r.ID, r.Status, compactSig(r.Title, 48)))
		}
		sc.appendWithinBudget(&buf, "linked_records: "+strings.Join(parts, "; ")+"\n", maxBytes)
	}
	return buf.String()
}

func (g *symGraph) collectCompactEdges(hop0 []int) (callers, callees, tests []string) {
	seenIn, seenOut, seenTests := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, u := range hop0 {
		for _, e := range g.in[u] {
			if strings.HasSuffix(e.fromPath, "_test.go") && !seenTests[e.fromPath] && len(tests) < 4 {
				seenTests[e.fromPath] = true
				tests = append(tests, e.fromPath)
			}
			if e.fromIdx >= 0 && len(callers) < 4 {
				q := g.syms[e.fromIdx].QualName()
				if !seenIn[q] {
					seenIn[q] = true
					callers = append(callers, q)
				}
			}
		}
		for _, e := range g.out[u] {
			if e.toIdx >= 0 && len(callees) < 4 {
				q := g.syms[e.toIdx].QualName()
				if !seenOut[q] {
					seenOut[q] = true
					callees = append(callees, q)
				}
			}
		}
	}
	sort.Strings(callers)
	sort.Strings(callees)
	sort.Strings(tests)
	return callers, callees, tests
}

func compactSig(s string, maxLen int) string {
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

func extractCompactCapsuleMeta(capsule string) (pivots []CapsulePivot, files []string, symCount int) {
	pivots = ParseCapsulePivots(capsule)
	fileSet := map[string]bool{}
	for _, line := range strings.Split(capsule, "\n") {
		if !strings.HasPrefix(line, "[Hop 0] ") && !strings.HasPrefix(line, "[Hop 1] ") {
			continue
		}
		symCount++
		rest := line[strings.IndexByte(line, ']')+2:]
		if spec, _, ok := strings.Cut(rest, " "); ok {
			if colon := strings.LastIndexByte(spec, ':'); colon > 0 {
				if p := spec[:colon]; p != "" {
					fileSet[p] = true
				}
			}
		}
	}
	return pivots, sortedMapKeys(fileSet), symCount
}
