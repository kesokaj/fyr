package work

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

func pickCrossFamilyRole(active []CatalogEntry, goneIDs map[string]bool, prevModel, role, tier string) (RolePick, error) {
	var buckets [][]CatalogEntry
	if tier == "pro" {
		buckets = [][]CatalogEntry{
			filterEntries(active, func(e CatalogEntry) bool {
				return e.Family == "anthropic" && strings.Contains(strings.ToLower(e.ID), "opus")
			}),
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "anthropic" && e.Tier == "pro" }),
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "anthropic" }),
		}
	} else {
		buckets = [][]CatalogEntry{
			filterEntries(active, func(e CatalogEntry) bool {
				return e.Family == "anthropic" && strings.Contains(strings.ToLower(e.ID), "sonnet")
			}),
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "anthropic" && e.Tier == "flash" }),
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "anthropic" }),
		}
	}
	for _, b := range buckets {
		if len(b) > 0 {
			best := rankBucket(b, role, tier)
			fb := goneFallbackNote(prevModel, best.ID, goneIDs)
			return RolePick{
				Role:           role,
				Tier:           tier,
				Model:          best.ID,
				Reason:         fmt.Sprintf("role=%s -> Anthropic (%s)", role, best.ID),
				FallbackReason: fb,
			}, nil
		}
	}
	gem, _, _ := pickGeminiOrAny(active, goneIDs, "", "builder", tier)
	fb := fmt.Sprintf("role=%s fallback to Gemini (%s): no active Anthropic model in agy models", role, gem.ID)
	if prevModel != "" && goneIDs[prevModel] {
		fb = fmt.Sprintf("role=%s fallback to Gemini (%s): previous model %s is gone and no Anthropic model is active", role, gem.ID, prevModel)
	}
	return RolePick{
		Role:           role,
		Tier:           tier,
		Model:          gem.ID,
		Reason:         fb,
		FallbackReason: fb,
	}, nil
}

func pickGeminiOrAny(active []CatalogEntry, goneIDs map[string]bool, prevModel, role, tier string) (CatalogEntry, string, string) {
	var buckets [][]CatalogEntry
	switch {
	case role == "mechanical":
		buckets = [][]CatalogEntry{
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "gemini" && e.Tier == "flash" }),
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "gemini" }),
			active,
		}
	case role == "planner":
		buckets = [][]CatalogEntry{
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "gemini" && isArgon(e.ID) && e.Tier == "pro" }),
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "gemini" && isCarbon(e.ID) && e.Tier == "pro" }),
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "gemini" && e.Tier == "pro" }),
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "gemini" }),
			active,
		}
	case tier == "flash":
		buckets = [][]CatalogEntry{
			filterEntries(active, func(e CatalogEntry) bool {
				return e.Family == "gemini" && e.Subfamily == "hydrogen" && e.Tier == "flash"
			}),
			filterEntries(active, func(e CatalogEntry) bool {
				return e.Family == "gemini" && isCarbon(e.ID) && (e.Effort == "medium" || e.Effort == "low")
			}),
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "gemini" && e.Tier == "flash" }),
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "gemini" }),
			active,
		}
	default:
		buckets = [][]CatalogEntry{
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "gemini" && isCarbon(e.ID) && e.Tier == "pro" }),
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "gemini" && isArgon(e.ID) && e.Tier == "pro" }),
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "gemini" && e.Tier == "pro" }),
			filterEntries(active, func(e CatalogEntry) bool { return e.Family == "gemini" }),
			active,
		}
	}
	for _, b := range buckets {
		if len(b) > 0 {
			best := rankBucket(b, role, tier)
			famLabel := "Gemini"
			if best.Family != "gemini" {
				famLabel = best.Family
			}
			return best, fmt.Sprintf("role=%s -> %s (%s)", role, famLabel, best.ID), goneFallbackNote(prevModel, best.ID, goneIDs)
		}
	}
	return active[0], fmt.Sprintf("role=%s -> %s", role, active[0].ID), goneFallbackNote(prevModel, active[0].ID, goneIDs)
}

