package search

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	kickoffTestPrompt     = "Project: /repo/fyr. Run fyr resume (handoff HANDOFF-1, plan PLAN-1); do Phase 2B only.\nFiles: internal/search/hook_pretool.go\nStanding approvals (closed set): tests and commit.\nDone when: tests pass."
	autoKickoffTestPrompt = "This is a Kickoff written by fyr auto.\nFiles: a.go"
	interactiveTestPrompt = "Now let's interactively design the cache eviction policy for service.go"
)

type kickoffAskHarness struct {
	dir     string
	home    string
	normalQ map[string]any
}

func newKickoffAskHarness(t *testing.T) *kickoffAskHarness {
	t.Helper()
	t.Setenv("FYR_RELAY", "")
	return &kickoffAskHarness{
		dir:  setupHookRepo(t),
		home: t.TempDir(),
		normalQ: map[string]any{
			"questions": []any{
				map[string]any{
					"question": "Which helper name do you prefer?",
					"options":  []any{"parsePipe", "splitPipe"},
				},
			},
		},
	}
}

func (h *kickoffAskHarness) invokePreTool(t *testing.T, body map[string]any) HookDecisionOutput {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal pre-tool-use: %v", err)
	}
	var out bytes.Buffer
	if err := Search(Options{Home: h.home, Hook: "pre-tool-use", HookIn: bytes.NewReader(raw)}, &out); err != nil {
		t.Fatalf("pre-tool-use: %v", err)
	}
	var dec HookDecisionOutput
	if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
		t.Fatalf("unmarshal %q: %v", out.String(), err)
	}
	return dec
}

func (h *kickoffAskHarness) invokePreToolAsk(t *testing.T, conv, tr string) HookDecisionOutput {
	t.Helper()
	return h.invokePreTool(t, map[string]any{
		"conversationId": conv,
		"workspacePaths": []string{h.dir},
		"transcriptPath": tr,
		"toolCall":       map[string]any{"name": "ask_question", "args": h.normalQ},
	})
}

func (h *kickoffAskHarness) invokePreInv(t *testing.T, convID string, invNum int, prompt string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"conversationId": convID,
		"workspacePaths": []string{h.dir},
		"invocationNum":  invNum,
		"lastUserInput":  prompt,
	})
	if err != nil {
		t.Fatalf("marshal pre-invocation: %v", err)
	}
	if err := Search(Options{Dir: h.dir, Home: h.home, Hook: "pre-invocation", HookIn: bytes.NewReader(raw)}, &bytes.Buffer{}); err != nil {
		t.Fatalf("pre-invocation (conv=%s inv=%d): %v", convID, invNum, err)
	}
}

