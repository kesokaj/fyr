package models

import (
	"context"
	"math"
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
	}{{80, 0}, {50, 0}, {35, 0.075}, {20, 0.15}, {19, 0.50}, {0, 0.50}, {DisabledWindow, 0}} {
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
	wantUCB := min(0.25, 0.15*math.Sqrt(math.Log(10.0+math.E)/7.0))
	if math.Abs(sc.PosteriorMean-wantPost) > 1e-3 || math.Abs(sc.ExploreBonus-wantUCB) > 1e-3 || math.Abs(sc.HeadroomPenalty-0.075) > 1e-3 || sc.HeadroomPct != 35 {
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
