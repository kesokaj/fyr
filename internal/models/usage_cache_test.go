package models

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHasFreshPoolQuotas(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	validPools := []Pool{
		{Name: "Gemini Models", Five: 90, Week: 80},
		{Name: "Claude and GPT models", Five: 60, Week: 45},
	}

	cases := []struct {
		name string
		cat  Catalog
		ttl  time.Duration
		want bool
	}{
		{
			name: "fresh 5m old",
			cat:  Catalog{QuotaUpdatedAt: now.Add(-5 * time.Minute).Format(time.RFC3339), Pools: validPools},
			ttl:  DefaultPoolQuotaTTL,
			want: true,
		},
		{
			name: "exact boundary 10m old",
			cat:  Catalog{QuotaUpdatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339), Pools: validPools},
			ttl:  DefaultPoolQuotaTTL,
			want: true,
		},
		{
			name: "disabled window valid",
			cat: Catalog{
				QuotaUpdatedAt: now.Add(-1 * time.Minute).Format(time.RFC3339),
				Pools:          []Pool{{Name: "Claude", Five: DisabledWindow, Week: 50}},
			},
			ttl:  DefaultPoolQuotaTTL,
			want: true,
		},
		{
			name: "stale past 10m",
			cat:  Catalog{QuotaUpdatedAt: now.Add(-10*time.Minute - time.Second).Format(time.RFC3339), Pools: validPools},
			ttl:  DefaultPoolQuotaTTL,
			want: false,
		},
		{
			name: "missing timestamp",
			cat:  Catalog{Pools: validPools},
			ttl:  DefaultPoolQuotaTTL,
			want: false,
		},
		{
			name: "empty pools",
			cat:  Catalog{QuotaUpdatedAt: now.Format(time.RFC3339)},
			ttl:  DefaultPoolQuotaTTL,
			want: false,
		},
		{
			name: "corrupt timestamp",
			cat:  Catalog{QuotaUpdatedAt: "not-a-timestamp", Pools: validPools},
			ttl:  DefaultPoolQuotaTTL,
			want: false,
		},
		{
			name: "far future timestamp",
			cat:  Catalog{QuotaUpdatedAt: now.Add(5 * time.Minute).Format(time.RFC3339), Pools: validPools},
			ttl:  DefaultPoolQuotaTTL,
			want: false,
		},
		{
			name: "corrupt negative window",
			cat: Catalog{
				QuotaUpdatedAt: now.Add(-time.Minute).Format(time.RFC3339),
				Pools:          []Pool{{Name: "Claude", Five: -2, Week: 50}},
			},
			ttl:  DefaultPoolQuotaTTL,
			want: false,
		},
		{
			name: "corrupt over 100 pct",
			cat: Catalog{
				QuotaUpdatedAt: now.Add(-time.Minute).Format(time.RFC3339),
				Pools:          []Pool{{Name: "Claude", Five: 50, Week: 105}},
			},
			ttl:  DefaultPoolQuotaTTL,
			want: false,
		},
		{
			name: "corrupt blank pool name",
			cat: Catalog{
				QuotaUpdatedAt: now.Add(-time.Minute).Format(time.RFC3339),
				Pools:          []Pool{{Name: "   ", Five: 50, Week: 50}},
			},
			ttl:  DefaultPoolQuotaTTL,
			want: false,
		},
		{
			name: "non-positive ttl bypasses cache",
			cat:  Catalog{QuotaUpdatedAt: now.Format(time.RFC3339), Pools: validPools},
			ttl:  0,
			want: false,
		},
	}

	for _, tc := range cases {
		if got := HasFreshPoolQuotas(tc.cat, now, tc.ttl); got != tc.want {
			t.Errorf("%s: HasFreshPoolQuotas = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPickRoleFromCatalogQuotaCacheBehavior(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	oldNow, oldUsage := usageNowFunc, UsageProbeFunc
	usageNowFunc = func() time.Time { return now }
	t.Cleanup(func() {
		usageNowFunc = oldNow
		UsageProbeFunc = oldUsage
	})

	entries := ParseModelsOutput(
		"gemini-4-carbon-high\tGemini 4 Carbon (High)\n" +
			"claude-opus-5-5-high\tClaude Opus 5.5 (High)\n",
	)
	for i := range entries {
		entries[i].Status = "active"
	}

	calls := 0
	liveUsage := "Gemini Models\tWeekly Limit Remaining\t80%\tx\n" +
		"Gemini Models\tFive Hour Limit Remaining\t90%\tx\n" +
		"Claude and GPT models\tWeekly Limit Remaining\t65%\tx\n" +
		"Claude and GPT models\tFive Hour Limit Remaining\t75%\tx\n"
	UsageProbeFunc = func(context.Context, string) (string, error) {
		calls++
		return liveUsage, nil
	}

	// 1. Fresh healthy cache -> zero agy probe, picks Anthropic, returns nil livePools.
	freshHealthy := Catalog{
		UpdatedAt:      now.Format(time.RFC3339),
		QuotaUpdatedAt: now.Add(-3 * time.Minute).Format(time.RFC3339),
		Entries:        entries,
		Pools: []Pool{
			{Name: "Gemini Models", Five: 90, Week: 85},
			{Name: "Claude and GPT models", Five: 80, Week: 50},
		},
	}
	pick, livePools, err := PickRoleFromCatalogForHome(ctx, freshHealthy, PickRoleOptions{AgyPath: "agy"}, "judge", "pro")
	if err != nil || pick.Model != "claude-opus-5-5-high" || calls != 0 || len(livePools) != 0 {
		t.Fatalf("fresh healthy cache: pick=%+v livePools=%+v calls=%d err=%v", pick, livePools, calls, err)
	}
	if !strings.Contains(pick.Reason, "cached") {
		t.Errorf("fresh healthy cache reason = %q, want cached marker", pick.Reason)
	}

	// 2. Fresh low-quota cache -> zero agy probe, falls back to Gemini.
	freshLow := Catalog{
		UpdatedAt:      now.Format(time.RFC3339),
		QuotaUpdatedAt: now.Add(-2 * time.Minute).Format(time.RFC3339),
		Entries:        entries,
		Pools: []Pool{
			{Name: "Gemini Models", Five: 90, Week: 85},
			{Name: "Claude and GPT models", Five: 15, Week: 50},
		},
	}
	pickLow, liveLow, err := PickRoleFromCatalogForHome(ctx, freshLow, PickRoleOptions{AgyPath: "agy"}, "reviewer", "pro")
	if err != nil || pickLow.Model != "gemini-4-carbon-high" || calls != 0 || len(liveLow) != 0 || !strings.Contains(pickLow.FallbackReason, "quota low") {
		t.Fatalf("fresh low cache: pick=%+v livePools=%+v calls=%d err=%v", pickLow, liveLow, calls, err)
	}

	// 3. Stale (> 10m) cache -> invokes live probe and returns livePools to persist.
	stale := freshHealthy
	stale.QuotaUpdatedAt = now.Add(-11 * time.Minute).Format(time.RFC3339)
	pickStale, liveStale, err := PickRoleFromCatalogForHome(ctx, stale, PickRoleOptions{AgyPath: "agy"}, "judge", "pro")
	if err != nil || pickStale.Model != "claude-opus-5-5-high" || calls != 1 || len(liveStale) != 2 {
		t.Fatalf("stale cache: pick=%+v livePools=%+v calls=%d err=%v", pickStale, liveStale, calls, err)
	}

	// 4. ForceRefresh bypasses fresh cache -> invokes live probe.
	pickRef, liveRef, err := PickRoleFromCatalogForHome(ctx, freshHealthy, PickRoleOptions{AgyPath: "agy", ForceRefresh: true}, "judge", "pro")
	if err != nil || pickRef.Model != "claude-opus-5-5-high" || calls != 2 || len(liveRef) != 2 {
		t.Fatalf("force refresh: pick=%+v livePools=%+v calls=%d err=%v", pickRef, liveRef, calls, err)
	}

	// 5. Corrupt cached quota triggers live probe; if live probe also fails, falls back gracefully.
	UsageProbeFunc = func(context.Context, string) (string, error) {
		calls++
		return "", errors.New("usage offline")
	}
	corrupt := freshHealthy
	corrupt.Pools = []Pool{{Name: "Claude", Five: 999, Week: 50}}
	pickCorr, liveCorr, err := PickRoleFromCatalogForHome(ctx, corrupt, PickRoleOptions{AgyPath: "agy"}, "verifier", "pro")
	if err != nil || pickCorr.Model != "gemini-4-carbon-high" || calls != 3 || len(liveCorr) != 0 || !strings.Contains(pickCorr.FallbackReason, "/usage unreadable") {
		t.Fatalf("corrupt cache + offline probe: pick=%+v livePools=%+v calls=%d err=%v", pickCorr, liveCorr, calls, err)
	}
}

func TestPickRoleExpiredSameFamilyAndCrossFamilyRepickReason(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 15, 0, 0, 0, time.UTC)
	oldNow, oldUsage := usageNowFunc, UsageProbeFunc
	usageNowFunc = func() time.Time { return now }
	t.Cleanup(func() {
		usageNowFunc = oldNow
		UsageProbeFunc = oldUsage
	})

	entries := []CatalogEntry{
		{ID: "gemini-4-carbon-high", DisplayName: "Gemini 4 Carbon (High)", Family: "gemini", Tier: "pro", Effort: "high", Status: "active"},
		{ID: "claude-opus-5-5-high", DisplayName: "Claude Opus 5.5 (High)", Family: "anthropic", Tier: "pro", Effort: "high", Status: "active"},
		{ID: "custom-other-pro-high", DisplayName: "Custom Other Pro (High)", Family: "other", Tier: "pro", Effort: "high", Status: "active"},
	}

	// 1. Expired exhausted cat.Pools on !crossFamily role ("builder"):
	//    - With AgyPath == "", expired pools are dropped (nil) -> succeeds without ErrAllPoolsExhausted.
	expiredExhausted := Catalog{
		UpdatedAt:      now.Format(time.RFC3339),
		QuotaUpdatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339),
		Entries:        entries,
		Pools: []Pool{
			{Name: "Gemini Models", Five: 0, Week: 0},
			{Name: "Claude and GPT models", Five: 0, Week: 0},
		},
	}
	pickNoAgy, liveNoAgy, err := PickRoleFromCatalogForHome(ctx, expiredExhausted, PickRoleOptions{AgyPath: ""}, "builder", "pro")
	if err != nil || pickNoAgy.Model != "gemini-4-carbon-high" || len(liveNoAgy) != 0 {
		t.Fatalf("expired !crossFamily without agy applied stale pools: pick=%+v live=%+v err=%v", pickNoAgy, liveNoAgy, err)
	}
	//    - With AgyPath != "", expired pools trigger live usage probe and return livePools.
	probeCalls := 0
	UsageProbeFunc = func(context.Context, string) (string, error) {
		probeCalls++
		return "Gemini Models\tWeekly Limit Remaining\t80%\tx\n" +
			"Gemini Models\tFive Hour Limit Remaining\t90%\tx\n" +
			"Claude and GPT models\tWeekly Limit Remaining\t60%\tx\n" +
			"Claude and GPT models\tFive Hour Limit Remaining\t70%\tx\n", nil
	}
	pickWithAgy, liveWithAgy, err := PickRoleFromCatalogForHome(ctx, expiredExhausted, PickRoleOptions{AgyPath: "agy"}, "builder", "pro")
	if err != nil || pickWithAgy.Model != "gemini-4-carbon-high" || probeCalls != 1 || len(liveWithAgy) != 2 {
		t.Fatalf("expired !crossFamily with agy did not probe live pools: pick=%+v live=%+v calls=%d err=%v", pickWithAgy, liveWithAgy, probeCalls, err)
	}

	// 2. Cross-family fallback re-pick: initial gem is gemini-4-carbon-high, activePools exhausts Gemini
	//    (and Claude is low) while Other Models is healthy -> re-picks custom-other-pro-high and updates reason & fb.
	repickCat := Catalog{
		UpdatedAt:      now.Format(time.RFC3339),
		QuotaUpdatedAt: now.Add(-time.Minute).Format(time.RFC3339),
		Entries:        entries,
		Pools: []Pool{
			{Name: "Gemini Models", Five: 0, Week: 0},
			{Name: "Claude Models", Five: 10, Week: 10},
			{Name: "OpenAI Models", Five: 90, Week: 90},
		},
	}
	pickRepick, _, err := PickRoleFromCatalogForHome(ctx, repickCat, PickRoleOptions{AgyPath: "agy"}, "judge", "pro")
	if err != nil || pickRepick.Model != "custom-other-pro-high" {
		t.Fatalf("cross-family repick: pick=%+v err=%v, want custom-other-pro-high", pickRepick, err)
	}
	if !strings.Contains(pickRepick.Reason, "custom-other-pro-high") || strings.Contains(pickRepick.Reason, "gemini-4-carbon-high") {
		t.Errorf("cross-family repick Reason named stale model: %q", pickRepick.Reason)
	}
	if !strings.Contains(pickRepick.FallbackReason, "custom-other-pro-high") || strings.Contains(pickRepick.FallbackReason, "gemini-4-carbon-high") {
		t.Errorf("cross-family repick FallbackReason named stale model: %q", pickRepick.FallbackReason)
	}

	// 3. Cross-family anthropic caller with expired exhausted cat.Pools + unparseable live /usage output:
	//    must NOT fall back to expired cat.Pools (activePools must be cleared to nil).
	UsageProbeFunc = func(context.Context, string) (string, error) {
		return "garbled unparseable /usage output\n", nil
	}
	pickUnparseable, liveUnparseable, err := PickRoleFromCatalogForHome(
		ctx, expiredExhausted,
		PickRoleOptions{AgyPath: "agy", CallerFamily: "anthropic"},
		"judge", "pro",
	)
	if err != nil || pickUnparseable.Model != "gemini-4-carbon-high" || len(liveUnparseable) != 0 {
		t.Fatalf("expired anthropic caller + unparseable /usage fell back to stale pools: pick=%+v live=%+v err=%v", pickUnparseable, liveUnparseable, err)
	}
}
