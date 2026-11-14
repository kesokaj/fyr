package search

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultSliceHops   = 2
	defaultSliceBudget = 2000
	hop1MaxFullLines   = 40
	hop1SnippetLines   = 5
)

type hop1Cand struct {
	idx        int
	callLine   int
	callPath   string
	confidence string
}

type sliceTiers struct {
	hop0 []int
	hop1 []hop1Cand
	hop2 []int
}

// BuildContextSlice emits a tiered 0/1/2-hop context capsule within o.Budget tokens (chars / 4).
func BuildContextSlice(o Options, out io.Writer) error {
	if o.Hops == 0 {
		o.Hops = defaultSliceHops
	}
	if o.Budget == 0 {
		o.Budget = defaultSliceBudget
	}
	sc, err := prepare(o, out)
	if err != nil {
		return err
	}
	defer sc.close()
	return sc.buildContextSlice(o, out)
}

func (sc *scope) buildContextSlice(o Options, out io.Writer) error {
	if o.Hops <= 0 && o.Slice != "" {
		o.Hops = defaultSliceHops
	}
	if o.Budget <= 0 {
		o.Budget = defaultSliceBudget
	}
	g, err := sc.loadSymGraph()
	if err != nil {
		return err
	}
	if len(g.syms) == 0 {
		return nil
	}
	active := sc.sessionActiveFiles(o.Session, o.ActiveFiles)
	ranked := g.rank(o.Slice, active)
	tiers := g.selectSliceTiers(o.Slice, ranked, o.Hops)
	maxBytes := max(200, o.Budget*4)
	content := sc.renderSliceCapsule(o, g, tiers, maxBytes)
	_, err = io.WriteString(out, content)
	return err
}

func (g *symGraph) selectSliceTiers(query string, ranked []RankedSymbol, maxHops int) sliceTiers {
	terms := parseQueryTerms(query)
	hop := map[int]int{}
	var t sliceTiers
	for _, s := range ranked {
		if matchesPromptTerms(s, terms) {
			hop[s.Idx] = 0
			t.hop0 = append(t.hop0, s.Idx)
			if len(t.hop0) >= 5 {
				break
			}
		}
	}
	if len(t.hop0) == 0 {
		for i := 0; i < min(3, len(ranked)); i++ {
			hop[ranked[i].Idx] = 0
			t.hop0 = append(t.hop0, ranked[i].Idx)
		}
	}
	if maxHops >= 1 {
		t.hop1 = g.collectHop1(t.hop0, hop)
	}
	if maxHops >= 2 {
		t.hop2 = g.collectHop2(t.hop1, hop)
	}
	return t
}

