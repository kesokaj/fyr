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

	// 5. Swarm 20%/30% quota fallback on judge role reads pools from models.json.
	writeQuotaCat := func(five, week int) {
		qJSON := fmt.Sprintf(`{"entries":[
			{"id":"gemini-4-carbon-high","family":"gemini","subfamily":"carbon","tier":"pro","status":"active"},
			{"id":"claude-opus-5-5-high","family":"anthropic","subfamily":"opus","tier":"pro","status":"active"}
		],"pools":[{"name":"Claude and GPT models","five":%d,"week":%d}]}`, five, week)
		if err := os.WriteFile(filepath.Join(catDir, "models.json"), []byte(qJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeQuotaCat(25, 50)
	if d := runPreToolHookForTest(t, home, "gemini-4-carbon-high", "swarm", map[string]any{"action": "add", "name": "reviewer-1", "brief": "diff"}); d.Decision != "allow" || d.Overwrite == nil || d.Overwrite["model"] != "gemini-4-carbon-high" {
		t.Errorf("5h < 30%% expected Gemini fallback, got %+v", d)
	}
	writeQuotaCat(50, 15)
	if d := runPreToolHookForTest(t, home, "gemini-4-carbon-high", "swarm", map[string]any{"action": "add", "name": "verifier-1", "brief": "e2e"}); d.Decision != "allow" || d.Overwrite == nil || d.Overwrite["model"] != "gemini-4-carbon-high" {
		t.Errorf("week < 20%% expected Gemini fallback, got %+v", d)
	}
	writeQuotaCat(35, 25)
	if d := runPreToolHookForTest(t, home, "gemini-4-carbon-high", "swarm", map[string]any{"action": "add", "name": "reviewer-2", "brief": "diff"}); d.Decision != "allow" || d.Overwrite == nil || d.Overwrite["model"] != "claude-opus-5-5-high" {
		t.Errorf("healthy 35%%/25%% expected claude-opus-5-5-high, got %+v", d)
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

	// Turn 1 on fast model + Feature -> emits JSON modelTip field.
	sys1 := runTurn("conv-nudge", "gemini-3.8-flash-medium", "Implement distributed locking for service.go")
	if !strings.Contains(sys1, `"modelTip":`) {
		t.Errorf("Turn 1 on flash feature should emit modelTip:\n%s", sys1)
	}
	// Turn 2 in same session -> suppressed via read_ledger sentinel.
	if err := os.WriteFile(transcript, []byte("turn 1\nturn 2 grew\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sys2 := runTurn("conv-nudge", "gemini-3.8-flash-medium", "Add design options for multi-phase cache invalidation")
	if strings.Contains(sys2, `"modelTip":`) {
		t.Errorf("Turn 2 in same generation should suppress duplicate modelTip:\n%s", sys2)
	}
	// Orchestrator model -> stays quiet.
	sysOrch := runTurn("conv-orch", "gemini-pro-orchestrator", "Implement distributed locking for service.go")
	if strings.Contains(sysOrch, `"modelTip":`) {
		t.Errorf("Orchestrator session should never emit modelTip:\n%s", sysOrch)
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
		{name: "worker-1", brief: "security review and verifier checks", want: "builder"},
		{name: "repo-explorer", brief: "scan internal packages", want: "builder"},
		{name: "sec-audit", brief: "check auth boundaries", want: "judge"},
		{name: "verifier-1", brief: "run tests", want: "judge"},
		{name: "doc-writer", brief: "update README docs", want: "mechanical"},
		{name: "explorer-repo", brief: "scan internal packages", want: "mechanical"},
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

func TestPreToolUseRunWorkflowGuard(t *testing.T) {
	home := t.TempDir()
	fastModel := "gemini-3.8-flash-medium"
	proModel := "gemini-4-carbon-high"

	// 1. Guarded subagents forbid model="flash"/"flash_lite" even on pro session.
	for _, mTier := range []string{`"flash"`, `"flash_lite"`} {
		script := fmt.Sprintf("await parallel(files, lambda f: agent(prompt=f, type_name='reviewer', model=%s, schema={'type':'string'}))", mTier)
		d := runPreToolHookForTest(t, home, proModel, "run_workflow", map[string]any{"Script": script, "Description": "review"})
		if d.Decision != "deny" || !strings.Contains(d.Reason, "[MODEL_OVERRIDE]") {
			t.Errorf("model=%s expected [MODEL_OVERRIDE] deny, got %+v", mTier, d)
		}
	}

	// 2. Fast session rewrites model="inherit" to "pro" in inline Script while keeping other args intact.
	inheritScript := "a = await agent(prompt='Check', schema={'type':'string'}, type_name='security', model='inherit')\nb = await agent(prompt='Scan', schema={'type':'string'}, type_name='explorer', model='flash')\n"
	upg := runPreToolHookForTest(t, home, fastModel, "run_workflow", map[string]any{"Script": inheritScript, "Description": "audit", "WaitMsBeforeAsync": 5000})
	if upg.Decision != "allow" || upg.Overwrite == nil {
		t.Fatalf("fast session model=inherit expected allow+overwrite, got %+v", upg)
	}
	rewritten, _ := upg.Overwrite["Script"].(string)
	if !strings.Contains(rewritten, `model="pro"`) || !strings.Contains(rewritten, `type_name='explorer', model='flash'`) {
		t.Errorf("unexpected rewritten Script:\n%s", rewritten)
	}
	if upg.Overwrite["Description"] != "audit" {
		t.Errorf("overwrite dropped Description: %+v", upg.Overwrite)
	}

	// 3. Fast session denies omitted model on guarded subagent with [AUTO_SUBAGENT_PRO].
	omittedScript := "await agent(prompt='Review', schema={'type':'string'}, type_name='verifier')"
	denyOmitted := runPreToolHookForTest(t, home, fastModel, "run_workflow", map[string]any{"Script": omittedScript, "Description": "verify"})
	if denyOmitted.Decision != "deny" || !strings.Contains(denyOmitted.Reason, "[AUTO_SUBAGENT_PRO]") {
		t.Errorf("fast session omitted model expected [AUTO_SUBAGENT_PRO] deny, got %+v", denyOmitted)
	}

	// 4. Pro session allows omitted model, and fast session allows explicit model="pro".
	if d := runPreToolHookForTest(t, home, proModel, "run_workflow", map[string]any{"Script": omittedScript, "Description": "verify"}); d.Decision != "allow" || d.Overwrite != nil {
		t.Errorf("pro session omitted model expected clean allow, got %+v", d)
	}
	explicitPro := "await agent(prompt='Review', schema={'type':'string'}, type_name='reviewer', model='pro')"
	if d := runPreToolHookForTest(t, home, fastModel, "run_workflow", map[string]any{"Script": explicitPro, "Description": "review"}); d.Decision != "allow" || d.Overwrite != nil {
		t.Errorf("fast session explicit model='pro' expected clean allow, got %+v", d)
	}

	// 5. ScriptPath with flash override denies [MODEL_OVERRIDE]; inherit on fast denies [AUTO_SUBAGENT_PRO].
	wfDir := t.TempDir()
	spFlash := filepath.Join(wfDir, "bad.py")
	if err := os.WriteFile(spFlash, []byte("agent('p', {'type':'string'}, 'r', 'bugs', 'inherit', 'flash')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if d := runPreToolHookForTest(t, home, proModel, "run_workflow", map[string]any{"ScriptPath": spFlash, "Description": "bugs"}); d.Decision != "deny" || !strings.Contains(d.Reason, "[MODEL_OVERRIDE]") {
		t.Errorf("positional flash ScriptPath expected [MODEL_OVERRIDE] deny, got %+v", d)
	}
	spInherit := filepath.Join(wfDir, "inh.py")
	if err := os.WriteFile(spInherit, []byte("await agent(prompt='p=1', schema={'type':'string'}, type_name='dreamer', model='inherit')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if d := runPreToolHookForTest(t, home, fastModel, "run_workflow", map[string]any{"ScriptPath": spInherit, "Description": "dream"}); d.Decision != "deny" || !strings.Contains(d.Reason, "[AUTO_SUBAGENT_PRO]") {
		t.Errorf("fast ScriptPath inherit expected [AUTO_SUBAGENT_PRO] deny, got %+v", d)
	}

	// 6. Symlink or >1 MiB ScriptPath fails closed with [MODEL_OVERRIDE]; missing file still allows.
	spLink := filepath.Join(wfDir, "link.py")
	if err := os.Symlink(spInherit, spLink); err != nil {
		t.Fatal(err)
	}
	if d := runPreToolHookForTest(t, home, proModel, "run_workflow", map[string]any{"ScriptPath": spLink, "Description": "link"}); d.Decision != "deny" || !strings.Contains(d.Reason, "[MODEL_OVERRIDE]") {
		t.Errorf("symlink ScriptPath expected [MODEL_OVERRIDE] deny, got %+v", d)
	}
	spBig := filepath.Join(wfDir, "big.py")
	if err := os.WriteFile(spBig, make([]byte, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if d := runPreToolHookForTest(t, home, proModel, "run_workflow", map[string]any{"ScriptPath": spBig, "Description": "big"}); d.Decision != "deny" || !strings.Contains(d.Reason, "[MODEL_OVERRIDE]") {
		t.Errorf("oversize ScriptPath expected [MODEL_OVERRIDE] deny, got %+v", d)
	}
	if d := runPreToolHookForTest(t, home, proModel, "run_workflow", map[string]any{"ScriptPath": filepath.Join(wfDir, "missing.py"), "Description": "missing"}); d.Decision != "allow" {
		t.Errorf("missing ScriptPath expected allow, got %+v", d)
	}

	// 7. Python comment trivia between agent and '(' or around kwargs never evades guard.
	commentScript := `agent # comment bypass
(
  # leading comment
  "Role",
  "Prompt",
  type_name # inline
  = # value
  "reviewer" # trail
  ,
  model # inline
  = # value
  "flash" # trail
)`
	if d := runPreToolHookForTest(t, home, proModel, "run_workflow", map[string]any{"Script": commentScript, "Description": "comment-bypass"}); d.Decision != "deny" || !strings.Contains(d.Reason, "[MODEL_OVERRIDE]") {
		t.Errorf("comment-trivia bypass expected [MODEL_OVERRIDE] deny, got %+v", d)
	}
	commentInherit := `agent # note
(type_name="verifier", prompt="p", role="V", model = "inherit" # trail
)`
	commentUpg := runPreToolHookForTest(t, home, fastModel, "run_workflow", map[string]any{"Script": commentInherit, "Description": "comment-upgrade"})
	if commentUpg.Decision != "allow" || commentUpg.Overwrite == nil {
		t.Fatalf("fast session with comment-trivia inherit expected overwrite upgrade, got %+v", commentUpg)
	}
}
