package models

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestScoreCandidateMathAndHeadroomCurve(t *testing.T) {
	for _, tc := range []struct {
		role, wantTier, candTier, effort string
		want                             float64
	}{
		{"builder", "pro", "pro", "xhigh", 0.67},
		{"builder", "pro", "pro", "high", 0.66},
		{"builder", "flash", "flash", "medium", 0.55},
		{"mechanical", "light", "light", "low", 0.44},
	} {
		if got := RoleBaselineForTier(tc.role, tc.wantTier, tc.candTier, tc.effort); math.Abs(got-tc.want) > 1e-6 {
			t.Errorf("RoleBaselineForTier(%s,%s,%s,%s) = %v, want %v", tc.role, tc.wantTier, tc.candTier, tc.effort, got, tc.want)
		}
	}
	if RoleBaselineForTier("mechanical", "flash", "pro", "high") >= RoleBaselineForTier("mechanical", "flash", "light", "medium") {
		t.Errorf("mechanical pro baseline must rank below light")
	}
	for _, tc := range []struct {
		pct  int
		want float64
	}{{80, 0}, {50, 0}, {35, 0.09}, {20, 0.36}, {19, 0.3844}, {0, 1.0}, {DisabledWindow, 0}} {
		if got := HeadroomPenaltyForPct(tc.pct); math.Abs(got-tc.want) > 1e-6 {
			t.Errorf("HeadroomPenaltyForPct(%d) = %v, want %v", tc.pct, got, tc.want)
		}
	}
	if pct, ok := PoolHeadroomPct(Pool{Name: "Gemini", Five: DisabledWindow, Week: 35}); !ok || pct != 35 {
		t.Errorf("PoolHeadroomPct(disabled, 35) = (%d, %v), want (35, true)", pct, ok)
	}
	neon := CatalogEntry{ID: "gemini-4-neon-high", Family: "gemini", Tier: "pro", Effort: "high", Status: "active", Evidence: ModelEvidence{DecayedRuns: 6.0, DecayedSuccesses: 5.4}}
	sc := ScoreCandidate(neon, "builder", "pro", 10.0, []Pool{{Name: "Gemini Models", Five: 35, Week: 80}})
	wantPost := (5.4 + 4.0*0.66) / 10.0
	wantUCB := min(UCBExploreCap, UCBExploreCoeff*math.Sqrt(math.Log(10.0+math.E)/7.0))
	if math.Abs(sc.PosteriorMean-wantPost) > 1e-3 || math.Abs(sc.ExploreBonus-wantUCB) > 1e-3 || math.Abs(sc.HeadroomPenalty-0.09) > 1e-3 || sc.HeadroomPct != 35 {
		t.Fatalf("ScoreCandidate mismatch: got %+v", sc)
	}
}