func goneFallbackNote(prevModel, newModel string, goneIDs map[string]bool) string {
	if prevModel != "" && prevModel != newModel && goneIDs[prevModel] {
		return fmt.Sprintf("previous model %s is gone from agy models; fell back to %s", prevModel, newModel)
	}
	return ""
}

func isCarbon(id string) bool {
	return strings.Contains(strings.ToLower(id), "carbon")
}

func isArgon(id string) bool {
	return strings.Contains(strings.ToLower(id), "argon")
}

func filterEntries(in []CatalogEntry, keep func(CatalogEntry) bool) []CatalogEntry {
	var out []CatalogEntry
	for _, e := range in {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

func rankBucket(candidates []CatalogEntry, role, tier string) CatalogEntry {
	cp := append([]CatalogEntry(nil), candidates...)
	sort.SliceStable(cp, func(i, j int) bool {
		return betterCandidate(cp[i], cp[j], role, tier)
	})
	return cp[0]
}

func betterCandidate(a, b CatalogEntry, role, tier string) bool {
	rateA, evA := effectivePassRate(a.Evidence)
	rateB, evB := effectivePassRate(b.Evidence)
	if evA && evB {
		if timeoutPenalty(a, rateA) != timeoutPenalty(b, rateB) {
			return !timeoutPenalty(a, rateA)
		}
		if math.Abs(rateA-rateB) > 1e-9 {
			return rateA > rateB
		}
		if a.Evidence.WorkMeanSeconds > 0 && b.Evidence.WorkMeanSeconds > 0 &&
			math.Abs(a.Evidence.WorkMeanSeconds-b.Evidence.WorkMeanSeconds) > 1e-9 {
			return a.Evidence.WorkMeanSeconds < b.Evidence.WorkMeanSeconds
		}
	} else if evA != evB {
		if evA {
			return rateA >= 0.5
		}
		return rateB < 0.5
	}
	if cmp := compareModelVersions(a.ID, b.ID); cmp != 0 {
		return cmp > 0
	}
	if ea, eb := effortRank(a, role, tier), effortRank(b, role, tier); ea != eb {
		return ea > eb
	}
	return a.ID < b.ID
}

func timeoutPenalty(e CatalogEntry, rate float64) bool {
	return e.Evidence.WorkRuns >= 5 && rate < 0.50 && e.Evidence.WorkMeanSeconds > 1800
}

func compareModelVersions(idA, idB string) int {
	va, vb := parseVersionNums(idA), parseVersionNums(idB)
	n := max(len(va), len(vb))
	for i := 0; i < n; i++ {
		var xa, xb int
		if i < len(va) {
			xa = va[i]
		}
		if i < len(vb) {
			xb = vb[i]
		}
		if xa != xb {
			if xa > xb {
				return 1
			}
			return -1
		}
	}
	return 0
}

func parseVersionNums(id string) []int {
	raw := numTokenRe.FindAllString(id, -1)
	nums := make([]int, 0, len(raw))
	for _, s := range raw {
		if n, err := strconv.Atoi(s); err == nil {
			nums = append(nums, n)
		}
	}
	return nums
}

func effortRank(e CatalogEntry, role, tier string) int {
	if role == "builder" && tier == "flash" && e.Subfamily == "hydrogen" {
		switch e.Effort {
		case "medium":
			return 5
		case "low":
			return 4
		case "":
			return 3
		default:
			return 1
		}
	}
	if role == "builder" && tier == "flash" && isCarbon(e.ID) {
		switch e.Effort {
		case "medium":
			return 4
		case "low":
			return 3
		case "high":
			return 2
		default:
			return 1
		}
	}
	switch e.Effort {
	case "max":
		return 6
	case "xhigh":
		return 5
	case "high":
		return 4
	case "":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}
