package search

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestSubagentFlashGuardAndCrossFamilyNotice(t *testing.T) {
	home := t.TempDir()
	catDir := filepath.Join(home, harness.StateDir)
	if err := os.MkdirAll(catDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCat := func(five, week int, includeClaude bool) {
		t.Helper()
		entries := `[{"id":"gemini-4-carbon-high","display_name":"Gemini 4 Carbon (high)","family":"gemini","subfamily":"carbon","tier":"pro","effort":"high","status":"active"}`
		if includeClaude {
			entries += `,{"id":"claude-opus-5-5-high","display_name":"Claude Opus 5.5 (high)","family":"anthropic","subfamily":"opus","tier":"pro","effort":"high","status":"active"}`
		}
		entries += `]`
		raw := `{"entries":` + entries + `,"pools":[{"name":"Claude and GPT models","five":` + itoaT(five) + `,"week":` + itoaT(week) + `}]}`
		if err := os.WriteFile(filepath.Join(catDir, "models.json"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeCat(75, 65, true)

	// 1. All guarded roles (including judge and self+guarded Role) deny flash/flash_lite.
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"judge flash", map[string]any{"TypeName": "judge", "Model": "flash", "Prompt": "Audit diff."}},
		{"judge flash_lite", map[string]any{"TypeName": "judge", "Model": "flash_lite", "Prompt": "Audit diff."}},
		{"reviewer flash", map[string]any{"TypeName": "reviewer", "Model": "flash", "Prompt": "Review diff."}},
		{"security flash_lite", map[string]any{"TypeName": "security", "Model": "flash_lite", "Prompt": "Check auth."}},
		{"verifier flash", map[string]any{"TypeName": "verifier", "Model": "flash", "Prompt": "Run e2e."}},
		{"dreamer flash_lite", map[string]any{"TypeName": "dreamer", "Model": "flash_lite", "Prompt": "Consolidate."}},
		{"self disguised as judge flash", map[string]any{"TypeName": "self", "Role": "Cross-Family Judge", "Model": "flash", "Prompt": "Judge diff."}},
	} {
		dec := runPreToolHookForTest(t, home, "gemini-4-carbon-high", "invoke_subagent", tc.args)
		if dec.Decision != "deny" || !strings.Contains(dec.Reason, "[MODEL_OVERRIDE]") {
			t.Errorf("%s: got %+v, want deny [MODEL_OVERRIDE]", tc.name, dec)
		}
	}

	// Non-guarded self builder with flash stays allowed.
	if dec := runPreToolHookForTest(t, home, "gemini-4-carbon-high", "invoke_subagent", map[string]any{
		"TypeName": "self", "Role": "Wave 2 Builder", "Model": "flash", "Prompt": "Build slice.",
	}); dec.Decision != "allow" {
		t.Errorf("self builder with flash should allow, got %+v", dec)
	}

	// Swarm dreamer with weak/flash model denies [MODEL_OVERRIDE].
	if dec := runPreToolHookForTest(t, home, "gemini-4-carbon-high", "swarm", map[string]any{
		"action": "add", "name": "dreamer-1", "model": "flash_lite", "brief": "Consolidate memory.",
	}); dec.Decision != "deny" || !strings.Contains(dec.Reason, "[MODEL_OVERRIDE]") {
		t.Errorf("swarm dreamer flash_lite: got %+v, want deny [MODEL_OVERRIDE]", dec)
	}

	// 2. Healthy Claude judge in models.json emits [CROSS_FAMILY_NOTICE] on Gemini session (< 20ms, no Overwrite).
	start := time.Now()
	noticeDec := runPreToolHookForTest(t, home, "gemini-4-carbon-high", "invoke_subagent", map[string]any{
		"TypeName": "reviewer", "Prompt": "Review the diff.",
	})
	elapsed := time.Since(start)
	if noticeDec.Decision != "allow" || noticeDec.Overwrite != nil {
		t.Fatalf("Gemini reviewer with healthy Claude pick: got %+v, want allow with nil Overwrite", noticeDec)
	}
	for _, want := range []string{"[CROSS_FAMILY_NOTICE]", "claude-opus-5-5-high", "workflows/review_parallel.py", "fyr gate --judge", "swarm"} {
		if !strings.Contains(noticeDec.Reason, want) {
			t.Errorf("cross-family notice missing %q: %q", want, noticeDec.Reason)
		}
	}
	if elapsed > 20*time.Millisecond {
		t.Logf("note: hook took %v (expected < 20ms steady state)", elapsed)
	}

	// Fast session upgrades to Model="pro" AND still includes [CROSS_FAMILY_NOTICE].
	upgDec := runPreToolHookForTest(t, home, "gemini-3.8-flash-medium", "invoke_subagent", map[string]any{
		"TypeName": "judge", "Prompt": "Judge changes.",
	})
	if upgDec.Decision != "allow" || upgDec.Overwrite == nil || upgDec.Overwrite["Model"] != "pro" {
		t.Fatalf("fast session judge: got %+v, want allow+Overwrite[Model]=pro", upgDec)
	}
	if !strings.Contains(upgDec.Reason, "[AUTO_MODEL_UPGRADE]") || !strings.Contains(upgDec.Reason, "[CROSS_FAMILY_NOTICE]") {
		t.Errorf("fast session judge reason missing upgrade+cross-family notice: %q", upgDec.Reason)
	}

	// 3. Session already on Anthropic Pro (unupgraded) does NOT emit cross-family notice.
	claudeSess := runPreToolHookForTest(t, home, "claude-opus-5-5-high", "invoke_subagent", map[string]any{
		"TypeName": "reviewer", "Prompt": "Review diff.",
	})
	if claudeSess.Decision != "allow" || strings.Contains(claudeSess.Reason, "[CROSS_FAMILY_NOTICE]") {
		t.Errorf("Anthropic Pro session should not emit cross-family notice: %+v", claudeSess)
	}

	// 4. Low Claude quota (< 30% 5h or < 20% week) suppresses cross-family notice.
	writeCat(20, 50, true)
	lowQuota := runPreToolHookForTest(t, home, "gemini-4-carbon-high", "invoke_subagent", map[string]any{
		"TypeName": "reviewer", "Prompt": "Review diff.",
	})
	if lowQuota.Decision != "allow" || strings.Contains(lowQuota.Reason, "[CROSS_FAMILY_NOTICE]") {
		t.Errorf("low Anthropic quota should suppress cross-family notice: %+v", lowQuota)
	}
}

func itoaT(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestPreToolUseSoftSearchNudgeGrepAndBash(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()

	callPre := func(convID, tool string, args map[string]any) HookDecisionOutput {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"toolCall":       map[string]any{"name": tool, "args": args},
		})
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(payload)}, &out); err != nil {
			t.Fatalf("pre-tool-use (%s): %v", tool, err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal %q: %v", out.String(), err)
		}
		return dec
	}

	// 1. Before indexing (no search.db / 0 symbols) -> no nudge on either tool or bash grep.
	if d := callPre("conv-unindexed", "grep_search", map[string]any{"Query": "ReconcileProjectIndex", "SearchPath": dir}); d.Decision != "allow" || d.Reason != "" {
		t.Fatalf("unindexed grep_search should stay quiet allow, got %+v", d)
	}
	if d := callPre("conv-unindexed", "run_command", map[string]any{"CommandLine": "rg ReconcileProjectIndex service.go"}); d.Decision != "allow" || d.Reason != "" {
		t.Fatalf("unindexed bash rg should stay quiet allow, got %+v", d)
	}

	// Index the repo so search.db has symbols (ReconcileProjectIndex, AlphaHelper, etc.).
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// 2. Built-in grep_search on indexed symbol -> soft allow nudge (NEVER deny!), deduped on 2nd call in same turn.
	d1 := callPre("conv-nudge-grep", "grep_search", map[string]any{"Query": "ReconcileProjectIndex", "SearchPath": dir})
	if d1.Decision != "allow" || !strings.Contains(d1.Reason, "[SEARCH_NUDGE]") || !strings.Contains(d1.Reason, "fyr search --refs ReconcileProjectIndex") {
		t.Fatalf("indexed grep_search = %+v, want allow + [SEARCH_NUDGE]", d1)
	}
	d2 := callPre("conv-nudge-grep", "Grep", map[string]any{"pattern": "AlphaHelper", "path": dir})
	if d2.Decision != "allow" || d2.Reason != "" {
		t.Fatalf("2nd grep on same turn should be deduped, got %+v", d2)
	}

	// 3. Bash rg / grep targeting code symbols (including || true and export PATH prefix) -> soft allow nudge.
	dBash := callPre("conv-nudge-bash", "run_command", map[string]any{
		"CommandLine": "export PATH=\"$HOME/.local/bin:$PATH\"\nrg -n ReconcileProjectIndex service.go || true",
	})
	if dBash.Decision != "allow" || !strings.Contains(dBash.Reason, "[SEARCH_NUDGE]") || !strings.Contains(dBash.Reason, "fyr search --slice ReconcileProjectIndex") {
		t.Fatalf("indexed bash rg = %+v, want allow + [SEARCH_NUDGE]", dBash)
	}

	// 4. Non-noisy must-skip cases: non-code paths (.agents/scratch/, logs, .md), regex/prose patterns, piped stdout grep.
	for _, tc := range []struct {
		name string
		tool string
		args map[string]any
	}{
		{"scratch path", "grep_search", map[string]any{"Query": "ReconcileProjectIndex", "SearchPath": ".agents/scratch/run.log"}},
		{"markdown path", "run_command", map[string]any{"CommandLine": "rg ReconcileProjectIndex README.md"}},
		{"complex regex", "grep_search", map[string]any{"Query": "foo.*bar|baz", "SearchPath": dir}},
		{"short prose word", "run_command", map[string]any{"CommandLine": "grep error service.go"}},
		{"piped stdout filter", "run_command", map[string]any{"CommandLine": "go test ./... | grep ReconcileProjectIndex"}},
	} {
		d := callPre("conv-skip-"+tc.name, tc.tool, tc.args)
		if d.Decision != "allow" || strings.Contains(d.Reason, "[SEARCH_NUDGE]") {
			t.Errorf("%s should not nudge: got %+v", tc.name, d)
		}
	}
}