func (h *kickoffAskHarness) writeTranscript(t *testing.T, rows []map[string]any) string {
	t.Helper()
	var buf bytes.Buffer
	for _, row := range rows {
		line, err := json.Marshal(row)
		if err != nil {
			t.Fatalf("marshal transcript row: %v", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	p := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return p
}

func (h *kickoffAskHarness) seedSessionMetaRole(t *testing.T, conv, role, name string) {
	t.Helper()
	st, _, err := openLedgerStore(h.dir)
	if err != nil {
		t.Fatalf("openLedgerStore: %v", err)
	}
	_, err = st.db.Exec(
		`INSERT OR REPLACE INTO session_meta(conversation_id, generation_id, last_transcript_bytes, turn_count, est_tokens, agent_role, agent_name, tool_calls) VALUES (?, 0, 0, 1, 0, ?, ?, 0)`,
		conv, role, name,
	)
	st.close()
	if err != nil {
		t.Fatalf("seed session_meta.agent_role(%s): %v", role, err)
	}
}

func TestKickoffGuardMixedSessionClearsOnInteractiveTurn(t *testing.T) {
	h := newKickoffAskHarness(t)

	t.Run("turn1 kickoff then turn2 interactive vet_history clears guard", func(t *testing.T) {
		testKickoffTurn1ThenTurn2VetClears(t, h)
	})
	t.Run("turn1 kickoff then interactive LastUserInput allows without fallthrough", func(t *testing.T) {
		testKickoffTurn1ThenInteractiveLastInput(t, h)
	})
	t.Run("mixed transcript latest interactive clears guard", func(t *testing.T) {
		testKickoffMixedTranscriptClears(t, h)
	})
	t.Run("large mixed transcript reads latest prompt near tail", func(t *testing.T) {
		testKickoffLargeMixedTranscriptTail(t, h)
	})
	t.Run("large single-turn kickoff transcript stays blocked", func(t *testing.T) {
		testKickoffLargeSingleTurnStaysBlocked(t, h)
	})
	t.Run("mixed transcript re-entering kickoff blocks again", func(t *testing.T) {
		testKickoffMixedTranscriptReenter(t, h)
	})
	t.Run("large transcript ignores partial mid-line fragment at tail cut", func(t *testing.T) {
		testKickoffPartialMidLineFragment(t, h)
	})
	t.Run("symlinked transcript path is rejected", func(t *testing.T) {
		testKickoffSymlinkTranscriptRejected(t, h)
	})
	t.Run("turn1 kickoff turn2 interactive turn3 kickoff re-blocks", func(t *testing.T) {
		testKickoffTurn3ReentryBlocks(t, h)
	})
	t.Run("headless guards stay blocked with interactive LastUserInput", func(t *testing.T) {
		testKickoffHeadlessGuards(t, h)
	})
}

func testKickoffTurn1ThenTurn2VetClears(t *testing.T, h *kickoffAskHarness) {
	const conv = "conv-mixed-vet"
	h.invokePreInv(t, conv, 0, kickoffTestPrompt)
	if d := h.invokePreTool(t, map[string]any{
		"conversationId": conv,
		"workspacePaths": []string{h.dir},
		"toolCall":       map[string]any{"name": "ask_question", "args": h.normalQ},
	}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
		t.Fatalf("turn 1 kickoff ask_question: got %+v, want deny [KICKOFF_GUARD]", d)
	}

	h.invokePreInv(t, conv, 1, interactiveTestPrompt)
	staleKickoffTr := h.writeTranscript(t, []map[string]any{
		{"type": "user", "content": kickoffTestPrompt},
	})
	if d := h.invokePreToolAsk(t, conv, staleKickoffTr); d.Decision != "allow" {
		t.Fatalf("turn 2 interactive ask_question (empty LastUserInput): got %+v, want allow", d)
	}
}

func testKickoffTurn1ThenInteractiveLastInput(t *testing.T, h *kickoffAskHarness) {
	const conv = "conv-mixed-lastinput"
	h.invokePreInv(t, conv, 0, kickoffTestPrompt)
	staleKickoffTr := h.writeTranscript(t, []map[string]any{
		{"type": "user", "content": kickoffTestPrompt},
	})
	if d := h.invokePreTool(t, map[string]any{
		"conversationId": conv,
		"workspacePaths": []string{h.dir},
		"transcriptPath": staleKickoffTr,
		"lastUserInput":  interactiveTestPrompt,
		"toolCall":       map[string]any{"name": "ask_question", "args": h.normalQ},
	}); d.Decision != "allow" {
		t.Fatalf("interactive LastUserInput after turn 1 kickoff: got %+v, want allow", d)
	}
}

func testKickoffMixedTranscriptClears(t *testing.T, h *kickoffAskHarness) {
	tr := h.writeTranscript(t, []map[string]any{
		{"type": "user", "content": kickoffTestPrompt},
		{"type": "assistant", "content": "Finished Phase 2B."},
		{"type": "user", "content": interactiveTestPrompt},
		{"type": "assistant", "content": "Checking design options."},
	})
	if d := h.invokePreToolAsk(t, "conv-mixed-transcript", tr); d.Decision != "allow" {
		t.Fatalf("mixed transcript (kickoff -> interactive): got %+v, want allow", d)
	}
}

func testKickoffLargeMixedTranscriptTail(t *testing.T, h *kickoffAskHarness) {
	pad := strings.Repeat("x", 16<<10)
	rows := []map[string]any{{"type": "user", "content": kickoffTestPrompt}}
	for range 36 {
		rows = append(rows, map[string]any{"type": "assistant", "content": pad})
	}
	rows = append(rows,
		map[string]any{"type": "user", "content": interactiveTestPrompt},
		map[string]any{"type": "assistant", "content": "Ready to ask."},
	)
	tr := h.writeTranscript(t, rows)
	if d := h.invokePreToolAsk(t, "conv-large-mixed-transcript", tr); d.Decision != "allow" {
		t.Fatalf("large mixed transcript: got %+v, want allow", d)
	}
}

func testKickoffLargeSingleTurnStaysBlocked(t *testing.T, h *kickoffAskHarness) {
	pad := strings.Repeat("y", 16<<10)
	rows := []map[string]any{{"type": "user", "content": kickoffTestPrompt}}
	for range 36 {
		rows = append(rows, map[string]any{"type": "assistant", "content": pad})
	}
	tr := h.writeTranscript(t, rows)
	if d := h.invokePreToolAsk(t, "conv-large-single-kickoff", tr); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
		t.Fatalf("large single-turn kickoff transcript: got %+v, want deny [KICKOFF_GUARD]", d)
	}
}

func testKickoffMixedTranscriptReenter(t *testing.T, h *kickoffAskHarness) {
	for _, tc := range []struct {
		name string
		rows []map[string]any
	}{
		{
			name: "kickoff_interactive_kickoff",
			rows: []map[string]any{
				{"type": "user", "content": kickoffTestPrompt},
				{"type": "user", "content": interactiveTestPrompt},
				{"type": "user", "content": kickoffTestPrompt},
			},
		},
		{
			name: "interactive_then_kickoff",
			rows: []map[string]any{
				{"type": "user", "content": interactiveTestPrompt},
				{"type": "user", "content": kickoffTestPrompt},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := h.writeTranscript(t, tc.rows)
			if d := h.invokePreToolAsk(t, "conv-transcript-"+tc.name, tr); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
				t.Fatalf("%s: got %+v, want deny [KICKOFF_GUARD]", tc.name, d)
			}
		})
	}
}

func testKickoffPartialMidLineFragment(t *testing.T, h *kickoffAskHarness) {
	firstLine, err := json.Marshal(map[string]any{"type": "user", "content": kickoffTestPrompt})
	if err != nil {
		t.Fatal(err)
	}
	fakeUser := `{"type":"user","content":"` + interactiveTestPrompt + `"}`
	assistantTail := fakeUser + "\n"
	padAfter := strings.Repeat(`{"type":"assistant","content":"`+strings.Repeat("z", 1024)+`"}`+"\n", (askTranscriptTailBytes/1060)-2)
	rem := askTranscriptTailBytes - len(assistantTail) - len(padAfter)
	if rem <= 0 {
		t.Fatalf("unexpected non-positive rem=%d", rem)
	}
	var tailBuf bytes.Buffer
	tailBuf.WriteString(assistantTail)
	tailBuf.WriteString(padAfter)
	if rem > 1 {
		tailBuf.WriteString(strings.Repeat(" ", rem-1))
	}
	tailBuf.WriteByte('\n')
	if tailBuf.Len() != askTranscriptTailBytes {
		t.Fatalf("tailBuf len = %d, want %d", tailBuf.Len(), askTranscriptTailBytes)
	}
	var full bytes.Buffer
	full.Write(firstLine)
	full.WriteByte('\n')
	full.WriteString(`{"type":"assistant","content":"prefix_without_newline_`)
	full.Write(tailBuf.Bytes())
	tr := filepath.Join(t.TempDir(), "partial-cut.jsonl")
	if err := os.WriteFile(tr, full.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if d := h.invokePreToolAsk(t, "conv-partial-cut", tr); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
		t.Fatalf("mid-line fake user at tail boundary bypassed kickoff guard: got %+v, want deny [KICKOFF_GUARD]", d)
	}
}

func testKickoffSymlinkTranscriptRejected(t *testing.T, h *kickoffAskHarness) {
	target := h.writeTranscript(t, []map[string]any{
		{"type": "user", "content": kickoffTestPrompt},
	})
	linkPath := filepath.Join(t.TempDir(), "transcript-link.jsonl")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if d := h.invokePreToolAsk(t, "conv-symlink-transcript", linkPath); d.Decision != "allow" {
		t.Fatalf("symlink transcript must be ignored, got %+v", d)
	}
}

func testKickoffTurn3ReentryBlocks(t *testing.T, h *kickoffAskHarness) {
	const conv = "conv-kickoff-reenter"
	h.invokePreInv(t, conv, 0, kickoffTestPrompt)
	h.invokePreInv(t, conv, 1, interactiveTestPrompt)
	if d := h.invokePreTool(t, map[string]any{
		"conversationId": conv,
		"workspacePaths": []string{h.dir},
		"toolCall":       map[string]any{"name": "ask_question", "args": h.normalQ},
	}); d.Decision != "allow" {
		t.Fatalf("turn 2 interactive before re-entry: got %+v, want allow", d)
	}

	h.invokePreInv(t, conv, 2, kickoffTestPrompt)
	if d := h.invokePreTool(t, map[string]any{
		"conversationId": conv,
		"workspacePaths": []string{h.dir},
		"toolCall":       map[string]any{"name": "ask_question", "args": h.normalQ},
	}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
		t.Fatalf("turn 3 kickoff after interactive turn 2: got %+v, want deny [KICKOFF_GUARD]", d)
	}
}

func testKickoffHeadlessGuards(t *testing.T, h *kickoffAskHarness) {
	t.Run("FYR_RELAY=1", func(t *testing.T) {
		t.Setenv("FYR_RELAY", "1")
		if d := h.invokePreTool(t, map[string]any{
			"conversationId": "conv-headless-relay",
			"workspacePaths": []string{h.dir},
			"lastUserInput":  interactiveTestPrompt,
			"toolCall":       map[string]any{"name": "ask_question", "args": h.normalQ},
		}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") || !strings.Contains(d.Reason, "(relay)") {
			t.Fatalf("FYR_RELAY=1 with interactive input: got %+v, want deny [KICKOFF_GUARD] (relay)", d)
		}
	})

	t.Run("subagent parentConversationId", func(t *testing.T) {
		if d := h.invokePreTool(t, map[string]any{
			"conversationId":       "conv-headless-child",
			"parentConversationId": "conv-headless-parent",
			"workspacePaths":       []string{h.dir},
			"lastUserInput":        interactiveTestPrompt,
			"toolCall":             map[string]any{"name": "ask_question", "args": h.normalQ},
		}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
			t.Fatalf("subagent parentConversationId with interactive input: got %+v, want deny [KICKOFF_GUARD]", d)
		}
	})

	t.Run("non-main AgentName", func(t *testing.T) {
		if d := h.invokePreTool(t, map[string]any{
			"conversationId": "conv-headless-agentname",
			"agentName":      "reviewer",
			"workspacePaths": []string{h.dir},
			"lastUserInput":  interactiveTestPrompt,
			"toolCall":       map[string]any{"name": "ask_question", "args": h.normalQ},
		}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
			t.Fatalf("non-main AgentName with interactive input: got %+v, want deny [KICKOFF_GUARD]", d)
		}
	})

	t.Run("non-main wd.role", func(t *testing.T) {
		isAuto, src := detectKickoffOrWorkerSessionCtx(
			context.Background(),
			h.dir,
			watchdogPreState{role: "builder"},
			nil,
			"",
			HookInput{ConversationID: "conv-headless-wdrole", LastUserInput: interactiveTestPrompt},
		)
		if !isAuto || src != "role:builder" {
			t.Fatalf("non-main wd.role with interactive input: got (%v, %q), want (true, role:builder)", isAuto, src)
		}
	})

	t.Run("non-main session_meta.agent_role", func(t *testing.T) {
		testKickoffSessionMetaNonMain(t, h)
	})

	t.Run("main session_meta.agent_role allows interactive LastUserInput", func(t *testing.T) {
		const conv = "conv-main-session-meta"
		h.seedSessionMetaRole(t, conv, "main", "")
		if d := h.invokePreTool(t, map[string]any{
			"conversationId": conv,
			"workspacePaths": []string{h.dir},
			"lastUserInput":  interactiveTestPrompt,
			"toolCall":       map[string]any{"name": "ask_question", "args": h.normalQ},
		}); d.Decision != "allow" {
			t.Fatalf("main session_meta.agent_role with interactive input: got %+v, want allow", d)
		}
	})
}

func testKickoffSessionMetaNonMain(t *testing.T, h *kickoffAskHarness) {
	const conv = "conv-headless-session-meta"
	h.seedSessionMetaRole(t, conv, "reviewer", "reviewer")

	isAuto, src := detectKickoffOrWorkerSessionCtx(
		context.Background(),
		h.dir,
		watchdogPreState{},
		nil,
		"",
		HookInput{ConversationID: conv, LastUserInput: interactiveTestPrompt},
	)
	if !isAuto || src != "session_meta:reviewer" {
		t.Fatalf("session_meta.agent_role with empty wd & interactive input: got (%v, %q), want (true, session_meta:reviewer)", isAuto, src)
	}

	if d := h.invokePreTool(t, map[string]any{
		"conversationId": conv,
		"workspacePaths": []string{h.dir},
		"lastUserInput":  interactiveTestPrompt,
		"toolCall":       map[string]any{"name": "ask_question", "args": h.normalQ},
	}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
		t.Fatalf("full pre-tool-use with session_meta.agent_role & interactive input: got %+v, want deny [KICKOFF_GUARD]", d)
	}
}

func TestKickoffGuardTranscriptNonUserAndTruncation(t *testing.T) {
	h := newKickoffAskHarness(t)

	t.Run("non-user row prompt never clears kickoff guard", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			row  map[string]any
		}{
			{"assistant_prompt", map[string]any{"type": "assistant", "prompt": "normal interactive text"}},
			{"tool_use_prompt", map[string]any{"type": "tool_use", "prompt": "normal interactive text"}},
			{"model_role_prompt", map[string]any{"role": "model", "prompt": "normal interactive text"}},
			{"system_author_prompt", map[string]any{"author": "system", "prompt": "normal interactive text"}},
			{"planner_userInput", map[string]any{"type": "PLANNER_RESPONSE", "userInput": "normal interactive text"}},
			{"generic_lastUserInput", map[string]any{"type": "GENERIC", "lastUserInput": "normal interactive text"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tr := h.writeTranscript(t, []map[string]any{
					{"type": "user", "content": kickoffTestPrompt},
					tc.row,
				})
				if d := h.invokePreToolAsk(t, "conv-nonuser-deny-"+tc.name, tr); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
					t.Fatalf("%s: got %+v, want deny [KICKOFF_GUARD]", tc.name, d)
				}
			})
		}
	})

	t.Run("tool or subagent kickoff prompt never false-triggers interactive session", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			row  map[string]any
		}{
			{"tool_use_subagent", map[string]any{"type": "tool_use", "name": "invoke_subagent", "prompt": autoKickoffTestPrompt}},
			{"tool_role_prompt", map[string]any{"role": "tool", "prompt": autoKickoffTestPrompt}},
			{"assistant_prompt", map[string]any{"type": "assistant", "prompt": autoKickoffTestPrompt}},
			{"tool_result_user_input", map[string]any{"type": "tool_result", "user_input": autoKickoffTestPrompt}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tr := h.writeTranscript(t, []map[string]any{
					{"type": "user", "content": interactiveTestPrompt},
					tc.row,
				})
				if d := h.invokePreToolAsk(t, "conv-nonuser-allow-"+tc.name, tr); d.Decision != "allow" {
					t.Fatalf("%s: got %+v, want allow", tc.name, d)
				}
			})
		}
	})

	t.Run("user-role and role-less prompt rows still enforce kickoff", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			row  map[string]any
		}{
			{"roleless_prompt", map[string]any{"prompt": kickoffTestPrompt}},
			{"user_prompt", map[string]any{"type": "user", "prompt": kickoffTestPrompt}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tr := h.writeTranscript(t, []map[string]any{tc.row})
				if d := h.invokePreToolAsk(t, "conv-valid-prompt-"+tc.name, tr); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
					t.Fatalf("%s: got %+v, want deny [KICKOFF_GUARD]", tc.name, d)
				}
			})
		}
	})

	t.Run("unicode escape noise does not starve tail scan", func(t *testing.T) {
		pad16K := strings.Repeat("a", 16<<10)
		noise200 := "<section>a & b</section>" + strings.Repeat("z", 180)
		buildNoise := func(withInteractive bool) string {
			rows := []map[string]any{{"type": "user", "content": kickoffTestPrompt}}
			for range 20 {
				rows = append(rows, map[string]any{"type": "assistant", "content": pad16K})
			}
			if withInteractive {
				rows = append(rows, map[string]any{"type": "user", "content": interactiveTestPrompt})
			}
			for range askTranscriptMaxCandidates + 50 {
				rows = append(rows, map[string]any{"type": "assistant", "content": noise200})
			}
			p := h.writeTranscript(t, rows)
			fi, err := os.Stat(p)
			if err != nil || fi.Size() <= askTranscriptTailBytes {
				t.Fatalf("expected > %d bytes transcript, got err=%v size=%v", askTranscriptTailBytes, err, fi)
			}
			return p
		}

		if d := h.invokePreToolAsk(t, "conv-u-noise-mixed", buildNoise(true)); d.Decision != "allow" {
			t.Fatalf("mixed transcript with >1024 \\u003c/\\u003e lines: got %+v, want allow", d)
		}
		if d := h.invokePreToolAsk(t, "conv-u-noise-single", buildNoise(false)); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
			t.Fatalf("single-turn kickoff with >1024 \\u003c/\\u003e lines: got %+v, want deny [KICKOFF_GUARD]", d)
		}
	})

	t.Run("candidate cap truncation never falls back to stale turn1 kickoff", func(t *testing.T) {
		pad16K := strings.Repeat("b", 16<<10)
		pad200 := strings.Repeat("c", 200)
		rows := []map[string]any{{"type": "user", "content": kickoffTestPrompt}}
		for range 20 {
			rows = append(rows, map[string]any{"type": "assistant", "content": pad16K})
		}
		for range askTranscriptMaxCandidates + 50 {
			rows = append(rows, map[string]any{
				"type":    "assistant",
				"nested":  map[string]any{"prompt": "noop"},
				"content": pad200,
			})
		}
		tr := h.writeTranscript(t, rows)
		fi, err := os.Stat(tr)
		if err != nil || fi.Size() <= askTranscriptTailBytes {
			t.Fatalf("expected > %d bytes transcript, got err=%v size=%v", askTranscriptTailBytes, err, fi)
		}
		if d := h.invokePreToolAsk(t, "conv-cap-truncated", tr); d.Decision != "allow" {
			t.Fatalf("truncated tail scan fell back to stale turn 1 kickoff: got %+v, want allow", d)
		}
	})
}
