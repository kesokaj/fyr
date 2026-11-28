package models

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveRoleModelForHome(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	listFn := func(ctx context.Context, agy string) ([]string, error) {
		return []string{
			"gemini-4-carbon-high",
			"gemini-3.8-flash-high",
			"claude-opus-5-5-high",
			"claude-sonnet-5-5-high",
		}, nil
	}

	home := t.TempDir()
	m, note, err := ResolveRoleModelWithOptions(ctx, ResolveRoleOptions{Home: home, AgyPath: "agy", Role: "builder", Tier: "pro", PersistPools: true, ListModels: listFn})
	if err != nil || m != "gemini-4-carbon-high" || !strings.Contains(note, "Gemini") {
		t.Fatalf("builder pro: got m=%q note=%q err=%v", m, note, err)
	}
	mFlash, _, err := ResolveRoleModelWithOptions(ctx, ResolveRoleOptions{Home: home, AgyPath: "agy", Role: "builder", Tier: "flash", PersistPools: true, ListModels: listFn})
	if err != nil || mFlash != "gemini-3.8-flash-high" {
		t.Fatalf("builder flash: got %q err=%v, want gemini-3.8-flash-high", mFlash, err)
	}

	usageFn := func(ctx context.Context, agy string) (string, error) {
		return "Gemini Models\tWeekly Limit Remaining\t80%\tx\n" +
			"Gemini Models\tFive Hour Limit Remaining\t90%\tx\n" +
			"Claude and GPT models\tWeekly Limit Remaining\t45%\tx\n" +
			"Claude and GPT models\tFive Hour Limit Remaining\t60%\tx\n", nil
	}
	jModel, jNote, err := ResolveRoleModelWithOptions(ctx, ResolveRoleOptions{Home: home, AgyPath: "agy", Role: "judge", Tier: "pro", PersistPools: true, ListModels: listFn, UsageProbe: usageFn})
	if err != nil || jModel != "claude-opus-5-5-high" || !strings.Contains(jNote, "Anthropic") {
		t.Fatalf("judge healthy quota: got m=%q note=%q err=%v", jModel, jNote, err)
	}
	vModel, _, err := ResolveRoleModelWithOptions(ctx, ResolveRoleOptions{Home: home, AgyPath: "agy", Role: "verifier", Tier: "sonnet", PersistPools: true, ListModels: listFn, UsageProbe: usageFn})
	if err != nil || vModel != "claude-sonnet-5-5-high" {
		t.Fatalf("verifier sonnet healthy quota: got %q err=%v, want claude-sonnet-5-5-high", vModel, err)
	}

	usageFn = func(ctx context.Context, agy string) (string, error) {
		return "Gemini Models\tWeekly Limit Remaining\t80%\tx\n" +
			"Gemini Models\tFive Hour Limit Remaining\t90%\tx\n" +
			"Claude and GPT models\tWeekly Limit Remaining\t15%\tx\n" +
			"Claude and GPT models\tFive Hour Limit Remaining\tdisabled\tx\n", nil
	}
	fbModel, fbNote, err := ResolveRoleModelWithOptions(ctx, ResolveRoleOptions{Home: home, AgyPath: "agy", Role: "judge", Tier: "pro", PersistPools: true, ListModels: listFn, UsageProbe: usageFn})
	if err != nil || fbModel != "gemini-4-carbon-high" || !strings.Contains(fbNote, "fallback to Gemini") {
		t.Fatalf("judge low quota fallback: got m=%q note=%q err=%v", fbModel, fbNote, err)
	}
	fbFlash, fbFlashNote, err := ResolveRoleModelWithOptions(ctx, ResolveRoleOptions{Home: home, AgyPath: "agy", Role: "reviewer", Tier: "flash", PersistPools: true, ListModels: listFn, UsageProbe: usageFn})
	if err != nil || fbFlash != "gemini-3.8-flash-high" || !strings.Contains(fbFlashNote, "fallback to Gemini") {
		t.Fatalf("reviewer flash low quota fallback: got m=%q note=%q err=%v", fbFlash, fbFlashNote, err)
	}

	expModel, expNote, _ := ResolveRoleModelWithOptions(ctx, ResolveRoleOptions{Home: home, AgyPath: "agy", Role: "judge", Tier: "pro", ExplicitModel: "custom-model", PersistPools: true, ListModels: listFn, UsageProbe: usageFn})
	if expModel != "custom-model" || !strings.Contains(expNote, "explicit") {
		t.Fatalf("explicit override: got m=%q note=%q", expModel, expNote)
	}
}

