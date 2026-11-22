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
		ExploreBonus: sc.ExploreBonus, EfficiencyBonus: sc.EfficiencyBonus,
		HeadroomPenalty: sc.HeadroomPenalty, RankScore: sc.RankScore, DecayedRuns: sc.DecayedRuns,
	}
}

func formatScoreSummary(sc ScoreBreakdown) string {
	return fmt.Sprintf("score=%.2f post=%.2f ucb=%.2f pen=%.2f n=%.1f",
		sc.RankScore, sc.PosteriorMean, sc.ExploreBonus, sc.HeadroomPenalty, sc.DecayedRuns)
}

func pickCrossFamilyRole(active []CatalogEntry, goneIDs map[string]bool, pools []Pool, prevModel, role, tier, callerFamily string) RolePick {
	if callerFamily == "anthropic" {
		return pickCrossFamilyGeminiPreferred(active, goneIDs, pools, prevModel, role, tier)
	}
	return pickCrossFamilyAnthropicPreferred(active, goneIDs, pools, prevModel, role, tier)
}

func pickCrossFamilyGeminiPreferred(active []CatalogEntry, goneIDs map[string]bool, pools []Pool, prevModel, role, tier string) RolePick {
	usable := nonExhaustedOrAll(active, pools)
	anthHealthy := filterEntries(usable, func(e CatalogEntry) bool { return isAnthropicFamily(e.Family) })
	if pool, low := familyPoolAtOrBelow(pools, true, FamilyFailoverHeadroomPct); low && len(anthHealthy) > 0 {
		best, sc := pickFromFamilyBuckets(anthHealthy, role, tier, pools, false)
		fb := fmt.Sprintf("role=%s fallback to Anthropic (%s): %s quota low (5h=%s, week=%s; floor %d%%)",
			role, best.ID, pool.Name, formatPoolPct(pool.Five), formatPoolPct(pool.Week), FamilyFailoverHeadroomPct)
		return buildRolePick(role, tier, best.ID, fb+" ("+formatScoreSummary(sc)+")", fb, sc)
	}
	gemEntries := filterEntries(usable, func(e CatalogEntry) bool { return isGeminiFamily(e.Family, e.ID) })
	if len(gemEntries) > 0 {
		best, sc := pickFromFamilyBuckets(gemEntries, role, tier, pools, true)
		reason := fmt.Sprintf("role=%s -> Gemini (%s; %s)", role, best.ID, formatScoreSummary(sc))
		return buildRolePick(role, tier, best.ID, reason, goneFallbackNote(prevModel, best.ID, goneIDs), sc)
	}
	if len(anthHealthy) > 0 {
		best, sc := pickFromFamilyBuckets(anthHealthy, role, tier, pools, false)
		fb := fmt.Sprintf("role=%s fallback to Anthropic (%s): no active Gemini model in agy models", role, best.ID)
		return buildRolePick(role, tier, best.ID, fb, fb, sc)
	}
	chosen, sc, reason, fb := pickGeminiOrAny(usable, goneIDs, pools, prevModel, role, tier)
	return buildRolePick(role, tier, chosen.ID, reason, fb, sc)
}

func pickCrossFamilyAnthropicPreferred(active []CatalogEntry, goneIDs map[string]bool, pools []Pool, prevModel, role, tier string) RolePick {
	usable := nonExhaustedOrAll(active, pools)
	gemHealthy := filterEntries(usable, func(e CatalogEntry) bool { return isGeminiFamily(e.Family, e.ID) })
	if pool, ok := findAnthropicPool(pools); ok && (pool.Week < AnthropicJudgeFloorWeek || pool.Five < AnthropicJudgeFloorFive) && len(gemHealthy) > 0 {
		gem, sc := pickFromFamilyBuckets(gemHealthy, "builder", tier, pools, true)
		fb := fmt.Sprintf("role=%s fallback to Gemini (%s): %s quota low (5h=%s, week=%s; floor %d%%/%d%%)",
			role, gem.ID, pool.Name, formatPoolPct(pool.Five), formatPoolPct(pool.Week), AnthropicJudgeFloorFive, AnthropicJudgeFloorWeek)
		return buildRolePick(role, tier, gem.ID, fb+" ("+formatScoreSummary(sc)+")", fb, sc)
	}
	anthEntries := filterEntries(usable, func(e CatalogEntry) bool { return isAnthropicFamily(e.Family) })
	if len(anthEntries) > 0 {
		best, sc := pickFromFamilyBuckets(anthEntries, role, tier, pools, false)
		reason := fmt.Sprintf("role=%s -> Anthropic (%s; %s)", role, best.ID, formatScoreSummary(sc))
		return buildRolePick(role, tier, best.ID, reason, goneFallbackNote(prevModel, best.ID, goneIDs), sc)
	}
	gem, sc, _, _ := pickGeminiOrAny(usable, goneIDs, pools, "", "builder", tier)
	fb := fmt.Sprintf("role=%s fallback to Gemini (%s): no active Anthropic model in agy models", role, gem.ID)
	if prevModel != "" && goneIDs[prevModel] {
		fb = fmt.Sprintf("role=%s fallback to Gemini (%s): previous model %s is gone and no Anthropic model is active", role, gem.ID, prevModel)
	}
	return buildRolePick(role, tier, gem.ID, fb, fb, sc)
}