func (g *symGraph) collectHop1(hop0 []int, hop map[int]int) []hop1Cand {
	var out []hop1Cand
	for _, u := range hop0 {
		for _, e := range g.out[u] {
			if _, seen := hop[e.toIdx]; !seen {
				hop[e.toIdx] = 1
				out = append(out, hop1Cand{idx: e.toIdx, callLine: e.line, callPath: e.fromPath, confidence: e.confidence})
			}
		}
		for _, e := range g.in[u] {
			if e.fromIdx >= 0 {
				if _, seen := hop[e.fromIdx]; !seen {
					hop[e.fromIdx] = 1
					out = append(out, hop1Cand{idx: e.fromIdx, callLine: e.line, callPath: e.fromPath, confidence: e.confidence})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return g.syms[out[i].idx].Score > g.syms[out[j].idx].Score
	})
	return out
}

func (g *symGraph) collectHop2(hop1 []hop1Cand, hop map[int]int) []int {
	var out []int
	for _, c := range hop1 {
		for _, e := range g.out[c.idx] {
			if _, seen := hop[e.toIdx]; !seen {
				hop[e.toIdx] = 2
				out = append(out, e.toIdx)
			}
		}
		for _, e := range g.in[c.idx] {
			if e.fromIdx >= 0 {
				if _, seen := hop[e.fromIdx]; !seen {
					hop[e.fromIdx] = 2
					out = append(out, e.fromIdx)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return g.syms[out[i]].Score > g.syms[out[j]].Score
	})
	return out
}

func (sc *scope) renderSliceCapsule(o Options, g *symGraph, tiers sliceTiers, maxBytes int) string {
	var buf bytes.Buffer
	hdr := fmt.Sprintf("# slice: %s (budget: %d tokens)\n", o.Slice, o.Budget)
	if len(hdr) <= maxBytes {
		buf.WriteString(hdr)
	}
	paths := map[string]bool{}
	for _, idx := range tiers.hop0 {
		paths[g.syms[idx].Path] = true
	}
	for _, c := range tiers.hop1 {
		paths[g.syms[c.idx].Path] = true
	}
	var extrasBuf bytes.Buffer
	extrasCap := min(max(0, maxBytes-buf.Len()), max(360, maxBytes/2))
	sc.appendSliceExtras(&extrasBuf, paths, o.BrainRefs, extrasCap)
	symMax := max(buf.Len(), maxBytes-extrasBuf.Len())
	hop0Cap, hop1Cap := symMax, symMax
	if len(tiers.hop1) > 0 && len(tiers.hop2) > 0 {
		hop0Cap = symMax * 60 / 100
		hop1Cap = symMax * 85 / 100
	} else if len(tiers.hop1) > 0 {
		hop0Cap = symMax * 75 / 100
	}
	for _, idx := range tiers.hop0 {
		s := g.syms[idx]
		sc.appendWithinBudget(&buf, sc.formatHop0Symbol(s, hop0Cap-buf.Len()), hop0Cap)
	}
	for _, c := range tiers.hop1 {
		s := g.syms[c.idx]
		sc.appendWithinBudget(&buf, sc.formatHop1Symbol(s, c, hop1Cap-buf.Len()), hop1Cap)
	}
	for _, idx := range tiers.hop2 {
		s := g.syms[idx]
		line := fmt.Sprintf("[Hop 2] %s:%d-%d (%s)  %s\n", s.Path, s.StartLine, s.EndLine, s.QualName(), s.Sig)
		sc.appendWithinBudget(&buf, line, symMax)
	}
	sc.appendWithinBudget(&buf, extrasBuf.String(), maxBytes)
	return buf.String()
}

func (sc *scope) appendWithinBudget(buf *bytes.Buffer, block string, capBytes int) {
	if block != "" && buf.Len()+len(block) <= capBytes {
		buf.WriteString(block)
	}
}

func (sc *scope) appendSliceExtras(buf *bytes.Buffer, paths map[string]bool, brainRefs func(string) []LinkedRecord, maxBytes int) {
	recs := sc.activeLinkedRecords(paths, brainRefs)
	if len(recs) > 0 {
		wroteHdr := false
		for _, r := range recs {
			line := fmt.Sprintf("  %s [%s] %s\n", r.ID, r.Status, r.Title)
			if !wroteHdr {
				if buf.Len()+len("linked_records:\n")+len(line) > maxBytes {
					break
				}
				buf.WriteString("linked_records:\n")
				wroteHdr = true
			}
			sc.appendWithinBudget(buf, line, maxBytes)
		}
	}
	hints := sc.activeCoChangeHints(paths)
	if len(hints) > 0 {
		wroteHdr := false
		for _, h := range hints {
			line := "  " + h + "\n"
			if !wroteHdr {
				if buf.Len()+len("co_change_hint:\n")+len(line) > maxBytes {
					break
				}
				buf.WriteString("co_change_hint:\n")
				wroteHdr = true
			}
			sc.appendWithinBudget(buf, line, maxBytes)
		}
	}
}

func (sc *scope) formatHop0Symbol(s RankedSymbol, remBytes int) string {
	hdr := fmt.Sprintf("[Hop 0] %s:%d-%d (%s)\n", s.Path, s.StartLine, s.EndLine, s.QualName())
	if len(hdr) > remBytes {
		return ""
	}
	lines := sc.readLinesRange(s.OID, s.Path, s.StartLine, s.EndLine)
	if len(lines) == 0 {
		lines = []string{s.Sig}
	}
	return fitLinesToBudget(hdr, lines, s.StartLine, remBytes)
}

func (sc *scope) formatHop1Symbol(s RankedSymbol, c hop1Cand, remBytes int) string {
	conf := c.confidence
	if conf == "" {
		conf = "EXTRACTED"
	}
	span := s.EndLine - s.StartLine + 1
	if span <= hop1MaxFullLines {
		hdr := fmt.Sprintf("[Hop 1] %s:%d-%d (%s) [%s]\n", s.Path, s.StartLine, s.EndLine, s.QualName(), conf)
		lines := sc.readLinesRange(s.OID, s.Path, s.StartLine, s.EndLine)
		if len(lines) == 0 {
			lines = []string{s.Sig}
		}
		return fitLinesToBudget(hdr, lines, s.StartLine, remBytes)
	}
	start := s.StartLine
	if c.callLine >= s.StartLine && c.callLine <= s.EndLine {
		start = max(s.StartLine, c.callLine-2)
	}
	end := min(s.EndLine, start+hop1SnippetLines-1)
	hdr := fmt.Sprintf("[Hop 1] %s:%d-%d (%s snippet %d-%d) [%s]\n", s.Path, s.StartLine, s.EndLine, s.QualName(), start, end, conf)
	lines := sc.readLinesRange(s.OID, s.Path, start, end)
	return fitLinesToBudget(hdr, lines, start, remBytes)
}

func fitLinesToBudget(hdr string, lines []string, startLine, remBytes int) string {
	if len(hdr) > remBytes {
		return ""
	}
	var b strings.Builder
	b.WriteString(hdr)
	for i, line := range lines {
		row := fmt.Sprintf("  %d: %s\n", startLine+i, line)
		if b.Len()+len(row) > remBytes {
			break
		}
		b.WriteString(row)
	}
	return b.String()
}

func (sc *scope) readLinesRange(oid, relPath string, startLine, endLine int) []string {
	for _, root := range sc.projects {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
		if err != nil {
			continue
		}
		all := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		s, e := max(1, startLine), min(len(all), endLine)
		if s <= e {
			return all[s-1 : e]
		}
	}
	var out []string
	for line := startLine; line <= endLine; line++ {
		out = append(out, sc.lineText(oid, line))
	}
	return out
}

func (sc *scope) buildFileSkeleton(targetPath string, out io.Writer) error {
	rel := sc.normalizePath(targetPath)
	q := `SELECT f.path, sy.start_line, sy.end_line, sy.sig
		FROM symbols sy JOIN files f ON f.oid = sy.oid
		WHERE f.project IN ` + sc.in() + ` AND (f.path = ? OR f.path LIKE '%' || ?)
		ORDER BY CASE WHEN f.path = ? THEN 0 ELSE 1 END, f.path, sy.start_line, sy.end_line`
	args := append(sc.keys(), rel, "/"+rel, rel)
	rows, err := sc.st.db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	chosenPath := ""
	count := 0
	for rows.Next() {
		var p, sig string
		var start, end int
		if err := rows.Scan(&p, &start, &end, &sig); err != nil {
			return err
		}
		if chosenPath == "" {
			chosenPath = p
			fmt.Fprintf(out, "# %s\n", p)
		}
		if p != chosenPath {
			break
		}
		fmt.Fprintf(out, "%d-%d  %s\n", start, end, sig)
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("search: no indexed symbols for %s", targetPath)
	}
	return nil
}

func (sc *scope) normalizePath(p string) string {
	p = strings.TrimSpace(p)
	for _, root := range sc.projects {
		if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return strings.TrimPrefix(filepath.ToSlash(p), "./")
}
