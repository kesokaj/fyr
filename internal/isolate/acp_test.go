package isolate

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestACPShellGrantAndSubshellBypass(t *testing.T) {
	grants := []string{"command(echo)", "command(allowed)", "command(cat)", "command(tee)"}
	cases := []struct {
		cmd  string
		want bool
	}{
		{`echo "$(allowed)"`, true},
		{`echo "$(echo "a;b")"`, true},
		{`echo '$(allowed; evil)'`, true},
		{`echo "$(allowed; evil)"`, false},
		{`echo "$(allowed && evil)"`, false},
		{"echo \"`allowed; evil`\"", false},
		{`cat <(allowed; evil)`, false},
		{`tee >(allowed | evil)`, false},
		{`echo "$(echo "a;b"; evil)"`, false},
		{`echo "$( (allowed); evil )"`, false},
		{`echo "$((1+2); evil)"`, false},
		{`echo "unclosed`, false},
		{`echo $(allowed`, false},
		{"echo `allowed", false},
		{`echo 'unclosed`, false},
		{`echo a)`, false},
		{"echo ok # \"\nevil\n# \"", false},
		{"echo ok # '\nevil\n# '", false},
		{`echo $'x\'' ; evil # '`, false},
		{"echo ok # \"comment\"\necho still-ok", true},
		{`echo $'x\n\'y'`, true},
		{"echo ok\\\n#; evil", false},
		{"echo ok\r#; evil", false},
		{"echo ok\v#; evil", false},
		{"echo ok\f#; evil", false},
		{"echo ok\r#\"\"; evil", false},
	}
	for _, tc := range cases {
		if got := matchCommandGrants(grants, tc.cmd); got != tc.want {
			t.Errorf("matchCommandGrants(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}

	if ok, _ := checkToolGrant(nil, acpPermParams{ToolName: "read_file"}); ok {
		t.Fatal("empty grants allowed tool")
	}
	if ok, _ := checkToolGrant([]string{"read_file(*)"}, acpPermParams{ToolName: "view_file"}); !ok {
		t.Fatal("read_file(*) denied view_file")
	}
	if ok, _ := checkToolGrant([]string{"write_file(*)"}, acpPermParams{ToolName: "edit_file"}); !ok {
		t.Fatal("write_file(*) denied edit_file")
	}
	if ok, _ := checkToolGrant([]string{"command(*)"}, acpPermParams{Command: "rm -rf /"}); !ok {
		t.Fatal("command(*) denied command")
	}
	if got := pickOptionID([]acpPermOption{{OptionID: "allow_always", Kind: "allow"}}, true); got != "allow_always" {
		t.Fatalf("pickOptionID = %q, want allow_always", got)
	}
}

func TestACPClientProtocolAndHazards(t *testing.T) {
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	var denied []string
	var deniedMu sync.Mutex
	client := NewACPClient(s2cR, c2sW, []string{"command(git)"}, func(tool string) {
		deniedMu.Lock()
		denied = append(denied, tool)
		deniedMu.Unlock()
	})
	defer func() {
		_ = c2sW.Close()
		_ = s2cW.Close()
		_ = client.Close()
	}()

	errCh := make(chan error, 1)
	go runMockACPServer(c2sR, s2cW, errCh)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	sid, err := client.NewSession(ctx, "/tmp/app")
	if err != nil || sid != "sess-1" {
		t.Fatalf("NewSession = (%q, %v)", sid, err)
	}
	if err := client.SetSessionModel(ctx, sid, "gemini-4-carbon-high"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	if err := client.SetSessionMode(ctx, sid, "accept-edits"); err != nil {
		t.Fatalf("SetSessionMode: %v", err)
	}

	var events []ACPEvent
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = client.Steer(ctx, sid, "keep going")
	}()
	resp, ndjson, err := client.Prompt(ctx, sid, "gemini-4-carbon-high", "/tmp/app", "hello", func(ev ACPEvent) {
		events = append(events, ev)
	})
	wg.Wait()
	if err != nil || resp == nil || resp.Text != "hi!" || !strings.Contains(string(ndjson), `"tool_call"`) || len(events) < 3 {
		t.Fatalf("Prompt = (%+v, %q, %v, events=%d)", resp, ndjson, err, len(events))
	}
	if err := client.CloseSession(ctx, sid); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	deniedMu.Lock()
	if len(denied) == 0 || !strings.Contains(denied[0], "rm") {
		t.Errorf("denied = %v, want rm denial", denied)
	}
	deniedMu.Unlock()

	// Close while call pending removes pending entry cleanly.
	_ = s2cW.Close()
	_ = client.Close()
	if err := client.Initialize(ctx); err == nil {
		t.Fatal("expected error after Close")
	}
	client.mu.Lock()
	nPending := len(client.pending)
	client.mu.Unlock()
	if nPending != 0 {
		t.Fatalf("pending map leaked %d entries", nPending)
	}
}

func runMockACPServer(r io.Reader, w io.Writer, errCh chan<- error) {
	sc := bufio.NewScanner(r)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) != nil || msg.Method == "" {
			continue
		}
		switch msg.Method {
		case "initialize", "session/set_model", "session/set_mode", "session/close":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{}})
		case "session/new":
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"sessionId": "sess-1"}})
		case "session/prompt":
			if strings.Contains(string(msg.Params), "keep going") {
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{}})
				continue
			}
			// Request one allowed tool, one denied tool, and one unknown method.
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 901, "method": "session/request_permission", "params": map[string]any{"command": "git status"}})
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 902, "method": "session/request_permission", "params": map[string]any{"command": "rm -rf /"}})
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 903, "method": "unknown/method", "params": map[string]any{}})
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "sess-1", "update": map[string]any{"sessionUpdate": "tool_call", "name": "git", "title": "git status"}}})
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "sess-1", "update": map[string]any{"sessionUpdate": "usage_update", "usage": map[string]any{"inputTokens": 10, "outputTokens": 5}}}})
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "sess-1", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "text": "hi!"}}})
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"stopReason": "end_turn"}})
		}
	}
	errCh <- sc.Err()
}

