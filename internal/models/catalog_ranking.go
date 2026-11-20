package models

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

func buildRolePick(role, tier, model, reason, fb string, sc ScoreBreakdown) RolePick {
	return RolePick{
		Role: role, Tier: tier, Model: model, Reason: reason, FallbackReason: fb,
		PriorMean: sc.PriorMean, PosteriorMean: sc.PosteriorMean,
		ExploreBonus: sc.ExploreBonus, HeadroomPenalty: sc.HeadroomPenalty,
		RankScore: sc.RankScore, DecayedRuns: sc.DecayedRuns,
	}
}

func formatScoreSummary(sc ScoreBreakdown) string {
	return fmt.Sprintf("score=%.2f post=%.2f ucb=%.2f pen=%.2f n=%.1f",
		sc.RankScore, sc.PosteriorMean, sc.ExploreBonus, sc.HeadroomPenalty, sc.DecayedRuns)
}

func pickCrossFamilyRole(active []CatalogEntry, goneIDs map[string]bool, pools []Pool, prevModel, role, tier string) RolePick {
	if pool, ok := findAnthropicPool(pools); ok && (pool.Week < AnthropicJudgeFloorWeek || pool.Five < AnthropicJudgeFloorFive) {
		gem, sc, _, _ := pickGeminiOrAny(active, goneIDs, pools, "", "builder", tier)
		fb := fmt.Sprintf("role=%s fallback to Gemini (%s): %s quota low (5h=%s, week=%s; floor %d%%/%d%%)",
			role, gem.ID, pool.Name, formatPoolPct(pool.Five), formatPoolPct(pool.Week), AnthropicJudgeFloorFive, AnthropicJudgeFloorWeek)
		return buildRolePick(role, tier, gem.ID, fb+" ("+formatScoreSummary(sc)+")", fb, sc)
	}
	buckets := [][]CatalogEntry{
		filterEntries(active, func(e CatalogEntry) bool { return isAnthropicFamily(e.Family) && e.Tier == tier }),
		filterEntries(active, func(e CatalogEntry) bool { return isAnthropicFamily(e.Family) }),
	}
	for _, b := range buckets {
		if len(b) > 0 {
			best, sc := rankCandidates(b, role, tier, pools)
			reason := fmt.Sprintf("role=%s -> Anthropic (%s; %s)", role, best.ID, formatScoreSummary(sc))
			return buildRolePick(role, tier, best.ID, reason, goneFallbackNote(prevModel, best.ID, goneIDs), sc)
		}
	}
	gem, sc, _, _ := pickGeminiOrAny(active, goneIDs, pools, "", "builder", tier)
	fb := fmt.Sprintf("role=%s fallback to Gemini (%s): no active Anthropic model in agy models", role, gem.ID)
	if prevModel != "" && goneIDs[prevModel] {
		fb = fmt.Sprintf("role=%s fallback to Gemini (%s): previous model %s is gone and no Anthropic model is active", role, gem.ID, prevModel)
	}
	return buildRolePick(role, tier, gem.ID, fb, fb, sc)
}

func pickGeminiOrAny(active []CatalogEntry, goneIDs map[string]bool, pools []Pool, prevModel, role, tier string) (CatalogEntry, ScoreBreakdown, string, string) {
	matchesTier := func(e CatalogEntry) bool {
		return e.Tier == tier || (role == "mechanical" && tier == "flash" && e.Tier == "light")
	}
	buckets := [][]CatalogEntry{
		filterEntries(active, func(e CatalogEntry) bool { return isGeminiFamily(e.Family, e.ID) && matchesTier(e) }),
		filterEntries(active, func(e CatalogEntry) bool { return isGeminiFamily(e.Family, e.ID) }),
		filterEntries(active, matchesTier),
		active,
	}
	for _, b := range buckets {
		if len(b) > 0 {
			best, sc := rankCandidates(b, role, tier, pools)
			famLabel := "Gemini"
			if !isGeminiFamily(best.Family, best.ID) {
				famLabel = nonEmpty(best.Family, "other")
			}
			return best, sc, fmt.Sprintf("role=%s -> %s (%s; %s)", role, famLabel, best.ID, formatScoreSummary(sc)), goneFallbackNote(prevModel, best.ID, goneIDs)
		}
	}
	sc := ScoreCandidate(active[0], role, tier, TotalRoleSamples(active), pools)
	return active[0], sc, fmt.Sprintf("role=%s -> %s", role, active[0].ID), goneFallbackNote(prevModel, active[0].ID, goneIDs)
}

func isGeminiFamily(family, id string) bool {
	f := strings.ToLower(strings.TrimSpace(family))
	return f == "gemini" || f == "google" || strings.HasPrefix(strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "models/")), "gemini")
}

func isAnthropicFamily(family string) bool {
	return strings.EqualFold(strings.TrimSpace(family), "anthropic")
}

func goneFallbackNote(prevModel, newModel string, goneIDs map[string]bool) string {
	if prevModel != "" && prevModel != newModel && goneIDs[prevModel] {
		return fmt.Sprintf("previous model %s is gone from agy models; fell back to %s", prevModel, newModel)
	}
	return ""
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

func rankCandidates(candidates []CatalogEntry, role, tier string, pools []Pool) (CatalogEntry, ScoreBreakdown) {
	totalSamples := TotalRoleSamples(candidates)
	type scored struct {
		entry CatalogEntry
		score ScoreBreakdown
	}
	items := make([]scored, len(candidates))
	for i, c := range candidates {
		items[i] = scored{entry: c, score: ScoreCandidate(c, role, tier, totalSamples, pools)}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return betterScoredCandidate(items[i].entry, items[i].score, items[j].entry, items[j].score)
	})
	return items[0].entry, items[0].score
}

func betterScoredCandidate(a CatalogEntry, sa ScoreBreakdown, b CatalogEntry, sb ScoreBreakdown) bool {
	if math.Abs(sa.RankScore-sb.RankScore) > 1e-6 {
		return sa.RankScore > sb.RankScore
	}
	if a.Evidence.WorkMeanSeconds > 0 && b.Evidence.WorkMeanSeconds > 0 &&
		math.Abs(a.Evidence.WorkMeanSeconds-b.Evidence.WorkMeanSeconds) > 1e-6 {
		return a.Evidence.WorkMeanSeconds < b.Evidence.WorkMeanSeconds
	}
	if cmp := compareModelVersions(a.ID, b.ID); cmp != 0 {
		return cmp > 0
	}
	if ea, eb := canonicalEffortRank(entryEffort(a)), canonicalEffortRank(entryEffort(b)); ea != eb {
		return ea > eb
	}
	return false
}

func populateEntryScores(entries []CatalogEntry, pools []Pool) {
	active := filterEntries(entries, func(e CatalogEntry) bool { return e.Status != "gone" && isAutoPickEligible(e) })
	totalSamples := TotalRoleSamples(active)
	for i := range entries {
		if entries[i].InteractiveOnly || entries[i].Mode == "orchestrator" {
			entries[i].Score = nil
			continue
		}
		role, tier := defaultEntryRoleTier(entries[i])
		sc := ScoreCandidate(entries[i], role, tier, totalSamples, pools)
		entries[i].Score = &sc
	}
}

func defaultEntryRoleTier(e CatalogEntry) (string, string) {
	tier := nonEmpty(e.Tier, "pro")
	if isAnthropicFamily(e.Family) {
		return "judge", tier
	}
	if tier == "light" || tier == "flash" {
		return "mechanical", tier
	}
	return "builder", tier
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
