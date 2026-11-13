package search

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func runPreToolHookForTest(t *testing.T, home, modelName, toolName string, args map[string]any) HookDecisionOutput {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"conversationId": "conv-overwrite-test",
		"modelName":      modelName,
		"toolCall": map[string]any{
			"name": toolName,
			"args": args,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RunHook(Options{Home: home, Hook: "pre-tool-use"}, bytes.NewReader(payload), &out); err != nil {
		t.Fatalf("RunHook pre-tool-use: %v", err)
	}
	var dec HookDecisionOutput
	if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
		t.Fatalf("unmarshal HookDecisionOutput %q: %v", out.String(), err)
	}
	return dec
}

func TestPreToolUseSubagentAutoUpgradeAndSwarmModelFill(t *testing.T) {
	home := t.TempDir()
	// 1. Guarded subagent on fast session model with omitted Model -> allow + overwrite Model="pro" preserving all args.
	dec := runPreToolHookForTest(t, home, "gemini-3.8-flash-medium", "invoke_subagent", map[string]any{
		"TypeName":  "reviewer",
		"Role":      "Code Reviewer",
		"Prompt":    "Review the diff.",
		"Workspace": "inherit",
		"model":     "inherit",
	})
	if dec.Decision != "allow" || dec.Overwrite == nil {
		t.Fatalf("expected allow+overwrite on fast model guarded subagent, got %+v", dec)
	}
	if dec.Overwrite["Model"] != "pro" {
		t.Errorf("overwrite Model = %v, want pro", dec.Overwrite["Model"])
	}
	if _, hasLower := dec.Overwrite["model"]; hasLower {
		t.Errorf("lowercase model key should be cleaned from overwrite map: %+v", dec.Overwrite)
	}
	for k, want := range map[string]string{"TypeName": "reviewer", "Role": "Code Reviewer", "Prompt": "Review the diff.", "Workspace": "inherit"} {
		if dec.Overwrite[k] != want {
			t.Errorf("overwrite[%q] = %v, want %q", k, dec.Overwrite[k], want)
		}
	}

	// 2. Guarded subagent on Carbon or Orchestrator -> allow with NO overwrite.
	for _, m := range []string{"gemini-4-carbon-high", "gemini-pro-orchestrator"} {
		d := runPreToolHookForTest(t, home, m, "invoke_subagent", map[string]any{"TypeName": "verifier", "Prompt": "Run tests."})
		if d.Decision != "allow" || d.Overwrite != nil {
			t.Errorf("model %q should allow without overwrite, got %+v", m, d)
		}
	}

	// 3. Explicit non-inherit override on guarded subagent -> still deny.
	denied := runPreToolHookForTest(t, home, "gemini-3.8-flash-medium", "invoke_subagent", map[string]any{
		"TypeName": "security",
		"Model":    "flash",
	})
	if denied.Decision != "deny" || !strings.Contains(denied.Reason, "[MODEL_OVERRIDE]") {
		t.Errorf("explicit override must still deny, got %+v", denied)
	}

	// 4. Swarm action=add with omitted model reads <home>/.config/fyr/models.json and fills active model.
	catDir := filepath.Join(home, harness.StateDir)
	if err := os.MkdirAll(catDir, 0o700); err != nil {
		t.Fatal(err)
	}
	catJSON := `{"entries":[
		{"id":"gemini-4-carbon-high","family":"gemini","subfamily":"carbon","tier":"pro","status":"active"},
		{"id":"gemini-4-hydrogen-medium","family":"gemini","subfamily":"hydrogen","tier":"flash","status":"active"},
		{"id":"claude-opus-5-5-high","family":"anthropic","subfamily":"opus","tier":"pro","status":"active"},
		{"id":"gpt-oss-120b-medium","family":"openai","tier":"flash","status":"active","retiring_on":"2026-11-18"}
	]}`
	if err := os.WriteFile(filepath.Join(catDir, "models.json"), []byte(catJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	swBuild := runPreToolHookForTest(t, home, "gemini-4-hydrogen-medium", "default_api:swarm", map[string]any{
		"action": "add",
		"name":   "worker-1",
		"brief":  "Implement caching helper.",
	})
	if swBuild.Decision != "allow" || swBuild.Overwrite == nil || swBuild.Overwrite["model"] != "gemini-4-carbon-high" {
		t.Errorf("swarm builder pick = %+v, want overwrite model=gemini-4-carbon-high", swBuild)
	}
	if swBuild.Overwrite["name"] != "worker-1" || swBuild.Overwrite["action"] != "add" {
		t.Errorf("swarm overwrite dropped original args: %+v", swBuild.Overwrite)
	}
	swRev := runPreToolHookForTest(t, home, "gemini-4-carbon-high", "swarm", map[string]any{
		"action": "add",
		"name":   "reviewer",
		"brief":  "Verify and review auth changes.",
		"model":  "auto",
	})
	if swRev.Decision != "allow" || swRev.Overwrite == nil || swRev.Overwrite["model"] != "claude-opus-5-5-high" {
		t.Errorf("swarm judge/reviewer pick = %+v, want overwrite model=claude-opus-5-5-high", swRev)
	}
	swWeak := runPreToolHookForTest(t, home, "gemini-4-carbon-high", "swarm", map[string]any{
		"action": "add",
		"name":   "security-audit",
		"brief":  "Check auth endpoints.",
		"model":  "gemini-3.8-flash-low",
	})
	if swWeak.Decision != "deny" || !strings.Contains(swWeak.Reason, "[MODEL_OVERRIDE]") {
		t.Errorf("swarm weak override on security task must deny, got %+v", swWeak)
	}
}

func TestPreInvocationModelSuggestorSessionDedupAndOrchestratorQuiet(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte("turn 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runTurn := func(convID, model, prompt string) string {
		inJSON, _ := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"transcriptPath": transcript,
			"modelName":      model,
			"invocationNum":  1,
			"lastUserInput":  prompt,
		})
		var out bytes.Buffer
		if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(inJSON)}, &out); err != nil {
			t.Fatalf("Search pre-invocation: %v", err)
		}
		var resp PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.InjectSteps[0].SystemMessage.SystemMessage
	}

	// Turn 1 on fast model + Feature -> emits 1-line model tip.
	sys1 := runTurn("conv-nudge", "gemini-3.8-flash-medium", "Implement distributed locking for service.go")
	if !strings.Contains(sys1, "model tip:") {
		t.Errorf("Turn 1 on flash feature should emit model tip:\n%s", sys1)
	}
	// Turn 2 in same session -> suppressed via read_ledger sentinel.
	if err := os.WriteFile(transcript, []byte("turn 1\nturn 2 grew\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sys2 := runTurn("conv-nudge", "gemini-3.8-flash-medium", "Add design options for multi-phase cache invalidation")
	if strings.Contains(sys2, "model tip:") {
		t.Errorf("Turn 2 in same generation should suppress duplicate model tip:\n%s", sys2)
	}
	// Orchestrator model -> stays quiet.
	sysOrch := runTurn("conv-orch", "gemini-pro-orchestrator", "Implement distributed locking for service.go")
	if strings.Contains(sysOrch, "model tip:") {
		t.Errorf("Orchestrator session should never emit model tip:\n%s", sysOrch)
	}
}

func TestInferSwarmRoleAndEffortWeight(t *testing.T) {
	roleCases := []struct {
		name  string
		brief string
		want  string
	}{
		{name: "second-worker", brief: "update docker runner section 3", want: "builder"},
		{name: "documented-fix", brief: "handle docker socket", want: "builder"},
		{name: "sec-audit", brief: "check auth boundaries", want: "judge"},
		{name: "verifier-1", brief: "run tests", want: "judge"},
		{name: "doc-writer", brief: "update README docs", want: "mechanical"},
		{name: "repo-explorer", brief: "scan internal packages", want: "mechanical"},
	}
	for _, tc := range roleCases {
		got := inferSwarmRole(map[string]any{"name": tc.name, "brief": tc.brief})
		if got != tc.want {
			t.Errorf("inferSwarmRole(name=%q, brief=%q) = %q, want %q", tc.name, tc.brief, got, tc.want)
		}
	}
	if !(effortWeight("m-max") > effortWeight("m-xhigh") &&
		effortWeight("m-xhigh") > effortWeight("m-high") &&
		effortWeight("m-high") > effortWeight("m-plain") &&
		effortWeight("m-plain") > effortWeight("m-medium") &&
		effortWeight("m-medium") > effortWeight("m-low")) {
		t.Errorf("effortWeight order broken: max=%d xhigh=%d high=%d plain=%d med=%d low=%d",
			effortWeight("m-max"), effortWeight("m-xhigh"), effortWeight("m-high"),
			effortWeight("m-plain"), effortWeight("m-medium"), effortWeight("m-low"))
	}
}
