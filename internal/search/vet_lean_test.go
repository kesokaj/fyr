package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestVetPrecisionFixPlanAndSlashWords(t *testing.T) {
	if got := ClassifyIntent("Ok, add this to a fix plan and research this.", 0); got == IntentBug {
		t.Errorf("ClassifyIntent(\"fix plan\") = %s, want non-BUG", got)
	}
	if got := ClassifyIntent("Should we go forward with this design?", 0); got != IntentQuestion {
		t.Errorf("ClassifyIntent question = %s, want QUESTION", got)
	}
	skills, _ := MatchSkills("Should we go forward with this design?", IntentQuestion, nil, nil)
	for _, s := range skills {
		if s.Name == "golang" {
			t.Errorf("English word \"go\" in question matched golang skill: %+v", skills)
		}
	}

	present, _ := EvaluateAnchors("Compare argcon/carbon and pro/flash for yes/no routing", SliceResult{}, TurnBrainContext{})
	for _, a := range present {
		if a == "target" || a == "scope" {
			t.Errorf("prose slash terms falsely satisfied anchor %q in %v", a, present)
		}
	}
	presentGo, _ := EvaluateAnchors("Inspect internal/search/hook.go and README.md", SliceResult{}, TurnBrainContext{})
	foundTarget := false
	for _, a := range presentGo {
		if a == "target" {
			foundTarget = true
		}
	}
	if !foundTarget {
		t.Errorf("expected real repo paths to satisfy target anchor, got %v", presentGo)
	}
}

func TestCompactHookCapsuleAndLargeFnMapOnly(t *testing.T) {
	dir := newRepo(t)
	var bigSrc strings.Builder
	bigSrc.WriteString("package sample\n\nfunc BigWorker(root string) error {\n")
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&bigSrc, "\tif root == %q { return nil }\n", fmt.Sprintf("x-%d", i))
	}
	bigSrc.WriteString("\treturn SmallCallee(root)\n}\n\nfunc SmallCallee(root string) error {\n\treturn nil\n}\n")
	writeFile(t, dir, "worker.go", bigSrc.String())
	writeFile(t, dir, "worker_test.go", "package sample\nimport \"testing\"\nfunc TestBigWorker(t *testing.T) { _ = BigWorker(\"a\") }\n")
	commitAll(t, dir, "add big worker + test")

	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	sc, _, _, err := prepareHookScope(dir)
	if err != nil {
		t.Fatalf("prepareHookScope: %v", err)
	}
	defer sc.close()

	slice := sc.buildVetSlice(Options{
		Dir:     dir,
		Home:    home,
		Session: "conv-compact-map",
	}, "Inspect BigWorker in worker.go", SessionState{TurnCount: 1}, true)
	if !slice.Confident || slice.Capsule == "" {
		t.Fatalf("expected confident compact slice, got %+v", slice)
	}
	if len(slice.Capsule) > compactHookMaxBytes {
		t.Errorf("compact capsule len = %d, want <= %d:\n%s", len(slice.Capsule), compactHookMaxBytes, slice.Capsule)
	}
	if !strings.Contains(slice.Capsule, "[Hop 0] worker.go:3-30 (BigWorker) snippet —") {
		t.Errorf("expected >18-line BigWorker as snippet map line, got:\n%s", slice.Capsule)
	}
	if !strings.Contains(slice.Capsule, "graph:") || !strings.Contains(slice.Capsule, "affected_tests:") {
		t.Errorf("expected graph: and affected_tests: lines in compact capsule:\n%s", slice.Capsule)
	}
	for _, p := range ParseCapsulePivots(slice.Capsule) {
		if p.Path == "worker.go" && p.StartLine == 3 && p.EndLine == 30 {
			t.Errorf("snippet-only large function BigWorker should NOT pre-seed read_ledger pivot, got %+v", p)
		}
	}
}

func TestPreInvocationSessionDedupAndCompactionReset(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte("initial turn content\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	turnCtxCalls := 0
	turnCtx := func(_, _, _ string, _ []string) TurnBrainContext {
		turnCtxCalls++
		return TurnBrainContext{
			Memories: []MatchedRecord{{ID: "MEMORY-service-invariants", Kind: "memory", Status: "closed", Title: "Service invariants", Body: "Keep root non-empty."}},
			ADRs:     []MatchedRecord{{ID: "ADR-521", Kind: "adr", Status: "accepted", Title: "Lean vet hook", Body: "Never block clean prompts."}},
		}
	}
	runTurn := func(inv int, prompt string) string {
		inJSON, _ := json.Marshal(map[string]any{
			"conversationId": "conv-dedup-1",
			"workspacePaths": []string{dir},
			"transcriptPath": transcript,
			"invocationNum":  inv,
			"lastUserInput":  prompt,
		})
		var out bytes.Buffer
		if err := Search(Options{
			Dir:              dir,
			Home:             home,
			Hook:             "pre-invocation",
			HookIn:           bytes.NewReader(inJSON),
			BrainTurnContext: turnCtx,
		}, &out); err != nil {
			t.Fatalf("Search inv %d: %v", inv, err)
		}
		var resp PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal inv %d: %v", inv, err)
		}
		return resp.InjectSteps[0].SystemMessage.SystemMessage
	}

	// Turn 1: injects MEMORY-service-invariants, ADR-521, and golang skill.
	sys1 := runTurn(1, "Update ReconcileProjectIndex in service.go and run go test")
	for _, want := range []string{"MEMORY-service-invariants", "ADR-521", "skills/golang/SKILL.md"} {
		if !strings.Contains(sys1, want) {
			t.Errorf("Turn 1 missing %q:\n%s", want, sys1)
		}
	}

	// Turn 2 (transcript grows slightly -> new turn in same generation): dedup strips already-injected records and skills.
	if err := os.WriteFile(transcript, []byte("initial turn content\nmore lines after turn 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sys2 := runTurn(1, "Update AlphaHelper in service.go and run go test")
	for _, unwanted := range []string{"MEMORY-service-invariants", "ADR-521", "skills/golang/SKILL.md"} {
		if strings.Contains(sys2, unwanted) {
			t.Errorf("Turn 2 should deduplicate %q:\n%s", unwanted, sys2)
		}
	}

	// Compaction (transcript shrinks -> generation_id increments): records and skills re-prime!
	if err := os.WriteFile(transcript, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	sys3 := runTurn(1, "Update ReconcileProjectIndex in service.go and run go test")
	for _, want := range []string{"MEMORY-service-invariants", "ADR-521", "skills/golang/SKILL.md"} {
		if !strings.Contains(sys3, want) {
			t.Errorf("Post-compaction Turn 3 missing %q:\n%s", want, sys3)
		}
	}
	if turnCtxCalls < 3 {
		t.Errorf("turnCtxCalls = %d, expected >= 3", turnCtxCalls)
	}
}

func TestCompactSigPreservesUTF8Runes(t *testing.T) {
	sig := "func Render(αβγδεζηθ string) → error"
	for maxLen := 4; maxLen <= len(sig)+2; maxLen++ {
		got := compactSig(sig, maxLen)
		if !utf8.ValidString(got) {
			t.Fatalf("compactSig(sig, %d) produced invalid UTF-8: %q", maxLen, got)
		}
		if len(got) > maxLen {
			t.Fatalf("compactSig(sig, %d) len=%d exceeds maxLen: %q", maxLen, len(got), got)
		}
	}
}
