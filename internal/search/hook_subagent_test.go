package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func runSubagentPreToolHook(t *testing.T, opts Options, convID, modelName, toolName string, args map[string]any, ws []string, cwd string) HookDecisionOutput {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"conversationId": convID,
		"workspacePaths": ws,
		"cwd":            cwd,
		"modelName":      modelName,
		"toolCall": map[string]any{
			"name": toolName,
			"args": args,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	opts.Hook = "pre-tool-use"
	opts.HookIn = bytes.NewReader(payload)
	var out bytes.Buffer
	if err := Search(opts, &out); err != nil {
		t.Fatalf("Search pre-tool-use (%s): %v", toolName, err)
	}
	var dec HookDecisionOutput
	if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
		t.Fatalf("unmarshal HookDecisionOutput %q: %v", out.String(), err)
	}
	return dec
}

func extractWorkerCapsuleSlice(t *testing.T, enriched string) string {
	t.Helper()
	idx := strings.Index(enriched, workerCapsuleTag)
	if idx < 0 {
		t.Fatalf("missing %s in:\n%s", workerCapsuleTag, enriched)
	}
	return enriched[idx:]
}

func TestPreToolUseWorkerJITCapsuleOverwrite(t *testing.T) {
	dir := setupHookRepo(t)
	writeFile(t, dir, "internal/alpha/alpha.go", "package alpha\n\nfunc AlphaWork(name string) error {\n\treturn nil\n}\n")
	commitAll(t, dir, "add alpha package")
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	catDir := filepath.Join(home, harness.StateDir)
	if err := os.MkdirAll(catDir, 0o700); err != nil {
		t.Fatal(err)
	}
	catJSON := `{"entries":[{"id":"gemini-4-carbon-high","family":"gemini","subfamily":"carbon","tier":"pro","status":"active"},{"id":"claude-opus-5-5-high","family":"anthropic","subfamily":"opus","tier":"pro","status":"active"}]}`
	if err := os.WriteFile(filepath.Join(catDir, "models.json"), []byte(catJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	baseOpts := Options{
		Home: home,
		BrainTurnContext: func(_, _, prompt string, files []string) TurnBrainContext {
			return TurnBrainContext{
				Memories: []MatchedRecord{
					{ID: "MEMORY-low", Kind: "memory", Title: "Stale low validity", Validity: 0.25, Body: "Should be excluded (< 0.40)."},
					{ID: "MEMORY-reconcile", Kind: "memory", Title: "Reconcile invariant", Provenance: "operator", Validity: 0.90, Body: "Keep reconcile deterministic."},
				},
				ADRs: []MatchedRecord{
					{ID: "ADR-525", Kind: "adr", Status: "accepted", Title: "Pre-hook capsule contract", Body: "Fenced capsule <= 1200 B."},
				},
			}
		},
	}

	t.Run("guarded_subagent_merges_model_and_capsule", func(t *testing.T) {
		dec := runSubagentPreToolHook(t, baseOpts, "conv-sub-1", "gemini-3.8-flash-medium", "invoke_subagent", map[string]any{
			"TypeName":  "reviewer",
			"Role":      "Code Reviewer",
			"Prompt":    "Review ReconcileProjectIndex in service.go",
			"Workspace": "inherit",
			"model":     "inherit",
		}, []string{dir}, "")
		if dec.Decision != "allow" || dec.Overwrite == nil {
			t.Fatalf("expected allow+overwrite, got %+v", dec)
		}
		if dec.Overwrite["Model"] != "pro" {
			t.Errorf("expected Model=pro merged, got %v", dec.Overwrite["Model"])
		}
		if _, hasLower := dec.Overwrite["model"]; hasLower {
			t.Errorf("lowercase model key leaked: %+v", dec.Overwrite)
		}
		prompt, _ := dec.Overwrite["Prompt"].(string)
		cap := extractWorkerCapsuleSlice(t, prompt)
		if len(cap) > 1200 {
			t.Errorf("capsule exceeded 1200 B: %d\n%s", len(cap), cap)
		}
		for _, want := range []string{"inferred_files: service.go", "MEMORY-reconcile", "ADR-525", "BEGIN DATA", "END DATA", "[Hop 0]", "ReconcileProjectIndex"} {
			if !strings.Contains(cap, want) {
				t.Errorf("capsule missing %q:\n%s", want, cap)
			}
		}
		if strings.Contains(cap, "MEMORY-low") {
			t.Errorf("low-validity memory (< 0.40) leaked into capsule:\n%s", cap)
		}
	})

	t.Run("unguarded_subagent_gets_capsule_without_model_upgrade", func(t *testing.T) {
		dec := runSubagentPreToolHook(t, baseOpts, "conv-sub-2", "gemini-3.8-flash-medium", "invoke_subagent", map[string]any{
			"TypeName": "explorer",
			"Role":     "Explorer",
			"Prompt":   "Trace AlphaWork in internal/alpha/alpha.go",
		}, []string{dir}, "")
		if dec.Decision != "allow" || dec.Overwrite == nil {
			t.Fatalf("expected allow+overwrite on unguarded explorer, got %+v", dec)
		}
		if _, hasModel := dec.Overwrite["Model"]; hasModel {
			t.Errorf("unguarded subagent unexpectedly got Model overwrite: %+v", dec.Overwrite)
		}
		prompt, _ := dec.Overwrite["Prompt"].(string)
		if !strings.Contains(prompt, workerCapsuleTag) || !strings.Contains(prompt, "AlphaWork") {
			t.Errorf("expected [FYR_WORKER_CAPSULE] + AlphaWork in explorer Prompt:\n%s", prompt)
		}
	})

	t.Run("swarm_add_merges_model_and_files_header_verbatim", func(t *testing.T) {
		origBrief := "Files: internal/alpha/alpha.go\nImplement error return in AlphaWork."
		dec := runSubagentPreToolHook(t, baseOpts, "conv-swarm-1", "gemini-4-hydrogen-medium", "default_api:swarm", map[string]any{
			"action": "add",
			"name":   "worker-1",
			"brief":  origBrief,
		}, []string{dir}, "")
		if dec.Decision != "allow" || dec.Overwrite == nil || dec.Overwrite["model"] != "gemini-4-carbon-high" {
			t.Fatalf("expected model + brief overwrite, got %+v", dec)
		}
		brief, _ := dec.Overwrite["brief"].(string)
		if !strings.HasPrefix(brief, origBrief+"\n\n") {
			t.Errorf("existing Files: header was not kept verbatim at top:\n%s", brief)
		}
		cap := extractWorkerCapsuleSlice(t, brief)
		if strings.Contains(cap, "inferred_files:") || strings.Contains(cap, "Files:") {
			t.Errorf("existing Files: header should not be duplicated inside capsule:\n%s", cap)
		}

		// Explicit valid model still injects brief capsule; weak judge model still denies first.
		expDec := runSubagentPreToolHook(t, baseOpts, "conv-swarm-2", "gemini-4-carbon-high", "swarm", map[string]any{
			"action": "add",
			"name":   "worker-2",
			"model":  "gemini-4-carbon-high",
			"brief":  "Files: internal/alpha/alpha.go\nUpdate AlphaWork.",
		}, []string{dir}, "")
		if expDec.Decision != "allow" || expDec.Overwrite == nil || expDec.Overwrite["model"] != "gemini-4-carbon-high" {
			t.Fatalf("expected brief overwrite with explicit model preserved, got %+v", expDec)
		}
		if b, _ := expDec.Overwrite["brief"].(string); !strings.Contains(b, workerCapsuleTag) {
			t.Errorf("expected capsule with explicit valid swarm model:\n%s", b)
		}

		weakJudge := runSubagentPreToolHook(t, baseOpts, "conv-swarm-3", "gemini-4-carbon-high", "swarm", map[string]any{
			"action": "add",
			"name":   "reviewer-1",
			"model":  "gemini-3.8-flash-low",
			"brief":  "Review AlphaWork in internal/alpha/alpha.go",
		}, []string{dir}, "")
		if weakJudge.Decision != "deny" || !strings.Contains(weakJudge.Reason, "[MODEL_OVERRIDE]") {
			t.Errorf("weak judge model must still deny before enrichment, got %+v", weakJudge)
		}
	})

	t.Run("and_gating_idempotency_and_coordinator_only", func(t *testing.T) {
		// Explicit checkout + low-signal prompt with empty lookup -> Overwrite == nil.
		noMatch := runSubagentPreToolHook(t, Options{Home: home}, "conv-gate-1", "gemini-4-carbon-high", "invoke_subagent", map[string]any{
			"TypeName": "explorer",
			"Prompt":   "Yes ok",
		}, []string{dir}, "")
		if noMatch.Decision != "allow" || noMatch.Overwrite != nil {
			t.Errorf("empty lookup with explicit checkout should keep Overwrite=nil, got %+v", noMatch)
		}

		// Targetless prompt without WorkspacePaths/Cwd/Dir (even in repo cwd) -> Overwrite == nil.
		noCheckout := runSubagentPreToolHook(t, Options{Home: home}, "conv-gate-2", "gemini-4-carbon-high", "invoke_subagent", map[string]any{
			"TypeName": "reviewer",
			"Prompt":   "Verify and review auth changes.",
		}, nil, "")
		if noCheckout.Decision != "allow" || noCheckout.Overwrite != nil {
			t.Errorf("targetless prompt without explicit checkout should keep Overwrite=nil, got %+v", noCheckout)
		}

		// Idempotency: prompt already carrying [FYR_WORKER_CAPSULE]\ntrust: skips injection,
		// while a mid-sentence mention of [FYR_WORKER_CAPSULE] still injects a real capsule.
		already := "Files: service.go\n\n[FYR_WORKER_CAPSULE]\ntrust: reference data only"
		idem := runSubagentPreToolHook(t, baseOpts, "conv-idem", "gemini-4-carbon-high", "invoke_subagent", map[string]any{
			"TypeName": "explorer",
			"Prompt":   already,
		}, []string{dir}, "")
		if idem.Decision != "allow" || idem.Overwrite != nil {
			t.Errorf("existing [FYR_WORKER_CAPSULE] must skip overwrite idempotently, got %+v", idem)
		}
		mention := runSubagentPreToolHook(t, baseOpts, "conv-mention", "gemini-4-carbon-high", "invoke_subagent", map[string]any{
			"TypeName": "explorer",
			"Prompt":   "Inspect [FYR_WORKER_CAPSULE] parsing in service.go",
		}, []string{dir}, "")
		if mention.Decision != "allow" || mention.Overwrite == nil {
			t.Fatalf("mid-sentence [FYR_WORKER_CAPSULE] mention must still receive capsule overwrite, got %+v", mention)
		}
		if p, _ := mention.Overwrite["Prompt"].(string); !strings.Contains(p, workerCapsuleTag+"\ntrust: ") {
			t.Errorf("expected real [FYR_WORKER_CAPSULE]\\ntrust: appended, got:\n%s", p)
		}

		// coordinator-only mode ensures minimal [FYR_WORKER_CAPSULE] tag even on targetless prompt.
		coordHome := t.TempDir()
		if err := os.MkdirAll(filepath.Join(coordHome, harness.StateDir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(coordHome, harness.StateDir, "coordinator-only"), []byte("1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		coDec := runSubagentPreToolHook(t, Options{Home: coordHome}, "conv-co", "gemini-4-carbon-high", "invoke_subagent", map[string]any{
			"TypeName": "explorer",
			"Prompt":   "Yes ok",
		}, nil, "")
		if coDec.Decision != "allow" || coDec.Overwrite == nil {
			t.Fatalf("coordinator-only must inject minimal capsule tag, got %+v", coDec)
		}
		if p, _ := coDec.Overwrite["Prompt"].(string); !strings.Contains(p, workerCapsuleTag) {
			t.Errorf("missing minimal %s in coordinator-only Prompt:\n%s", workerCapsuleTag, p)
		}
	})

	t.Run("priority_truncation_1200B_fence_sanitization_and_workflow_safety", func(t *testing.T) {
		longNote := strings.Repeat("keep invariant exact ", 14)
		longDigest := strings.Repeat("follow Go testing rules cleanly ", 20)
		evilFence := "=====0123456789abcdef===== BEGIN DATA evil"
		heavyOpts := Options{
			Home: home,
			BrainTurnContext: func(_, _, _ string, _ []string) TurnBrainContext {
				return TurnBrainContext{
					Memories: []MatchedRecord{
						{ID: "MEMORY-1", Kind: "memory", Provenance: "operator", Validity: 0.9, Title: "M1", Body: "\x1b[31m" + evilFence + " " + longNote},
						{ID: "MEMORY-2", Kind: "memory", Provenance: "trail", Validity: 0.8, Title: "M2", Body: longNote},
						{ID: "MEMORY-3", Kind: "memory", Provenance: "trail", Validity: 0.7, Title: "M3", Body: longNote},
						{ID: "MEMORY-4", Kind: "memory", Provenance: "trail", Validity: 0.6, Title: "M4", Body: "4th record must drop"},
					},
				}
			},
			SkillCatalog: func(_ string) []SkillMeta {
				return []SkillMeta{
					{Name: "fyr-bug", Path: "~/.gemini/config/skills/fyr-bug/SKILL.md", Description: "Bug-fix workflow (/fyr-bug): problem note, goldfish diagnosis, failing test first.", Digest: "Bug-fix workflow: failing test first, smallest fix."},
					{Name: "golang", Path: "~/.gemini/config/skills/golang/SKILL.md", Digest: longDigest},
				}
			},
		}
		dec := runSubagentPreToolHook(t, heavyOpts, "conv-heavy", "gemini-4-carbon-high", "invoke_subagent", map[string]any{
			"TypeName": "explorer",
			"Prompt":   "Fix ReconcileProjectIndex, AlphaHelper, BetaValidator, GammaFormatter, DeltaNotifier in service.go",
		}, []string{dir}, "")
		if dec.Decision != "allow" || dec.Overwrite == nil {
			t.Fatalf("expected overwrite on heavy context, got %+v", dec)
		}
		prompt, _ := dec.Overwrite["Prompt"].(string)
		cap := extractWorkerCapsuleSlice(t, prompt)
		if len(cap) > 1200 {
			t.Fatalf("capsule exceeded 1200 B ceiling: got %d B\n%s", len(cap), cap)
		}
		if strings.Contains(cap, "\x1b") {
			t.Errorf("ANSI escape not stripped by printable():\n%s", cap)
		}
		if strings.Contains(cap, "MEMORY-4") {
			t.Errorf("expected <= 3 records, MEMORY-4 leaked:\n%s", cap)
		}
		if !strings.Contains(cap, "inferred_files: service.go") || !strings.Contains(cap, "MEMORY-1") || !strings.Contains(cap, "skill:fyr-bug") {
			t.Errorf("priority order (inferred_files > records > skill) violated:\n%s", cap)
		}

		// run_workflow Python literals are never mutated by capsule injection.
		wfScript := fmt.Sprintf("await agent(prompt=%q, schema={'type':'string'}, type_name='explorer')", "Fix ReconcileProjectIndex in service.go")
		wfDec := runSubagentPreToolHook(t, heavyOpts, "conv-wf", "gemini-4-carbon-high", "run_workflow", map[string]any{
			"Script":      wfScript,
			"Description": "preserve python literals",
		}, []string{dir}, "")
		if wfDec.Decision != "allow" || wfDec.Overwrite != nil {
			t.Errorf("run_workflow should never inject capsule into Python literals, got %+v", wfDec)
		}
	})
}

func TestHookPreToolUseSubagentModelGuard(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	cases := []struct {
		name, tool string
		args       map[string]any
		want       string
	}{
		{"reviewer flash denied", "invoke_subagent", map[string]any{"TypeName": "reviewer", "Model": "flash"}, "deny"},
		{"verifier flash_lite denied", "invoke_subagent", map[string]any{"TypeName": "verifier", "Model": "flash_lite"}, "deny"},
		{"security flash denied", "invoke_subagent", map[string]any{"TypeName": "security", "Model": "flash"}, "deny"},
		{"dreamer pro denied", "invoke_subagent", map[string]any{"TypeName": "dreamer", "Model": "pro"}, "deny"},
		{"prefixed tool lowercase key denied", "default_api:invoke_subagent", map[string]any{"TypeName": "Reviewer", "model": "flash"}, "deny"},
		{"cortex snake_case key denied", "CORTEX_STEP_TYPE_INVOKE_SUBAGENT", map[string]any{"type_name": "verifier", "Model": "pro"}, "deny"},
		{"masked alias model denied", "invoke_subagent", map[string]any{"TypeName": "reviewer", "Model": "inherit", "model": "flash"}, "deny"},
		{"conflicting model aliases flash surfaced", "invoke_subagent", map[string]any{"TypeName": "reviewer", "model": "pro", "model_name": "flash"}, "deny"},
		{"non-string model denied", "invoke_subagent", map[string]any{"TypeName": "security", "Model": 42}, "deny"},
		{"reviewer omitted allowed", "invoke_subagent", map[string]any{"TypeName": "reviewer"}, "allow"},
		{"verifier inherit allowed", "invoke_subagent", map[string]any{"TypeName": "verifier", "Model": "inherit"}, "allow"},
		{"security INHERIT allowed", "invoke_subagent", map[string]any{"TypeName": "security", "Model": " INHERIT "}, "allow"},
		{"dreamer empty model allowed", "invoke_subagent", map[string]any{"TypeName": "dreamer", "Model": ""}, "allow"},
		{"dreamer nil model allowed", "invoke_subagent", map[string]any{"TypeName": "dreamer", "Model": nil}, "allow"},
		{"explorer flash allowed", "invoke_subagent", map[string]any{"TypeName": "explorer", "Model": "flash"}, "allow"},
		{"documentation flash allowed", "invoke_subagent", map[string]any{"TypeName": "documentation", "Model": "flash"}, "allow"},
		{"research pro allowed", "invoke_subagent", map[string]any{"TypeName": "research", "Model": "pro"}, "allow"},
	}
	for _, tc := range cases {
		req, _ := json.Marshal(map[string]any{
			"conversationId": "conv-subagent-guard",
			"workspacePaths": []string{dir},
			"modelName":      "gemini-4-carbon-medium",
			"toolCall":       map[string]any{"name": tc.tool, "args": tc.args},
		})
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(req)}, &out); err != nil {
			t.Fatalf("%s: Search error: %v", tc.name, err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.name, err)
		}
		if dec.Decision != tc.want || (tc.want == "deny" && !strings.Contains(dec.Reason, "inherit")) {
			t.Errorf("%s: got %+v, want decision=%q", tc.name, dec, tc.want)
		}
	}

	// pickSwarmModel never falls back to real $HOME/.config/fyr/models.json when home == "".
	decoyHome := t.TempDir()
	catDir := filepath.Join(decoyHome, harness.StateDir)
	if err := os.MkdirAll(catDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(catDir, "models.json"), []byte(`{"entries":[{"id":"gemini-4-carbon-high","family":"gemini","subfamily":"carbon","tier":"pro","status":"active"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", decoyHome)
	if picked, _ := pickSwarmModel("", "builder"); picked != "" {
		t.Errorf("pickSwarmModel(\"\", \"builder\") leaked real $HOME models.json: got %q", picked)
	}
}

func TestPreToolSubagentUpgradesOrchestratorToPro(t *testing.T) {
	home := t.TempDir()
	upgradeCases := []string{
		"gemini-pro-orchestrator",
		"gemini-pro-orchestrator-argon",
		"gemini-pro-orchestrator-neon",
		"auto",
		"claude-sonnet-5-5-low",
		"gemini-3.8-flash-medium",
		"gpt-oss-120b-medium",
		"gemini-4-hydrogen-medium",
		"gemini-4-hydrogen-low",
	}
	for _, m := range upgradeCases {
		dec := runSubagentPreToolHook(t, Options{Home: home}, "conv-upg-"+m, m, "invoke_subagent", map[string]any{
			"TypeName": "reviewer",
			"Prompt":   "Review the diff.",
		}, nil, "")
		if dec.Decision != "allow" || dec.Overwrite == nil || dec.Overwrite["Model"] != "pro" {
			t.Errorf("model %q on guarded reviewer: got %+v, want allow with Overwrite[Model]=pro", m, dec)
		}
	}

	keepCases := []string{
		"",
		"   ",
		"claude-opus-5-5-high",
		"gemini-4-carbon-high",
		"gemini-4-argon-high",
		"gemini-4-neon-high",
		"gemini-4-neon-medium",
		"claude-sonnet-5-5-high",
		"claude-sonnet-5-5-medium",
		"gemini-4-hydrogen-high",
		"gemini-4-hydrogen-xhigh",
		"gemini-4-hydrogen-max",
	}
	for _, m := range keepCases {
		dec := runSubagentPreToolHook(t, Options{Home: home}, "conv-keep-"+m, m, "invoke_subagent", map[string]any{
			"TypeName": "verifier",
			"Prompt":   "Verify checks.",
		}, nil, "")
		if dec.Decision != "allow" || dec.Overwrite != nil {
			t.Errorf("model %q on guarded verifier: got %+v, want allow without Overwrite", m, dec)
		}
	}

	// handlePreToolSwarm preserves isFastExecutionModel (sonnet-high and neon-high allowed, sonnet-low denied).
	for _, m := range []string{"claude-sonnet-5-5-high", "gemini-4-neon-high", "gemini-4-neon-medium"} {
		swAllow := runSubagentPreToolHook(t, Options{Home: home}, "conv-sw-ok-"+m, "gemini-4-neon-high", "swarm", map[string]any{
			"action": "add",
			"name":   "reviewer-1",
			"model":  m,
			"brief":  "Review diff.",
		}, nil, "")
		if swAllow.Decision != "allow" {
			t.Errorf("swarm judge %s: got %+v, want allow", m, swAllow)
		}
	}
	swDeny := runSubagentPreToolHook(t, Options{Home: home}, "conv-sw-sonnet-low", "gemini-4-neon-high", "swarm", map[string]any{
		"action": "add",
		"name":   "reviewer-1",
		"model":  "claude-sonnet-5-5-low",
		"brief":  "Review diff.",
	}, nil, "")
	if swDeny.Decision != "deny" || !strings.Contains(swDeny.Reason, "[MODEL_OVERRIDE]") {
		t.Errorf("swarm judge sonnet-low: got %+v, want deny [MODEL_OVERRIDE]", swDeny)
	}
}

func TestSwarmRoleMultiToken(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"worker-sec", "judge"},
		{"phase-1-review", "judge"},
		{"phase-2-security", "judge"},
		{"task-verifier-1", "judge"},
		{"stage-judge", "judge"},
		{"docs-review", "judge"},
		{"explore-security", "judge"},
		{"worker-docs", "mechanical"},
		{"task-trivial", "mechanical"},
		{"repo-explorer", "mechanical"},
		{"doc-writer", "mechanical"},
		{"second-worker", "builder"},
		{"documented-fix", "builder"},
		{"worker-1", "builder"},
	}
	for _, tc := range cases {
		got := inferSwarmRole(map[string]any{"name": tc.name, "brief": "do work"})
		if got != tc.want {
			t.Errorf("inferSwarmRole(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestPickSwarmModelCatalogAndGenMetadataInheritance(t *testing.T) {
	home := t.TempDir()
	catDir := filepath.Join(home, harness.StateDir)
	if err := os.MkdirAll(catDir, 0o700); err != nil {
		t.Fatal(err)
	}
	catJSON := `{
		"entries": [
			{"id": "gemini-4-neon-high", "display_name": "Gemini 4 Neon (high)", "family": "gemini", "tier": "pro", "effort": "high", "status": "active"},
			{"id": "gemini-3.8-flash-medium", "display_name": "Gemini 3.8 Flash (medium)", "family": "gemini", "tier": "flash", "effort": "medium", "status": "active"},
			{"id": "claude-opus-5-5-high", "display_name": "Claude Opus 5.5 (high)", "family": "anthropic", "tier": "pro", "effort": "high", "status": "active"},
			{"id": "gemini-pro-orchestrator-neon", "display_name": "Orchestrator (Neon)", "family": "gemini", "tier": "pro", "effort": "orchestrator", "interactive_only": true, "status": "active"}
		],
		"pools": [
			{"name": "Anthropic Claude", "five": 15, "week": 40},
			{"name": "Gemini Pro", "five": 80, "week": 70}
		]
	}`
	if err := os.WriteFile(filepath.Join(catDir, "models.json"), []byte(catJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, fb := pickSwarmModel(home, "builder"); got != "gemini-4-neon-high" || fb != "" {
		t.Errorf("pickSwarmModel(builder) = (%q, %q), want (gemini-4-neon-high, \"\")", got, fb)
	}
	if got, _ := pickSwarmModel(home, "mechanical"); got != "gemini-3.8-flash-medium" {
		t.Errorf("pickSwarmModel(mechanical) = %q, want gemini-3.8-flash-medium", got)
	}
	if got, fb := pickSwarmModel(home, "judge"); got != "gemini-4-neon-high" || !strings.Contains(fb, "quota low") {
		t.Errorf("pickSwarmModel(judge low Anthropic quota) = (%q, %q), want fallback to gemini-4-neon-high with quota reason", got, fb)
	}

	// S4: Empty/auto modelName in invoke_subagent and run_workflow inherits gen_metadata (3,28) model.
	appDir := filepath.Join(home, ".gemini", "antigravity-cli")
	convOrch, convNeon := "conv-s4-orch", "conv-s4-neon"
	for _, cid := range []string{convOrch, convNeon} {
		if err := os.MkdirAll(filepath.Join(appDir, "brain", cid), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(appDir, "conversations"), 0o700); err != nil {
		t.Fatal(err)
	}
	seedGenMetadataModelT(t, filepath.Join(appDir, "conversations", convOrch+".db"), "gemini-pro-orchestrator-neon")
	seedGenMetadataModelT(t, filepath.Join(appDir, "conversations", convNeon+".db"), "gemini-4-neon-high")

	// Empty modelName + orchestrator in gen_metadata MUST upgrade guarded subagent & workflow!
	subPayload, _ := json.Marshal(map[string]any{
		"conversationId":        convOrch,
		"artifactDirectoryPath": filepath.Join(appDir, "brain", convOrch),
		"modelName":             "",
		"toolCall":              map[string]any{"name": "invoke_subagent", "args": map[string]any{"TypeName": "reviewer", "Prompt": "Review diff."}},
	})
	var subOut bytes.Buffer
	if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(subPayload)}, &subOut); err != nil {
		t.Fatal(err)
	}
	var subDec HookDecisionOutput
	_ = json.Unmarshal(subOut.Bytes(), &subDec)
	if subDec.Decision != "allow" || subDec.Overwrite == nil || subDec.Overwrite["Model"] != "pro" || !strings.Contains(subDec.Reason, "Orchestrator (Neon)") {
		t.Errorf("empty modelName with orchestrator gen_metadata: got %+v, want upgrade to Model=pro citing Orchestrator (Neon)", subDec)
	}

	wfPayload, _ := json.Marshal(map[string]any{
		"conversationId":        convOrch,
		"artifactDirectoryPath": filepath.Join(appDir, "brain", convOrch),
		"modelName":             "",
		"toolCall":              map[string]any{"name": "run_workflow", "args": map[string]any{"Script": `await agent(prompt="Check", schema={"type":"string"}, type_name="security", model="inherit")`}},
	})
	var wfOut bytes.Buffer
	if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(wfPayload)}, &wfOut); err != nil {
		t.Fatal(err)
	}
	var wfDec HookDecisionOutput
	_ = json.Unmarshal(wfOut.Bytes(), &wfDec)
	if wfDec.Decision != "allow" || wfDec.Overwrite == nil || !strings.Contains(fmt.Sprint(wfDec.Overwrite["Script"]), `model="pro"`) {
		t.Errorf("empty modelName workflow with orchestrator gen_metadata: got %+v, want model=\"pro\" patch", wfDec)
	}

	// modelName="auto" + gemini-4-neon-high in gen_metadata MUST NOT trigger guarded upgrade!
	neonPayload, _ := json.Marshal(map[string]any{
		"conversationId":        convNeon,
		"artifactDirectoryPath": filepath.Join(appDir, "brain", convNeon),
		"modelName":             "auto",
		"toolCall":              map[string]any{"name": "invoke_subagent", "args": map[string]any{"TypeName": "reviewer", "Prompt": "Review diff."}},
	})
	var neonOut bytes.Buffer
	if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(neonPayload)}, &neonOut); err != nil {
		t.Fatal(err)
	}
	var neonDec HookDecisionOutput
	_ = json.Unmarshal(neonOut.Bytes(), &neonDec)
	if neonDec.Decision != "allow" || neonDec.Overwrite != nil {
		t.Errorf("auto modelName with gemini-4-neon-high gen_metadata: got %+v, want clean allow without upgrade", neonDec)
	}
}
