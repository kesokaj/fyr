package creds

import (
	"bytes"
	"unicode/utf8"
)

const (
	maxLineScan     = 64 << 10
	maxLogicalLine  = 16 << 10
	minContextBytes = 6
	contextWindow   = 256
)

// peelLogicalLine unwraps up to 3 outer JSON-string layers (for transcript.jsonl
// tool outputs and nested arguments) to recover the canonical 0-layer source or
// Markdown line enclosing b[start:end], requiring surrounding non-marker context.
func peelLogicalLine(b []byte, start, end int, codeOnly bool) ([]byte, []byte, bool) {
	ps, pe, ok := physicalLineBounds(b, start, end)
	if !ok {
		return nil, nil, false
	}
	cur, uStart, uEnd := b[ps:pe], start-ps, end-ps
	outerTail := b[pe:min(len(b), pe+300)]
	for range 3 {
		qOpen, qClose, found := enclosingZeroQuote(cur, uStart, uEnd)
		if !found {
			break
		}
		unesc, ns, ne := unescapeLayerSpan(cur[qOpen+1:qClose], uStart-(qOpen+1), uEnd-(qOpen+1))
		ls, le := subLineBounds(unesc, ns, ne)
		sub := unesc[ls:le]
		if _, enclosed := enclosesMarker(sub, ns-ls, ne-ls, codeOnly); !enclosed {
			break
		}
		outerTail = joinTails(unesc[le:], cur[qClose+1:])
		cur, uStart, uEnd = sub, ns-ls, ne-ls
	}
	if len(cur) > maxLogicalLine {
		return nil, nil, false
	}
	closeEnd, ok := enclosesMarker(cur, uStart, uEnd, codeOnly)
	if !ok || surroundingContextLen(cur, uStart, uEnd) < minContextBytes {
		return nil, nil, false
	}
	win := normContextWindow(cur, uStart, uEnd)
	return win, joinTails(cur[closeEnd:], outerTail), len(win) > 0
}

func normContextWindow(cur []byte, uStart, uEnd int) []byte {
	afterLeft := bytes.TrimLeft(cur, " \t\r\n")
	afterNum := stripLineNumPrefix(afterLeft)
	drop := (len(cur) - len(afterLeft)) + (len(afterLeft) - len(afterNum))
	norm := bytes.TrimRight(afterNum, " \t\r\n")
	ms := clamp(uStart-drop, 0, len(norm))
	me := clamp(uEnd-drop, ms, len(norm))
	return norm[max(0, ms-contextWindow):min(len(norm), me+contextWindow)]
}

func clamp(v, lo, hi int) int { return min(max(v, lo), hi) }

func joinTails(a, b []byte) []byte {
	a = a[:min(len(a), 256)]
	b = b[:min(len(b), 256)]
	return append(append(append([]byte{}, a...), '\n'), b...)
}

func physicalLineBounds(b []byte, start, end int) (int, int, bool) {
	ps := start
	for ps > 0 && start-ps < maxLineScan && !isLineBreakOrBin(b[ps-1]) {
		ps--
	}
	pe := end
	for pe < len(b) && pe-end < maxLineScan && !isLineBreakOrBin(b[pe]) {
		pe++
	}
	if start-ps >= maxLineScan || pe-end >= maxLineScan {
		return 0, 0, false
	}
	ps = stripSQLiteChunkPrefix(b, ps, start)
	ps, pe = stripSQLiteRecordBounds(b, ps, pe, start, end)
	return ps, pe, true
}

func enclosingZeroQuote(s []byte, start, end int) (int, int, bool) {
	qOpen, open := -1, false
	for i := 0; i < len(s); i++ {
		if s[i] != '"' || !evenBackslashesBefore(s, i) {
			continue
		}
		if !open {
			qOpen, open = i, true
			continue
		}
		if qOpen < start && end <= i {
			return qOpen, i, true
		}
		open = false
	}
	return 0, 0, false
}

func evenBackslashesBefore(s []byte, idx int) bool {
	bs := 0
	for idx-1-bs >= 0 && s[idx-1-bs] == '\\' {
		bs++
	}
	return bs%2 == 0
}

type spanTracker struct {
	start, end       int
	ns, ne           int
	setStart, setEnd bool
}

func (st *spanTracker) markBefore(pos, outLen int) {
	if !st.setStart && st.start <= pos {
		st.ns, st.setStart = outLen, true
	}
	if !st.setEnd && st.end <= pos {
		st.ne, st.setEnd = outLen, true
	}
}

