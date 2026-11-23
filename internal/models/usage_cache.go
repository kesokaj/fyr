package models

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	// DefaultPoolQuotaTTL is the maximum age of cached models.json pool quotas
	// reused by warm `fyr models --pick` when all active pools are >= 30%.
	DefaultPoolQuotaTTL = 10 * time.Minute

	// LowPoolQuotaTTL shortens quota cache TTL when any active pool is < 30%.
	LowPoolQuotaTTL = 2 * time.Minute

	// LowPoolQuotaPct is the headroom threshold below which LowPoolQuotaTTL applies.
	LowPoolQuotaPct = 30

	maxQuotaClockSkew = time.Minute
)

// usageNowFunc returns the current UTC clock; overridden in unit tests.
var usageNowFunc = func() time.Time { return time.Now().UTC() }

// PickRoleOptions configures catalog-based role picking and quota cache behavior.
type PickRoleOptions struct {
	Home         string
	AgyPath      string
	ForceRefresh bool
	QuotaTTL     time.Duration // <= 0 uses AdaptivePoolQuotaTTL(cat.Pools)
	CallerFamily string        // optional caller family for cross-family roles
}

// AdaptivePoolQuotaTTL returns LowPoolQuotaTTL (2m) when any active pool has
// headroom < 30%, otherwise DefaultPoolQuotaTTL (10m).
func AdaptivePoolQuotaTTL(pools []Pool) time.Duration {
	minPct := 101
	for _, p := range pools {
		if pct, ok := PoolHeadroomPct(p); ok && pct < minPct {
			minPct = pct
		}
	}
	if minPct < LowPoolQuotaPct {
		return LowPoolQuotaTTL
	}
	return DefaultPoolQuotaTTL
}

// HasFreshPoolQuotas reports whether cat carries structurally valid pool quotas
// whose QuotaUpdatedAt timestamp is within ttl of now (and not far in the future).
func HasFreshPoolQuotas(cat Catalog, now time.Time, ttl time.Duration) bool {
	if ttl <= 0 || len(cat.Pools) == 0 {
		return false
	}
	rawTS := strings.TrimSpace(cat.QuotaUpdatedAt)
	if rawTS == "" {
		return false
	}
	ts, err := time.Parse(time.RFC3339Nano, rawTS)
	if err != nil {
		return false
	}
	age := now.UTC().Sub(ts.UTC())
	if age > ttl || age < -maxQuotaClockSkew {
		return false
	}
	return validCachedPools(cat.Pools)
}

func validCachedPools(pools []Pool) bool {
	if len(pools) == 0 || len(pools) > poolsLimit {
		return false
	}
	for _, p := range pools {
		name := strings.TrimSpace(p.Name)
		if name == "" || len(name) > poolLimit {
			return false
		}
		if !validPoolWindow(p.Five) || !validPoolWindow(p.Week) {
			return false
		}
	}
	return true
}

func validPoolWindow(v int) bool {
	return v == DisabledWindow || (v >= 0 && v <= 100)
}

// PickRoleFromCatalogForHome selects an active model from cat for role+tier,
// reusing fresh cached pool quotas when available unless opts.ForceRefresh is set,
// and returning any freshly probed live pools to persist into models.json.
func PickRoleFromCatalogForHome(ctx context.Context, cat Catalog, opts PickRoleOptions, role, tier string) (RolePick, []Pool, error) {
	normRole, normTier, err := NormalizeRoleTier(role, tier)
	if err != nil {
		return RolePick{}, nil, err
	}
	normCaller, err := NormalizeCallerFamily(opts.CallerFamily)
	if err != nil {
		return RolePick{}, nil, err
	}
	ttl := opts.QuotaTTL
	if ttl <= 0 {
		ttl = AdaptivePoolQuotaTTL(cat.Pools)
	}
	crossFamily := IsCrossFamilyRole(normRole)
	freshCache := !opts.ForceRefresh && HasFreshPoolQuotas(cat, usageNowFunc(), ttl)
	if !crossFamily {
		return pickSameFamilyWithQuota(ctx, cat, opts, normRole, normTier, normCaller, freshCache)
	}
	if opts.AgyPath == "" && !freshCache {
		pick, err := PickFromEntriesWithPoolsAndCaller(cat.Entries, cat.Picks, nil, normRole, normTier, normCaller)
		return pick, nil, err
	}
	return pickCrossFamilyWithQuota(ctx, cat, opts, normRole, normTier, normCaller, freshCache)
}