func TestACPBoundedLineOverflow(t *testing.T) {
	pr, pw := io.Pipe()
	rOut, wOut := io.Pipe()
	t.Cleanup(func() {
		_ = pr.Close()
		_ = pw.Close()
		_ = rOut.Close()
		_ = wOut.Close()
	})
	client := NewACPClient(pr, wOut, nil, nil)
	go func() {
		chunk := strings.Repeat("a", 1<<20)
		for i := 0; i < 17; i++ {
			if _, err := io.WriteString(pw, chunk); err != nil {
				return
			}
		}
		_ = pw.Close()
	}()
	select {
	case <-client.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("client did not close on oversized line")
	}
	if err := client.Close(); err == nil || !strings.Contains(err.Error(), "max size") {
		t.Fatalf("closeErr = %v, want max size error", err)
	}
}

func TestACPSessionSetModelErrorAndRace(t *testing.T) {
	r := newRig(t)
	badACP := filepath.Join(r.bin, "agy-bad-acp")
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"initialize"'*) echo '{"jsonrpc":"2.0","id":1,"result":{}}' ;;
    *'"session/new"'*) echo '{"jsonrpc":"2.0","id":2,"result":{"sessionId":"s1"}}' ;;
    *'"session/set_model"'*) echo '{"jsonrpc":"2.0","id":3,"error":{"code":-32602,"message":"bad model"}}' ;;
  esac
done
`
	put(t, badACP, script, 0o755)
	c := r.config("acp-bad")
	c.ACP = true
	c.Model = "bad-model"
	c.Timeout = 3 * time.Second
	b := r.prepared(c)
	tok := filepath.Join(r.real, TokenRel)
	if rc := b.Session(context.Background(), badACP, tok, "hi", "out.txt"); rc != -1 {
		t.Fatalf("Session rc = %d, want -1 when SetSessionModel fails", rc)
	}
	if !strings.Contains(r.out.String(), "bad model") {
		t.Errorf("expected bad model error note, got:\n%s", r.out.String())
	}
	b.After()
	b.ExitChecks()

	// Good ACP session + concurrent Steer/SessionID/Reasons under -race.
	goodACP := filepath.Join(r.bin, "agy-good-acp")
	goodScript := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s\n' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  case "$line" in
    *'"session/new"'*) printf '{"jsonrpc":"2.0","id":%s,"result":{"sessionId":"s2"}}\n' "$id" ;;
    *'"session/prompt"'*)
      sleep 0.05
      printf '{"jsonrpc":"2.0","id":%s,"result":{"stopReason":"end_turn","text":"ok"}}\n' "$id"
      ;;
    *'"session/close"'*)
      printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id"
      exit 0
      ;;
    *) printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id" ;;
  esac
done
`
	put(t, goodACP, goodScript, 0o755)
	c2 := r.config("acp-good")
	c2.ACP = true
	c2.Model = "m1"
	c2.Timeout = 5 * time.Second
	b2 := r.prepared(c2)
	done := make(chan int, 1)
	go func() {
		done <- b2.Session(context.Background(), goodACP, tok, "hi", "out.txt")
	}()
	for i := 0; i < 10; i++ {
		_ = b2.SessionID()
		_ = b2.Reasons()
		_ = b2.Started()
		_ = b2.TimedOut()
		_ = b2.Has(ReasonReplaced)
		_ = b2.Steer(context.Background(), "nudge")
		time.Sleep(10 * time.Millisecond)
	}
	if rc := <-done; rc != 0 {
		t.Fatalf("good ACP Session rc = %d\n%s", rc, r.out.String())
	}
	b2.After()
	b2.ExitChecks()
	if got, err := os.ReadFile(filepath.Join(b2.Dir(), "session.id")); err != nil || strings.TrimSpace(string(got)) != "s2" {
		t.Errorf("session.id = (%q, %v), want s2", got, err)
	}
}

func TestACPSteerWhilePromptInFlight(t *testing.T) {
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	client := NewACPClient(s2cR, c2sW, nil, nil)
	t.Cleanup(func() {
		_ = c2sW.Close()
		_ = s2cW.Close()
		_ = client.Close()
	})

	promptInFlight := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(c2sR)
		enc := json.NewEncoder(s2cW)
		var promptID json.RawMessage
		for sc.Scan() {
			var msg struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(sc.Bytes(), &msg) != nil || msg.Method != "session/prompt" {
				continue
			}
			if strings.Contains(string(msg.Params), "steer now") {
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{}})
				if len(promptID) > 0 {
					_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": promptID, "result": map[string]any{"stopReason": "end_turn", "text": "done"}})
				}
				continue
			}
			promptID = append([]byte(nil), msg.ID...)
			close(promptInFlight)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	type promptOut struct {
		resp *PromptResponse
		err  error
	}
	done := make(chan promptOut, 1)
	go func() {
		resp, _, err := client.Prompt(ctx, "s1", "m1", "/tmp", "work", nil)
		done <- promptOut{resp, err}
	}()
	select {
	case <-promptInFlight:
	case <-ctx.Done():
		t.Fatal("Prompt did not start in time")
	}
	if err := client.Steer(ctx, "s1", "steer now"); err != nil {
		t.Fatalf("Steer while Prompt in flight: %v", err)
	}
	out := <-done
	if out.err != nil || out.resp == nil || out.resp.Text != "done" {
		t.Fatalf("Prompt = (%+v, %v), want text=done", out.resp, out.err)
	}
}
