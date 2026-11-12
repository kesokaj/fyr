package work

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestModelCatalogRefreshAndRoute(t *testing.T) {
	ctx := context.Background()
	oldUsage := usageProbeFunc
	oldList := listModelsFunc
	t.Cleanup(func() {
		usageProbeFunc = oldUsage
		listModelsFunc = oldList
	})

	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	listFile := filepath.Join(tmp, "models.txt")
	failFlag := filepath.Join(tmp, "fail")
	agyScript := filepath.Join(tmp, "agy")

	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"models\" ]; then\n" +
		"  [ -e \"" + failFlag + "\" ] && { echo 'network down' >&2; exit 1; }\n" +
		"  cat \"" + listFile + "\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(agyScript, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	// Seed work/*/status.json evidence for claude-sonnet-5-5-high (3 work passes) and claude-sonnet-5-6-high (1/3 pass).
	waveDir := filepath.Join(home, harness.StateDir, "work", "2026-11-10-120000-1")
	if err := os.MkdirAll(waveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	statusJSON := `{
		"stamp": "2026-11-10-120000-1",
		"workers": [
			{"name": "w1", "state": "PASS", "model": "claude-sonnet-5-5-high", "agy_s": 20},
			{"name": "w2", "state": "PASS", "model": "claude-sonnet-5-5-high", "agy_s": 30},
			{"name": "w3", "state": "PASS", "model": "claude-sonnet-5-5-high", "agy_s": 25},
			{"name": "w4", "state": "PASS", "model": "claude-sonnet-5-6-high", "agy_s": 15},
			{"name": "w5", "state": "FAIL", "model": "claude-sonnet-5-6-high", "agy_s": 20},
			{"name": "w6", "state": "FAIL", "model": "claude-sonnet-5-6-high", "agy_s": 25}
		]
	}`
	if err := os.WriteFile(filepath.Join(waveDir, "status.json"), []byte(statusJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	// Extra eval evidence pushes both Sonnet candidates to >= 5 total runs:
	// 5-5-high: 3/3 work + 3/3 eval = 6/6 (100%), 5-6-high: 1/3 work + 1/3 eval = 2/6 (33%).
	extra := map[string]ModelEvidence{
		"claude-sonnet-5-5-high": {EvalRuns: 3, EvalPass: 3},
		"claude-sonnet-5-6-high": {EvalRuns: 3, EvalPass: 1},
	}

	// 1. Initial refresh: strips spinner \r and ANSI CSI codes, parses tabs and 2+ spaces.
	initialOut := "Fetching available models...\r\x1b[32mgemini-4-carbon-high\x1b[0m\tGemini 4 Carbon (High)\n" +
		"gemini-4-argon-high  Gemini 4 Argon (High)\n" +
		"gemini-3.8-flash-high\tGemini 3.8 Flash (High)\n" +
		"claude-opus-5-5-high    Claude Opus 5.5 (High)\n" +
		"claude-sonnet-5-5-high\tClaude Sonnet 5.5 (High)\n" +
		"claude-sonnet-5-6-high\tClaude Sonnet 5.6 (High)\n" +
		"gpt-oss-120b-medium\tGPT-OSS 120B (Medium)\n"
	if err := os.WriteFile(listFile, []byte(initialOut), 0o600); err != nil {
		t.Fatal(err)
	}

	day1 := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	cat1, err := refreshCatalogAt(ctx, home, agyScript, extra, day1)
	if err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	if len(cat1.Entries) != 7 {
		t.Fatalf("got %d entries, want 7: %+v", len(cat1.Entries), cat1.Entries)
	}
	for _, e := range cat1.Entries {
		if e.Status != "active" || e.FirstSeen != "2026-11-10" || e.LastSeen != "2026-11-10" {
			t.Errorf("initial entry %s: status=%q first=%q last=%q", e.ID, e.Status, e.FirstSeen, e.LastSeen)
		}
	}

	// Verify 0600 file mode on models.json.
	info, err := os.Stat(CatalogPath(home))
	if err != nil {
		t.Fatalf("stat models.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("models.json perm = %o, want 0600", perm)
	}

	// Builder prefers Carbon; judge pro prefers Opus; judge flash uses >=5 run pass rate (5-5 over newer 5-6); mechanical prefers Gemini flash.
	if got := cat1.Picks["builder:pro"].Model; got != "gemini-4-carbon-high" {
		t.Errorf("builder:pro = %q, want gemini-4-carbon-high", got)
	}
	if got := cat1.Picks["judge:pro"].Model; got != "claude-opus-5-5-high" {
		t.Errorf("judge:pro = %q, want claude-opus-5-5-high", got)
	}
	if got := cat1.Picks["judge:flash"].Model; got != "claude-sonnet-5-5-high" {
		t.Errorf("judge:flash (evidence override) = %q, want claude-sonnet-5-5-high", got)
	}
	if got := cat1.Picks["mechanical:flash"].Model; got != "gemini-3.8-flash-high" {
		t.Errorf("mechanical:flash = %q, want gemini-3.8-flash-high", got)
	}

	// Healthy quota: ResolveRoleModel returns listed Anthropic ID for judge.
	usageProbeFunc = func(ctx context.Context, agy string) (string, error) {
		return "Gemini\tWeekly Limit Remaining\t80%\tx\nGemini\tFive Hour Limit Remaining\t90%\tx\n" +
			"Claude\tWeekly Limit Remaining\t50%\tx\nClaude\tFive Hour Limit Remaining\t70%\tx\n", nil
	}
	jModel, jNote, err := ResolveRoleModel(ctx, agyScript, "judge", "pro", "")
	if err != nil || jModel != "claude-opus-5-5-high" || !strings.Contains(jNote, "Anthropic") {
		t.Fatalf("ResolveRoleModel judge healthy: m=%q note=%q err=%v", jModel, jNote, err)
	}

	// Low Anthropic quota: ResolveRoleModel falls back to listed Gemini Carbon ID.
	usageProbeFunc = func(ctx context.Context, agy string) (string, error) {
		return "Claude\tWeekly Limit Remaining\t12%\tx\nClaude\tFive Hour Limit Remaining\t80%\tx\n", nil
	}
	fbModel, fbNote, err := ResolveRoleModel(ctx, agyScript, "judge", "pro", "")
	if err != nil || fbModel != "gemini-4-carbon-high" || !strings.Contains(fbNote, "fallback to Gemini") {
		t.Fatalf("ResolveRoleModel judge low quota: m=%q note=%q err=%v", fbModel, fbNote, err)
	}

	// 2. Second refresh on day 2: remove claude-opus-5-5-high and all Anthropic models, add gemini-4-carbon-medium.
	secondOut := "gemini-4-carbon-high\tGemini 4 Carbon (High)\n" +
		"gemini-4-carbon-medium\tGemini 4 Carbon (Medium)\n" +
		"gemini-4-argon-high\tGemini 4 Argon (High)\n" +
		"gemini-3.8-flash-high\tGemini 3.8 Flash (High)\n" +
		"gpt-oss-120b-medium\tGPT-OSS 120B (Medium)\n"
	if err := os.WriteFile(listFile, []byte(secondOut), 0o600); err != nil {
		t.Fatal(err)
	}
	listedNow := []string{"gemini-4-carbon-high", "gemini-4-carbon-medium", "gemini-4-argon-high", "gemini-3.8-flash-high", "gpt-oss-120b-medium"}

	day2 := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	cat2, err := refreshCatalogAt(ctx, home, agyScript, extra, day2)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	statusByID := map[string]CatalogEntry{}
	for _, e := range cat2.Entries {
		statusByID[e.ID] = e
	}
	if e := statusByID["gemini-4-carbon-medium"]; e.Status != "new" || e.FirstSeen != "2026-11-11" {
		t.Errorf("added model: %+v, want status=new first_seen=2026-11-11", e)
	}
	if e := statusByID["claude-opus-5-5-high"]; e.Status != "gone" || e.FirstSeen != "2026-11-10" || e.LastSeen != "2026-11-10" {
		t.Errorf("removed model: %+v, want status=gone preserving 2026-11-10", e)
	}
	if e := statusByID["gemini-4-carbon-high"]; e.Status != "active" || e.FirstSeen != "2026-11-10" || e.LastSeen != "2026-11-11" {
		t.Errorf("kept model: %+v, want status=active first=2026-11-10 last=2026-11-11", e)
	}

	jp := cat2.Picks["judge:pro"]
	if !slices.Contains(listedNow, jp.Model) {
		t.Errorf("judge:pro picked unlisted model %q; listed=%v", jp.Model, listedNow)
	}
	if jp.FallbackReason == "" || !strings.Contains(jp.FallbackReason, "claude-opus-5-5-high") {
		t.Errorf("judge:pro FallbackReason = %q, want mention of gone claude-opus-5-5-high", jp.FallbackReason)
	}

	// ResolveRoleModel returns only currently listed IDs even when quota is healthy.
	usageProbeFunc = func(ctx context.Context, agy string) (string, error) {
		return "Claude\tWeekly Limit Remaining\t80%\tx\nClaude\tFive Hour Limit Remaining\t80%\tx\n", nil
	}
	for _, role := range []string{"builder", "judge", "reviewer", "verifier", "mechanical"} {
		gotID, _, err := ResolveRoleModel(ctx, agyScript, role, "pro", "")
		if err != nil {
			t.Fatalf("ResolveRoleModel(%s): %v", role, err)
		}
		if !slices.Contains(listedNow, gotID) {
			t.Errorf("ResolveRoleModel(%s) returned unlisted ID %q (listed=%v)", role, gotID, listedNow)
		}
	}

	// 3. Failed refresh keeps existing models.json intact.
	savedBefore, err := os.ReadFile(CatalogPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(failFlag, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshCatalog(ctx, home, agyScript, nil); err == nil {
		t.Fatal("RefreshCatalog succeeded when agy models failed; want error")
	}
	savedAfter, err := os.ReadFile(CatalogPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if string(savedAfter) != string(savedBefore) {
		t.Errorf("failed RefreshCatalog modified existing models.json")
	}
	var decoded Catalog
	if err := json.Unmarshal(savedAfter, &decoded); err != nil || len(decoded.Entries) != len(cat2.Entries) {
		t.Errorf("models.json corrupted after failed refresh: %v", err)
	}
}

func TestCatalogJoinsBareEvalEvidenceOntoHighestEffort(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	parsed := ParseModelsOutput(
		"gemini-4-argon-low\tGemini 4 Argon (Low)\n" +
			"gemini-4-argon-medium\tGemini 4 Argon (Medium)\n" +
			"gemini-4-argon-high\tGemini 4 Argon (High)\n" +
			"gpt-oss-120b-low\tGPT-OSS 120B (Low)\n" +
			"gpt-oss-120b-medium\tGPT-OSS 120B (Medium)\n" +
			"claude-opus-5-5-medium\tClaude Opus 5.5 (Medium)\n" +
			"claude-opus-5-5-high\tClaude Opus 5.5 (High)\n" +
			"claude-sonnet-4-6\tClaude Sonnet 4.6\n" +
			"claude-sonnet-5-5-high\tClaude Sonnet 5.5 (High)\n" +
			"gemini-3.8-flash-medium\tGemini 3.8 Flash (Medium)\n",
	)
	prev := &Catalog{
		Entries: []CatalogEntry{
			{ID: "gemini-3.8-flash-high", Family: "gemini", Tier: "flash", Effort: "high", Status: "active", FirstSeen: "2026-11-10", LastSeen: "2026-11-10"},
		},
	}
	evidence := map[string]ModelEvidence{
		// Bare metrics.db eval key joins onto highest-effort -high despite -low/-medium appearing first.
		"gemini-4-argon": {EvalRuns: 34, EvalPass: 31},
		// Bare key joins onto -medium when no -high exists in the catalog.
		"gpt-oss-120b": {EvalRuns: 6, EvalPass: 5},
		// Exact match + bare key add without double-counting the exact match.
		"claude-opus-5-5-high": {WorkRuns: 3, WorkPass: 3, WorkMeanSeconds: 42, EvalRuns: 2, EvalPass: 2},
		"claude-opus-5-5":      {EvalRuns: 5, EvalPass: 4},
		// Exact match on an already-bare active catalog entry is counted once, never twice.
		"claude-sonnet-4-6": {EvalRuns: 8, EvalPass: 7},
		// Effort-suffixed unlisted key is not bare and must not bleed onto a sibling effort.
		"claude-sonnet-5-5-low": {EvalRuns: 9, EvalPass: 9},
		// When -high is gone and -medium is active, bare key joins onto active -medium.
		"gemini-3.8-flash": {EvalRuns: 7, EvalPass: 6},
	}

	cat := BuildCatalog(now, parsed, prev, evidence)
	byID := map[string]CatalogEntry{}
	for _, e := range cat.Entries {
		byID[e.ID] = e
	}

	if got := byID["gemini-4-argon-high"].Evidence; got.EvalRuns != 34 || got.EvalPass != 31 {
		t.Errorf("gemini-4-argon-high evidence = %+v, want EvalRuns=34 EvalPass=31", got)
	}
	if got := byID["gemini-4-argon-medium"].Evidence; got != (ModelEvidence{}) {
		t.Errorf("gemini-4-argon-medium evidence = %+v, want zero", got)
	}
	if got := byID["gemini-4-argon-low"].Evidence; got != (ModelEvidence{}) {
		t.Errorf("gemini-4-argon-low evidence = %+v, want zero", got)
	}
	if got := byID["gpt-oss-120b-medium"].Evidence; got.EvalRuns != 6 || got.EvalPass != 5 {
		t.Errorf("gpt-oss-120b-medium evidence = %+v, want EvalRuns=6 EvalPass=5", got)
	}
	if got := byID["gpt-oss-120b-low"].Evidence; got != (ModelEvidence{}) {
		t.Errorf("gpt-oss-120b-low evidence = %+v, want zero", got)
	}
	wantOpus := ModelEvidence{WorkRuns: 3, WorkPass: 3, WorkMeanSeconds: 42, EvalRuns: 7, EvalPass: 6}
	if got := byID["claude-opus-5-5-high"].Evidence; got != wantOpus {
		t.Errorf("claude-opus-5-5-high evidence = %+v, want %+v", got, wantOpus)
	}
	if got := byID["claude-opus-5-5-medium"].Evidence; got != (ModelEvidence{}) {
		t.Errorf("claude-opus-5-5-medium evidence = %+v, want zero", got)
	}
	if got := byID["claude-sonnet-4-6"].Evidence; got.EvalRuns != 8 || got.EvalPass != 7 {
		t.Errorf("claude-sonnet-4-6 exact bare entry = %+v, want EvalRuns=8 EvalPass=7 (no double count)", got)
	}
	if got := byID["claude-sonnet-5-5-high"].Evidence; got != (ModelEvidence{}) {
		t.Errorf("claude-sonnet-5-5-high evidence = %+v, want zero (suffixed -low must not join)", got)
	}
	if got := byID["gemini-3.8-flash-medium"].Evidence; got.EvalRuns != 7 || got.EvalPass != 6 {
		t.Errorf("gemini-3.8-flash-medium active fallback = %+v, want EvalRuns=7 EvalPass=6", got)
	}
	if got := byID["gemini-3.8-flash-high"].Evidence; got != (ModelEvidence{}) {
		t.Errorf("gemini-3.8-flash-high gone entry = %+v, want zero", got)
	}
}

func TestCatalogEvalPassDoesNotMaskFailingWorkPass(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	parsed := ParseModelsOutput(
		"gemini-4-carbon-high\tGemini 4 Carbon (High)\n" +
			"gemini-4-carbon-medium\tGemini 4 Carbon (Medium)\n",
	)

	// 1. Failing WorkPass (7/16 = 43.75% < 50%, WorkRuns >= 5) must NOT be masked by 31/35 bare EvalPass
	//    against an untested peer (0/0) or against a >=50% evidence peer (3/5 work pass).
	catUntestedPeer := BuildCatalog(now, parsed, nil, map[string]ModelEvidence{
		"gemini-4-carbon-high": {WorkRuns: 16, WorkPass: 7, WorkMeanSeconds: 90},
		"gemini-4-carbon":      {EvalRuns: 35, EvalPass: 31},
	})
	byID := map[string]CatalogEntry{}
	for _, e := range catUntestedPeer.Entries {
		byID[e.ID] = e
	}
	if got := byID["gemini-4-carbon-high"].Evidence; got.WorkRuns != 16 || got.WorkPass != 7 || got.EvalRuns != 35 || got.EvalPass != 31 {
		t.Fatalf("joined carbon-high evidence = %+v, want Work=7/16 Eval=31/35", got)
	}
	if got := catUntestedPeer.Picks["builder:pro"].Model; got != "gemini-4-carbon-medium" {
		t.Errorf("builder:pro with 7/16 work + 31/35 eval = %q, want gemini-4-carbon-medium", got)
	}

	catPassingPeer := BuildCatalog(now, parsed, nil, map[string]ModelEvidence{
		"gemini-4-carbon-high":   {WorkRuns: 6, WorkPass: 1, WorkMeanSeconds: 50},
		"gemini-4-carbon":        {EvalRuns: 30, EvalPass: 30}, // raw combined 31/36 = 86.1% > 60%
		"gemini-4-carbon-medium": {WorkRuns: 5, WorkPass: 3, WorkMeanSeconds: 70},
	})
	if got := catPassingPeer.Picks["builder:pro"].Model; got != "gemini-4-carbon-medium" {
		t.Errorf("builder:pro against 3/5 work peer = %q, want gemini-4-carbon-medium", got)
	}

	// 2. Exact 50% WorkPass (3/6) is NOT below 50%, so joined EvalPass keeps -high selected.
	catAtHalf := BuildCatalog(now, parsed, nil, map[string]ModelEvidence{
		"gemini-4-carbon-high": {WorkRuns: 6, WorkPass: 3},
		"gemini-4-carbon":      {EvalRuns: 10, EvalPass: 9},
	})
	if got := catAtHalf.Picks["builder:pro"].Model; got != "gemini-4-carbon-high" {
		t.Errorf("builder:pro at 3/6 (50%%) work + 9/10 eval = %q, want gemini-4-carbon-high", got)
	}

	// 3. WorkRuns < 5 (1/4) does NOT trigger the WorkRuns >= 5 guard; combined 10/14 >= 50% picks -high.
	catUnderFive := BuildCatalog(now, parsed, nil, map[string]ModelEvidence{
		"gemini-4-carbon-high": {WorkRuns: 4, WorkPass: 1},
		"gemini-4-carbon":      {EvalRuns: 10, EvalPass: 9},
	})
	if got := catUnderFive.Picks["builder:pro"].Model; got != "gemini-4-carbon-high" {
		t.Errorf("builder:pro at 1/4 (<5) work + 9/10 eval = %q, want gemini-4-carbon-high", got)
	}
}

func TestCatalogEvidenceEdgeCases(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	parsed := ParseModelsOutput(
		"claude-sonnet-5-5-high\tClaude Sonnet 5.5 (High)\n" +
			"gemini-4-carbon-high\tGemini 4 Carbon (High)\n",
	)
	prev := &Catalog{
		Entries: []CatalogEntry{
			{ID: "claude-opus-5-4-low", Family: "anthropic", Tier: "pro", Effort: "low", Status: "active", FirstSeen: "2026-11-01", LastSeen: "2026-11-01"},
			{ID: "claude-opus-5-4-high", Family: "anthropic", Tier: "pro", Effort: "high", Status: "active", FirstSeen: "2026-11-01", LastSeen: "2026-11-01"},
		},
	}
	cat := BuildCatalog(now, parsed, prev, map[string]ModelEvidence{
		"claude-sonnet-5.5":      {EvalRuns: 5, EvalPass: 5},
		"models/gemini-4-carbon": {EvalRuns: 6, EvalPass: 5},
		"claude-opus-5-4":        {EvalRuns: 4, EvalPass: 3},
		"unlisted-model":         {EvalRuns: 9, EvalPass: 9},
	})
	byID := map[string]CatalogEntry{}
	for _, e := range cat.Entries {
		byID[e.ID] = e
	}
	if got := byID["claude-sonnet-5-5-high"].Evidence; got.EvalRuns != 5 || got.EvalPass != 5 {
		t.Errorf("dot-folded bare stem = %+v, want 5/5", got)
	}
	if got := byID["gemini-4-carbon-high"].Evidence; got.EvalRuns != 6 || got.EvalPass != 5 {
		t.Errorf("models/ prefixed bare stem = %+v, want 5/6", got)
	}
	if got := byID["claude-opus-5-4-high"].Evidence; got.EvalRuns != 4 || got.EvalPass != 3 {
		t.Errorf("gone highest-effort fallback = %+v, want 3/4", got)
	}
	if got := byID["claude-opus-5-4-low"].Evidence; got != (ModelEvidence{}) {
		t.Errorf("gone lower-effort = %+v, want zero", got)
	}
	if _, ok := byID["unlisted-model"]; ok {
		t.Errorf("unlisted bare key created a phantom catalog entry")
	}
}
