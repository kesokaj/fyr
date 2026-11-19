package redact

import (
	"regexp"
	"strings"
)

// A key block opens at a PEM header, or at a header whose dashes a chat UI
// rewrote, and closes at the matching footer. A footer followed on the same
// line by another header keeps the block open (two keys concatenated).
var (
	blockClose = regexp.MustCompile(strings.Join([]string{"(?i)---", "--END[^-]*(---", "--)?|END[A-Z ]*PRI", "VATE KEY"}, ""))
	blockLabel = regexp.MustCompile(`(?i)^BEGIN ([A-Z0-9][A-Z0-9 ]*[A-Z0-9])`)
)

// labelAt returns the label of the header that starts at s ("RSA PRIVATE
// KEY"), or "" when the header names none.
func labelAt(s string) string {
	idx := strings.Index(strings.ToUpper(s), "BEGIN")
	if idx < 0 {
		return ""
	}
	if m := blockLabel.FindStringSubmatch(s[idx:]); m != nil {
		return strings.ToUpper(m[1])
	}
	return ""
}

func blockOpenStart(raw string, jsonl bool) (string, []int) {
	line := strings.TrimSuffix(raw, "\n")
	if open := blockOpen.FindStringIndex(line); open != nil || !jsonl {
		return line, open
	}
	line = decoded(line)
	return line, blockOpen.FindStringIndex(line)
}

func decodedBlockClose(raw string, first bool) (string, int, []int) {
	dec := decoded(strings.TrimSuffix(raw, "\n"))
	off := 0
	if first {
		open := blockOpen.FindStringIndex(dec)
		if open == nil {
			return "", 0, nil
		}
		off = open[1]
	}
	loc := blockClose.FindStringIndex(dec[off:])
	if loc == nil {
		return "", 0, nil
	}
	return dec, off, loc
}

// blockEnd returns the index of the line that closes the key block opened on
// line i, or the last non-empty line and false when nothing closes it.
func blockEnd(lines []string, i int, jsonl bool) (int, bool) {
	openLine, open := blockOpenStart(lines[i], jsonl)
	if open == nil {
		return i, false
	}
	j, off, label := i, open[1], labelAt(openLine[open[0]:])
	cur, isDecoded := openLine, openLine != strings.TrimSuffix(lines[i], "\n")
	for j < len(lines) {
		loc := blockClose.FindStringIndex(cur[off:])
		if loc == nil && jsonl && !isDecoded {
			if dec, dOff, dLoc := decodedBlockClose(lines[j], j == i); dLoc != nil {
				cur, off, loc, isDecoded = dec, dOff, dLoc, true
			}
		}
		if loc == nil {
			j++
			if j < len(lines) {
				cur, off, isDecoded = lines[j], 0, false
			}
			continue
		}
		after := off + loc[1]
		// A block opened with a label closes only at a footer naming that
		// opening label: a line mentioning a generic footer or a different
		// label ("-----END NOTE-----") must not close the key early (D14, SD90).
		if label != "" && !strings.Contains(strings.ToUpper(cur[off+loc[0]:after]), label) {
			off = after
			continue
		}
		if nCur, nOff, next, nDec := nextBlockOpen(cur, after, jsonl, isDecoded); next != nil {
			label = labelAt(nCur[nOff+next[0]:])
			cur, off, isDecoded = nCur, nOff+next[1], nDec
			continue
		}
		return j, true
	}
	last := len(lines) - 1
	if lines[last] == "" {
		last--
	}
	return last, false
}

func nextBlockOpen(cur string, after int, jsonl, isDecoded bool) (string, int, []int, bool) {
	if next := blockOpen.FindStringIndex(cur[after:]); next != nil {
		return cur, after, next, isDecoded
	}
	if !jsonl || isDecoded {
		return "", 0, nil, false
	}
	tail := strings.TrimSuffix(cur[after:], "\n")
	if dec := decoded(tail); dec != "" {
		if next := blockOpen.FindStringIndex(dec); next != nil {
			return dec, 0, next, true
		}
	}
	if decQ := decoded(`"` + tail); decQ != "" {
		if next := blockOpen.FindStringIndex(decQ); next != nil {
			return decQ, 0, next, true
		}
	}
	return "", 0, nil, false
}
