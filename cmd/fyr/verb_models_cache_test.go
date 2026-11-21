package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/models"
)

func TestVerbModelsPickQuotaCacheTTLAndRefresh(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	bin := filepath.Join(tmp, "bin")
	callsLog := filepath.Join(tmp, "agy_calls.log")
	usageFile := filepath.Join(tmp, "usage.txt")
	failUsage := filepath.Join(tmp, "fail_usage")
	for _, d := range []string{home, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	agyScript := "#!/bin/sh\n" +
		"echo \"$1 $2\" >> \"" + callsLog + "\"\n" +
		"if [ \"$1\" = \"models\" ]; then\n" +
		"  printf 'gemini-4-carbon-high\\tGemini 4 Carbon (High)\\nclaude-opus-5-5-high\\tClaude Opus 5.5 (High)\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"changelog\" ]; then\n" +
		"  printf '1.6.0:\\n· Added Gemini 4 Carbon\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"-p\" ] && [ \"$2\" = \"/usage\" ]; then\n" +
		"  [ -e \"" + failUsage + "\" ] && { echo 'usage failed' >&2; exit 1; }\n" +
		"  cat \"" + usageFile + "\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(agyScript), 0o755); err != nil {
		t.Fatal(err)
	}
	healthyUsage := "Gemini Models\tWeekly Limit Remaining\t80%\tx\n" +
		"Gemini Models\tFive Hour Limit Remaining\t90%\tx\n" +
		"Claude and GPT models\tWeekly Limit Remaining\t55%\tx\n" +
		"Claude and GPT models\tFive Hour Limit Remaining\t75%\tx\n"
	if err := os.WriteFile(usageFile, []byte(healthyUsage), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	now := time.Now().UTC().Truncate(time.Second)
	seedCatalog := func(quotaTS string, pools []models.Pool) {
		t.Helper()
		cat := models.Catalog{
			UpdatedAt:      now.Format(time.RFC3339),
			QuotaUpdatedAt: quotaTS,
			Entries: []models.CatalogEntry{
				{ID: "gemini-4-carbon-high", Family: "gemini", Tier: "pro", Effort: "high", Status: "active"},
				{ID: "claude-opus-5-5-high", Family: "anthropic", Tier: "pro", Effort: "high", Status: "active"},
			},
			Pools: pools,
		}
		if err := models.SaveCatalog(home, cat); err != nil {
			t.Fatal(err)
		}
	}
	readCalls := func() []string {
		raw, err := os.ReadFile(callsLog)
		if err != nil {
			return nil
		}
		s := strings.TrimSpace(string(raw))
		if s == "" {
			return nil
		}
		return strings.Split(s, "\n")
	}

	// 1. Fresh cache (2m old): zero agy invocations, < 50ms, models.json untouched.
	freshTS := now.Add(-2 * time.Minute).Format(time.RFC3339)
	seedCatalog(freshTS, []models.Pool{
		{Name: "Gemini Models", Five: 90, Week: 85},
		{Name: "Claude and GPT models", Five: 80, Week: 60},
	})
	beforeBytes, err := os.ReadFile(models.CatalogPath(home))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	t0 := time.Now()
	if err := run([]string{"models", "--pick", "judge", "--home", home}, &out); err != nil {
		t.Fatalf("warm --pick judge: %v", err)
	}
	elapsed := time.Since(t0)
	if got := strings.TrimSpace(out.String()); got != "claude-opus-5-5-high" {
		t.Fatalf("warm --pick judge = %q, want claude-opus-5-5-high", got)
	}
	if calls := readCalls(); len(calls) != 0 {
		t.Fatalf("fresh cache unexpectedly invoked agy: %v", calls)
	}
	if elapsed >= 50*time.Millisecond {
		t.Errorf("warm --pick judge took %s, want < 50ms", elapsed)
	}
	afterBytes, err := os.ReadFile(models.CatalogPath(home))
	if err != nil || string(afterBytes) != string(beforeBytes) {
		t.Errorf("fresh cache hit mutated models.json")
	}

	// 2. Stale (> 10m) cache: triggers live /usage refresh once and updates models.json.
	staleTS := now.Add(-15 * time.Minute).Format(time.RFC3339)
	seedCatalog(staleTS, []models.Pool{
		{Name: "Claude and GPT models", Five: 10, Week: 10},
	})
	out.Reset()
	if err := run([]string{"models", "--pick", "judge", "--json", "--home", home}, &out); err != nil {
		t.Fatalf("stale --pick judge: %v", err)
	}
	var jp models.RolePick
	if err := json.Unmarshal(out.Bytes(), &jp); err != nil || jp.Model != "claude-opus-5-5-high" {
		t.Fatalf("stale --pick judge JSON: err=%v pick=%+v", err, jp)
	}
	if calls := readCalls(); len(calls) != 1 || strings.TrimSpace(calls[0]) != "-p /usage" {
		t.Fatalf("stale cache agy calls = %v, want single [-p /usage]", calls)
	}
	reloaded, ok, err := models.LoadCatalog(home)
	if err != nil || !ok || reloaded.QuotaUpdatedAt == staleTS || len(reloaded.Pools) != 2 {
		t.Fatalf("stale refresh did not persist updated quota: ok=%v err=%v ts=%q pools=%+v", ok, err, reloaded.QuotaUpdatedAt, reloaded.Pools)
	}

	// Follow-up pick immediately hits newly refreshed cache with zero extra agy calls.
	out.Reset()
	if err := run([]string{"models", "--pick", "judge", "--home", home}, &out); err != nil {
		t.Fatalf("follow-up warm --pick judge: %v", err)
	}
	if calls := readCalls(); len(calls) != 1 {
		t.Fatalf("follow-up warm pick invoked agy again: %v", calls)
	}

	// 3. Explicit --refresh bypasses fresh cache (invoking models, changelog, and exactly 1 /usage).
	_ = os.Remove(callsLog)
	out.Reset()
	if err := run([]string{"models", "--refresh", "--pick", "judge", "--home", home}, &out); err != nil {
		t.Fatalf("--refresh --pick judge: %v", err)
	}
	refCalls := readCalls()
	usageCount := 0
	for _, c := range refCalls {
		if strings.TrimSpace(c) == "-p /usage" {
			usageCount++
		}
	}
	if usageCount != 1 {
		t.Fatalf("--refresh --pick judge /usage calls = %d (all=%v), want 1", usageCount, refCalls)
	}

	// 4. Corrupt cached quota + failing /usage falls back gracefully to Gemini.
	_ = os.Remove(callsLog)
	if err := os.WriteFile(failUsage, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedCatalog("corrupt-timestamp", []models.Pool{{Name: "Claude", Five: 999, Week: 50}})
	outStr, errStr, code := runCLIWithErr("models", "--pick", "judge", "--home", home)
	if code != 0 || strings.TrimSpace(outStr) != "gemini-4-carbon-high" {
		t.Fatalf("corrupt cache fallback: code=%d out=%q err=%q", code, outStr, errStr)
	}
	if !strings.Contains(errStr, "/usage unreadable") {
		t.Errorf("expected /usage unreadable fallback notice on stderr, got %q", errStr)
	}
}
