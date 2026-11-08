package search

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripSandboxPreamble(t *testing.T) {
	preamble := "The project root is the current directory, /tmp/sb/app; ignore any other checkout. This is a print-mode run (`agy -p`): the turn's end is the run's end. "
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "strips standard sandbox preamble",
			in:   preamble + "Add a Redis cache cluster to the greeting service",
			want: "Add a Redis cache cluster to the greeting service",
		},
		{
			name: "preserves bang bypass after preamble",
			in:   preamble + "!Fix RenderGreeting in internal/greet/greet.go",
			want: "!Fix RenderGreeting in internal/greet/greet.go",
		},
		{
			name: "preserves slash command after preamble",
			in:   preamble + "/review --code",
			want: "/review --code",
		},
		{
			name: "leaves ordinary prompt untouched",
			in:   "How does RenderGreeting work in internal/greet/greet.go?",
			want: "How does RenderGreeting work in internal/greet/greet.go?",
		},
	}
	for _, tc := range cases {
		if got := StripSandboxPreamble(tc.in); got != tc.want {
			t.Errorf("%s: StripSandboxPreamble(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}

	// Verify that an underspecified FEATURE prompt wrapped in the sandbox preamble
	// still gets VET_FIRST instead of having its anchors satisfied by /tmp/sb/app and (`agy -p`).
	res := VetPrompt(preamble+"Build a new distributed caching layer", "", SliceResult{SymbolHits: 0}, TurnBrainContext{})
	if res.Intent != IntentFeature || res.Gate != GateVetFirst {
		t.Errorf("VetPrompt with sandbox preamble: got intent=%s gate=%s, want FEATURE / VET_FIRST (present=%v)", res.Intent, res.Gate, res.PresentAnchors)
	}
}

func TestCheckTurnOrMidTurn(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcript, bytes.Repeat([]byte("x"), 4000), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt := "Investigate ReconcileProjectIndex in service.go"
	callHook := func(invNum int, userPrompt string) PreInvocationOutput {
		inJSON, _ := json.Marshal(map[string]any{
			"conversationId": "conv-midturn",
			"workspacePaths": []string{dir},
			"transcriptPath": transcript,
			"invocationNum":  invNum,
			"lastUserInput":  userPrompt,
		})
		var out bytes.Buffer
		if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(inJSON)}, &out); err != nil {
			t.Fatalf("pre-invocation(%d): %v", invNum, err)
		}
		var resp PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal(%d): %v", invNum, err)
		}
		return resp
	}

	// 1. Invocation 1 of Turn 1 injects capsule + turn contract.
	r1 := callHook(1, prompt)
	if len(r1.InjectSteps) != 1 || r1.InjectSteps[0].SystemMessage == nil {
		t.Fatalf("turn 1 inv 1: expected 1 injectStep, got %+v", r1)
	}
	if !strings.Contains(r1.InjectSteps[0].SystemMessage.SystemMessage, "[FYR_TURN_CONTRACT]") {
		t.Errorf("turn 1 inv 1 missing [FYR_TURN_CONTRACT]")
	}

	// 2. Invocation 2 (mid-turn tool loop, same prompt, no compaction) skips re-injection and keeps turn_count=1.
	r2 := callHook(2, prompt)
	if len(r2.InjectSteps) != 0 {
		t.Errorf("turn 1 inv 2 (mid-turn): expected 0 injectSteps, got %+v", r2.InjectSteps)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, turnCount := st.currentGeneration("conv-midturn")
	st.close()
	if turnCount != 1 {
		t.Errorf("after mid-turn inv 2, turn_count = %d, want 1", turnCount)
	}

	// 3. New user turn (invocationNum == 1) with the SAME prompt runs in full and increments turn_count to 2.
	r3 := callHook(1, prompt)
	if len(r3.InjectSteps) != 1 || r3.InjectSteps[0].SystemMessage == nil {
		t.Fatalf("turn 2 inv 1 (same prompt, new turn): expected 1 injectStep, got %+v", r3)
	}
	st2, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, turnCount2 := st2.currentGeneration("conv-midturn")
	st2.close()
	if turnCount2 != 2 {
		t.Errorf("after turn 2 inv 1, turn_count = %d, want 2", turnCount2)
	}
}

func TestAutonomousWaiverAndRunSlash(t *testing.T) {
	// 1. "Run /init-project ..." is classified as FEATURE and gets PASS_THROUGH when no ADR conflict exists.
	slashRes := VetPrompt("Run /init-project in this repository. Do not ask me anything.", "", SliceResult{}, TurnBrainContext{})
	if slashRes.Intent != IntentFeature || slashRes.Gate != GatePassThrough {
		t.Errorf("Run /init-project: got intent=%s gate=%s, want FEATURE / PASS_THROUGH", slashRes.Intent, slashRes.Gate)
	}

	// 2. Underspecified FEATURE with explicit autonomous waiver ("Do not ask me anything") gets ENRICH_AND_PROCEED.
	waiverRes := VetPrompt("Build a new greeting cache. Do not ask me anything.", "", SliceResult{}, TurnBrainContext{})
	if waiverRes.Intent != IntentFeature || waiverRes.Gate != GateEnrichAndProceed {
		t.Errorf("FEATURE with autonomous waiver: got intent=%s gate=%s, want FEATURE / ENRICH_AND_PROCEED", waiverRes.Intent, waiverRes.Gate)
	}

	// 3. Even with an autonomous waiver, an ADR conflict still triggers VET_FIRST (D8).
	conflictCtx := TurnBrainContext{
		ADRs: []MatchedRecord{
			{ID: "ADR-001", Kind: "adr", Status: "accepted", Title: "Stdlib only", Body: "Rejected: redis cache, external caching daemons."},
		},
	}
	confRes := VetPrompt("Add a redis cache to the server. Do not ask me anything.", "", SliceResult{}, conflictCtx)
	if confRes.Gate != GateVetFirst {
		t.Errorf("ADR conflict with waiver: got gate=%s, want VET_FIRST", confRes.Gate)
	}
}

func TestHooksDisabledMarker(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	marker := filepath.Join(dir, ".agents", "scratch", "no-hooks")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inJSON, _ := json.Marshal(map[string]any{
		"conversationId": "conv-nohooks",
		"workspacePaths": []string{dir},
		"invocationNum":  1,
		"lastUserInput":  "Investigate ReconcileProjectIndex in service.go",
	})
	var out bytes.Buffer
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(inJSON)}, &out); err != nil {
		t.Fatalf("pre-invocation with no-hooks: %v", err)
	}
	var resp PreInvocationOutput
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.InjectSteps) != 0 {
		t.Errorf("expected 0 injectSteps when .agents/scratch/no-hooks is present, got %+v", resp.InjectSteps)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agents", "scratch", "search.db")); err == nil {
		t.Errorf("expected search.db not to be created when .agents/scratch/no-hooks is present")
	}
}
