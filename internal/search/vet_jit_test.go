package search

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPreInvocationEmitsImprovedPromptAndSkillDigests(t *testing.T) {
	dir := setupHookRepo(t)
	writeFile(t, dir, "internal/alpha/alpha.go", "package alpha\n\nfunc AlphaWork() int { return 1 }\n")
	writeFile(t, dir, "internal/beta/beta.go", "package beta\n\nfunc BetaWork() int { return 2 }\n")
	commitAll(t, dir, "add multi-package files")
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte("turn 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	turnCtx := func(_, _, prompt string, _ []string) TurnBrainContext {
		return TurnBrainContext{
			ADRs: []MatchedRecord{
				{ID: "ADR-511", Kind: "adr", Status: "accepted", Title: "Zero-daemon hooks", Body: "Rejected: redis cluster."},
			},
		}
	}
	runTurn := func(prompt string) PreInvocationBanner {
		inJSON, _ := json.Marshal(map[string]any{
			"conversationId": "conv-jit-1a",
			"workspacePaths": []string{dir},
			"transcriptPath": transcript,
			"invocationNum":  0,
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
			t.Fatalf("Search pre-invocation: %v", err)
		}
		var resp PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil || len(resp.InjectSteps) == 0 || resp.InjectSteps[0].SystemMessage == nil {
			t.Fatalf("unexpected PreInvocationOutput (%v): %s", err, out.String())
		}
		return parseStructuredBanner(t, resp.InjectSteps[0].SystemMessage.SystemMessage)
	}

	// 1. Turn 1: high-score multi-clause prompt emits COORDINATE_SUBTASKS, ImprovedPrompt, SkillDigests, and disjoint Subtasks.
	p1 := "1. Update AlphaWork in internal/alpha/alpha.go returning error and verify with go test 2. Update BetaWork in internal/beta/beta.go returning error and verify with go test"
	b1 := runTurn(p1)
	if b1.Score == nil || *b1.Score < 80 {
		t.Fatalf("expected Score >= 80, got %v", b1.Score)
	}
	if b1.Directive != "COORDINATE_SUBTASKS" || b1.Route != "/fyr-swarm (isolated subtasks; zero shared memory)" {
		t.Errorf("Turn 1 directive/route = (%q, %q), want (COORDINATE_SUBTASKS, /fyr-swarm ...)", b1.Directive, b1.Route)
	}
	if b1.Enrichment == nil || !strings.Contains(b1.Enrichment.ImprovedPrompt, "[VET_IMPROVED_PROMPT]") {
		t.Fatalf("Turn 1 missing Enrichment.ImprovedPrompt: %+v", b1.Enrichment)
	}
	if len(b1.Enrichment.Subtasks) != 2 {
		t.Fatalf("Turn 1 subtasks len = %d, want 2: %+v", len(b1.Enrichment.Subtasks), b1.Enrichment.Subtasks)
	}
	assertDisjointSubtaskFiles(t, b1.Enrichment.Subtasks)
	if !slices.Contains(b1.Skills, "golang") || len(b1.Enrichment.SkillDigests) == 0 {
		t.Fatalf("Turn 1 expected golang in Skills and SkillDigests: skills=%v digests=%+v", b1.Skills, b1.Enrichment.SkillDigests)
	}
	for _, sd := range b1.Enrichment.SkillDigests {
		if sd.Name == "" || sd.Path == "" || sd.Digest == "" || len(sd.Digest) > maxSkillDigestBytes {
			t.Errorf("invalid SkillDigest entry: %+v", sd)
		}
	}

	// 2. Turn 2 (transcript grows, same generation): golang omitted from both Skills and SkillDigests.
	if err := os.WriteFile(transcript, []byte("turn 1\nturn 2 additional text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b2 := runTurn("Update AlphaWork in internal/alpha/alpha.go to return bool and verify with go test")
	if slices.Contains(b2.Skills, "golang") {
		t.Errorf("Turn 2 should deduplicate golang in Skills: %v", b2.Skills)
	}
	if b2.Enrichment != nil {
		for _, sd := range b2.Enrichment.SkillDigests {
			if sd.Name == "golang" {
				t.Errorf("Turn 2 should deduplicate golang in SkillDigests: %+v", b2.Enrichment.SkillDigests)
			}
		}
	}

	// 3. Turn 3 (transcript shrinks -> compaction increments generation_id): golang re-primes in Skills and SkillDigests.
	if err := os.WriteFile(transcript, []byte("c"), 0o600); err != nil {
		t.Fatal(err)
	}
	b3 := runTurn("Update BetaWork in internal/beta/beta.go to return bool and verify with go test")
	if !slices.Contains(b3.Skills, "golang") {
		t.Errorf("Turn 3 post-compaction should re-prime golang in Skills: %v", b3.Skills)
	}
	foundDigest := false
	if b3.Enrichment != nil {
		for _, sd := range b3.Enrichment.SkillDigests {
			if sd.Name == "golang" {
				foundDigest = true
			}
		}
	}
	if !foundDigest {
		t.Errorf("Turn 3 post-compaction should re-prime golang in SkillDigests: %+v", b3.Enrichment)
	}

	// 4. VET_FIRST takes precedence over COORDINATE_SUBTASKS when ADR conflict is present.
	bConflict := runTurn("1. Add redis cluster in internal/alpha/alpha.go; 2. Update internal/beta/beta.go")
	if bConflict.Directive != "VET_FIRST" {
		t.Errorf("expected VET_FIRST > COORDINATE_SUBTASKS precedence, got %q", bConflict.Directive)
	}

	// 5. `!` bypass gets PASS_THROUGH and persists !-prefixed raw_prompt, subtask_count=0, directive=PASS_THROUGH.
	bBypass := runTurn("!1. Fix internal/alpha/alpha.go; 2. Fix internal/beta/beta.go")
	if bBypass.Directive != "PASS_THROUGH" || (bBypass.Enrichment != nil && len(bBypass.Enrichment.Subtasks) != 0) {
		t.Errorf("expected PASS_THROUGH without subtasks on `!` bypass, got %+v", bBypass)
	}

	// 6. Verify vet_history persisted SubtaskCount and Directive cleanly across turns.
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatalf("openLedgerStore: %v", err)
	}
	defer st.close()
	rows, err := st.listConversationVetHistory("conv-jit-1a", 10)
	if err != nil || len(rows) < 5 {
		t.Fatalf("listConversationVetHistory: err=%v len=%d", err, len(rows))
	}
	if rows[0].SubtaskCount != 2 || rows[0].Directive != "COORDINATE_SUBTASKS" {
		t.Errorf("row 0 v7 fields = (subtasks=%d, directive=%q), want (2, COORDINATE_SUBTASKS)", rows[0].SubtaskCount, rows[0].Directive)
	}
	last := rows[len(rows)-1]
	if !strings.HasPrefix(last.RawPrompt, "!") || last.SubtaskCount != 0 || last.Directive != "PASS_THROUGH" {
		t.Errorf("bypass row = %+v, want RawPrompt starting with !, SubtaskCount=0, Directive=PASS_THROUGH", last)
	}

	// 7. fyr auto Kickoff never triggers COORDINATE_SUBTASKS.
	bAuto := runTurn("This is a Kickoff written by fyr auto.\nFiles: internal/alpha/alpha.go, internal/beta/beta.go\n1. Update A; 2. Update B")
	if bAuto.Directive == "COORDINATE_SUBTASKS" || (bAuto.Enrichment != nil && len(bAuto.Enrichment.Subtasks) != 0) {
		t.Errorf("fyr auto Kickoff unexpectedly decomposed: %+v", bAuto)
	}
}

func TestPreInvocationPassThroughEmptyZeroToken(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	for _, tc := range []struct {
		name    string
		prompt  string
		wantSys bool
	}{
		{name: "approval", prompt: "go all approved", wantSys: false},
		{name: "trivial", prompt: "Fix typo in README.md", wantSys: false},
		{name: "bang_bypass", prompt: "!Fix BUG-001", wantSys: true},
	} {
		req, _ := json.Marshal(map[string]any{
			"conversationId": "conv-banner-" + tc.name,
			"workspacePaths": []string{dir},
			"invocationNum":  1,
			"lastUserInput":  tc.prompt,
		})
		var out bytes.Buffer
		if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(req)}, &out); err != nil {
			t.Fatalf("%s: Search error: %v", tc.name, err)
		}
		var resp PreInvocationOutput
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.name, err)
		}
		if !tc.wantSys {
			if len(resp.InjectSteps) != 0 || strings.TrimSpace(out.String()) != "{}" {
				t.Errorf("%s: expected zero-token {} on un-enriched PASS_THROUGH, got: %s", tc.name, out.String())
			}
			continue
		}
		if len(resp.InjectSteps) != 1 || resp.InjectSteps[0].SystemMessage == nil {
			t.Fatalf("%s: expected 1 injectStep, got %+v", tc.name, resp)
		}
		sm := resp.InjectSteps[0].SystemMessage
		if !strings.Contains(sm.SystemMessage, "[FYR_TURN_CONTRACT]") {
			t.Errorf("%s: expected [FYR_TURN_CONTRACT] in systemMessage, got:\n%s", tc.name, sm.SystemMessage)
		}
		if sm.Metadata != nil {
			t.Errorf("%s: Metadata must stay nil (BUG-523), got: %+v", tc.name, sm.Metadata)
		}
	}
}
