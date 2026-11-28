package show

import (
	"sort"
	"strconv"
	"strings"
)

const fullReadEnd = 1_000_000_000

// ParseFileHashRange parses cmd_hash values of the form r:<hash>:<start>-<end>,
// legacy r:<hash>, and f:<hash>.
func ParseFileHashRange(h string) (ranged bool, base string, start, end int) {
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

// RecordReadInterval records [start, end] for base in seen and returns true ONLY
// when the overlap with previously recorded intervals for base is >= 50% of
// (end - start + 1).
func RecordReadInterval(seen map[string][][2]int, base string, start, end int) bool {
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
