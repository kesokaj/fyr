package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	fyrisolate "github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/models"
)

func TestVerbModelsCLI(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	bin := filepath.Join(tmp, "bin")
	listPath := filepath.Join(tmp, "models.txt")
	usagePath := filepath.Join(tmp, "usage.txt")
	failPath := filepath.Join(tmp, "fail")
	for _, d := range []string{home, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	agyScript := "#!/bin/sh\n" +
		"if [ \"$1\" = \"models\" ]; then\n" +
		"  [ -e \"" + failPath + "\" ] && { echo 'offline' >&2; exit 1; }\n" +
		"  cat \"" + listPath + "\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"-p\" ] && [ \"$2\" = \"/usage\" ]; then\n" +
		"  cat \"" + usagePath + "\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(agyScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	initialModels := "gemini-4-carbon-high\tGemini 4 Carbon (High)\n" +
		"gemini-3.8-flash-high\tGemini 3.8 Flash (High)\n" +
		"claude-opus-5-5-high\tClaude Opus 5.5 (High)\n" +
		"claude-sonnet-5-5-high\tClaude Sonnet 5.5 (High)\n"
	if err := os.WriteFile(listPath, []byte(initialModels), 0o600); err != nil {
		t.Fatal(err)
	}
	healthyUsage := "Gemini\tWeekly Limit Remaining\t80%\tx\n" +
		"Gemini\tFive Hour Limit Remaining\t90%\tx\n" +
		"Claude\tWeekly Limit Remaining\t50%\tx\n" +
		"Claude\tFive Hour Limit Remaining\t70%\tx\n"
	if err := os.WriteFile(usagePath, []byte(healthyUsage), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1. Cold cache: `fyr models` refreshes automatically and mirrors agy models.
	var out bytes.Buffer
	if err := run([]string{"models", "--home", home}, &out); err != nil {
		t.Fatalf("fyr models: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"gemini-4-carbon-high",
		"gemini-3.8-flash-high",
		"claude-opus-5-5-high",
		"claude-sonnet-5-5-high",
		"Role picks:",
		"builder (pro): gemini-4-carbon-high",
		"judge (pro): claude-opus-5-5-high",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("fyr models missing %q:\n%s", want, out.String())
		}
	}

	// 2. `--pick` selects builder Carbon and Anthropic judge when quota is above floor.
	out.Reset()
	if err := run([]string{"models", "--pick", "builder", "--home", home}, &out); err != nil {
		t.Fatalf("--pick builder: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "gemini-4-carbon-high" {
		t.Errorf("--pick builder = %q, want gemini-4-carbon-high", got)
	}

	out.Reset()
	if err := run([]string{"models", "--pick", "judge", "--tier", "pro", "--json", "--home", home}, &out); err != nil {
		t.Fatalf("--pick judge --json: %v", err)
	}
	var jp models.RolePick
	if err := json.Unmarshal(out.Bytes(), &jp); err != nil {
		t.Fatalf("unmarshal RolePick: %v\n%s", err, out.String())
	}
	if jp.Model != "claude-opus-5-5-high" {
		t.Errorf("judge pick = %+v, want claude-opus-5-5-high", jp)
	}

	// 3. Remove claude-opus-5-5-high and claude-sonnet-5-5-high, run --refresh: marks gone and prints fallback reason.
	reducedModels := "gemini-4-carbon-high\tGemini 4 Carbon (High)\n" +
		"gemini-3.8-flash-high\tGemini 3.8 Flash (High)\n"
	if err := os.WriteFile(listPath, []byte(reducedModels), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"models", "--refresh", "--home", home}, &out); err != nil {
		t.Fatalf("fyr models --refresh: %v\n%s", err, out.String())
	}
	for _, want := range []string{"claude-opus-5-5-high", "gone", "fallback to Gemini"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("after removal, fyr models --refresh missing %q:\n%s", want, out.String())
		}
	}

	// 4. Failed --refresh exits 1 and keeps existing models.json intact.
	beforeBytes, err := os.ReadFile(models.CatalogPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(failPath, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err = run([]string{"models", "--refresh", "--home", home}, &out)
	var code exitCode
	if !errors.As(err, &code) || code != 1 {
		t.Fatalf("failed refresh: err=%v, want exit 1", err)
	}
	afterBytes, err := os.ReadFile(models.CatalogPath(home))
	if err != nil || string(afterBytes) != string(beforeBytes) {
		t.Errorf("failed refresh altered models.json")
	}

	// 5. Usage errors exit 2 (`--tier` without `--pick`, unknown role).
	for _, badArgs := range [][]string{
		{"models", "--tier", "pro", "--home", home},
		{"models", "--pick", "unknown-role", "--home", home},
	} {
		out.Reset()
		err := run(badArgs, &out)
		if !errors.As(err, &code) || code != 2 {
			t.Errorf("%v: err=%v, want exit 2\n%s", badArgs, err, out.String())
		}
	}
}

// TestVerbModelsReadsMetricsEvidence verifies that cmd/fyr feeds
// metrics.Store.ModelEvidence (including schema-7 work_waves setup_s, agy_s
// and check_s) into both `fyr models --refresh` and refreshModelsBestEffort,
// even after raw work/*/status.json directories are absent.
func TestVerbModelsReadsMetricsEvidence(t *testing.T) {
	// Must live outside every git checkout so refreshModelsBestEffort's CheckWorkDir passes.
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	bin := filepath.Join(tmp, "bin")
	for _, d := range []string{home, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}

	modelsList := "gemini-4-carbon-high\tGemini 4 Carbon (High)\n" +
		"gemini-3.9-carbon-high\tGemini 3.9 Carbon (High)\n" +
		"claude-opus-5-5-high\tClaude Opus 5.5 (High)\n"
	usageOut := "Gemini\tWeekly Limit Remaining\t80%\tx\n" +
		"Gemini\tFive Hour Limit Remaining\t90%\tx\n" +
		"Claude\tWeekly Limit Remaining\t80%\tx\n" +
		"Claude\tFive Hour Limit Remaining\t90%\tx\n"
	agyScript := "#!/bin/sh\n" +
		"if [ \"$1\" = \"models\" ]; then printf '%s' '" + modelsList + "'; exit 0; fi\n" +
		"if [ \"$1\" = \"-p\" ] && [ \"$2\" = \"/usage\" ]; then printf '%s' '" + usageOut + "'; exit 0; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(agyScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Write 6 graded runs per Carbon model into work/*/status.json, ingest into metrics.db,
	// then delete work/ completely so ONLY metrics.db holds the evidence!
	now := time.Now().UTC().Truncate(time.Second)
	waveDir := filepath.Join(fyrisolate.Root(home, "work"), "2026-11-11-120000-7")
	if err := os.MkdirAll(waveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	type workerSnap struct {
		Name     string `json:"name"`
		State    string `json:"state"`
		Model    string `json:"model"`
		SetupSec int64  `json:"setup_s"`
		AgySec   int64  `json:"agy_s"`
		CheckSec int64  `json:"check_s"`
	}
	type waveSnap struct {
		Stamp       string       `json:"stamp"`
		ProjectRoot string       `json:"project_root"`
		UpdatedAt   string       `json:"updated_at"`
		Workers     []workerSnap `json:"workers"`
	}
	var workers []workerSnap
	for i := 0; i < 6; i++ {
		st4 := "FAIL"
		if i == 0 {
			st4 = "PASS" // gemini-4-carbon-high: 1/6 PASS
		}
		workers = append(workers,
			workerSnap{
				Name: "c4-" + string(rune('a'+i)), State: st4, Model: "gemini-4-carbon-high",
				SetupSec: 10, AgySec: 80, CheckSec: 10,
			},
			workerSnap{
				Name: "c39-" + string(rune('a'+i)), State: "PASS", Model: "gemini-3.9-carbon-high",
				SetupSec: 5, AgySec: 35, CheckSec: 10,
			},
		)
	}
	rawSnap, err := json.Marshal(waveSnap{
		Stamp: "2026-11-11-120000-7", ProjectRoot: "/repo",
		UpdatedAt: now.Format(time.RFC3339), Workers: workers,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(waveDir, "status.json"), rawSnap, 0o600); err != nil {
		t.Fatal(err)
	}
	var ingestOut bytes.Buffer
	if err := run([]string{"metrics", "ingest", "--home", home}, &ingestOut); err != nil {
		t.Fatalf("metrics ingest: %v\n%s", err, ingestOut.String())
	}
	if err := os.RemoveAll(fyrisolate.Root(home, "work")); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"models", "--refresh", "--json", "--home", home}, &out); err != nil {
		t.Fatalf("fyr models --refresh --json: %v\n%s", err, out.String())
	}
	var cat models.Catalog
	if err := json.Unmarshal(out.Bytes(), &cat); err != nil {
		t.Fatalf("unmarshal catalog: %v\n%s", err, out.String())
	}
	var got39 *models.CatalogEntry
	for i := range cat.Entries {
		if cat.Entries[i].ID == "gemini-3.9-carbon-high" {
			got39 = &cat.Entries[i]
		}
	}
	if got39 == nil || got39.Evidence.WorkRuns != 6 || got39.Evidence.WorkPass != 6 || got39.Evidence.WorkMeanSeconds != 50 {
		t.Fatalf("gemini-3.9-carbon-high entry = %+v, want 6/6 runs and 50s mean from metrics.db", got39)
	}
	if bp := cat.Picks["builder:pro"]; bp.Model != "gemini-3.9-carbon-high" {
		t.Errorf("builder:pro pick = %+v, want evidence-backed gemini-3.9-carbon-high (6/6 > 1/6)", bp)
	}

	// Verify refreshModelsBestEffort also feeds metrics.Store.ModelEvidence.
	_ = os.Remove(models.CatalogPath(home))
	refreshModelsBestEffort(context.Background(), home)
	cat2, ok, err := models.LoadCatalog(home)
	if err != nil || !ok {
		t.Fatalf("LoadCatalog after refreshModelsBestEffort: ok=%v err=%v", ok, err)
	}
	if bp := cat2.Picks["builder:pro"]; bp.Model != "gemini-3.9-carbon-high" {
		t.Errorf("refreshModelsBestEffort builder:pro pick = %+v, want gemini-3.9-carbon-high", bp)
	}

	// Verify --explain and --pick planner CLI flags work cleanly and reject invalid combinations.
	out.Reset()
	if err := run([]string{"models", "--explain", "gemini-4-carbon-high", "--home", home}, &out); err != nil {
		t.Fatalf("fyr models --explain: %v\n%s", err, out.String())
	}
	for _, want := range []string{"ID: gemini-4-carbon-high", "Subfamily:", "architecture", "Best-for:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("--explain missing %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := run([]string{"models", "--pick", "planner", "--home", home}, &out); err != nil {
		t.Fatalf("fyr models --pick planner: %v\n%s", err, out.String())
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Errorf("--pick planner returned empty model")
	}
	out.Reset()
	if err := run([]string{"models", "--explain", "gemini-4-carbon-high", "--pick", "builder", "--home", home}, &out); exitOf(err) != 2 {
		t.Errorf("--explain + --pick exit = %d (%v), want 2", exitOf(err), err)
	}
}

func TestVerbModelsColdCacheEnrichesPoolsOnly(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	bin := filepath.Join(tmp, "bin")
	callsPath := filepath.Join(tmp, "calls.log")
	for _, d := range []string{home, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	agyScript := "#!/bin/sh\n" +
		"echo \"$1 $2\" >> \"" + callsPath + "\"\n" +
		"if [ \"$1\" = \"models\" ]; then\n" +
		"  printf 'gemini-4-carbon-high\\tGemini 4 Carbon (High)\\nclaude-opus-5-5-high\\tClaude Opus 5.5 (High)\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"-p\" ] && [ \"$2\" = \"/usage\" ]; then\n" +
		"  printf 'Gemini Models\\tWeekly Limit Remaining\\t80%%\\tx\\nGemini Models\\tFive Hour Limit Remaining\\t90%%\\tx\\nClaude and GPT models\\tWeekly Limit Remaining\\t55%%\\tx\\nClaude and GPT models\\tFive Hour Limit Remaining\\t75%%\\tx\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"changelog\" ]; then\n" +
		"  printf '1.6.0:\\n· Added Gemini 4 Carbon\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(agyScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Cold cache: `fyr models --json` enriches pools, keeps changelog gated on --refresh.
	var out bytes.Buffer
	if err := run([]string{"models", "--json", "--home", home}, &out); err != nil {
		t.Fatalf("cold fyr models --json: %v\n%s", err, out.String())
	}
	var coldCat models.Catalog
	if err := json.Unmarshal(out.Bytes(), &coldCat); err != nil {
		t.Fatalf("unmarshal cold catalog: %v\n%s", err, out.String())
	}
	if len(coldCat.Pools) != 2 || coldCat.QuotaUpdatedAt == "" {
		t.Fatalf("cold cache did not enrich pools: pools=%+v quotaUpdatedAt=%q", coldCat.Pools, coldCat.QuotaUpdatedAt)
	}
	savedCold, ok, err := models.LoadCatalog(home)
	if err != nil || !ok || len(savedCold.Pools) != 2 {
		t.Fatalf("cold cache models.json missing pools: ok=%v err=%v pools=%+v", ok, err, savedCold.Pools)
	}
	callsRaw, _ := os.ReadFile(callsPath)
	if strings.Contains(string(callsRaw), "changelog") {
		t.Errorf("cold cache unexpectedly ran changelog:\n%s", callsRaw)
	}
	for _, e := range savedCold.Entries {
		if e.ChangelogNote != "" {
			t.Errorf("cold cache unexpectedly set ChangelogNote on %s: %q", e.ID, e.ChangelogNote)
		}
	}

	// Explicit --refresh enriches both changelog and pools.
	out.Reset()
	if err := run([]string{"models", "--refresh", "--json", "--home", home}, &out); err != nil {
		t.Fatalf("refresh fyr models --json: %v\n%s", err, out.String())
	}
	callsRaw2, _ := os.ReadFile(callsPath)
	if !strings.Contains(string(callsRaw2), "changelog") {
		t.Errorf("--refresh did not invoke changelog:\n%s", callsRaw2)
	}
	var refCat models.Catalog
	if err := json.Unmarshal(out.Bytes(), &refCat); err != nil {
		t.Fatalf("unmarshal refreshed catalog: %v", err)
	}
	var gotCarbonNote string
	for _, e := range refCat.Entries {
		if e.ID == "gemini-4-carbon-high" {
			gotCarbonNote = e.ChangelogNote
		}
	}
	if !strings.Contains(gotCarbonNote, "Added Gemini 4 Carbon") || len(refCat.Pools) != 2 {
		t.Errorf("--refresh catalog mismatch: carbonNote=%q pools=%+v", gotCarbonNote, refCat.Pools)
	}
}

func TestVerbModelsPickWithoutAgyAndStderrErrors(t *testing.T) {
	home := t.TempDir()
	emptyBin := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", emptyBin)

	mixed := models.Catalog{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Entries: []models.CatalogEntry{
			{ID: "gemini-4-carbon-high", Family: "google", Tier: "pro", Status: "active"},
			{ID: "claude-opus-5-5-high", Family: "anthropic", Tier: "pro", Status: "active"},
		},
	}
	if err := models.SaveCatalog(home, mixed); err != nil {
		t.Fatal(err)
	}
	catPath := models.CatalogPath(home)
	before, err := os.ReadFile(catPath)
	if err != nil {
		t.Fatal(err)
	}

	// Cross-family --pick without agy on PATH falls back to Gemini with FallbackReason and leaves models.json untouched.
	outStr, errStr, code := runCLIWithErr("models", "--pick", "judge", "--home", home)
	if code != 0 || strings.TrimSpace(outStr) != "gemini-4-carbon-high" {
		t.Fatalf("--pick judge without agy: code=%d out=%q err=%q", code, outStr, errStr)
	}
	if !strings.Contains(errStr, "agy not found on PATH") {
		t.Errorf("expected fallback reason on stderr, got %q", errStr)
	}
	outStr, errStr, code = runCLIWithErr("models", "--pick", "reviewer", "--json", "--home", home)
	if code != 0 || errStr != "" {
		t.Fatalf("--pick reviewer --json without agy: code=%d out=%q err=%q", code, outStr, errStr)
	}
	var rp models.RolePick
	if err := json.Unmarshal([]byte(outStr), &rp); err != nil || rp.Model != "gemini-4-carbon-high" || rp.FallbackReason == "" {
		t.Fatalf("unexpected RolePick json: err=%v rp=%+v out=%q", err, rp, outStr)
	}
	after, err := os.ReadFile(catPath)
	if err != nil || string(after) != string(before) {
		t.Errorf("read-only --pick mutated models.json")
	}

	// Explain error goes to stderr, leaving stdout clean even with --json.
	outStr, errStr, code = runCLIWithErr("models", "--explain", "no-such-model", "--json", "--home", home)
	if code != 1 || outStr != "" || !strings.Contains(errStr, "no entry matching") {
		t.Errorf("--explain missing: code=%d out=%q err=%q", code, outStr, errStr)
	}

	// Anthropic-only catalog without agy fails closed to stderr.
	anthOnly := models.Catalog{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Entries: []models.CatalogEntry{
			{ID: "claude-opus-5-5-high", Family: "anthropic", Tier: "pro", Status: "active"},
		},
	}
	if err := models.SaveCatalog(home, anthOnly); err != nil {
		t.Fatal(err)
	}
	outStr, errStr, code = runCLIWithErr("models", "--pick", "verifier", "--json", "--home", home)
	if code != 1 || outStr != "" || !strings.Contains(errStr, "agy not found on PATH") {
		t.Errorf("Anthropic-only --pick without agy: code=%d out=%q err=%q", code, outStr, errStr)
	}

	// writesState() parity for models.
	cJudge := newCtx("models", allVerbs()["models"], &bytes.Buffer{})
	cJudge.home = home
	cJudge.modelsPick = "judge"
	if !cJudge.writesState() {
		t.Errorf("writesState() = false for cross-family --pick judge")
	}
	cBuilder := newCtx("models", allVerbs()["models"], &bytes.Buffer{})
	cBuilder.home = home
	cBuilder.modelsPick = "builder"
	if cBuilder.writesState() {
		t.Errorf("writesState() = true for read-only --pick builder with existing catalog")
	}
}

func TestVerbModelsBenchAndScoreBreakdownCLI(t *testing.T) {
	home, emptyBin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", emptyBin)
	oldBench := models.BenchRunnerFunc
	t.Cleanup(func() { models.BenchRunnerFunc = oldBench })
	validResp := `{"sum":44,"go_func":"func ClampScore(v int) int { if v < 0 { return 0 }; return v }","ok":true}`
	models.BenchRunnerFunc = func(ctx context.Context, h, agy, modelID, prompt string, timeout time.Duration) (string, error) {
		if strings.Contains(modelID, "nova") {
			return `{"sum":0,"go_func":"bad","ok":true}`, nil
		}
		return validResp, nil
	}
	seed := models.Catalog{
		UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
		QuotaUpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Pools:          []models.Pool{{Name: "Gemini Models", Five: 35, Week: 80}},
		Entries: []models.CatalogEntry{
			{ID: "gemini-pro-orchestrator", Family: "gemini", Tier: "pro", Status: "active", InteractiveOnly: true},
			{ID: "gemini-4-neon-high", DisplayName: "Gemini 4 Neon (High)", Status: "new"},
			{ID: "gemini-5-nova-high", DisplayName: "Gemini 5 Nova (High)", Status: "new"},
			{ID: "gemini-3.8-flash-high", DisplayName: "Gemini 3.8 Flash (High)", Status: "active"},
			{ID: "gemini-2.5-flash-lite-low", DisplayName: "Gemini 2.5 Flash Lite (Low)", Status: "active"},
		},
	}
	if err := models.SaveCatalog(home, seed); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"models", "--bench", "--json", "--home", home}, &out); err != nil {
		t.Fatalf("fyr models --bench --json: %v\n%s", err, out.String())
	}
	var rep models.BenchReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || rep.Probed != 3 || rep.Passed != 2 || rep.Failed != 1 {
		t.Fatalf("BenchReport: err=%v rep=%+v", err, rep)
	}
	if got := rep.Picks["builder:pro"].Model; got != "gemini-4-neon-high" {
		t.Errorf("post-bench builder:pro = %q, want gemini-4-neon-high", got)
	}
	ms, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatalf("open metrics.db: %v", err)
	}
	evMap, err := ms.ModelEvidence(time.Time{}, time.Time{})
	_ = ms.Close()
	if err != nil || evMap["gemini-4-neon-high"].TrialRuns != 1 || evMap["gemini-4-neon-high"].TrialPass != 1 || evMap["gemini-4-neon-high"].DecayedRuns <= 0 || evMap["gemini-4-neon-high"].DecayedRuns > 0.25 {
		t.Fatalf("metrics.db neon bench evidence = %+v (err=%v)", evMap["gemini-4-neon-high"], err)
	}
	// Verify --bench stamped trials.policy_model in metrics.db (exported JSONL includes policy_model).
	expPath := filepath.Join(t.TempDir(), "export.jsonl")
	out.Reset()
	if err := run([]string{"metrics", "export", expPath, "--home", home}, &out); err != nil {
		t.Fatalf("metrics export: %v", err)
	}
	expRaw, err := os.ReadFile(expPath)
	if err != nil || !strings.Contains(string(expRaw), `"policy_model":"gemini-4-neon-high"`) {
		t.Fatalf("bench trial missing policy_model in metrics export: err=%v\n%s", err, string(expRaw))
	}
	out.Reset()
	if err := run([]string{"models", "--bench", "--model", "gemini-2.5-flash-lite-low", "--home", home}, &out); err != nil || !strings.Contains(out.String(), "PASS") || !strings.Contains(out.String(), "post=") {
		t.Fatalf("fyr models --bench --model: err=%v\n%s", err, out.String())
	}
	out.Reset()
	if err := run([]string{"models", "--pick", "builder", "--json", "--home", home}, &out); err != nil {
		t.Fatalf("--pick builder --json: %v", err)
	}
	var pick models.RolePick
	if err := json.Unmarshal(out.Bytes(), &pick); err != nil || pick.Model != "gemini-4-neon-high" || pick.PosteriorMean <= 0 || pick.ExploreBonus <= 0 || pick.HeadroomPenalty <= 0 || pick.DecayedRuns <= 0 {
		t.Errorf("pick JSON missing score breakdown: err=%v pick=%+v", err, pick)
	}
	out.Reset()
	if err := run([]string{"models", "--explain", "neon", "--home", home}, &out); err != nil {
		t.Fatalf("--explain neon: %v", err)
	}
	for _, want := range []string{"posterior=", "ucb1=", "headroom_penalty=", "decayed_runs="} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("--explain missing %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := run([]string{"models", "--home", home}, &out); err != nil || !strings.Contains(out.String(), "SCORE (POST/UCB/PEN)") {
		t.Fatalf("fyr models table: err=%v\n%s", err, out.String())
	}
	for _, bad := range [][]string{
		{"models", "--model", "gemini-4-neon-high", "--home", home},
		{"models", "--max", "2", "--home", home},
		{"models", "--bench", "--max", "0", "--home", home},
		{"models", "--bench", "--pick", "builder", "--home", home},
	} {
		out.Reset()
		if code := exitOf(run(bad, &out)); code != 2 {
			t.Errorf("%v: exit=%d, want 2", bad, code)
		}
	}
	if _, errStr, code := runCLIWithErr("models", "--bench", "--model", "gemini-pro-orchestrator", "--home", home); code != 1 || !strings.Contains(errStr, "interactive-only") {
		t.Errorf("--bench interactive orchestrator: code=%d err=%q, want exit 1", code, errStr)
	}
}

func TestV910ModelsCallerFamilyRolesExhaustedAndDevEval(t *testing.T) {
	home, emptyBin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", emptyBin)
	now := time.Now().UTC().Truncate(time.Second)

	// 1. Seed catalog with fresh healthy pools and test all 7+ agent roles + --caller-family.
	seed := models.Catalog{
		UpdatedAt:      now.Format(time.RFC3339),
		QuotaUpdatedAt: now.Add(-time.Minute).Format(time.RFC3339),
		Pools: []models.Pool{
			{Name: "Gemini Models", Five: 80, Week: 80},
			{Name: "Claude and GPT models", Five: 75, Week: 70},
		},
		Entries: []models.CatalogEntry{
			{ID: "gemini-4-carbon-high", Family: "gemini", Tier: "pro", Effort: "high", Status: "active"},
			{ID: "gemini-3.8-flash-high", Family: "gemini", Tier: "flash", Effort: "high", Status: "active"},
			{ID: "claude-opus-5-5-high", Family: "anthropic", Tier: "pro", Effort: "high", Status: "active"},
			{ID: "claude-sonnet-5-5-high", Family: "anthropic", Tier: "flash", Effort: "high", Status: "active"},
		},
	}
	if err := models.SaveCatalog(home, seed); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"models", "--pick", "security", "--caller-family", "anthropic", "--home", home}, "gemini-4-carbon-high"},
		{[]string{"models", "--pick", "security", "--caller-family", "gemini", "--home", home}, "claude-opus-5-5-high"},
		{[]string{"models", "--pick", "explorer", "--home", home}, "gemini-3.8-flash-high"},
		{[]string{"models", "--pick", "documentation", "--home", home}, "gemini-3.8-flash-high"},
		{[]string{"models", "--pick", "dreamer", "--home", home}, "gemini-4-carbon-high"},
	} {
		var out bytes.Buffer
		if err := run(tc.args, &out); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if got := strings.TrimSpace(out.String()); got != tc.want {
			t.Errorf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
	// Must-fail: invalid --caller-family or --caller-family without --pick exits 2.
	for _, bad := range [][]string{
		{"models", "--caller-family", "anthropic", "--home", home},
		{"models", "--pick", "judge", "--caller-family", "unknown", "--home", home},
	} {
		var out bytes.Buffer
		if code := exitOf(run(bad, &out)); code != 2 {
			t.Errorf("%v: exit=%d, want 2", bad, code)
		}
	}

	// 2. All active pools <= 0% exits non-zero from --pick.
	exhausted := seed
	exhausted.Pools = []models.Pool{
		{Name: "Gemini Models", Five: 0, Week: 0},
		{Name: "Claude and GPT models", Five: 0, Week: 50},
	}
	if err := models.SaveCatalog(home, exhausted); err != nil {
		t.Fatal(err)
	}
	if outStr, errStr, code := runCLIWithErr("models", "--pick", "builder", "--home", home); code == 0 || outStr != "" || !strings.Contains(errStr, "exhausted") {
		t.Errorf("exhausted --pick builder: code=%d out=%q err=%q", code, outStr, errStr)
	}
	if outStr, errStr, code := runCLIWithErr("models", "--pick", "judge", "--home", home); code == 0 || outStr != "" || !strings.Contains(errStr, "exhausted") {
		t.Errorf("exhausted --pick judge: code=%d out=%q err=%q", code, outStr, errStr)
	}

	// 3. dev/fyr-eval fallback feeds loadMetricsModelEvidence even without main metrics.db.
	devPath := filepath.Join(home, harness.StateDir, "dev", "fyr-eval", harness.StateDir, "metrics.db")
	devStore, err := metrics.Open(devPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := devStore.RecordTrial(metrics.Trial{
		Run: "dev/ev/1", Label: "eval=builder-smoke", Kind: "capability", Harness: "v9.10.0",
		Status: "pass", StartedAt: now.Add(-time.Hour), Seconds: 4, PolicyModel: "gemini-4-carbon-high",
	}); err != nil {
		t.Fatal(err)
	}
	devStore.Close()

	ev := loadMetricsModelEvidence(home)
	if got := ev["gemini-4-carbon-high"]; got.TrialRuns != 1 || got.TrialPass != 1 {
		t.Fatalf("loadMetricsModelEvidence missed dev/fyr-eval trial: %+v", got)
	}
}

func TestMergeMetricsEvidenceWeightedMeans(t *testing.T) {
	out := map[string]models.ModelEvidence{}
	mergeMetricsEvidenceInto(out, map[string]metrics.ModelEvidence{
		"gemini-4-carbon-high": {
			WorkRuns: 2, WorkPass: 2, WorkMeanSeconds: 10.0,
			EvalRuns: 2, EvalPass: 2, TrialRuns: 2, TrialPass: 2,
			MeanLatencySeconds: 10.0,
		},
	})
	mergeMetricsEvidenceInto(out, map[string]metrics.ModelEvidence{
		"gemini-4-carbon-high": {
			WorkRuns: 6, WorkPass: 6, WorkMeanSeconds: 50.0,
			EvalRuns: 0, EvalPass: 0,
			MeanLatencySeconds: 50.0,
		},
	})
	got := out["gemini-4-carbon-high"]
	// WorkMeanSeconds: (10*2 + 50*6) / (2+6) = 320 / 8 = 40.0
	if got.WorkMeanSeconds != 40.0 {
		t.Errorf("WorkMeanSeconds = %.4f, want 40.0", got.WorkMeanSeconds)
	}
	// MeanLatencySeconds: (10*(2+2) + 50*(6+0)) / (4+6) = (40 + 300) / 10 = 34.0
	if got.MeanLatencySeconds != 34.0 {
		t.Errorf("MeanLatencySeconds = %.4f, want 34.0", got.MeanLatencySeconds)
	}
	if got.WorkRuns != 8 || got.EvalRuns != 2 {
		t.Errorf("counts mismatch: WorkRuns=%d EvalRuns=%d", got.WorkRuns, got.EvalRuns)
	}
}
