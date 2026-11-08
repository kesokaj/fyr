package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestACPCommand(t *testing.T) {
	want := []string{"acp", "--stdio", "--process-per-session"}
	if got := ACPCommand(); !slices.Equal(got, want) {
		t.Fatalf("ACPCommand() = %v, want %v", got, want)
	}
}

func TestACPClientLifecycleAndGrants(t *testing.T) {
	srvInR, srvInW := io.Pipe()
	srvOutR, srvOutW := io.Pipe()
	defer srvInR.Close()
	defer srvOutW.Close()

	var (
		mu         sync.Mutex
		deniedList []string
		permOpts   []string
	)
	grants := []string{"read_file(*)", "write_file(*)", "command(git)", "command(go)"}
	client := NewACPClient(srvOutR, srvInW, grants, func(tool string) {
		mu.Lock()
		deniedList = append(deniedList, tool)
		mu.Unlock()
	})
	defer client.Close()

	go func() {
		sc := bufio.NewScanner(srvInR)
		enc := json.NewEncoder(srvOutW)
		var nextPermID int64 = 100
		readPermReply := func() {
			if !sc.Scan() {
				return
			}
			var reply jsonrpcMessage
			if json.Unmarshal(sc.Bytes(), &reply) != nil {
				return
			}
			var res struct {
				Outcome struct {
					OptionID string `json:"optionId"`
				} `json:"outcome"`
			}
			_ = json.Unmarshal(reply.Result, &res)
			mu.Lock()
			permOpts = append(permOpts, res.Outcome.OptionID)
			mu.Unlock()
		}
		for sc.Scan() {
			var msg jsonrpcMessage
			if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
				continue
			}
			switch msg.Method {
			case "initialize":
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"protocolVersion": 1}})
			case "session/new":
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"sessionId": "sid-test-1"}})
			case "session/load", "session/set_model", "session/set_mode", "session/cancel", "session/close":
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{}})
			case "session/prompt":
				// Request permission with a string front ID (as `agy acp --process-per-session` sends)
				// and lowerCamelCase `commandLine`, plus a denied command (`curl ...`).
				id1 := "jetski-acp-front:1"
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0", "id": id1, "method": "session/request_permission",
					"params": map[string]any{
						"sessionId": "sid-test-1",
						"toolCall":  map[string]any{"title": "run_command", "kind": "execute", "rawInput": map[string]any{"commandLine": `git commit -m "fix: a | b; c" && go test ./... || true`}},
						"options":   []map[string]any{{"optionId": "allow_once", "kind": "allow_once"}, {"optionId": "reject_once", "kind": "reject_once"}},
					},
				})
				readPermReply()
				id2 := nextPermID
				nextPermID++
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0", "id": id2, "method": "session/request_permission",
					"params": map[string]any{
						"sessionId": "sid-test-1",
						"toolCall":  map[string]any{"title": "run_command", "kind": "execute", "rawInput": map[string]any{"CommandLine": "curl https://example.com"}},
						"options":   []map[string]any{{"optionId": "allow_always", "kind": "allow_always"}, {"optionId": "reject_once", "kind": "reject_once"}},
					},
				})
				readPermReply()
				// Emit tool_call (subagent), usage_update, and agent_message_chunk notifications
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0", "method": "session/update",
					"params": map[string]any{
						"sessionId": "sid-test-1",
						"update": map[string]any{
							"sessionUpdate": "tool_call",
							"name":          "invoke_subagent",
							"status":        "running",
							"rawInput":      map[string]any{"TypeName": "verifier", "Role": "Test Runner"},
						},
					},
				})
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0", "method": "session/update",
					"params": map[string]any{
						"sessionId": "sid-test-1",
						"update": map[string]any{
							"sessionUpdate": "agent_message_chunk",
							"content":       map[string]any{"type": "text", "text": "Hello from ACP!"},
						},
					},
				})
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0", "id": msg.ID,
					"result": map[string]any{
						"stopReason": "end_turn",
						"usage":      map[string]any{"inputTokens": 120, "outputTokens": 45},
					},
				})
			}
		}
	}()

	ctx := context.Background()
	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	sid, err := client.NewSession(ctx, "/tmp/work")
	if err != nil || sid != "sid-test-1" {
		t.Fatalf("NewSession = (%q, %v)", sid, err)
	}
	if err := client.LoadSession(ctx, sid, "/tmp/work"); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if err := client.SetSessionModel(ctx, sid, "gemini-3-flash"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	if err := client.SetSessionMode(ctx, sid, "accept-edits"); err != nil {
		t.Fatalf("SetSessionMode: %v", err)
	}

	var events []ACPEvent
	resp, evBytes, err := client.Prompt(ctx, sid, "gemini-3-flash", "/tmp/work", "test prompt", func(ev ACPEvent) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if resp.Text != "Hello from ACP!" || resp.InputTok != 120 || resp.OutputTok != 45 {
		t.Fatalf("unexpected PromptResponse: %+v", resp)
	}
	if len(events) < 3 || events[0].Kind != "init" || events[1].Kind != "subagent" || events[len(events)-1].Kind != "result" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if events[1].Summary != "verifier (Test Runner)" {
		t.Fatalf("subagent summary = %q, want %q", events[1].Summary, "verifier (Test Runner)")
	}
	evStr := string(evBytes)
	if !strings.Contains(evStr, `"event":"init"`) || !strings.Contains(evStr, `"event":"step_update"`) || !strings.Contains(evStr, `"event":"result"`) {
		t.Fatalf("events.jsonl missing expected lines:\n%s", evStr)
	}

	if err := client.Cancel(ctx, sid); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := client.CloseSession(ctx, sid); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	mu.Lock()
	gotDenied := slices.Clone(deniedList)
	gotOpts := slices.Clone(permOpts)
	mu.Unlock()
	if len(gotDenied) != 1 || !strings.Contains(gotDenied[0], "curl") {
		t.Fatalf("deniedList = %v, want 1 curl entry", gotDenied)
	}
	if !slices.Contains(gotOpts, "allow_once") || !slices.Contains(gotOpts, "reject_once") {
		t.Fatalf("permOpts = %v, want allow_once and reject_once", gotOpts)
	}
}

const fakeACPAgy = `#!/usr/bin/env python3
import json, os, sys

rec = @REC@
n_file = os.path.join(rec, "starts.n")
starts = 1
if os.path.exists(n_file):
    starts = int(open(n_file).read().strip()) + 1
open(n_file, "w").write(str(starts))
open(os.path.join(rec, f"args.{starts}"), "w").write("\n".join(sys.argv[1:]) + "\n")

turn = 0
while True:
    raw = sys.stdin.readline()
    if not raw:
        break
    raw = raw.strip()
    if not raw:
        continue
    msg = json.loads(raw)
    mid = msg.get("id")
    method = msg.get("method", "")
    if mid is not None and not method:
        continue
    if method == "initialize":
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": mid, "result": {"protocolVersion": 1}}) + "\n")
        sys.stdout.flush()
    elif method == "session/new":
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": mid, "result": {"sessionId": "warm-sid-42"}}) + "\n")
        sys.stdout.flush()
    elif method in ("session/set_model", "session/set_mode", "session/load", "session/cancel", "session/close"):
        sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": mid, "result": {}}) + "\n")
        sys.stdout.flush()
    elif method == "session/prompt":
        turn += 1
        params = msg.get("params", {})
        sid = params.get("sessionId", "")
        text = params.get("prompt", [{}])[0].get("text", "")
        if "DENY_ME" in text:
            sys.stdout.write(json.dumps({
                "jsonrpc": "2.0", "id": 900 + turn, "method": "session/request_permission",
                "params": {
                    "sessionId": sid,
                    "toolCall": {"title": "run_command", "kind": "execute", "rawInput": {"CommandLine": "curl https://bad.example"}},
                    "options": [{"optionId": "allow_always"}, {"optionId": "reject_once"}],
                }
            }) + "\n")
            sys.stdout.flush()
            _ = sys.stdin.readline()
        sys.stdout.write(json.dumps({
            "jsonrpc": "2.0", "method": "session/update",
            "params": {
                "sessionId": sid,
                "update": {
                    "sessionUpdate": "tool_call",
                    "name": "read_file",
                    "title": "read_file README.md",
                    "status": "completed",
                }
            }
        }) + "\n")
        sys.stdout.write(json.dumps({
            "jsonrpc": "2.0", "method": "session/update",
            "params": {
                "sessionId": sid,
                "update": {
                    "sessionUpdate": "agent_message_chunk",
                    "content": {"type": "text", "text": f"turn-{turn}:{sid}"},
                }
            }
        }) + "\n")
        sys.stdout.write(json.dumps({
            "jsonrpc": "2.0", "id": mid,
            "result": {"stopReason": "end_turn", "usage": {"inputTokens": 10 * turn, "outputTokens": 5 * turn}}
        }) + "\n")
        sys.stdout.flush()
`

func TestACPBoxWarmMultiTurnAndExitChecks(t *testing.T) {
	r := newRig(t)
	put(t, r.agy, strings.ReplaceAll(fakeACPAgy, "@REC@", ShellQuote(r.rec)), 0o755)

	var seenEvents []ACPEvent
	c := r.config("acp-warm")
	c.ACP = true
	c.OnEvent = func(ev ACPEvent) {
		seenEvents = append(seenEvents, ev)
	}
	b := r.prepared(c)
	ctx := context.Background()
	tok := filepath.Join(r.real, TokenRel)

	if rc := b.Session(ctx, r.agy, tok, "first turn", "out.txt"); rc != 0 {
		t.Fatalf("turn 1 rc = %d\n%s", rc, r.out.String())
	}
	if got := b.SessionID(); got != "warm-sid-42" {
		t.Fatalf("SessionID() = %q, want warm-sid-42", got)
	}
	if err := b.Steer(ctx, "steer note"); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if rc := b.Session(ctx, r.agy, tok, "second turn", "out2.txt"); rc != 0 {
		t.Fatalf("turn 2 rc = %d\n%s", rc, r.out.String())
	}

	// Verify warm reuse: fakeACPAgy was started only ONCE across both Session calls!
	if starts := strings.TrimSpace(r.read("starts.n")); starts != "1" {
		t.Fatalf("agy process starts = %q, want 1 (warm multi-turn reuse)", starts)
	}
	out1, _ := os.ReadFile(filepath.Join(b.Dir(), "out.txt"))
	out2, _ := os.ReadFile(filepath.Join(b.Dir(), "out2.txt"))
	if strings.TrimSpace(string(out1)) != "turn-1:warm-sid-42" {
		t.Fatalf("out.txt = %q, want turn-1:warm-sid-42", out1)
	}
	// Note: Steer incremented turn to 2, so second Session is turn 3
	if !strings.HasSuffix(strings.TrimSpace(string(out2)), ":warm-sid-42") {
		t.Fatalf("out2.txt = %q", out2)
	}
	sidBytes, err := os.ReadFile(filepath.Join(b.Dir(), "session.id"))
	if err != nil || strings.TrimSpace(string(sidBytes)) != "warm-sid-42" {
		t.Fatalf("session.id = %q, err = %v", sidBytes, err)
	}
	evBytes, err := os.ReadFile(filepath.Join(b.Dir(), "events.jsonl"))
	if err != nil || !strings.Contains(string(evBytes), `"event":"init"`) || !strings.Contains(string(evBytes), `"event":"result"`) {
		t.Fatalf("events.jsonl = %q, err = %v", evBytes, err)
	}
	if len(seenEvents) == 0 {
		t.Fatal("expected OnEvent callbacks to fire")
	}

	if !b.After() {
		t.Fatal("After reported sandbox not intact")
	}
	b.ExitChecks()
	if len(b.Reasons()) != 0 {
		t.Fatalf("unexpected reasons after ExitChecks: %v\n%s", b.Reasons(), r.out.String())
	}
}

func TestACPBoxAutoDeniedReason(t *testing.T) {
	r := newRig(t)
	put(t, r.agy, strings.ReplaceAll(fakeACPAgy, "@REC@", ShellQuote(r.rec)), 0o755)

	c := r.config("acp-denied")
	c.ACP = true
	b := r.prepared(c)
	ctx := context.Background()
	tok := filepath.Join(r.real, TokenRel)

	if rc := b.Session(ctx, r.agy, tok, "please DENY_ME now", "out.txt"); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if !b.After() {
		t.Fatal("After reported sandbox not intact")
	}
	b.ExitChecks()
	if !b.Has(ReasonDenied) {
		t.Fatalf("Reasons() = %v, want %q", b.Reasons(), ReasonDenied)
	}
}
