package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreInvocationWarmLatencyUnder40ms(t *testing.T) {
	dir := newRepo(t)
	home := t.TempDir()
	for pkgIdx := 0; pkgIdx < 6; pkgIdx++ {
		for fileIdx := 0; fileIdx < 6; fileIdx++ {
			rel := fmt.Sprintf("internal/pkg%d/mod%d.go", pkgIdx, fileIdx)
			src := fmt.Sprintf("package pkg%d\n\nfunc HandleSlice%d_%d(input string) (string, error) {\n\treturn input, nil\n}\n", pkgIdx, pkgIdx, fileIdx)
			writeFile(t, dir, rel, src)
		}
	}
	writeFile(t, dir, "service.go", hookSampleServiceSrc)
	commitAll(t, dir, "populate warm latency fixture")

	if err := Index(Options{Dir: dir, Home: home}, io.Discard); err != nil {
		t.Fatalf("Index: %v", err)
	}

	makePayload := func(convID string) []byte {
		b, _ := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"modelName":      "gemini-4-neon-high",
			"invocationNum":  1,
			"lastUserInput":  "Update ReconcileProjectIndex in service.go to validate empty root and run go test",
		})
		return b
	}

	// Prime SQLite driver / OS page cache once, then measure a fresh Turn-0 conversation.
	var primeOut bytes.Buffer
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(makePayload("conv-warm-prime"))}, &primeOut); err != nil {
		t.Fatalf("prime pre-invocation: %v", err)
	}

	//Forbid git subprocesses by putting an empty PATH during the warm Turn-0 measurement!
	t.Setenv("PATH", t.TempDir())
	payload := makePayload("conv-warm-turn0")
	var out bytes.Buffer
	start := time.Now()
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(payload)}, &out); err != nil {
		t.Fatalf("warm Turn-0 pre-invocation: %v", err)
	}
	warmLimit := 40 * time.Millisecond
	if defaultHookDeadline > 200*time.Millisecond {
		warmLimit = 200 * time.Millisecond // -race adds overhead under go test -race ./... contention
	}
	elapsed := time.Since(start)
	if elapsed >= warmLimit {
		t.Fatalf("warm Turn-0 PreInvocation took %v, want < %v", elapsed, warmLimit)
	}

	var resp PreInvocationOutput
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.InjectSteps) != 1 || resp.InjectSteps[0].SystemMessage == nil {
		t.Fatalf("unmarshal warm output: err=%v raw=%s", err, out.String())
	}
	sysMsg := resp.InjectSteps[0].SystemMessage.SystemMessage
	if !strings.Contains(sysMsg, "ReconcileProjectIndex") {
		t.Errorf("warm Turn-0 missing ReconcileProjectIndex slice:\n%s", sysMsg)
	}

	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatalf("openLedgerStore: %v", err)
	}
	defer st.close()
	rows, err := st.listConversationVetHistory("conv-warm-turn0", 5)
	if err != nil || len(rows) != 1 {
		t.Fatalf("listConversationVetHistory: err=%v rows=%+v", err, rows)
	}
	if rows[0].InjectedBytes != len(sysMsg) || rows[0].TimedOut || rows[0].DurationMS < 0 || rows[0].DurationMS >= int(warmLimit.Milliseconds()) {
		t.Errorf("unexpected v11 vet_history metrics: %+v (want InjectedBytes=%d, TimedOut=false, DurationMS<%d)", rows[0], len(sysMsg), warmLimit.Milliseconds())
	}
}

