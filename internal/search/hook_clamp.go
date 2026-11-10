package search

import (
	"strconv"
	"strings"
)

const (
	maxReadFileLines = 800
	maxReadFileBytes = 46080
)

// clampReadRange models read_file's 800-line and 46,080-byte caps (including "%d: "
// line-number prefixes) so read_ledger stamps only the complete lines read_file
// delivers, returning record=false when StartLine or ByteOffset delivers zero lines.
func clampReadRange(dir, target string, args map[string]any) (int, int, bool) {
	start, end := extractLineRangeArgs(args)
	rawOffset := extractIntArg(args, "ByteOffset", "byte_offset", "byteOffset")
	if rawOffset < 0 {
		return 0, 0, false
	}
	_, data, totalLines, ok := readFileDigestAndLines(CanonicalizePath(dir, target))
	if !ok {
		return start, end, true
	}
	if start > totalLines {
		return 0, 0, false
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return 1, 1, rawOffset == 0
	}
	reqStart, reqEnd := normalizeLineRange(start, end, len(lines))
	reqEnd = min(reqEnd, reqStart+maxReadFileLines-1)
	winStart, winEnd, cumBytes, effStart, effEnd := rawOffset, rawOffset+maxReadFileBytes, 0, 0, 0
	for lineNum := reqStart; lineNum <= reqEnd; lineNum++ {
		contentEnd := cumBytes + len(strconv.Itoa(lineNum)) + 2 + len(lines[lineNum-1])
		nextCum := contentEnd + 1
		if lineNum == reqEnd {
			nextCum = contentEnd
		}
		if nextCum <= winStart {
			cumBytes = nextCum
			continue
		}
		if effStart == 0 {
			effStart = lineNum
		}
		if contentEnd > winEnd {
			break
		}
		effEnd, cumBytes = lineNum, nextCum
	}
	if effEnd == 0 {
		return 0, 0, false
	}
	return effStart, effEnd, true
}
