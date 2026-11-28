package ingest

import (
	"encoding/json"
	"strconv"
	"strings"
)

const fullReadEnd = 1_000_000_000

// ExtractReadRange extracts 1-based [start, end] line bounds from StartLine/EndLine
// or Offset/Limit arguments on a Read tool call.
func ExtractReadRange(a AgyArgs) (start, end int, ranged bool) {
	start = firstPositiveInt(
		a.StartLine, a.StartLineLower, a.StartLineNumber,
		a.StartLineCamel, a.StartLineNumCamel, a.Offset, a.OffsetLower,
	)
	end = firstPositiveInt(
		a.EndLine, a.EndLineLower, a.EndLineNumber,
		a.EndLineCamel, a.EndLineNumCamel,
	)
	limit := firstPositiveInt(a.Limit, a.LimitLower, a.LineCount, a.LineCountCamel)
	if end == 0 && limit > 0 {
		if start > 0 {
			end = start + limit - 1
		} else {
			start, end = 1, limit
		}
	}
	if start == 0 && end > 0 {
		start = 1
	}
	if start > 0 && end >= start {
		return start, end, true
	}
	if start > 0 {
		return start, fullReadEnd, true
	}
	return 0, 0, false
}

func firstPositiveInt(vals ...any) int {
	for _, v := range vals {
		if n, ok := parseIntArg(v); ok && n > 0 {
			return n
		}
	}
	return 0
}

func parseIntArg(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), n > 0
	case int:
		return n, n > 0
	case int64:
		return int(n), n > 0
	case json.Number:
		p, err := n.Int64()
		return int(p), err == nil && p > 0
	case string:
		p, err := strconv.Atoi(strings.TrimSpace(n))
		return p, err == nil && p > 0
	}
	return 0, false
}
