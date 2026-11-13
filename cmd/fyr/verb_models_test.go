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

	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/work"
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
	var jp work.RolePick
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
	beforeBytes, err := os.ReadFile(work.CatalogPath(home))
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
	afterBytes, err := os.ReadFile(work.CatalogPath(home))
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
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Write 6 graded runs per Carbon model into work/*/status.json, ingest into metrics.db,
	// then delete work/ completely so ONLY metrics.db holds the evidence!
	now := time.Now().UTC().Truncate(time.Second)
	waveDir := filepath.Join(sandbox.Root(home, "work"), "2026-11-11-120000-7")
	if err := os.MkdirAll(waveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var workers []work.WorkerStatus
	for i := 0; i < 6; i++ {
		st4 := "FAIL"
		if i == 0 {
			st4 = "PASS" // gemini-4-carbon-high: 1/6 PASS
		}
		workers = append(workers,
			work.WorkerStatus{
				Name: "c4-" + string(rune('a'+i)), State: st4, Model: "gemini-4-carbon-high",
				SetupSec: 10, AgySec: 80, CheckSec: 10,
			},
			work.WorkerStatus{
				Name: "c39-" + string(rune('a'+i)), State: "PASS", Model: "gemini-3.9-carbon-high",
				SetupSec: 5, AgySec: 35, CheckSec: 10,
			},
		)
	}
	rawSnap, err := json.Marshal(work.WaveStatus{
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
	if err := os.RemoveAll(sandbox.Root(home, "work")); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"models", "--refresh", "--json", "--home", home}, &out); err != nil {
		t.Fatalf("fyr models --refresh --json: %v\n%s", err, out.String())
	}
	var cat work.Catalog
	if err := json.Unmarshal(out.Bytes(), &cat); err != nil {
		t.Fatalf("unmarshal catalog: %v\n%s", err, out.String())
	}
	var got39 *work.CatalogEntry
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
	_ = os.Remove(work.CatalogPath(home))
	refreshModelsBestEffort(context.Background(), home)
	cat2, ok, err := work.LoadCatalog(home)
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
