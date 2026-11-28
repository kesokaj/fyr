package creds

import (
	"encoding/binary"
	"slices"
	"sort"
)

const sqliteHeaderMagic = "SQLite format 3\x00"

// scanSQLiteOverflow reassembles overflowed table B-tree leaf cells (0x0d) in
// SQLite database files so multi-kilobyte records or chunks split across 4 KiB
// overflow page headers produce the same line/token digests as unsplit exports,
// returning the sorted file byte intervals already scanned in reassembled cells.
func scanSQLiteOverflow(b []byte, maxPayload int64, kinds, unparsed map[string]int, digests map[string]map[[32]byte]int) [][2]int {
	pageSize, usable, nPages, ok := sqliteGeometry(b)
	if !ok {
		return nil
	}
	usedPage := map[int]bool{}
	var covered [][2]int
	for pageNo := 1; pageNo <= nPages; pageNo++ {
		cells, spans := extractLeafOverflowCells(b, pageNo, pageSize, usable, nPages, maxPayload, usedPage)
		for _, cell := range cells {
			recordWindowCreds(cell, len(cell), kinds, unparsed, digests)
		}
		covered = append(covered, spans...)
	}
	sort.Slice(covered, func(i, j int) bool { return covered[i][0] < covered[j][0] })
	return covered
}

func sqliteGeometry(b []byte) (pageSize, usable, nPages int, ok bool) {
	if len(b) < 100 || string(b[:16]) != sqliteHeaderMagic {
		return 0, 0, 0, false
	}
	ps := int(binary.BigEndian.Uint16(b[16:18]))
	if ps == 1 {
		ps = 65536
	}
	if ps < 512 || ps > 65536 || ps&(ps-1) != 0 {
		return 0, 0, 0, false
	}
	usable = ps - int(b[20])
	if usable < 480 || len(b) < ps {
		return 0, 0, 0, false
	}
	return ps, usable, len(b) / ps, true
}

func extractLeafOverflowCells(b []byte, pageNo, pageSize, usable, nPages int, maxPayload int64, usedPage map[int]bool) ([][]byte, [][2]int) {
	pOff := (pageNo - 1) * pageSize
	hdrOff := 0
	if pageNo == 1 {
		hdrOff = 100
	}
	if pOff+hdrOff+8 > len(b) || b[pOff+hdrOff] != 0x0d {
		return nil, nil
	}
	cellCount := int(binary.BigEndian.Uint16(b[pOff+hdrOff+3 : pOff+hdrOff+5]))
	if hdrOff+8+2*cellCount > usable {
		return nil, nil
	}
	var cells [][]byte
	var spans [][2]int
	var pageLocals [][2]int
	for i := range cellCount {
		ptr := pOff + hdrOff + 8 + 2*i
		cOff := int(binary.BigEndian.Uint16(b[ptr : ptr+2]))
		if cOff < hdrOff+8+2*cellCount || cOff >= usable {
			continue
		}
		cell, cSpans, ok := reassembleOverflowCell(b, pOff, cOff, pageSize, usable, nPages, maxPayload, usedPage, pageLocals)
		if ok {
			pageLocals = append(pageLocals, cSpans[0])
			cells = append(cells, cell)
			spans = append(spans, cSpans...)
		}
	}
	return cells, spans
}

func reassembleOverflowCell(b []byte, pOff, cOff, pageSize, usable, nPages int, maxPayload int64, usedPage map[int]bool, pageLocals [][2]int) ([]byte, [][2]int, bool) {
	pLen, n1 := sqliteVarint9(b, pOff+cOff, pOff+usable)
	_, n2 := sqliteVarint9(b, pOff+cOff+n1, pOff+usable)
	maxLocal := usable - 35
	if n1 == 0 || n2 == 0 || pLen <= uint64(maxLocal) || pLen > uint64(maxPayload) {
		return nil, nil, false
	}
	minLocal := ((usable-12)*32/255 - 23)
	k := minLocal + (int(pLen)-minLocal)%(usable-4)
	if k > maxLocal {
		k = minLocal
	}
	if cOff+n1+n2+k+4 > usable {
		return nil, nil, false
	}
	lStart, lEnd := pOff+cOff+n1+n2, pOff+cOff+n1+n2+k
	if overlapsAny(lStart, lEnd, pageLocals) {
		return nil, nil, false
	}
	nextPg := int(binary.BigEndian.Uint32(b[lEnd : lEnd+4]))
	return followOverflowChain(b, int(pLen), lStart, lEnd, nextPg, pageSize, usable, nPages, usedPage)
}

