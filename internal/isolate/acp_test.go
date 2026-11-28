package isolate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

	// Good ACP session + concurrent Box state reads under -race.
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
		_ = b2.currentACP()
		_ = b2.Reasons()
		_ = b2.Started()
		_ = b2.TimedOut()
		_ = b2.Has(ReasonReplaced)
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