func (st *spanTracker) markEscapeEnd(tokEnd, outBefore, outAfter int) {
	if !st.setStart && st.start < tokEnd {
		st.ns, st.setStart = outBefore, true
	}
	if !st.setEnd && st.end < tokEnd {
		st.ne, st.setEnd = outAfter, true
	}
}

func (st *spanTracker) finish(outLen int) (int, int) {
	if !st.setStart {
		st.ns = outLen
	}
	if !st.setEnd {
		st.ne = outLen
	}
	return st.ns, st.ne
}

func unescapeLayerSpan(s []byte, start, end int) ([]byte, int, int) {
	out := make([]byte, 0, len(s))
	st := spanTracker{start: start, end: end}
	for i := 0; i < len(s); i++ {
		outBefore := len(out)
		st.markBefore(i, outBefore)
		if s[i] != '\\' || i+1 >= len(s) {
			out = append(out, s[i])
			continue
		}
		out, i = decodeEscapeAt(out, s, i+1)
		st.markEscapeEnd(i+1, outBefore, len(out))
	}
	ns, ne := st.finish(len(out))
	return out, ns, ne
}

func decodeEscapeAt(out, s []byte, escIdx int) ([]byte, int) {
	if dec, step, ok := decodeUnicodeEscape(s, escIdx); ok {
		return append(out, dec...), escIdx + step
	}
	return appendJSONEscape(out, s[escIdx]), escIdx
}

func decodeUnicodeEscape(s []byte, uIdx int) ([]byte, int, bool) {
	if uIdx+4 >= len(s) || s[uIdx] != 'u' {
		return nil, 0, false
	}
	var r rune
	for k := 1; k <= 4; k++ {
		d, ok := unhex(s[uIdx+k])
		if !ok {
			return nil, 0, false
		}
		r = (r << 4) | rune(d)
	}
	if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0x7f || (r >= 0xd800 && r <= 0xdfff) {
		return nil, 0, false
	}
	return utf8.AppendRune(nil, r), 4, true
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func appendJSONEscape(out []byte, c byte) []byte {
	switch c {
	case '\\', '"', '/':
		return append(out, c)
	case 'n':
		return append(out, '\n')
	case 'r':
		return append(out, '\r')
	case 't':
		return append(out, '\t')
	default:
		return append(out, '\\', c)
	}
}

func subLineBounds(s []byte, start, end int) (int, int) {
	ls := start
	for ls > 0 && !isLineBreakOrBin(s[ls-1]) {
		ls--
	}
	le := end
	for le < len(s) && !isLineBreakOrBin(s[le]) {
		le++
	}
	return ls, le
}

func enclosesMarker(line []byte, start, end int, codeOnly bool) (int, bool) {
	if closeEnd, ok := enclosedInCodeSpan(line, start, end); ok {
		return closeEnd, true
	}
	if codeOnly {
		return 0, false
	}
	_, qClose, ok := enclosingZeroQuote(line, start, end)
	return qClose + 1, ok
}

func enclosedInCodeSpan(line []byte, start, end int) (int, bool) {
	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		run := tickRun(line, i)
		closeStart, nextIdx, ok := findClosingTickRun(line, i+run, run)
		if ok && i+run <= start && end <= closeStart {
			return nextIdx, true
		}
		i = nextIdx
	}
	return 0, false
}

func findClosingTickRun(line []byte, from, wantRun int) (closeStart, nextIdx int, ok bool) {
	for j := from; j < len(line); {
		if line[j] != '`' {
			j++
			continue
		}
		crun := tickRun(line, j)
		if crun == wantRun {
			return j, j + crun, true
		}
		j += crun
	}
	return 0, len(line), false
}

func tickRun(s []byte, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	return n
}

func surroundingContextLen(line []byte, start, end int) int {
	n := 0
	countPart := func(part []byte) {
		for i := 0; i < len(part); i++ {
			c := part[i]
			if c == '\\' && i+1 < len(part) && (part[i+1] == 'n' || part[i+1] == 'r') {
				i++
				continue
			}
			if c != ' ' && c != '\t' && c != '"' && c != '\'' && c != '`' {
				n++
			}
		}
	}
	countPart(stripLineNumPrefix(bytes.TrimLeft(line[:start], " \t")))
	countPart(line[end:])
	return n
}
