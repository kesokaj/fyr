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
