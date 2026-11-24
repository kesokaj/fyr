package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

func TestHookWorkerCapsuleSpecContractsLatencyAndFailOpen(t *testing.T) {
	dir := recordRepo(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".agents/scratch/*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "service.go"), []byte("package svc\n\nfunc ReconcileState(id string) error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bo := brain.Options{Home: home, Dir: dir}
	adrID, err := brain.Add(bo, brain.AddInput{
		Kind: "adr", Status: "accepted", Title: "Deterministic state reconciliation",
		Body: strings.NewReader("Controls service.go invariants.\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	specBody := strings.Join([]string{
		"# SPEC-service",
		"## Purpose",
		"Defines `service.go` contracts (" + adrID + ").",
		"## Interfaces",
		"```go",
		"func ReconcileState(id string) error",
		"```",
		"## Data",
		"- State transitions are atomic.",
		"## Failure modes",
		"| Condition | Trigger | Behavior | Recovery |",
		"| --- | --- | --- | --- |",
		"| Empty ID | id == \"\" | Return ErrID | Pass valid ID |",
		"## Verification",
		"- Run `go test ./...`.",
	}, "\n")
	if _, err := brain.Add(bo, brain.AddInput{
		Kind: "spec", ID: "SPEC-service", Status: "current", Title: "Service spec",
		Body: strings.NewReader(specBody),
	}); err != nil {
		t.Fatal(err)
	}
	if err := search.Index(search.Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	callWorkerHook := func(h string, convID string) (time.Duration, search.HookDecisionOutput) {
		raw, _ := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"modelName":      "gemini-4-carbon-high",
			"toolCall": map[string]any{
				"name": "invoke_subagent",
				"args": map[string]any{
					"TypeName": "explorer",
					"Prompt":   "Inspect ReconcileState in service.go",
				},
			},
		})
		cache := newTurnBrainCache(h, false)
		var out bytes.Buffer
		start := time.Now()
		err := search.Search(search.Options{
			Dir: dir, Home: h, Hook: "pre-tool-use", HookIn: bytes.NewReader(raw),
			BrainRefs:           makeBrainRefs(cache, dir),
			BrainRefsForDir:     makeBrainRefsForDir(cache),
			BrainTurnContext:    makeBrainTurnContext(cache),
			BrainTurnContextCtx: makeBrainTurnContextCtx(cache),
			BrainSpecBodies:     makeBrainSpecBodies(cache),
		}, &out)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("PreToolUse(%s): %v", convID, err)
		}
		var dec search.HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal HookDecisionOutput: %v\n%s", err, out.String())
		}
		return elapsed, dec
	}

	// Warmup + warm latency check (< 40ms without -race; scaled under -race for pure-Go SQLite).
	warmBudget := 40 * time.Millisecond
	if search.DefaultHookBrainBudget() > 100*time.Millisecond {
		warmBudget = 200 * time.Millisecond
	}
	_, _ = callWorkerHook(home, "conv-spec-warm-0")
	elapsed, dec := callWorkerHook(home, "conv-spec-warm-1")
	if elapsed >= warmBudget {
		t.Errorf("warm PreToolUse worker hook with BrainSpecBodies took %v, want < %v", elapsed, warmBudget)
	}
	if dec.Decision != "allow" || dec.Overwrite == nil {
		t.Fatalf("expected allow+overwrite, got %+v", dec)
	}
	prompt, _ := dec.Overwrite["Prompt"].(string)
	if !strings.Contains(prompt, "BEGIN DATA spec_contracts") || !strings.Contains(prompt, "SPEC-service") {
		t.Errorf("expected fenced SPEC-service digest in worker prompt:\n%s", prompt)
	}

	// Fail-open when brain.db is unavailable (empty home without brain.db).
	missingHome := t.TempDir()
	_, decMissing := callWorkerHook(missingHome, "conv-spec-missing")
	if decMissing.Decision != "allow" {
		t.Errorf("missing brain.db should fail-open with allow, got %+v", decMissing)
	}

	// Direct loadCurrentSpecsFromStoreCtx cancellation, symlink refusal, and trail fallback.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := loadCurrentSpecsFromStoreCtx(cctx, dir, home); !errors.Is(err, context.Canceled) {
		t.Errorf("loadCurrentSpecsFromStoreCtx cancelled err = %v, want context.Canceled", err)
	}
	linkHome := t.TempDir()
	linkDB := brain.Path(linkHome)
	if err := os.MkdirAll(filepath.Dir(linkDB), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(brain.Path(home), linkDB); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCurrentSpecsFromStoreCtx(context.Background(), dir, linkHome); err == nil || !brain.IsRefusal(err) {
		t.Errorf("loadCurrentSpecsFromStoreCtx symlink err = %v (IsRefusal=%v), want refusal", err, brain.IsRefusal(err))
	}

	// Fail-open when brain.db query is slow beyond brainTurnQueryBudget.
	origBudget := brainTurnQueryBudget
	origLoader := loadSpecRecordsHook
	t.Cleanup(func() {
		brainTurnQueryBudget = origBudget
		loadSpecRecordsHook = origLoader
	})
	brainTurnQueryBudget = 5 * time.Millisecond
	loadSpecRecordsHook = func(ctx context.Context, _, _ string) ([]brain.Record, error) {
		select {
		case <-time.After(80 * time.Millisecond):
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	slowStart := time.Now()
	slowCache := newTurnBrainCache(home, false)
	gotSlow := makeBrainSpecBodies(slowCache)(dir, home, []string{"service.go"})
	slowElapsed := time.Since(slowStart)
	if len(gotSlow) != 0 {
		t.Errorf("slow spec loader should fail-open with empty sources, got %+v", gotSlow)
	}
	if slowElapsed >= 40*time.Millisecond {
		t.Errorf("slow spec loader exceeded fail-open budget: %v", slowElapsed)
	}
}

func TestMakeBrainSpecBodiesHydratesOlderADRAfterPartialTurnLoad(t *testing.T) {
	dir := recordRepo(t)
	home := t.TempDir()
	bo := brain.Options{Home: home, Dir: dir}

	oldestADR, err := brain.Add(bo, brain.AddInput{
		Kind: "adr", Status: "accepted", Title: "Legacy storage contract",
		Body: strings.NewReader("Enforces invariants in legacy_engine.go.\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 14; i++ {
		if _, err := brain.Add(bo, brain.AddInput{
			Kind: "adr", Status: "accepted", Title: fmt.Sprintf("Newer ADR %02d", i),
			Body: strings.NewReader(fmt.Sprintf("Unrelated newer decision %d.\n", i)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := brain.Add(bo, brain.AddInput{
		Kind: "spec", ID: "SPEC-legacy", Status: "current", Title: "Legacy engine spec",
		Body: strings.NewReader("## Purpose\nCovers `legacy_engine.go`.\n## Interfaces\n```go\nfunc Run()\n```\n## Data\n- Safe.\n## Failure modes\n| T | O | R | A |\n|---|---|---|---|\n| err | fail | 0 | stop |\n## Verification\n- Test.\n"),
	}); err != nil {
		t.Fatal(err)
	}

	cache := newTurnBrainCache(home, false)
	// Populate turnByQuery with partial hydration (Complete == false) first.
	_ = makeBrainTurnContextCtx(cache)(context.Background(), dir, home, "Refactor dashboard metrics rendering", nil)
	sources := makeBrainSpecBodies(cache)(dir, home, []string{"legacy_engine.go"})
	foundADR := false
	for _, s := range sources {
		if s.ID == oldestADR {
			foundADR = true
		}
	}
	if !foundADR {
		t.Fatalf("makeBrainSpecBodies missed older ADR %s after partial turn load; got %+v", oldestADR, sources)
	}
}
