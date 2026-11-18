package metrics

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// fullReadEnd represents an unbounded or full-file read range [1, fullReadEnd].
const fullReadEnd = 1_000_000_000

// extractReadRange extracts 1-based [start, end] line bounds from StartLine/EndLine
// or Offset/Limit arguments on a Read tool call.
func extractReadRange(a agyArgs) (start, end int, ranged bool) {
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

// parseFileHashRange parses cmd_hash values of the form r:<hash>:<start>-<end>,
// legacy r:<hash>, and f:<hash>.
func parseFileHashRange(h string) (ranged bool, base string, start, end int) {
	switch {
	case strings.HasPrefix(h, "r:"):
		rest := strings.TrimPrefix(h, "r:")
		if idx := strings.LastIndexByte(rest, ':'); idx > 0 {
			if sStr, eStr, ok := strings.Cut(rest[idx+1:], "-"); ok {
				s, err1 := strconv.Atoi(sStr)
				e, err2 := strconv.Atoi(eStr)
				if err1 == nil && err2 == nil && s >= 1 && e >= s {
					return true, rest[:idx], s, e
				}
			}
		}
		return true, rest, 1, fullReadEnd
	case strings.HasPrefix(h, "f:"):
		return false, strings.TrimPrefix(h, "f:"), 1, fullReadEnd
	default:
		return false, "", 0, 0
	}
}

// recordReadInterval records [start, end] for base in seen and returns true ONLY
// when the overlap with previously recorded intervals for base is >= 50% of
// (end - start + 1).
func recordReadInterval(seen map[string][][2]int, base string, start, end int) bool {
	if base == "" || start <= 0 || end < start {
		return false
	}
	existing := seen[base]
	var overlap int64
	for _, iv := range existing {
		lo := max(start, iv[0])
		hi := min(end, iv[1])
		if hi >= lo {
			overlap += int64(hi - lo + 1)
		}
	}
	span := int64(end - start + 1)
	seen[base] = mergeReadInterval(existing, start, end)
	return overlap*2 >= span
}

func mergeReadInterval(ivs [][2]int, start, end int) [][2]int {
	ivs = append(ivs, [2]int{start, end})
	sort.Slice(ivs, func(i, j int) bool {
		if ivs[i][0] != ivs[j][0] {
			return ivs[i][0] < ivs[j][0]
		}
		return ivs[i][1] < ivs[j][1]
	})
	out := ivs[:0]
	for _, iv := range ivs {
		n := len(out)
		if n > 0 && iv[0] <= out[n-1][1]+1 {
			if iv[1] > out[n-1][1] {
				out[n-1][1] = iv[1]
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}