func pickFromFamilyBuckets(familyEntries []CatalogEntry, role, tier string, pools []Pool, allowMechanicalLight bool) (CatalogEntry, ScoreBreakdown) {
	matched := filterEntries(familyEntries, func(e CatalogEntry) bool {
		return e.Tier == tier || (allowMechanicalLight && role == "mechanical" && tier == "flash" && e.Tier == "light")
	})
	if len(matched) > 0 {
		return rankCandidates(matched, role, tier, pools)
	}
	return rankCandidates(familyEntries, role, tier, pools)
}

func pickGeminiOrAny(active []CatalogEntry, goneIDs map[string]bool, pools []Pool, prevModel, role, tier string) (CatalogEntry, ScoreBreakdown, string, string) {
	matchesTier := func(e CatalogEntry) bool {
		return e.Tier == tier || (role == "mechanical" && tier == "flash" && e.Tier == "light")
	}
	usable := nonExhaustedOrAll(active, pools)
	if pool, low := familyPoolAtOrBelow(pools, true, FamilyFailoverHeadroomPct); low {
		nonGem := filterEntries(usable, func(e CatalogEntry) bool { return !isGeminiFamily(e.Family, e.ID) })
		if len(nonGem) > 0 {
			best, sc := pickFromFamilyBuckets(nonGem, role, tier, pools, false)
			famLabel := nonEmpty(best.Family, "other")
			fb := fmt.Sprintf("role=%s fallback to %s (%s): %s quota low (5h=%s, week=%s; floor %d%%)",
				role, famLabel, best.ID, pool.Name, formatPoolPct(pool.Five), formatPoolPct(pool.Week), FamilyFailoverHeadroomPct)
			return best, sc, fb + " (" + formatScoreSummary(sc) + ")", fb
		}
	}
	buckets := [][]CatalogEntry{
		filterEntries(usable, func(e CatalogEntry) bool { return isGeminiFamily(e.Family, e.ID) && matchesTier(e) }),
		filterEntries(usable, func(e CatalogEntry) bool { return isGeminiFamily(e.Family, e.ID) }),
		filterEntries(usable, matchesTier),
		usable,
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

func allActivePoolsExhausted(eligible []CatalogEntry, pools []Pool) bool {
	if len(pools) == 0 || len(eligible) == 0 {
		return false
	}
	matched := 0
	for _, e := range eligible {
		p, ok := FindPoolForModel(pools, e.Family, e.ID)
		if !ok {
			return false
		}
		pct, has := PoolHeadroomPct(p)
		if !has {
			return false
		}
		matched++
		if pct > 0 {
			return false
		}
	}
	return matched > 0
}

func nonExhaustedOrAll(entries []CatalogEntry, pools []Pool) []CatalogEntry {
	if len(pools) == 0 {
		return entries
	}
	healthy := filterEntries(entries, func(e CatalogEntry) bool {
		p, ok := FindPoolForModel(pools, e.Family, e.ID)
		if !ok {
			return true
		}
		pct, has := PoolHeadroomPct(p)
		return !has || pct > 0
	})
	if len(healthy) > 0 {
		return healthy
	}
	return entries
}

func findGeminiPool(pools []Pool) (Pool, bool) {
	for _, p := range pools {
		low := strings.ToLower(p.Name)
		if strings.Contains(low, "gemini") || strings.Contains(low, "google") {
			return p, true
		}
	}
	return Pool{}, false
}

func familyPoolAtOrBelow(pools []Pool, wantGemini bool, floorPct int) (Pool, bool) {
	var p Pool
	var ok bool
	if wantGemini {
		p, ok = findGeminiPool(pools)
	} else {
		p, ok = findAnthropicPool(pools)
	}
	if !ok {
		return Pool{}, false
	}
	pct, has := PoolHeadroomPct(p)
	return p, has && pct <= floorPct
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
