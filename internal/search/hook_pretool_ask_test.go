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

func TestKickoffGuardMixedSessionClearsOnInteractiveTurn(t *testing.T) {
	t.Setenv("FYR_RELAY", "")
	dir, home := setupHookRepo(t), t.TempDir()

	normalQ := map[string]any{
		"questions": []any{
			map[string]any{
				"question": "Which helper name do you prefer?",
				"options":  []any{"parsePipe", "splitPipe"},
			},
		},
	}
	kickoffPrompt := "Project: /repo/fyr. Run fyr resume (handoff HANDOFF-1, plan PLAN-1); do Phase 2B only.\nFiles: internal/search/hook_pretool.go\nStanding approvals (closed set): tests and commit.\nDone when: tests pass."
	interactivePrompt := "Now let's interactively design the cache eviction policy for service.go"

	invokePreTool := func(t *testing.T, body map[string]any) HookDecisionOutput {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal pre-tool-use: %v", err)
		}
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

	invokePreInv := func(t *testing.T, convID string, invNum int, prompt string) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"invocationNum":  invNum,
			"lastUserInput":  prompt,
		})
		if err != nil {
			t.Fatalf("marshal pre-invocation: %v", err)
		}
		if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(raw)}, &bytes.Buffer{}); err != nil {
			t.Fatalf("pre-invocation (conv=%s inv=%d): %v", convID, invNum, err)
		}
	}

	writeTranscript := func(t *testing.T, rows []map[string]any) string {
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

	// (a) Turn 1 Kickoff -> Turn 2 interactive PreInvocation -> PreToolUse ask_question (empty LastUserInput) => allow.
	t.Run("turn1 kickoff then turn2 interactive vet_history clears guard", func(t *testing.T) {
		const conv = "conv-mixed-vet"
		invokePreInv(t, conv, 0, kickoffPrompt)
		if d := invokePreTool(t, map[string]any{
			"conversationId": conv,
			"workspacePaths": []string{dir},
			"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
		}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
			t.Fatalf("turn 1 kickoff ask_question: got %+v, want deny [KICKOFF_GUARD]", d)
		}

		invokePreInv(t, conv, 1, interactivePrompt)
		staleKickoffTr := writeTranscript(t, []map[string]any{
			{"type": "user", "content": kickoffPrompt},
		})
		if d := invokePreTool(t, map[string]any{
			"conversationId": conv,
			"workspacePaths": []string{dir},
			"transcriptPath": staleKickoffTr,
			"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
		}); d.Decision != "allow" {
			t.Fatalf("turn 2 interactive ask_question (empty LastUserInput): got %+v, want allow", d)
		}
	})

	// (b) Turn 1 Kickoff -> PreToolUse ask_question with explicit interactive LastUserInput => allow.
	t.Run("turn1 kickoff then interactive LastUserInput allows without fallthrough", func(t *testing.T) {
		const conv = "conv-mixed-lastinput"
		invokePreInv(t, conv, 0, kickoffPrompt)
		staleKickoffTr := writeTranscript(t, []map[string]any{
			{"type": "user", "content": kickoffPrompt},
		})
		if d := invokePreTool(t, map[string]any{
			"conversationId": conv,
			"workspacePaths": []string{dir},
			"transcriptPath": staleKickoffTr,
			"lastUserInput":  interactivePrompt,
			"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
		}); d.Decision != "allow" {
			t.Fatalf("interactive LastUserInput after turn 1 kickoff: got %+v, want allow", d)
		}
	})

	// (c) Mixed transcript (Turn 1 Kickoff, Turn 2 interactive) -> PreToolUse ask_question => allow.
	t.Run("mixed transcript latest interactive clears guard", func(t *testing.T) {
		tr := writeTranscript(t, []map[string]any{
			{"type": "user", "content": kickoffPrompt},
			{"type": "assistant", "content": "Finished Phase 2B."},
			{"type": "user", "content": interactivePrompt},
			{"type": "assistant", "content": "Checking design options."},
		})
		if d := invokePreTool(t, map[string]any{
			"conversationId": "conv-mixed-transcript",
			"workspacePaths": []string{dir},
			"transcriptPath": tr,
			"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
		}); d.Decision != "allow" {
			t.Fatalf("mixed transcript (kickoff -> interactive): got %+v, want allow", d)
		}
	})

	// (c-large) Large mixed transcript (> 512 KiB) where Turn 2 interactive is near the tail => allow.
	t.Run("large mixed transcript reads latest prompt near tail", func(t *testing.T) {
		pad := strings.Repeat("x", 16<<10)
		rows := []map[string]any{
			{"type": "user", "content": kickoffPrompt},
		}
		for range 36 {
			rows = append(rows, map[string]any{"type": "assistant", "content": pad})
		}
		rows = append(rows,
			map[string]any{"type": "user", "content": interactivePrompt},
			map[string]any{"type": "assistant", "content": "Ready to ask."},
		)
		tr := writeTranscript(t, rows)
		if d := invokePreTool(t, map[string]any{
			"conversationId": "conv-large-mixed-transcript",
			"workspacePaths": []string{dir},
			"transcriptPath": tr,
			"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
		}); d.Decision != "allow" {
			t.Fatalf("large mixed transcript: got %+v, want allow", d)
		}
	})

	// (c-must-fail) Large single-turn Kickoff transcript (> 512 KiB assistant output) => still deny.
	t.Run("large single-turn kickoff transcript stays blocked", func(t *testing.T) {
		pad := strings.Repeat("y", 16<<10)
		rows := []map[string]any{
			{"type": "user", "content": kickoffPrompt},
		}
		for range 36 {
			rows = append(rows, map[string]any{"type": "assistant", "content": pad})
		}
		tr := writeTranscript(t, rows)
		if d := invokePreTool(t, map[string]any{
			"conversationId": "conv-large-single-kickoff",
			"workspacePaths": []string{dir},
			"transcriptPath": tr,
			"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
		}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
			t.Fatalf("large single-turn kickoff transcript: got %+v, want deny [KICKOFF_GUARD]", d)
		}
	})

	// (c-reblock) Transcript Turn 1 Kickoff -> Turn 2 interactive -> Turn 3 Kickoff => deny,
	// and Turn 1 interactive -> Turn 2 Kickoff => deny.
	t.Run("mixed transcript re-entering kickoff blocks again", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			rows []map[string]any
		}{
			{
				name: "kickoff_interactive_kickoff",
				rows: []map[string]any{
					{"type": "user", "content": kickoffPrompt},
					{"type": "user", "content": interactivePrompt},
					{"type": "user", "content": kickoffPrompt},
				},
			},
			{
				name: "interactive_then_kickoff",
				rows: []map[string]any{
					{"type": "user", "content": interactivePrompt},
					{"type": "user", "content": kickoffPrompt},
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tr := writeTranscript(t, tc.rows)
				if d := invokePreTool(t, map[string]any{
					"conversationId": "conv-transcript-" + tc.name,
					"workspacePaths": []string{dir},
					"transcriptPath": tr,
					"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
				}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
					t.Fatalf("%s: got %+v, want deny [KICKOFF_GUARD]", tc.name, d)
				}
			})
		}
	})

	// (c-boundary) Partial mid-line fragment at tail-window cut must not spoof a user turn.
	t.Run("large transcript ignores partial mid-line fragment at tail cut", func(t *testing.T) {
		firstLine, err := json.Marshal(map[string]any{"type": "user", "content": kickoffPrompt})
		if err != nil {
			t.Fatal(err)
		}
		fakeUser := `{"type":"user","content":"` + interactivePrompt + `"}`
		assistantTail := fakeUser + "\n"
		padAfter := strings.Repeat(`{"type":"assistant","content":"`+strings.Repeat("z", 1024)+`"}`+"\n", (askTranscriptTailBytes/1060)-2)
		rem := askTranscriptTailBytes - len(assistantTail) - len(padAfter)
		if rem <= 0 {
			t.Fatalf("unexpected non-positive rem=%d", rem)
		}
		// Ensure exact file layout: firstLine + "\n" + prefix (no newline) + fakeUser + "\n" + padAfter + exact spacer so scanStart lands on fakeUser.
		// Build prefix ending in non-newline right before fakeUser, and trailing padding after fakeUser totaling askTranscriptTailBytes.
		var tailBuf bytes.Buffer
		tailBuf.WriteString(assistantTail)
		tailBuf.WriteString(padAfter)
		if rem > 1 {
			tailBuf.WriteString(strings.Repeat(" ", rem-1))
			tailBuf.WriteByte('\n')
		} else {
			tailBuf.WriteByte('\n')
		}
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
		if d := invokePreTool(t, map[string]any{
			"conversationId": "conv-partial-cut",
			"workspacePaths": []string{dir},
			"transcriptPath": tr,
			"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
		}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
			t.Fatalf("mid-line fake user at tail boundary bypassed kickoff guard: got %+v, want deny [KICKOFF_GUARD]", d)
		}
	})

	// (c-symlink) Symlinked transcript path is ignored (Lstat regular-file/no-symlink guard).
	t.Run("symlinked transcript path is rejected", func(t *testing.T) {
		target := writeTranscript(t, []map[string]any{
			{"type": "user", "content": kickoffPrompt},
		})
		linkPath := filepath.Join(t.TempDir(), "transcript-link.jsonl")
		if err := os.Symlink(target, linkPath); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if d := invokePreTool(t, map[string]any{
			"conversationId": "conv-symlink-transcript",
			"workspacePaths": []string{dir},
			"transcriptPath": linkPath,
			"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
		}); d.Decision != "allow" {
			t.Fatalf("symlink transcript must be ignored, got %+v", d)
		}
	})

	// (d) Turn 1 Kickoff -> Turn 2 interactive -> Turn 3 Kickoff => deny [KICKOFF_GUARD].
	t.Run("turn1 kickoff turn2 interactive turn3 kickoff re-blocks", func(t *testing.T) {
		const conv = "conv-kickoff-reenter"
		invokePreInv(t, conv, 0, kickoffPrompt)
		invokePreInv(t, conv, 1, interactivePrompt)
		if d := invokePreTool(t, map[string]any{
			"conversationId": conv,
			"workspacePaths": []string{dir},
			"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
		}); d.Decision != "allow" {
			t.Fatalf("turn 2 interactive before re-entry: got %+v, want allow", d)
		}

		invokePreInv(t, conv, 2, kickoffPrompt)
		if d := invokePreTool(t, map[string]any{
			"conversationId": conv,
			"workspacePaths": []string{dir},
			"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
		}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
			t.Fatalf("turn 3 kickoff after interactive turn 2: got %+v, want deny [KICKOFF_GUARD]", d)
		}
	})

	// (e) Headless guards unconditionally deny [KICKOFF_GUARD] even with interactive LastUserInput.
	t.Run("headless guards stay blocked with interactive LastUserInput", func(t *testing.T) {
		t.Run("FYR_RELAY=1", func(t *testing.T) {
			t.Setenv("FYR_RELAY", "1")
			if d := invokePreTool(t, map[string]any{
				"conversationId": "conv-headless-relay",
				"workspacePaths": []string{dir},
				"lastUserInput":  interactivePrompt,
				"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
			}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") || !strings.Contains(d.Reason, "(relay)") {
				t.Fatalf("FYR_RELAY=1 with interactive input: got %+v, want deny [KICKOFF_GUARD] (relay)", d)
			}
		})

		t.Run("subagent parentConversationId", func(t *testing.T) {
			if d := invokePreTool(t, map[string]any{
				"conversationId":       "conv-headless-child",
				"parentConversationId": "conv-headless-parent",
				"workspacePaths":       []string{dir},
				"lastUserInput":        interactivePrompt,
				"toolCall":             map[string]any{"name": "ask_question", "args": normalQ},
			}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
				t.Fatalf("subagent parentConversationId with interactive input: got %+v, want deny [KICKOFF_GUARD]", d)
			}
		})

		t.Run("non-main AgentName", func(t *testing.T) {
			if d := invokePreTool(t, map[string]any{
				"conversationId": "conv-headless-agentname",
				"agentName":      "reviewer",
				"workspacePaths": []string{dir},
				"lastUserInput":  interactivePrompt,
				"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
			}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
				t.Fatalf("non-main AgentName with interactive input: got %+v, want deny [KICKOFF_GUARD]", d)
			}
		})

		t.Run("non-main wd.role", func(t *testing.T) {
			isAuto, src := detectKickoffOrWorkerSessionCtx(
				context.Background(),
				Options{Home: home},
				dir,
				watchdogPreState{role: "builder"},
				nil,
				"",
				HookInput{ConversationID: "conv-headless-wdrole", LastUserInput: interactivePrompt},
			)
			if !isAuto || src != "role:builder" {
				t.Fatalf("non-main wd.role with interactive input: got (%v, %q), want (true, role:builder)", isAuto, src)
			}
		})

		t.Run("non-main session_meta.agent_role", func(t *testing.T) {
			const conv = "conv-headless-session-meta"
			st, _, err := openLedgerStore(dir)
			if err != nil {
				t.Fatalf("openLedgerStore: %v", err)
			}
			_, err = st.db.Exec(
				`INSERT OR REPLACE INTO session_meta(conversation_id, generation_id, last_transcript_bytes, turn_count, est_tokens, agent_role, agent_name, tool_calls) VALUES (?, 0, 0, 1, 0, 'reviewer', 'reviewer', 0)`,
				conv,
			)
			st.close()
			if err != nil {
				t.Fatalf("seed session_meta.agent_role: %v", err)
			}

			// Direct call with empty wd (simulating watchdog skipped) must still block via session_meta.agent_role before LastUserInput!
			isAuto, src := detectKickoffOrWorkerSessionCtx(
				context.Background(),
				Options{Home: home},
				dir,
				watchdogPreState{},
				nil,
				"",
				HookInput{ConversationID: conv, LastUserInput: interactivePrompt},
			)
			if !isAuto || src != "session_meta:reviewer" {
				t.Fatalf("session_meta.agent_role with empty wd & interactive input: got (%v, %q), want (true, session_meta:reviewer)", isAuto, src)
			}

			// Full PreToolUse hook must also deny [KICKOFF_GUARD].
			if d := invokePreTool(t, map[string]any{
				"conversationId": conv,
				"workspacePaths": []string{dir},
				"lastUserInput":  interactivePrompt,
				"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
			}); d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
				t.Fatalf("full pre-tool-use with session_meta.agent_role & interactive input: got %+v, want deny [KICKOFF_GUARD]", d)
			}
		})

		t.Run("main session_meta.agent_role allows interactive LastUserInput", func(t *testing.T) {
			const conv = "conv-main-session-meta"
			st, _, err := openLedgerStore(dir)
			if err != nil {
				t.Fatalf("openLedgerStore: %v", err)
			}
			_, err = st.db.Exec(
				`INSERT OR REPLACE INTO session_meta(conversation_id, generation_id, last_transcript_bytes, turn_count, est_tokens, agent_role, agent_name, tool_calls) VALUES (?, 0, 0, 1, 0, 'main', '', 0)`,
				conv,
			)
			st.close()
			if err != nil {
				t.Fatalf("seed main session_meta.agent_role: %v", err)
			}
			if d := invokePreTool(t, map[string]any{
				"conversationId": conv,
				"workspacePaths": []string{dir},
				"lastUserInput":  interactivePrompt,
				"toolCall":       map[string]any{"name": "ask_question", "args": normalQ},
			}); d.Decision != "allow" {
				t.Fatalf("main session_meta.agent_role with interactive input: got %+v, want allow", d)
			}
		})
	})
}