func TestVetHistoryV11TimeoutApprovalDedupAndKneeDedup(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, io.Discard); err != nil {
		t.Fatalf("Index: %v", err)
	}
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	// 1,240,000 bytes => 310K tokens (>= 300K 60% knee for gemini-4-neon-high).
	if err := os.WriteFile(transcript, bytes.Repeat([]byte("a"), 1_240_000), 0o600); err != nil {
		t.Fatal(err)
	}

	runPreInv := func(convID, prompt string, extra int) PreInvocationOutput {
		t.Helper()
		if extra > 0 {
			if err := os.WriteFile(transcript, bytes.Repeat([]byte("a"), 1_240_000+extra), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		req, _ := json.Marshal(map[string]any{
			"conversationId": convID,
			"workspacePaths": []string{dir},
			"transcriptPath": transcript,
			"modelName":      "gemini-4-neon-high",
			"invocationNum":  0,
			"lastUserInput":  prompt,
		})
		var out bytes.Buffer
		if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(req)}, &out); err != nil {
			t.Fatalf("pre-invocation(%q): %v", prompt, err)
		}
		var resp PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return resp
	}

	// 1. Turn 1 above 300K knee emits [CONTEXT_KNEE_WARNING] once.
	r1 := runPreInv("conv-dedup-v11", "Inspect ReconcileProjectIndex in service.go", 0)
	if len(r1.InjectSteps) == 0 || !strings.Contains(r1.InjectSteps[0].SystemMessage.SystemMessage, "300K 60% model knee") {
		t.Fatalf("Turn 1 expected 300K knee warning, got %+v", r1)
	}

	// 2. Consecutive short IntentApproval ("go all approved") turns in the same knee bucket:
	//    - do NOT repeat [CONTEXT_KNEE_WARNING] (so PASS_THROUGH returns empty InjectSteps)
	//    - collapse into a single vet_history row instead of inserting duplicate rows!
	for i := 1; i <= 3; i++ {
		rApp := runPreInv("conv-dedup-v11", "go all approved", i*1000)
		if len(rApp.InjectSteps) != 0 {
			t.Fatalf("approval turn #%d repeated knee banner on no-op pass-through (%+v)", i, rApp.InjectSteps)
		}
	}

	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatalf("openLedgerStore: %v", err)
	}
	rows, err := st.listConversationVetHistory("conv-dedup-v11", 10)
	st.close()
	if err != nil || len(rows) != 2 {
		t.Fatalf("expected 2 vet_history rows (1 question + 1 collapsed approval), got len=%d err=%v rows=%+v", len(rows), err, rows)
	}
	if rows[1].Intent != IntentApproval || rows[1].InjectedBytes != 0 {
		t.Errorf("collapsed approval row = %+v, want Intent=APPROVAL InjectedBytes=0", rows[1])
	}

	// 3. Hard deadline timeout records best-effort timed_out = 1 in vet_history cleanly.
	done := make(chan struct{})
	defer func() { <-done }()
	reqTimeout, _ := json.Marshal(map[string]any{
		"conversationId": "conv-timeout-v11",
		"workspacePaths": []string{dir},
		"invocationNum":  1,
		"lastUserInput":  "Fix ReconcileProjectIndex in service.go",
	})
	var tOut bytes.Buffer
	err = Search(Options{
		Dir:          dir,
		Home:         home,
		Hook:         "pre-invocation",
		HookIn:       bytes.NewReader(reqTimeout),
		hookDeadline: 40 * time.Millisecond,
		hookDispatch: func(ctx context.Context, _ Options, _ string, _ string, _ HookInput, _ io.Writer) error {
			defer close(done)
			<-ctx.Done()
			return ctx.Err()
		},
	}, &tOut)
	if err != nil {
		t.Fatalf("timeout Search error: %v", err)
	}
	st2, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.close()
	tRows, err := st2.listConversationVetHistory("conv-timeout-v11", 5)
	if err != nil || len(tRows) != 1 || !tRows[0].TimedOut || tRows[0].DurationMS < 35 || tRows[0].InjectedBytes != 0 {
		t.Fatalf("timeout vet_history row = %+v, err=%v; want 1 row with TimedOut=true DurationMS>=35 InjectedBytes=0", tRows, err)
	}
}