func TestPhase1EvidenceMergeAndUnseenModelsCompete(t *testing.T) {
	parsed := ParseModelsOutput("gemini-4-neon-high\tGemini 4 Neon (High)\ngemini-4-neon-medium\tGemini 4 Neon (Medium)\ngemini-5-nova-medium\tGemini 5 Nova (Medium)\nclaude-6-titan-high\tClaude 6 Titan (High)\n")
	ev := map[string]ModelEvidence{
		"gemini-4-neon":        {EvalRuns: 2, EvalPass: 2, TrialRuns: 2, TrialPass: 2, JudgeRuns: 3, JudgePass: 3, RatingRuns: 2, RatingScoreSum: 1.8, DecayedRuns: 5.5, DecayedSuccesses: 5.3},
		"gemini-4-neon-high":   {WorkRuns: 1, WorkPass: 1, WorkMeanSeconds: 32, TrialRuns: 1, TrialPass: 1, EvalRuns: 1, EvalPass: 1, DecayedRuns: 1.0, DecayedSuccesses: 1.0},
		"gemini-5-nova-medium": {TrialRuns: 6, TrialPass: 1, EvalRuns: 6, EvalPass: 1, JudgeRuns: 2, JudgePass: 0, DecayedRuns: 7.8, DecayedSuccesses: 1.0},
	}
	cat := BuildCatalog(time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC), parsed, nil, ev)
	byID := map[string]CatalogEntry{}
	for _, e := range cat.Entries {
		byID[e.ID] = e
	}
	gotNeon := byID["gemini-4-neon-high"].Evidence
	if gotNeon.TrialRuns != 3 || gotNeon.JudgeRuns != 3 || gotNeon.RatingRuns != 2 || math.Abs(gotNeon.DecayedRuns-6.5) > 1e-6 || math.Abs(gotNeon.DecayedSuccesses-6.3) > 1e-6 {
		t.Fatalf("merged neon-high evidence = %+v", gotNeon)
	}
	if byID["gemini-4-neon-medium"].Evidence != (ModelEvidence{}) {
		t.Errorf("neon-medium received bare-stem evidence: %+v", byID["gemini-4-neon-medium"].Evidence)
	}
	if got := cat.Picks["builder:pro"].Model; !strings.HasPrefix(got, "gemini-4-neon-") {
		t.Errorf("builder:pro picked %q, want neon over failing nova", got)
	}
	if got := cat.Picks["judge:pro"].Model; got != "claude-6-titan-high" {
		t.Errorf("judge:pro = %q, want claude-6-titan-high", got)
	}
	cat.Pools = []Pool{{Name: "Claude", Five: 25, Week: 60}}
	if fb, err := PickFromCatalog(cat, "judge", "pro"); err != nil || !strings.HasPrefix(fb.Model, "gemini-") || !strings.Contains(fb.FallbackReason, "fallback to Gemini") {
		t.Fatalf("PickFromCatalog low Claude 5h pool = %+v (err=%v)", fb, err)
	}
}

func TestVerifyBenchResponseAndRunBench(t *testing.T) {
	if args := benchProbeArgs("gemini-3.8-flash-high", BenchPrompt); slices.Contains(args, "--json-schema") {
		t.Errorf("gemini bench args unexpectedly included --json-schema: %v", args)
	}
	if args := benchProbeArgs("claude-sonnet-5-5-high", BenchPrompt); !slices.Contains(args, "--json-schema") {
		t.Errorf("claude bench args missing --json-schema: %v", args)
	}
	validJSON := `{"sum":44,"go_func":"func ClampScore(v int) int { if v < 0 { return 0 }; if v > 100 { return 100 }; return v }","ok":true}`
	for _, good := range []string{
		validJSON,
		`{"status":"SUCCESS","structuredOutput":` + validJSON + `}`,
		`{"status":"SUCCESS","response":"` + "```json\\n" + strings.ReplaceAll(validJSON, `"`, `\"`) + "\\n```" + `"}`,
	} {
		if why, ok := VerifyBenchResponse(good); !ok || why != "" {
			t.Fatalf("VerifyBenchResponse(%q) = (%q, %v), want ok", good, why, ok)
		}
	}
	for wantWhy, raw := range map[string]string{
		"empty_output": "", "invalid_json": "{bad",
		"wrong_sum":      `{"sum":42,"go_func":"func ClampScore(v int) int { return v }","ok":true}`,
		"not_ok":         `{"sum":44,"go_func":"func ClampScore(v int) int { return v }","ok":false}`,
		"invalid_go_ast": `{"sum":44,"go_func":"func Wrong(v string) int { return 0 }","ok":true}`,
		"tool_attempted": `{"status":"SUCCESS","deniedActions":[{"tool":"run_command"}]}`,
	} {
		if why, ok := VerifyBenchResponse(raw); ok || why != wantWhy {
			t.Errorf("VerifyBenchResponse(%q) = (%q, %v), want %q", raw, why, ok, wantWhy)
		}
	}
	oldRunner := BenchRunnerFunc
	t.Cleanup(func() { BenchRunnerFunc = oldRunner })
	BenchRunnerFunc = func(ctx context.Context, home, agyPath, modelID, prompt string, timeout time.Duration) (string, error) {
		if timeout != 45*time.Second {
			t.Errorf("timeout = %s, want 45s", timeout)
		}
		if strings.Contains(modelID, "nova") {
			return "", context.DeadlineExceeded
		}
		return validJSON, nil
	}
	cat := Catalog{Entries: []CatalogEntry{
		{ID: "gemini-pro-orchestrator", Family: "gemini", Tier: "pro", Status: "active", InteractiveOnly: true},
		{ID: "gemini-4-tested-high", Family: "gemini", Tier: "pro", Status: "active", Evidence: ModelEvidence{TrialRuns: 1, DecayedRuns: 1.0}},
		{ID: "gemini-4-gone-high", Family: "gemini", Tier: "pro", Status: "gone"},
		{ID: "gemini-4-neon-high", Family: "gemini", Tier: "pro", Effort: "high", Status: "new"},
		{ID: "gemini-5-nova-medium", Family: "gemini", Tier: "pro", Effort: "medium", Status: "new"},
		{ID: "claude-opus-5-5-high", Family: "anthropic", Tier: "pro", Effort: "high", Status: "active"},
		{ID: "gemini-3.8-flash-high", Family: "gemini", Tier: "flash", Effort: "high", Status: "active"},
	}}
	targets, err := SelectBenchTargets(cat, "", DefaultBenchMax)
	if err != nil || len(targets) != 3 {
		t.Fatalf("SelectBenchTargets: got %d, err=%v", len(targets), err)
	}
	if _, err := SelectBenchTargets(cat, "gemini-pro-orchestrator", 1); err == nil {
		t.Errorf("expected error on interactive orchestrator")
	}
	rep, err := RunBench(context.Background(), t.TempDir(), "", targets, func(BenchTrialRecord) error { return nil })
	if err != nil || rep.Probed != 3 || rep.Passed != 2 || rep.Failed != 1 {
		t.Fatalf("RunBench = %+v (err=%v)", rep, err)
	}
}

