package search

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/redact"
)

const (
	maxReadFileLines      = 800
	maxReadFileBytes      = 46080
	clampSkeletonMinLines = 400
	clampSkeletonMinBytes = 16384
	clampOnceSentinelLine = -5
	clampMaxSkeletonLines = 60
)

type readFileSnapshot struct {
	resolved   string
	sumHex     string
	data       []byte
	totalLines int
	ok         bool
}

func loadReadFileSnapshot(resolved string) readFileSnapshot {
	sumHex, data, totalLines, ok := readFileDigestAndLines(resolved)
	return readFileSnapshot{
		resolved:   resolved,
		sumHex:     sumHex,
		data:       data,
		totalLines: totalLines,
		ok:         ok,
	}
}

func clampReadRangeWithSnapshot(args map[string]any, snap readFileSnapshot) (int, int, bool) {
	start, end := extractLineRangeArgs(args)
	rawOffset := extractIntArg(args, "ByteOffset", "byte_offset", "byteOffset")
	if rawOffset < 0 {
		return 0, 0, false
	}
	if !snap.ok {
		return start, end, true
	}
	if start > snap.totalLines {
		return 0, 0, false
	}
	lines := strings.Split(string(snap.data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return 1, 1, rawOffset == 0
	}
	reqStart, reqEnd := normalizeLineRange(start, end, len(lines))
	reqEnd = min(reqEnd, reqStart+maxReadFileLines-1)
	return effectiveClampedWindow(lines, reqStart, reqEnd, rawOffset)
}

func effectiveClampedWindow(lines []string, reqStart, reqEnd, rawOffset int) (int, int, bool) {
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

func checkUnrangedClampWithStore(ctx context.Context, st *store, root, conversationID string, snap readFileSnapshot, args map[string]any) (ReadDecision, bool) {
	if st == nil || !shouldGuardReadInRoot(root, snap.resolved) || ctx.Err() != nil || hasExplicitReadRange(args) || !snap.ok ||
		(snap.totalLines < clampSkeletonMinLines && len(snap.data) < clampSkeletonMinBytes) {
		return ReadDecision{}, false
	}
	convID := normalizeLedgerConvID(conversationID)
	genID, defTurn := st.currentGeneration(convID)
	if st.hasClampBypassOrCoverage(ctx, convID, genID, snap.resolved, snap.sumHex, snap.totalLines) {
		return ReadDecision{}, false
	}
	if ctx.Err() != nil {
		return ReadDecision{}, false
	}
	_ = st.upsertReadStamp(convID, genID, snap.resolved, clampOnceSentinelLine, clampOnceSentinelLine, snap.sumHex, defTurn, nowFunc().Unix())
	relPath := relDisplayPath(root, snap.resolved)
	skel := buildClampSkeleton(snap.resolved, string(snap.data))
	return ReadDecision{
		Allowed:      false,
		Reason:       formatClampedLedgerNotice(relPath, snap.totalLines, snap.sumHex, skel),
		ResolvedPath: snap.resolved,
		SHA256:       snap.sumHex,
		GenerationID: genID,
	}, true
}

func (s *store) hasClampBypassOrCoverage(ctx context.Context, convID string, genID int, resolved, sumHex string, totalLines int) bool {
	var count int
	minFull := min(totalLines, maxReadFileLines)
	err := s.db.QueryRowContext(ctx,
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
			lines = append(lines, fmt.Sprintf("%d-%d  %s", sy.StartLine, sy.EndLine, sy.Sig))
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
