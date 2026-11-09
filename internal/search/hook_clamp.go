package search

const (
	maxReadFileLines = 800
	maxReadFileBytes = 46080
)

// clampReadRange models read_file's 800-line and 46,080-byte caps so read_ledger
// stamps only the lines read_file actually delivers, and returns record=false when
// StartLine or ByteOffset lies outside the window (where read_file delivers nothing).
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
	reqStart, reqEnd := normalizeLineRange(start, end, totalLines)
	reqEnd = min(reqEnd, reqStart+maxReadFileLines-1)
	if rawOffset == 0 && len(data) <= maxReadFileBytes {
		return reqStart, reqEnd, true
	}
	lines := splitLines(string(data))
	if reqEnd = min(reqEnd, len(lines)); reqStart > reqEnd {
		return 0, 0, false
	}
	winStart, winEnd, cumBytes, effStart, effEnd := rawOffset, rawOffset+maxReadFileBytes, 0, 0, 0
	for lineNum := reqStart; lineNum <= reqEnd; lineNum++ {
		lineEnd := cumBytes + len(lines[lineNum-1]) + 1
		if lineEnd <= winStart {
			cumBytes = lineEnd
			continue
		}
		if effStart == 0 {
			effStart = lineNum
		}
		if lineEnd > winEnd {
			break
		}
		effEnd, cumBytes = lineNum, lineEnd
	}
	if effEnd == 0 {
		return 0, 0, false
	}
	return effStart, effEnd, true
}
