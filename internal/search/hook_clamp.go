package search

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/redact"
)

const (
	maxReadFileLines      = 800
	maxReadFileBytes      = 46080
	clampSkeletonMinLines = 400
	clampOnceSentinelLine = -5
	clampMaxSkeletonLines = 60
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

func hasExplicitReadRange(args map[string]any) bool {
	if args == nil {
		return false
	}
	_, end := extractLineRangeArgs(args)
	byteOffset := extractIntArg(args, "ByteOffset", "byte_offset", "byteOffset")
	return end > 0 || byteOffset > 0
}

// checkUnrangedClamp returns a [CLAMPED] skeleton-first deny on the first unranged read
// of any file >= clampSkeletonMinLines, stamping a per-hash retry sentinel so an explicit
// unranged retry does not deadlock.
func checkUnrangedClamp(sessionDir, home, toolDir, conversationID, target string, args map[string]any) (ReadDecision, bool) {
	if hasExplicitReadRange(args) {
		return ReadDecision{}, false
	}
	resolved := CanonicalizePath(toolDir, target)
	sumHex, data, totalLines, ok := readFileDigestAndLines(resolved)
	if !ok || totalLines < clampSkeletonMinLines {
		return ReadDecision{}, false
	}
	st, root, err := openSessionLedgerStore(resolveSessionLedgerDir(sessionDir, toolDir), home)
	if err != nil {
		return ReadDecision{}, false
	}
	defer st.close()
	convID := normalizeLedgerConvID(conversationID)
	genID, defTurn := st.currentGeneration(convID)
	if st.hasClampBypassOrCoverage(convID, genID, resolved, sumHex, totalLines) {
		return ReadDecision{}, false
	}
	_ = st.upsertReadStamp(convID, genID, resolved, clampOnceSentinelLine, clampOnceSentinelLine, sumHex, defTurn, nowFunc().Unix())
	relPath := relDisplayPath(root, resolved)
	skel := buildClampSkeleton(resolved, string(data))
	return ReadDecision{
		Allowed:      false,
		Reason:       formatClampedLedgerNotice(relPath, totalLines, sumHex, skel),
		ResolvedPath: resolved,
		SHA256:       sumHex,
		GenerationID: genID,
	}, true
}

func (s *store) hasClampBypassOrCoverage(convID string, genID int, resolved, sumHex string, totalLines int) bool {
	var count int
	minFull := min(totalLines, maxReadFileLines)
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM read_ledger WHERE conversation_id = ? AND generation_id = ? AND resolved_path = ? AND sha256 = ? AND ((start_line = ? AND end_line = ?) OR (start_line = 1 AND end_line >= ?))`,
		convID, genID, resolved, sumHex, clampOnceSentinelLine, clampOnceSentinelLine, minFull,
	).Scan(&count)
	return err == nil && count > 0
}

func buildClampSkeleton(resolvedPath, text string) string {
	syms, _ := extractSymbolsAndRefs(resolvedPath, text)
	if len(syms) > 0 {
		var lines []string
		for _, sy := range syms {
			lines = append(lines, fmt.Sprintf("%d-%d  %s", sy.startLine, sy.endLine, sy.sig))
			if len(lines) >= clampMaxSkeletonLines {
				break
			}
		}
		return redact.Redact(strings.Join(lines, "\n"))
	}
	rawLines := splitLines(text)
	if len(rawLines) == 0 {
		return ""
	}
	step := max(1, len(rawLines)/12)
	var out []string
	for i := 0; i < len(rawLines) && len(out) < 12; i += step {
		snippet := strings.TrimSpace(redact.Redact(rawLines[i]))
		if len(snippet) > 72 {
			snippet = snippet[:72] + "..."
		}
		out = append(out, fmt.Sprintf("L%d  %s", i+1, snippet))
	}
	return strings.Join(out, "\n")
}
