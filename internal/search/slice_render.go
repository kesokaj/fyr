package search

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	hop1MaxFullLines = 40
	hop1SnippetLines = 5
)

type sliceLineCache map[string][]string

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
	sc.appendSliceExtras(&extrasBuf, o, paths, extrasCap)
	symMax := max(buf.Len(), maxBytes-extrasBuf.Len())
	hop0Cap, hop1Cap := symMax, symMax
	if len(tiers.hop1) > 0 && len(tiers.hop2) > 0 {
		hop0Cap = symMax * 60 / 100
		hop1Cap = symMax * 85 / 100
	} else if len(tiers.hop1) > 0 {
		hop0Cap = symMax * 75 / 100
	}
	cache := sliceLineCache{}
	for _, idx := range tiers.hop0 {
		s := g.syms[idx]
		sc.appendWithinBudget(&buf, sc.formatHop0Symbol(cache, s, hop0Cap-buf.Len()), hop0Cap)
	}
	for _, c := range tiers.hop1 {
		s := g.syms[c.idx]
		sc.appendWithinBudget(&buf, sc.formatHop1Symbol(cache, s, c, hop1Cap-buf.Len()), hop1Cap)
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

func (sc *scope) appendSliceExtras(buf *bytes.Buffer, o Options, paths map[string]bool, maxBytes int) {
	recs := sc.activeLinkedRecords(paths, o.BrainRefs)
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
	sc.appendSliceContracts(buf, o, paths, maxBytes)
}

func (sc *scope) appendSliceContracts(buf *bytes.Buffer, o Options, paths map[string]bool, maxBytes int) {
	if o.BrainSpecBodies == nil || len(paths) == 0 {
		return
	}
	const hdr = "spec_contracts:\n"
	avail := maxBytes - buf.Len()
	if avail-len(hdr) < 24 {
		return
	}
	pathList := make([]string, 0, len(paths))
	for p := range paths {
		pathList = append(pathList, p)
	}
	sort.Strings(pathList)
	if o.Hook != "" {
		digest := FormatContractDigest(o.BrainSpecBodies(o.Dir, o.Home, pathList), min(defaultContractDigestBytes, avail-len(hdr)))
		if digest == "" {
			return
		}
		if block := formatFencedSliceContracts(hdr, digest, avail); block != "" {
			buf.WriteString(block)
		}
		return
	}
	capB := min(defaultContractDigestBytes, avail)
	digest := FormatContractDigest(o.BrainSpecBodies(o.Dir, o.Home, pathList), capB-len(hdr))
	if digest == "" {
		return
	}
	var blk strings.Builder
	blk.WriteString(hdr)
	for _, line := range strings.Split(digest, "\n") {
		row := "  " + line + "\n"
		if blk.Len()+len(row) > capB {
			break
		}
		blk.WriteString(row)
	}
	if blk.Len() > len(hdr) {
		buf.WriteString(blk.String())
	}
}

func formatFencedSliceContracts(hdr, digest string, avail int) string {
	f := New(digest)
	overhead := len(hdr) + len(indentBlockLines(f.Data("spec_contracts", "trail", "")))
	bodyBudget := avail - overhead
	if bodyBudget < 24 {
		return ""
	}
	var kept []string
	used := 0
	for _, line := range strings.Split(strings.TrimRight(digest, "\n"), "\n") {
		rowLen := 2 + len(line) + 1
		if used+rowLen <= bodyBudget {
			kept = append(kept, line)
			used += rowLen
			continue
		}
		if len(kept) == 0 && bodyBudget-3 >= 16 {
			if clipped := truncateBytesUTF8(line, bodyBudget-3); clipped != "" {
				kept = append(kept, clipped)
			}
		}
		break
	}
	for len(kept) > 0 {
		body := strings.Join(kept, "\n")
		if strings.TrimSpace(body) == "" {
			return ""
		}
		block := hdr + indentBlockLines(f.Data("spec_contracts", "trail", body))
		if len(block) <= avail {
			return block
		}
		kept = kept[:len(kept)-1]
	}
	return ""
}

func indentBlockLines(text string) string {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func (sc *scope) formatHop0Symbol(cache sliceLineCache, s RankedSymbol, remBytes int) string {
	makeHdr := func(end int) string {
		return fmt.Sprintf("[Hop 0] %s:%d-%d (%s)\n", s.Path, s.StartLine, end, s.QualName())
	}
	if len(makeHdr(s.StartLine)) > remBytes {
		return ""
	}
	lines := sc.readLinesRangeCached(cache, s.OID, s.Path, s.StartLine, s.EndLine)
	if len(lines) == 0 {
		lines = []string{s.Sig}
	}
	return fitLinesToBudget(makeHdr, lines, s.StartLine, remBytes)
}

func (sc *scope) formatHop1Symbol(cache sliceLineCache, s RankedSymbol, c hop1Cand, remBytes int) string {
	conf := c.confidence
	if conf == "" {
		conf = "EXTRACTED"
	}
	span := s.EndLine - s.StartLine + 1
	if span <= hop1MaxFullLines {
		makeHdr := func(end int) string {
			return fmt.Sprintf("[Hop 1] %s:%d-%d (%s) [%s]\n", s.Path, s.StartLine, end, s.QualName(), conf)
		}
		lines := sc.readLinesRangeCached(cache, s.OID, s.Path, s.StartLine, s.EndLine)
		if len(lines) == 0 {
			lines = []string{s.Sig}
		}
		return fitLinesToBudget(makeHdr, lines, s.StartLine, remBytes)
	}
	start := s.StartLine
	if c.callLine >= s.StartLine && c.callLine <= s.EndLine {
		start = max(s.StartLine, c.callLine-2)
	}
	end := min(s.EndLine, start+hop1SnippetLines-1)
	makeHdr := func(last int) string {
		return fmt.Sprintf("[Hop 1] %s:%d-%d (%s snippet %d-%d) [%s]\n", s.Path, s.StartLine, s.EndLine, s.QualName(), start, last, conf)
	}
	lines := sc.readLinesRangeCached(cache, s.OID, s.Path, start, end)
	return fitLinesToBudget(makeHdr, lines, start, remBytes)
}

func fitLinesToBudget(makeHdr func(endLine int) string, lines []string, startLine, remBytes int) string {
	if len(lines) == 0 {
		return ""
	}
	maxHdrLen := len(makeHdr(startLine + len(lines) - 1))
	var body strings.Builder
	lastLine := 0
	for i, line := range lines {
		row := fmt.Sprintf("  %d: %s\n", startLine+i, line)
		if maxHdrLen+body.Len()+len(row) > remBytes {
			if len(makeHdr(startLine+i))+body.Len()+len(row) > remBytes {
				break
			}
		}
		body.WriteString(row)
		lastLine = startLine + i
	}
	if lastLine == 0 {
		return ""
	}
	hdr := makeHdr(lastLine)
	if len(hdr)+body.Len() > remBytes {
		return ""
	}
	return hdr + body.String()
}

func (sc *scope) readLinesRange(oid, relPath string, startLine, endLine int) []string {
	return sc.readLinesRangeCached(nil, oid, relPath, startLine, endLine)
}

func (sc *scope) readLinesRangeCached(cache sliceLineCache, oid, relPath string, startLine, endLine int) []string {
	if cache != nil {
		if all, ok := cache[relPath]; ok {
			s, e := max(1, startLine), min(len(all), endLine)
			if s <= e {
				return all[s-1 : e]
			}
			return nil
		}
	}
	for _, root := range sc.projects {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
		if err != nil {
			continue
		}
		all := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		if cache != nil {
			cache[relPath] = all
		}
		s, e := max(1, startLine), min(len(all), endLine)
		if s <= e {
			return all[s-1 : e]
		}
		return nil
	}
	var out []string
	for line := startLine; line <= endLine; line++ {
		out = append(out, sc.lineText(oid, line))
	}
	return out
}