func TestResolveRoleModelPreservesFailingWorkPassUnderEvalEvidence(t *testing.T) {
	ctx := context.Background()

	home := t.TempDir()

	parsed := ParseModelsOutput(
		"gemini-4-carbon-high\tGemini 4 Carbon (High)\n" +
			"gemini-4-carbon-medium\tGemini 4 Carbon (Medium)\n" +
			"claude-opus-5-5-high\tClaude Opus 5.5 (High)\n",
	)
	cat := BuildCatalog(time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC), parsed, nil, map[string]ModelEvidence{
		"gemini-4-carbon-high": {WorkRuns: 16, WorkPass: 4, WorkMeanSeconds: 95},
		"gemini-4-carbon":      {EvalRuns: 10, EvalPass: 5},
		"claude-opus-5-5":      {EvalRuns: 8, EvalPass: 8},
	})
	if err := SaveCatalog(home, cat); err != nil {
		t.Fatal(err)
	}

	listFn := func(ctx context.Context, agy string) ([]string, error) {
		return []string{"gemini-4-carbon-high", "gemini-4-carbon-medium", "claude-opus-5-5-high"}, nil
	}
	usageFn := func(ctx context.Context, agy string) (string, error) {
		return "Gemini Models\tWeekly Limit Remaining\t80%\tx\n" +
			"Gemini Models\tFive Hour Limit Remaining\t90%\tx\n" +
			"Claude and GPT models\tWeekly Limit Remaining\t10%\tx\n" +
			"Claude and GPT models\tFive Hour Limit Remaining\t50%\tx\n", nil
	}

	bModel, _, err := ResolveRoleModelWithOptions(ctx, ResolveRoleOptions{Home: home, AgyPath: "agy", Role: "builder", Tier: "pro", PersistPools: true, ListModels: listFn, UsageProbe: usageFn})
	if err != nil || bModel != "gemini-4-carbon-medium" {
		t.Fatalf("builder pro with 7/16 work + 31/35 eval: got %q err=%v, want gemini-4-carbon-medium", bModel, err)
	}
	jFallback, jNote, err := ResolveRoleModelWithOptions(ctx, ResolveRoleOptions{Home: home, AgyPath: "agy", Role: "judge", Tier: "pro", PersistPools: true, ListModels: listFn, UsageProbe: usageFn})
	if err != nil || jFallback != "gemini-4-carbon-medium" || !strings.Contains(jNote, "fallback to Gemini") {
		t.Fatalf("judge low-quota fallback: got %q note=%q err=%v, want gemini-4-carbon-medium", jFallback, jNote, err)
	}

	t.Setenv("HOME", t.TempDir())
	bHomeModel, _, err := ResolveRoleModelWithOptions(ctx, ResolveRoleOptions{Home: home, AgyPath: "agy", Role: "builder", Tier: "pro", PersistPools: true, ListModels: listFn, UsageProbe: usageFn})
	if err != nil || bHomeModel != "gemini-4-carbon-medium" {
		t.Fatalf("ResolveRoleModelForHome(home): got %q err=%v, want gemini-4-carbon-medium", bHomeModel, err)
	}
}

func TestCheckHomeDir(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := CheckHomeDir(home); err != nil {
		t.Fatalf("clean home refused: %v", err)
	}
	if err := CheckHomeDir(""); err == nil {
		t.Fatal("empty home accepted")
	}
	cfg := filepath.Join(home, ".config")
	target := t.TempDir()
	if err := os.Symlink(target, cfg); err != nil {
		t.Skip(err)
	}
	if err := CheckHomeDir(home); err == nil || !strings.Contains(err.Error(), "symlink") && !strings.Contains(err.Error(), "non-directory") {
		t.Fatalf("symlink .config accepted: %v", err)
	}
}

func TestCatalogPathAndStoreNormalizeWhitespace(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	padded := "  " + home + "/  "
	if got, want := CatalogPath(padded), CatalogPath(home); got != want {
		t.Fatalf("CatalogPath(%q) = %q, want %q", padded, got, want)
	}
	cat := Catalog{Entries: []CatalogEntry{{ID: "m1", Status: "active"}}}
	if err := SaveCatalog(padded, cat); err != nil {
		t.Fatalf("SaveCatalog(padded): %v", err)
	}
	if _, err := os.Stat(CatalogPath(home)); err != nil {
		t.Fatalf("expected catalog at clean path %s: %v", CatalogPath(home), err)
	}
	if !CatalogExists(padded) || CatalogExists("   ") {
		t.Fatalf("CatalogExists unexpected: padded=%v blank=%v", CatalogExists(padded), CatalogExists("   "))
	}
	loaded, ok, err := LoadCatalog(padded)
	if err != nil || !ok || len(loaded.Entries) != 1 || loaded.Entries[0].ID != "m1" {
		t.Fatalf("LoadCatalog(padded) = (%+v, %v, %v)", loaded, ok, err)
	}
	if _, ok, err := LoadCatalog("   "); err != nil || ok {
		t.Fatalf("LoadCatalog(blank) = (%v, %v), want (false, nil)", ok, err)
	}
	if err := SaveCatalog("   ", cat); err == nil {
		t.Fatal("SaveCatalog(blank) succeeded, want error")
	}
}