func pickSameFamilyWithQuota(ctx context.Context, cat Catalog, opts PickRoleOptions, normRole, normTier, normCaller string, freshCache bool) (RolePick, []Pool, error) {
	activePools := cat.Pools
	var livePools []Pool
	if !freshCache {
		activePools = nil
		if opts.AgyPath != "" {
			if uText, err := probeUsageForHome(ctx, opts.Home, opts.AgyPath); err == nil {
				if parsed, perr := ParseUsage(uText); perr == nil {
					livePools, activePools = parsed, parsed
				}
			}
		}
	}
	pick, err := PickFromEntriesWithPoolsAndCaller(cat.Entries, cat.Picks, activePools, normRole, normTier, normCaller)
	return pick, livePools, err
}

func pickCrossFamilyWithQuota(ctx context.Context, cat Catalog, opts PickRoleOptions, normRole, normTier, normCaller string, freshCache bool) (RolePick, []Pool, error) {
	if normCaller == "anthropic" {
		return pickCrossFamilyAnthropicCaller(ctx, cat, opts, normRole, normTier, freshCache)
	}
	pick, err := PickFromEntriesWithPoolsAndCaller(cat.Entries, cat.Picks, nil, normRole, normTier, normCaller)
	if err != nil || !isAnthropicEntryID(cat.Entries, pick.Model) {
		return pick, nil, err
	}
	active := activeAutoPickEntries(cat.Entries)
	gem, gemScore, _, _ := pickGeminiOrAny(active, nil, nil, "", "builder", normTier)
	initialGemID := gem.ID
	model, reason, fb, livePools, activePools := resolveAnthropicQuota(ctx, cat, opts, normRole, pick.Model, initialGemID, freshCache)
	if allActivePoolsExhausted(active, activePools) {
		return RolePick{}, livePools, ErrAllPoolsExhausted
	}
	if fb != "" {
		if len(activePools) > 0 {
			gem, gemScore, _, _ = pickGeminiOrAny(active, nil, activePools, "", "builder", normTier)
			if isAnthropicFamily(gem.Family) {
				picked, perr := PickFromEntriesWithPoolsAndCaller(cat.Entries, cat.Picks, activePools, normRole, normTier, normCaller)
				return picked, livePools, perr
			}
			if gem.ID != "" && gem.ID != initialGemID {
				famLabel := "Gemini"
				if !isGeminiFamily(gem.Family, gem.ID) {
					famLabel = nonEmpty(gem.Family, "other")
				}
				reason = strings.ReplaceAll(reason, "Gemini ("+initialGemID+")", famLabel+" ("+gem.ID+")")
				fb = strings.ReplaceAll(fb, "Gemini ("+initialGemID+")", famLabel+" ("+gem.ID+")")
				reason = strings.ReplaceAll(reason, "("+initialGemID+")", "("+gem.ID+")")
				fb = strings.ReplaceAll(fb, "("+initialGemID+")", "("+gem.ID+")")
			}
			model = gem.ID
		}
		return buildRolePick(normRole, normTier, model, reason, fb, gemScore), livePools, nil
	}
	return finalizeAnthropicPick(cat, pick, activePools, normRole, normTier, reason), livePools, nil
}