func TestV910ModelScoringFailoverChangelogAndBudgets(t *testing.T) {
	t.Run("TestV910Changelog", func(t *testing.T) {
		if MaxModelsOut != 64<<10 || MaxChangelogOut != 1<<20 || ChangelogTimeout != 5*time.Second {
			t.Fatalf("limits = (%d, %d, %s)", MaxModelsOut, MaxChangelogOut, ChangelogTimeout)
		}
		home := t.TempDir()
		t.Setenv("HOME", home)
		// 90 KiB changelog (> 64 KiB MaxModelsOut, < 1 MiB MaxChangelogOut) must succeed.
		med := filepath.Join(t.TempDir(), "agy-med")
		medBody := "#!/bin/sh\nprintf '1.9.0: Added gemini-4-neon (1M context); retiring gpt-oss-120b on 2026-11-25\\n'\nhead -c 90000 /dev/zero | tr '\\0' 'x'\nprintf '\\n'\n"
		if err := os.WriteFile(med, []byte(medBody), 0o755); err != nil {
			t.Fatal(err)
		}
		got := RunAgyChangelog(context.Background(), med)
		if len(got) < 90000 || !strings.Contains(got, "gemini-4-neon") {
			t.Fatalf("90 KiB changelog truncated or empty: len=%d", len(got))
		}
		// > 1 MiB changelog returns empty cleanly.
		huge := filepath.Join(t.TempDir(), "agy-huge")
		hugeBody := "#!/bin/sh\nhead -c 1100000 /dev/zero | tr '\\0' 'x'\n"
		if err := os.WriteFile(huge, []byte(hugeBody), 0o755); err != nil {
			t.Fatal(err)
		}
		if over := RunAgyChangelog(context.Background(), huge); over != "" {
			t.Fatalf(">1 MiB changelog = %d bytes, want empty", len(over))
		}

		// Per-clause retirement parsing on ';' and '. ' never mis-tags siblings.
		entries := []CatalogEntry{
			{ID: "gemini-4-neon-high", Family: "gemini", Tier: "pro", Status: "active"},
			{ID: "gpt-oss-120b-medium", Family: "other", Tier: "flash", Status: "active"},
			{ID: "gemini-4-carbon-high", Family: "gemini", Tier: "pro", Status: "active"},
			{ID: "legacy-alpha-v1", Family: "other", Tier: "pro", Status: "active"},
		}
		for i := range entries {
			ApplyModelProfile(&entries[i])
		}
		applyChangelogText(
			"2026-11-20: Added gemini-4-neon (1M context); retiring gpt-oss-120b on 2026-11-25\n"+
				"2026-11-21: Updated gemini-4-carbon with faster reasoning. Deprecated legacy-alpha-v1 on 2026-12-10.\n",
			entries,
		)
		if entries[0].RetiringOn != "" || entries[0].ContextWindow != 1_000_000 {
			t.Errorf("sibling gemini-4-neon mis-tagged or missing 1M context: %+v", entries[0])
		}
		if entries[1].RetiringOn != "2026-11-25" {
			t.Errorf("gpt-oss RetiringOn = %q, want 2026-11-25", entries[1].RetiringOn)
		}
		if entries[2].RetiringOn != "" {
			t.Errorf("sibling gemini-4-carbon mis-tagged retiring: %+v", entries[2])
		}
		if entries[3].RetiringOn != "2026-12-10" {
			t.Errorf("legacy-alpha-v1 RetiringOn = %q, want 2026-12-10", entries[3].RetiringOn)
		}
	})

	t.Run("TestV910ModelScoringFailover", func(t *testing.T) {
		if UCBExploreCoeff != 0.04 || UCBExploreCap != 0.06 || PriorStrength != 4.0 {
			t.Fatalf("scoring constants = (%v, %v, %v)", UCBExploreCoeff, UCBExploreCap, PriorStrength)
		}
		// 1/1 newcomer must never outrank 94/100 proven peer.
		proven := CatalogEntry{ID: "gemini-4-carbon-high", Family: "gemini", Tier: "pro", Effort: "high", Status: "active",
			Evidence: ModelEvidence{DecayedRuns: 100, DecayedSuccesses: 94, BuilderEvidence: RoleEvidence{Runs: 100, Pass: 94, DecayedRuns: 100, DecayedSuccesses: 94}}}
		newcomer := CatalogEntry{ID: "gemini-5-nova-high", Family: "gemini", Tier: "pro", Effort: "high", Status: "active",
			Evidence: ModelEvidence{DecayedRuns: 1, DecayedSuccesses: 1, BuilderEvidence: RoleEvidence{Runs: 1, Pass: 1, DecayedRuns: 1, DecayedSuccesses: 1}}}
		sp := ScoreCandidate(proven, "builder", "pro", 101, nil)
		sn := ScoreCandidate(newcomer, "builder", "pro", 101, nil)
		if sn.RankScore >= sp.RankScore {
			t.Fatalf("1/1 newcomer (%+v) outranked 94/100 proven peer (%+v)", sn, sp)
		}

		// Role-partitioned shrinkage: builder specialist vs judge specialist.
		buildSpec := CatalogEntry{ID: "gemini-4-alpha-high", Family: "gemini", Tier: "pro", Effort: "high", Status: "active",
			Evidence: ModelEvidence{
				DecayedRuns: 20, DecayedSuccesses: 10,
				BuilderEvidence: RoleEvidence{Runs: 10, Pass: 9, DecayedRuns: 10, DecayedSuccesses: 9},
				JudgeEvidence:   RoleEvidence{Runs: 10, Pass: 1, DecayedRuns: 10, DecayedSuccesses: 1},
			}}
		judgeSpec := CatalogEntry{ID: "gemini-4-beta-high", Family: "gemini", Tier: "pro", Effort: "high", Status: "active",
			Evidence: ModelEvidence{
				DecayedRuns: 20, DecayedSuccesses: 10,
				BuilderEvidence: RoleEvidence{Runs: 10, Pass: 1, DecayedRuns: 10, DecayedSuccesses: 1},
				JudgeEvidence:   RoleEvidence{Runs: 10, Pass: 9, DecayedRuns: 10, DecayedSuccesses: 9},
			}}
		if ScoreCandidate(buildSpec, "builder", "pro", 40, nil).RankScore <= ScoreCandidate(judgeSpec, "builder", "pro", 40, nil).RankScore {
			t.Errorf("builder specialist did not outrank judge specialist on builder role")
		}
		if ScoreCandidate(judgeSpec, "judge", "pro", 40, nil).RankScore <= ScoreCandidate(buildSpec, "judge", "pro", 40, nil).RankScore {
			t.Errorf("judge specialist did not outrank builder specialist on judge role")
		}

		// Bounded EfficiencyBonus (<= 0.08).
		fastEff := CatalogEntry{ID: "gemini-4-fast-high", Family: "gemini", Tier: "pro", Effort: "high", Status: "active",
			Evidence: ModelEvidence{DecayedRuns: 20, DecayedSuccesses: 18, MeanTokenEfficiency: 5.0, WorkMeanSeconds: 15}}
		slowHeavy := CatalogEntry{ID: "gemini-4-slow-high", Family: "gemini", Tier: "pro", Effort: "high", Status: "active",
			Evidence: ModelEvidence{DecayedRuns: 20, DecayedSuccesses: 18, MeanTokenEfficiency: 1.2, WorkMeanSeconds: 300}}
		sf := ScoreCandidate(fastEff, "builder", "pro", 40, nil)
		ss := ScoreCandidate(slowHeavy, "builder", "pro", 40, nil)
		if sf.EfficiencyBonus <= 0 || sf.EfficiencyBonus > 0.08 || sf.RankScore <= ss.RankScore {
			t.Fatalf("efficiency bonus mismatch: fast=%+v slow=%+v", sf, ss)
		}

		// Smooth convex HeadroomPenaltyForPct: ((50-pct)/50)^2.
		for _, tc := range []struct {
			pct  int
			want float64
		}{{80, 0}, {50, 0}, {35, 0.09}, {25, 0.25}, {20, 0.36}, {5, 0.81}, {0, 1.0}, {DisabledWindow, 0}} {
			if got := HeadroomPenaltyForPct(tc.pct); math.Abs(got-tc.want) > 1e-6 {
				t.Errorf("HeadroomPenaltyForPct(%d) = %v, want %v", tc.pct, got, tc.want)
			}
		}

		// Symmetric failover & ErrAllPoolsExhausted & caller-family & all roles.
		entries := []CatalogEntry{
			{ID: "gemini-4-carbon-high", Family: "gemini", Tier: "pro", Effort: "high", Status: "active"},
			{ID: "gemini-3.8-flash-high", Family: "gemini", Tier: "flash", Effort: "high", Status: "active"},
			{ID: "claude-opus-5-5-high", Family: "anthropic", Tier: "pro", Effort: "high", Status: "active"},
			{ID: "claude-sonnet-5-5-high", Family: "anthropic", Tier: "flash", Effort: "high", Status: "active"},
		}
		// 0% Gemini -> builder fails over symmetrically to healthy Claude.
		bFail, err := PickFromEntriesWithPools(entries, nil, []Pool{
			{Name: "Gemini Models", Five: 0, Week: 50},
			{Name: "Claude and GPT models", Five: 80, Week: 75},
		}, "builder", "pro")
		if err != nil || bFail.Model != "claude-opus-5-5-high" || bFail.FallbackReason == "" {
			t.Fatalf("builder 0%% Gemini failover = (%+v, %v), want claude-opus-5-5-high", bFail, err)
		}
		// All pools <= 0% -> explicit ErrAllPoolsExhausted.
		if _, err := PickFromEntriesWithPools(entries, nil, []Pool{
			{Name: "Gemini Models", Five: 0, Week: 0},
			{Name: "Claude and GPT models", Five: 0, Week: 50},
		}, "builder", "pro"); !errors.Is(err, ErrAllPoolsExhausted) {
			t.Fatalf("all pools exhausted err = %v, want ErrAllPoolsExhausted", err)
		}
		// --caller-family anthropic picks Gemini for cross-family roles when healthy.
		healthy := []Pool{{Name: "Gemini Models", Five: 80, Week: 80}, {Name: "Claude and GPT models", Five: 80, Week: 80}}
		secPick, err := PickFromEntriesWithPoolsAndCaller(entries, nil, healthy, "security", "pro", "anthropic")
		if err != nil || secPick.Model != "gemini-4-carbon-high" {
			t.Fatalf("security caller=anthropic = (%+v, %v), want gemini-4-carbon-high", secPick, err)
		}
		// All 7+ agent roles normalized cleanly; unknown role & caller-family rejected.
		for _, role := range []string{"builder", "worker", "planner", "judge", "reviewer", "verifier", "security", "explorer", "documentation", "dreamer", "mechanical"} {
			if _, _, err := NormalizeRoleTier(role, ""); err != nil {
				t.Errorf("NormalizeRoleTier(%q) unexpected error: %v", role, err)
			}
		}
		if _, _, err := NormalizeRoleTier("bogus-role", ""); err == nil {
			t.Errorf("NormalizeRoleTier(bogus-role) succeeded, want error")
		}
		if _, err := NormalizeCallerFamily("bogus-fam"); err == nil {
			t.Errorf("NormalizeCallerFamily(bogus-fam) succeeded, want error")
		}
		// Quota-adaptive TTL: 2m when min pool < 30%, 10m otherwise.
		if got := AdaptivePoolQuotaTTL([]Pool{{Name: "Gemini", Five: 80, Week: 80}, {Name: "Claude", Five: 25, Week: 60}}); got != 2*time.Minute {
			t.Errorf("AdaptivePoolQuotaTTL(25%%) = %s, want 2m", got)
		}
		if got := AdaptivePoolQuotaTTL(healthy); got != 10*time.Minute {
			t.Errorf("AdaptivePoolQuotaTTL(80%%) = %s, want 10m", got)
		}
	})

	t.Run("TestV910ContextBudget", func(t *testing.T) {
		e := CatalogEntry{ID: "gemini-4-neon-high", DisplayName: "Gemini 4 Neon (High)"}
		ApplyModelProfile(&e)
		if e.ContextWindow != 1_000_000 {
			t.Errorf("gemini-4 ContextWindow = %d, want 1000000", e.ContextWindow)
		}
		orch := CatalogEntry{ID: "gemini-pro-orchestrator", DisplayName: "Orchestrator"}
		ApplyModelProfile(&orch)
		if orch.ContextWindow != 1_000_000 {
			t.Errorf("orchestrator ContextWindow = %d, want 1000000", orch.ContextWindow)
		}
		hint := CatalogEntry{ID: "claude-opus-5-5-high", DisplayName: "Claude Opus 5.5 (200K context)"}
		ApplyModelProfile(&hint)
		if hint.ContextWindow != 200_000 {
			t.Errorf("hinted ContextWindow = %d, want 200000", hint.ContextWindow)
		}
		// Parameter size 120B must NOT be misparsed as context window.
		oss := CatalogEntry{ID: "gpt-oss-120b-medium", DisplayName: "GPT-OSS 120B (Medium)"}
		ApplyModelProfile(&oss)
		if oss.ContextWindow != 128_000 {
			t.Errorf("gpt-oss ContextWindow = %d, want 128000", oss.ContextWindow)
		}
		// Calibrated knee capped at empirical degradation onset (180k-300k).
		if k := CalibratedContextKnee(1_000_000); k != 300_000 {
			t.Errorf("CalibratedContextKnee(1M) = %d, want 300000", k)
		}
		if k := CalibratedContextKnee(2_000_000); k != 300_000 {
			t.Errorf("CalibratedContextKnee(2M) = %d, want 300000", k)
		}
		if k := CalibratedContextKnee(300_000); k != 180_000 {
			t.Errorf("CalibratedContextKnee(300k) = %d, want 180000", k)
		}
		if k := CalibratedContextKnee(200_000); k != 120_000 {
			t.Errorf("CalibratedContextKnee(200k) = %d, want 120000", k)
		}
	})
}