func TestPreToolUseAskQuestionKickoffGuard(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	invokeRaw := func(payload map[string]any) HookDecisionOutput {
		t.Helper()
		raw, _ := json.Marshal(payload)
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(raw)}, &out); err != nil {
			t.Fatalf("pre-tool-use: %v", err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal %q: %v", out.String(), err)
		}
		return dec
	}

	normalQ := map[string]any{
		"questions": []any{
			map[string]any{
				"question": "Which helper name do you prefer?",
				"options":  []any{"parsePipe", "splitPipe"},
			},
		},
	}

	// 1. Normal interactive operator session -> NEVER blocked!
	if d := invokeRaw(map[string]any{
		"conversationId": "conv-interactive",
		"workspacePaths": []string{dir},
		"lastUserInput":  "Help me design a new cache eviction policy for service.go",
		"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
	}); d.Decision != "allow" {
		t.Fatalf("normal interactive ask_question must allow, got %+v", d)
	}

	// 2. Kickoff prompt in LastUserInput -> blocked with [KICKOFF_GUARD] (both ask_question and agy:ask_question).
	kickoffPrompt := "Project: /repo/fyr. Run fyr resume (handoff HANDOFF-1, plan PLAN-1); do Phase 2B only.\nFiles: internal/search/hook_pretool.go\nStanding approvals (closed set): tests and commit.\nDone when: tests pass."
	for _, toolName := range []string{"ask_question", "agy:ask_question"} {
		d := invokeRaw(map[string]any{
			"conversationId": "conv-kickoff-input",
			"workspacePaths": []string{dir},
			"lastUserInput":  kickoffPrompt,
			"toolCall":       map[string]any{"name": toolName, "args": normalQ},
		})
		if d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
			t.Errorf("%s during Kickoff prompt: got %+v, want deny [KICKOFF_GUARD]", toolName, d)
		}
	}

	// 3. Kickoff persisted in vet_history via PreInvocation -> blocked even when PreToolUse omits LastUserInput.
	preReq, _ := json.Marshal(map[string]any{
		"conversationId": "conv-kickoff-vet",
		"workspacePaths": []string{dir},
		"invocationNum":  0,
		"lastUserInput":  "Files: service.go\nProject: this clone\nImplement Phase 2B.",
	})
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(preReq)}, &bytes.Buffer{}); err != nil {
		t.Fatalf("pre-invocation: %v", err)
	}
	if d := invokeRaw(map[string]any{
		"conversationId": "conv-kickoff-vet",
		"workspacePaths": []string{dir},
		"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
	}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
		t.Fatalf("ask_question with persisted Kickoff vet_history: got %+v, want deny [KICKOFF_GUARD]", d)
	}

	// 4. Subagent metadata (parentConversationId) -> blocked with [KICKOFF_GUARD].
	if d := invokeRaw(map[string]any{
		"conversationId":       "conv-child-worker",
		"parentConversationId": "conv-parent-coord",
		"workspacePaths":       []string{dir},
		"toolCall":             map[string]any{"name": "ask_question", "args": normalQ},
	}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
		t.Fatalf("subagent ask_question: got %+v, want deny [KICKOFF_GUARD]", d)
	}

	// 5. Kickoff detected from transcriptPath latest user prompt -> blocked with [KICKOFF_GUARD].
	trPath := filepath.Join(t.TempDir(), "transcript.jsonl")
	trLine, _ := json.Marshal(map[string]any{"type": "user", "content": "This is a Kickoff written by fyr auto.\nFiles: service.go"})
	if err := os.WriteFile(trPath, append(trLine, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if d := invokeRaw(map[string]any{
		"conversationId": "conv-kickoff-transcript",
		"workspacePaths": []string{dir},
		"transcriptPath": trPath,
		"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
	}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
		t.Fatalf("transcript Kickoff ask_question: got %+v, want deny [KICKOFF_GUARD]", d)
	}

	// 6. Explicit PRD.md conflict, ADR- conflict, Re-plan trigger, or quota exhaustion -> ALLOWED inside Kickoff!
	for _, tc := range []struct {
		name     string
		question string
	}{
		{"PRD.md conflict", "PRD.md R4 conflict: this requires a new non-stdlib dependency. How should we proceed?"},
		{"ADR conflict", "Proposed change contradicts ADR-511 (zero daemons); should we amend ADR-511?"},
		{"Re-plan trigger", "Re-plan trigger: implementation requires touching files outside the Phase 2B lease."},
		{"Quota exhaustion", "Quota exhausted (0% remaining on Anthropic pool); switch judge family?"},
		{"Low quota", "Both pools have low quota; switch to fallback?"},
	} {
		d := invokeRaw(map[string]any{
			"conversationId": "conv-kickoff-escape",
			"workspacePaths": []string{dir},
			"lastUserInput":  kickoffPrompt,
			"toolCall": map[string]any{
				"name": "ask_question",
				"args": map[string]any{
					"questions": []any{map[string]any{"question": tc.question, "options": []any{"Option A", "Option B"}}},
				},
			},
		})
		if d.Decision != "allow" {
			t.Errorf("%s inside Kickoff should be allowed, got %+v", tc.name, d)
		}
	}

	// 7. Casual mentions (PRD.md without conflict, or allow/follow + quota substring trap) still block inside Kickoff.
	for _, tc := range []struct {
		name     string
		question string
	}{
		{"casual PRD.md", "Should I read PRD.md before editing?"},
		{"allow/follow quota substring", "Should we allow following the quota rule?"},
		{"workflow 50% quota substring", "Workflow uses 50% quota; should we rename the helper?"},
	} {
		d := invokeRaw(map[string]any{
			"conversationId": "conv-kickoff-casual",
			"workspacePaths": []string{dir},
			"lastUserInput":  kickoffPrompt,
			"toolCall": map[string]any{
				"name": "ask_question",
				"args": map[string]any{
					"questions": []any{map[string]any{"question": tc.question, "options": []any{"Yes", "No"}}},
				},
			},
		})
		if d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
			t.Errorf("%s should still deny with [KICKOFF_GUARD]: %+v", tc.name, d)
		}
	}

	// 8. FYR_RELAY=1 blocks ask_question with [KICKOFF_GUARD] (relay) even on a bare prompt.
	t.Run("FYR_RELAY=1 blocks bare ask_question", func(t *testing.T) {
		t.Setenv("FYR_RELAY", "1")
		d := invokeRaw(map[string]any{
			"conversationId": "conv-relay-env",
			"workspacePaths": []string{dir},
			"lastUserInput":  "Implement the next step",
			"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
		})
		if d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") || !strings.Contains(d.Reason, "(relay)") {
			t.Fatalf("FYR_RELAY=1 bare prompt ask_question: got %+v, want deny [KICKOFF_GUARD] (relay)", d)
		}
	})

	// 9. Relay Kickoff header matches [KICKOFF_GUARD] even when FYR_RELAY is unset.
	t.Run("relay kickoff prompt matches KICKOFF_GUARD without env", func(t *testing.T) {
		t.Setenv("FYR_RELAY", "")
		relayPrompt := "Project: this clone, the current directory; never another checkout.\nPlan: PLAN-1 — Demo\nFiles: service.go\nRELAY OVERRIDE: Do NOT run git push or git tag.\n\n## Open Tasks\n- [ ] Add helper\n"
		d := invokeRaw(map[string]any{
			"conversationId": "conv-relay-prompt",
			"workspacePaths": []string{dir},
			"lastUserInput":  relayPrompt,
			"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
		})
		if d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
			t.Fatalf("relay kickoff prompt without env: got %+v, want deny [KICKOFF_GUARD]", d)
		}
	})
}