func pickCrossFamilyAnthropicCaller(ctx context.Context, cat Catalog, opts PickRoleOptions, normRole, normTier string, freshCache bool) (RolePick, []Pool, error) {
	activePools, livePools := cat.Pools, ([]Pool)(nil)
	if !freshCache {
		activePools = nil
		if uText, err := probeUsageForHome(ctx, opts.Home, opts.AgyPath); err == nil {
			if parsed, perr := ParseUsage(uText); perr == nil {
				livePools, activePools = parsed, parsed
			}
		}
	}
	pick, err := PickFromEntriesWithPoolsAndCaller(cat.Entries, cat.Picks, activePools, normRole, normTier, "anthropic")
	if err != nil {
		return RolePick{}, livePools, err
	}
	if freshCache && pick.FallbackReason == "" && !strings.Contains(pick.Reason, "cached") {
		pick.Reason += "; cached"
	}
	return pick, livePools, nil
}

func activeAutoPickEntries(entries []CatalogEntry) []CatalogEntry {
	active := filterEntries(entries, func(e CatalogEntry) bool {
		ApplyModelProfile(&e)
		return e.Status != "gone" && isAutoPickEligible(e)
	})
	if len(active) == 0 {
		active = filterEntries(entries, func(e CatalogEntry) bool { return e.Status != "gone" })
	}
	return active
}

func resolveAnthropicQuota(ctx context.Context, cat Catalog, opts PickRoleOptions, role, anthropicModel, geminiFallback string, freshCache bool) (model, reason, fb string, livePools, activePools []Pool) {
	if freshCache {
		model, reason, fb = evaluateAnthropicPools(cat.Pools, role, anthropicModel, geminiFallback, true)
		return model, reason, fb, nil, cat.Pools
	}
	model, reason, fb, livePools = probeAnthropicQuotaPools(ctx, opts.Home, opts.AgyPath, role, anthropicModel, geminiFallback)
	return model, reason, fb, livePools, livePools
}

func finalizeAnthropicPick(cat Catalog, base RolePick, pools []Pool, normRole, normTier, reason string) RolePick {
	scoredPick, serr := PickFromEntriesWithPools(cat.Entries, cat.Picks, pools, normRole, normTier)
	if serr != nil {
		scoredPick = base
	}
	scoredPick.Reason = reason + "; " + formatScoreSummary(ScoreBreakdown{
		PriorMean: scoredPick.PriorMean, PosteriorMean: scoredPick.PosteriorMean,
		ExploreBonus: scoredPick.ExploreBonus, HeadroomPenalty: scoredPick.HeadroomPenalty,
		RankScore: scoredPick.RankScore, DecayedRuns: scoredPick.DecayedRuns,
	})
	if scoredPick.FallbackReason == "" {
		scoredPick.FallbackReason = base.FallbackReason
	}
	return scoredPick
}

func evaluateAnthropicPools(pools []Pool, role, anthropicModel, geminiFallback string, cached bool) (model, reason, fb string) {
	pool, ok := findAnthropicPool(pools)
	if !ok {
		fb = fmt.Sprintf("role=%s fallback to Gemini (%s): no Anthropic/Claude pool in /usage", role, geminiFallback)
		return geminiFallback, fb, fb
	}
	if pool.Week < AnthropicJudgeFloorWeek || pool.Five < AnthropicJudgeFloorFive {
		fb = fmt.Sprintf("role=%s fallback to Gemini (%s): %s quota low (5h=%s, week=%s; floor %d%%/%d%%)",
			role, geminiFallback, pool.Name, formatPoolPct(pool.Five), formatPoolPct(pool.Week), AnthropicJudgeFloorFive, AnthropicJudgeFloorWeek)
		return geminiFallback, fb, fb
	}
	suffix := ""
	if cached {
		suffix = "; cached"
	}
	note := fmt.Sprintf("role=%s -> Anthropic (%s; %s 5h=%s, week=%s%s)",
		role, anthropicModel, pool.Name, formatPoolPct(pool.Five), formatPoolPct(pool.Week), suffix)
	return anthropicModel, note, ""
}