func followOverflowChain(b []byte, total, lStart, lEnd, nextPg, pageSize, usable, nPages int, usedPage map[int]bool) ([]byte, [][2]int, bool) {
	buf := make([]byte, 0, total)
	buf = append(buf, b[lStart:lEnd]...)
	spans := [][2]int{{lStart, lEnd}}
	var chain []int
	rem := total - (lEnd - lStart)
	for rem > 0 {
		if nextPg < 2 || nextPg > nPages || usedPage[nextPg] || slices.Contains(chain, nextPg) {
			return nil, nil, false
		}
		oOff := (nextPg - 1) * pageSize
		chunk := min(rem, usable-4)
		if oOff+4+chunk > len(b) {
			return nil, nil, false
		}
		chain = append(chain, nextPg)
		spans = append(spans, [2]int{oOff + 4, oOff + 4 + chunk})
		buf = append(buf, b[oOff+4:oOff+4+chunk]...)
		rem -= chunk
		nextPg = int(binary.BigEndian.Uint32(b[oOff : oOff+4]))
	}
	if nextPg != 0 {
		return nil, nil, false
	}
	for _, pg := range chain {
		usedPage[pg] = true
	}
	return buf, spans, true
}

func sqliteVarint9(b []byte, pos, limit int) (uint64, int) {
	var v uint64
	for i := range 9 {
		if pos+i >= limit || pos+i >= len(b) {
			return 0, 0
		}
		c := b[pos+i]
		if i == 8 {
			return (v << 8) | uint64(c), 9
		}
		v = (v << 7) | uint64(c&0x7f)
		if c < 0x80 {
			return v, i + 1
		}
	}
	return 0, 0
}

func overlapsAny(lo, hi int, spans [][2]int) bool {
	for _, s := range spans {
		if lo < s[1] && s[0] < hi {
			return true
		}
	}
	return false
}

func inCoveredSpan(pos int, spans [][2]int) bool {
	if len(spans) == 0 {
		return false
	}
	idx := sort.Search(len(spans), func(i int) bool { return spans[i][1] > pos })
	return idx < len(spans) && spans[idx][0] <= pos && pos < spans[idx][1]
}

func stripSQLiteChunkPrefix(b []byte, ps, start int) int {
	lo := max(0, ps-64)
	for p := lo; p+41 <= start; p++ {
		if !isLowerHex40(b[p : p+40]) {
			continue
		}
		if body, ok := matchSQLiteChunkHeader(b, p, ps, start); ok {
			return body
		}
	}
	return ps
}

func matchSQLiteChunkHeader(b []byte, p, ps, start int) (int, bool) {
	for h := max(0, p-10); h+2 < p; h++ {
		tLine := int(b[h+1])
		tHead := int(b[h+2])
		if b[h] != 0x5d || (tLine != 1 && tLine != 2) || tHead < 13 || tHead%2 != 1 {
			continue
		}
		body := p + 40 + tLine + (tHead-13)/2
		if body > ps && body <= start {
			return body, true
		}
	}
	return 0, false
}

func stripSQLiteRecordBounds(b []byte, ps, pe, start, end int) (int, int) {
	lo := max(1, ps-maxLineScan)
	for r0 := min(ps, len(b)-1); r0 >= lo; r0-- {
		if b[r0] != 0x00 {
			continue
		}
		hdrStart := r0 - 1
		hdrSize, n := readVarint(b, hdrStart)
		if n == 1 && hdrSize >= 11 && hdrSize <= 32 && hdrStart+int(hdrSize) <= len(b) {
			bodyStart, bodyEnd, ok := parseBrainRecordCols(b, r0+1, hdrStart+int(hdrSize))
			if ok && bodyStart <= start && end <= min(len(b), bodyEnd) {
				return max(ps, bodyStart), min(pe, bodyEnd)
			}
		}
		break
	}
	return ps, pe
}

func parseBrainRecordCols(b []byte, pos, payload int) (int, int, bool) {
	var lens [9]int
	for i := range 9 {
		v, n := readVarint(b, pos)
		if n == 0 || v < 13 || v%2 != 1 {
			return 0, 0, false
		}
		lens[i] = int(v-13) / 2
		pos += n
	}
	if lens[4] != 10 || lens[8] < 20 || pos > payload {
		return 0, 0, false
	}
	datePos := payload + lens[0] + lens[1] + lens[2] + lens[3]
	if datePos+10 > len(b) || !isISODate(b[datePos:datePos+10]) {
		return 0, 0, false
	}
	bodyStart := datePos + 10 + lens[5]
	return bodyStart, bodyStart + lens[6], true
}

func readVarint(b []byte, pos int) (uint64, int) {
	var v uint64
	for i := 0; i < 4 && pos+i < len(b); i++ {
		c := b[pos+i]
		v = (v << 7) | uint64(c&0x7f)
		if c < 0x80 {
			return v, i + 1
		}
	}
	return 0, 0
}

func isISODate(s []byte) bool {
	return len(s) == 10 && s[4] == '-' && s[7] == '-' && s[0] == '2' && s[1] == '0'
}

func isLowerHex40(s []byte) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return len(s) == 40
}
