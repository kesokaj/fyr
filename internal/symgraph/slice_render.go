package symgraph

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/vet"
)

const (
	hop1MaxFullLines = 40
	hop1SnippetLines = 5
)

// SliceLineCache caches split file lines during slice capsule rendering.
type SliceLineCache map[string][]string

func (sc *Scope) renderSliceCapsule(o SliceOptions, g *Graph, tiers SliceTiers, maxBytes int) string {
	var buf bytes.Buffer
	hdr := fmt.Sprintf("# slice: %s (budget: %d tokens)\n", o.Slice, o.Budget)
	if len(hdr) <= maxBytes {
		buf.WriteString(hdr)
	}
	paths := map[string]bool{}
	for _, idx := range tiers.Hop0 {
		paths[g.Syms[idx].Path] = true
	}
	for _, c := range tiers.Hop1 {
		paths[g.Syms[c.Idx].Path] = true
	}
	var extrasBuf bytes.Buffer
	extrasCap := min(max(0, maxBytes-buf.Len()), max(360, maxBytes/2))
	sc.appendSliceExtras(&extrasBuf, o, paths, extrasCap)
	symMax := max(buf.Len(), maxBytes-extrasBuf.Len())
	hop0Cap, hop1Cap := symMax, symMax
	if len(tiers.Hop1) > 0 && len(tiers.Hop2) > 0 {
		hop0Cap = symMax * 60 / 100
		hop1Cap = symMax * 85 / 100
	} else if len(tiers.Hop1) > 0 {
		hop0Cap = symMax * 75 / 100
	}
	cache := SliceLineCache{}
	for _, idx := range tiers.Hop0 {
		s := g.Syms[idx]
		sc.appendWithinBudget(&buf, sc.FormatHop0Symbol(cache, s, hop0Cap-buf.Len()), hop0Cap)
	}
	for _, c := range tiers.Hop1 {
		s := g.Syms[c.Idx]
		sc.appendWithinBudget(&buf, sc.formatHop1Symbol(cache, s, c, hop1Cap-buf.Len()), hop1Cap)
	}
	for _, idx := range tiers.Hop2 {
		s := g.Syms[idx]
		line := fmt.Sprintf("[Hop 2] %s:%d-%d (%s)  %s\n", s.Path, s.StartLine, s.EndLine, s.QualName(), s.Sig)
		sc.appendWithinBudget(&buf, line, symMax)
	}
	sc.appendWithinBudget(&buf, extrasBuf.String(), maxBytes)
	return buf.String()
}

func (sc *Scope) appendWithinBudget(buf *bytes.Buffer, block string, capBytes int) {
	if block != "" && buf.Len()+len(block) <= capBytes {
		buf.WriteString(block)
	}
}

func (sc *Scope) appendSliceExtras(buf *bytes.Buffer, o SliceOptions, paths map[string]bool, maxBytes int) {
	if recs := sc.linkedRecords(paths, o.BrainRefs); len(recs) > 0 {
		lines := make([]string, 0, len(recs))
		for _, r := range recs {
			lines = append(lines, fmt.Sprintf("  %s [%s] %s\n", r.ID, r.Status, r.Title))
		}
		sc.appendTitledLines(buf, "linked_records:\n", lines, maxBytes)
	}
	if hints := sc.coChangeHints(paths); len(hints) > 0 {
		lines := make([]string, 0, len(hints))
		for _, h := range hints {
			lines = append(lines, "  "+h+"\n")
		}
		sc.appendTitledLines(buf, "co_change_hint:\n", lines, maxBytes)
	}
	sc.appendSliceContracts(buf, o, paths, maxBytes)
}

func (sc *Scope) appendTitledLines(buf *bytes.Buffer, title string, lines []string, maxBytes int) {
	if len(lines) == 0 || buf.Len()+len(title)+len(lines[0]) > maxBytes {
		return
	}
	buf.WriteString(title)
	for _, line := range lines {
		sc.appendWithinBudget(buf, line, maxBytes)
	}
}

func (sc *Scope) appendSliceContracts(buf *bytes.Buffer, o SliceOptions, paths map[string]bool, maxBytes int) {
	if o.FormatSpecDigest == nil || len(paths) == 0 {
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
		digest := o.FormatSpecDigest(pathList, min(DefaultContractDigestBytes, avail-len(hdr)))
		if digest == "" {
			return
		}
		if block := formatFencedSliceContracts(hdr, digest, avail); block != "" {
			buf.WriteString(block)
		}
		return
	}
	capB := min(DefaultContractDigestBytes, avail)
	digest := o.FormatSpecDigest(pathList, capB-len(hdr))
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
	f := vet.New(digest)
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
			if clipped := TruncateBytesUTF8(line, bodyBudget-3); clipped != "" {
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

// FormatHop0Symbol formats a full Hop-0 symbol body block fitted to remBytes.
func (sc *Scope) FormatHop0Symbol(cache SliceLineCache, s RankedSymbol, remBytes int) string {
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

func (sc *Scope) formatHop1Symbol(cache SliceLineCache, s RankedSymbol, c Hop1Cand, remBytes int) string {
	conf := c.Confidence
	if conf == "" {
		conf = ConfidenceExtracted
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
	if c.CallLine >= s.StartLine && c.CallLine <= s.EndLine {
		start = max(s.StartLine, c.CallLine-2)
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

func (sc *Scope) readLinesRangeCached(cache SliceLineCache, oid, relPath string, startLine, endLine int) []string {
	if all, ok := cache[relPath]; ok {
		return sliceLineWindow(all, startLine, endLine)
	}
	if all, ok := sc.loadFileLines(relPath); ok {
		if cache != nil {
			cache[relPath] = all
		}
		return sliceLineWindow(all, startLine, endLine)
	}
	if sc == nil || sc.LineText == nil {
		return nil
	}
	var out []string
	for line := startLine; line <= endLine; line++ {
		out = append(out, sc.LineText(oid, line))
	}
	return out
}

func (sc *Scope) loadFileLines(relPath string) ([]string, bool) {
	if sc == nil {
		return nil, false
	}
	for _, root := range sc.Projects {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
		if err != nil {
			continue
		}
		return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), true
	}
	return nil, false
}

func sliceLineWindow(all []string, startLine, endLine int) []string {
	s, e := max(1, startLine), min(len(all), endLine)
	if s > e {
		return nil
	}
	return all[s-1 : e]
}
