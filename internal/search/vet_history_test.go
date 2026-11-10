package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestVetHistoryTableAndHelpers(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatalf("openLedgerStore: %v", err)
	}
	defer st.close()

	res1 := VetResult{
		Intent:         IntentFeature,
		Gate:           GateVetFirst,
		Score:          15,
		PresentAnchors: []string{"goal"},
		MissingAnchors: []string{"target", "scope", "inputs_outputs", "constraints", "verification"},
		Skills:         []SkillMeta{{Name: "fyr-feature", Path: "skills/fyr-feature/SKILL.md"}},
	}
	if err := st.recordVetHistory("conv-hist", 1, "Build a new distributed cache", res1, []string{"service.go"}); err != nil {
		t.Fatalf("recordVetHistory turn 1: %v", err)
	}

	prev, err := st.lastVetHistory("conv-hist", 2)
	if err != nil || prev == nil {
		t.Fatalf("lastVetHistory(turn<2): err=%v prev=%+v", err, prev)
	}
	if prev.TurnNum != 1 || prev.Gate != GateVetFirst || prev.RawPrompt != "Build a new distributed cache" {
		t.Errorf("unexpected prev row: %+v", prev)
	}

	recentSkills, recentPivots, err := st.recentSuggestedSkills("conv-hist", 2, 3)
	if err != nil {
		t.Fatalf("recentSuggestedSkills: %v", err)
	}
	if !recentSkills["fyr-feature"] || len(recentPivots) != 1 || recentPivots[0] != "service.go" {
		t.Errorf("unexpected recentSkills=%v recentPivots=%v", recentSkills, recentPivots)
	}

	res2 := VetResult{
		Intent:         IntentTask,
		Gate:           GateEnrichAndProceed,
		Score:          85,
		ImprovedPrompt: "[VET_IMPROVED_PROMPT]\nscore: 85/100\ntask: cache in service.go",
		PresentAnchors: []string{"target", "goal", "scope", "inputs_outputs", "verification"},
		MissingAnchors: []string{"constraints"},
		Skills:         []SkillMeta{{Name: "golang", Path: "skills/golang/SKILL.md"}},
	}
	if err := st.recordVetHistory("conv-hist", 2, "In service.go return error on miss and run go test", res2, []string{"service.go"}); err != nil {
		t.Fatalf("recordVetHistory turn 2: %v", err)
	}

	rows, err := st.listConversationVetHistory("conv-hist", 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("listConversationVetHistory: err=%v len=%d", err, len(rows))
	}
	if rows[0].TurnNum != 1 || rows[1].TurnNum != 2 || rows[1].Score != 85 {
		t.Errorf("unexpected rows: %+v", rows)
	}
}

func TestVetScoreAndCarryForward(t *testing.T) {
	// 1. Full 100/100 score when all 6 anchors are present.
	brainCtx := TurnBrainContext{
		ADRs: []MatchedRecord{{ID: "ADR-511", Kind: "adr", Title: "Zero-daemon CLI hooks"}},
	}
	slice := SliceResult{
		Confident:  true,
		Pivots:     1,
		SymbolHits: 2,
		Files:      []string{"internal/search/store.go"},
		Capsule:    "[Hop 0] internal/search/store.go:40-68 (schemaSQL)",
	}
	score, present, missing := ComputeVetScore(
		"Update schemaSQL in internal/search/store.go to return error on busy and verify with go test",
		IntentTask,
		slice,
		brainCtx,
		nil,
	)
	if score != 100 || len(present) != 6 || len(missing) != 0 {
		t.Errorf("full anchor score = %d present=%v missing=%v, want 100 / 6 / 0", score, present, missing)
	}

	// 2. Conflict subtracts 40 from score.
	conflictCtx := TurnBrainContext{
		Conflicts: []string{"PRD.md R3: no daemons"},
	}
	confScore, _, _ := ComputeVetScore("Add background daemon in service.go", IntentFeature, slice, conflictCtx, nil)
	if confScore >= 45 {
		t.Errorf("expected conflict score < 45, got %d", confScore)
	}

	// 3. Carry-forward from Turn N-1 VET_FIRST merges anchors and upgrades Turn N to ENRICH_AND_PROCEED.
	prevRow := &VetHistoryRow{
		ConversationID: "conv-cf",
		TurnNum:        1,
		Intent:         IntentFeature,
		Gate:           GateVetFirst,
		Score:          15,
		RawPrompt:      "Build a new caching layer",
		PresentAnchors: []string{"goal"},
		MissingAnchors: []string{"target", "scope", "inputs_outputs", "constraints", "verification"},
	}
	cfScore, cfPresent, _ := ComputeVetScore(
		"In internal/search/store.go return error on miss and verify with go test",
		IntentFeature,
		slice,
		brainCtx,
		prevRow,
	)
	if cfScore < 85 || len(cfPresent) < 5 {
		t.Errorf("carry-forward score = %d present=%v, want >= 85 and >= 5 anchors", cfScore, cfPresent)
	}

	// 4. 3-turn skill deduplication suppresses already-suggested skills on unchanged file surface,
	// and re-allows them when the file surface changes.
	skills := []SkillMeta{{Name: "golang", Path: "skills/golang/SKILL.md"}}
	suppressed := filterSuggestedSkills(skills, []string{"service.go"}, map[string]bool{"golang": true}, []string{"service.go"})
	if len(suppressed) != 0 {
		t.Errorf("expected golang skill suppressed on unchanged surface, got %+v", suppressed)
	}
	unsuppressed := filterSuggestedSkills(skills, []string{"web/app.ts", "cmd/main.go"}, map[string]bool{"golang": true}, []string{"service.go"})
	if len(unsuppressed) != 1 {
		t.Errorf("expected skill allowed when file surface changes, got %+v", unsuppressed)
	}
}

func TestSynthesizeImprovedPromptAndStatefulHookTurn(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	brainCtx := TurnBrainContext{
		ADRs: []MatchedRecord{{ID: "ADR-511", Kind: "adr", Title: "Zero-daemon CLI hooks"}},
		Bugs: []MatchedRecord{{ID: "BUG-507", Kind: "bug", Title: "Guard real home writes"}},
	}
	slice := SliceResult{
		Confident:  true,
		Pivots:     1,
		SymbolHits: 2,
		Files:      []string{"internal/search/service.go"},
		Capsule:    "[Hop 0] internal/search/service.go:3-5 (ReconcileProjectIndex)",
	}

	start := time.Now()
	res := VetPrompt(
		"Fix ReconcileProjectIndex in internal/search/service.go to return error on empty root and verify with go test",
		IntentBug,
		slice,
		brainCtx,
	)
	elapsed := time.Since(start)
	if elapsed >= 2*time.Millisecond {
		t.Logf("note: VetPrompt + SynthesizeImprovedPrompt took %v", elapsed)
	}
	if res.Score < 45 {
		t.Fatalf("expected Score >= 45, got %d", res.Score)
	}
	for _, want := range []string{
		"[VET_IMPROVED_PROMPT]",
		"score: ",
		"targets: internal/search/service.go:3-5 (ReconcileProjectIndex)",
		"scope: internal/search/service.go",
		"constraints: ADR-511 (Zero-daemon CLI hooks); BUG-507 (Guard real home writes)",
		"verify: go test -race ./internal/search/...",
		"directive: EXECUTE_IMPROVED_PROMPT",
	} {
		if !strings.Contains(res.TurnContract, want) {
			t.Errorf("TurnContract missing %q:\n%s", want, res.TurnContract)
		}
	}

	// End-to-end stateful multi-turn hook: Turn 1 is VET_FIRST, Turn 2 answers it and carries forward Turn 1.
	in1, _ := json.Marshal(map[string]any{
		"conversationId": "conv-stateful-vet",
		"workspacePaths": []string{dir},
		"invocationNum":  1,
		"lastUserInput":  "Build a new caching layer",
	})
	var out1 bytes.Buffer
	if err := Search(Options{Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(in1)}, &out1); err != nil {
		t.Fatalf("hook turn 1: %v", err)
	}
	var resp1 PreInvocationOutput
	if err := json.Unmarshal(out1.Bytes(), &resp1); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp1.InjectSteps[0].SystemMessage.SystemMessage, "directive: VET_FIRST") {
		t.Fatalf("expected Turn 1 VET_FIRST, got:\n%s", resp1.InjectSteps[0].SystemMessage.SystemMessage)
	}

	in2, _ := json.Marshal(map[string]any{
		"conversationId": "conv-stateful-vet",
		"workspacePaths": []string{dir},
		"invocationNum":  2,
		"lastUserInput":  "Put it in ReconcileProjectIndex in service.go, return error on miss, and verify with go test",
	})
	var out2 bytes.Buffer
	if err := Search(Options{Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(in2)}, &out2); err != nil {
		t.Fatalf("hook turn 2: %v", err)
	}
	var resp2 PreInvocationOutput
	if err := json.Unmarshal(out2.Bytes(), &resp2); err != nil {
		t.Fatal(err)
	}
	sys2 := resp2.InjectSteps[0].SystemMessage.SystemMessage
	for _, want := range []string{
		"directive: EXECUTE_IMPROVED_PROMPT",
		"[VET_IMPROVED_PROMPT]",
		"Build a new caching layer (follow-up turn 1:",
		"service.go:3-5 (ReconcileProjectIndex)",
	} {
		if !strings.Contains(sys2, want) {
			t.Errorf("Turn 2 systemMessage missing %q:\n%s", want, sys2)
		}
	}
}

func TestBuildVetSliceFiltersUnconfidentShellFiles(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	// Add a shell fixture with 3-letter helper functions ("can", "see", "one") that used to leak into SliceResult.Files.
	writeFile(t, dir, "scripts/fixture.sh", "#!/usr/bin/env bash\ncan() { echo ok; }\nsee() { echo ok; }\none() { echo ok; }\n")
	commitAll(t, dir, "add shell fixture")
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	sc, _, _, err := prepareHookScope(dir)
	if err != nil {
		t.Fatalf("prepareHookScope: %v", err)
	}
	defer sc.close()

	// Unconfident prompt with 3-letter words ("can", "see", "one") must return Files: nil.
	unconf := sc.buildVetSlice(Options{Dir: dir, Home: home}, "I want you to investigate if we can make it better. As one example I see it loading the bash skill very often.", SessionState{}, false)
	if len(unconf.Files) != 0 {
		t.Errorf("expected unconfident buildVetSlice Files=nil, got %v", unconf.Files)
	}

	// Confident prompt matching ReconcileProjectIndex must populate Files ONLY from pivots (service.go), excluding scripts/fixture.sh.
	conf := sc.buildVetSlice(Options{Dir: dir, Home: home}, "Investigate ReconcileProjectIndex when we can see one failure", SessionState{}, false)
	if !conf.Confident || len(conf.Files) == 0 {
		t.Fatalf("expected confident slice with pivot files, got %+v", conf)
	}
	for _, f := range conf.Files {
		if strings.HasSuffix(f, ".sh") {
			t.Errorf("confident slice leaked non-pivot .sh file %q in Files=%v", f, conf.Files)
		}
	}

	// EvaluateAnchors must not count unconfident SliceResult.SymbolHits/Files toward target or scope.
	present, _ := EvaluateAnchors("Something is broken", SliceResult{SymbolHits: 4, Files: []string{"scripts/fixture.sh"}, Confident: false}, TurnBrainContext{})
	for _, a := range present {
		if a == "target" || a == "scope" {
			t.Errorf("unconfident slice falsely satisfied anchor %q in %v", a, present)
		}
	}
}

func TestMatchSkillsBashVsGolangAndDiagnosticMentions(t *testing.T) {
	// 1. Diagnostic mention ("loading the bash skill very often") suppresses bash.
	skills1, sec1 := MatchSkills(
		"I want you to investigate the VET_PROCESS if we can make it better. As 1 example i see it loading the bash skill very often.",
		IntentBug,
		[]string{"internal/search/vet.go", "internal/search/hook.go"},
		nil,
	)
	if sec1 {
		t.Errorf("expected SecurityRequired=false on VET_PROCESS investigation")
	}
	for _, s := range skills1 {
		if s.Name == "bash" {
			t.Errorf("expected bash skill NOT to match diagnostic prompt, got %+v", skills1)
		}
	}
	if len(skills1) < 2 || skills1[0].Name != "fyr-bug" || skills1[1].Name != "golang" {
		t.Errorf("expected [fyr-bug, golang] for Go pivot investigation, got %+v", skills1)
	}

	// 2. Negated mention ("don't use docker") suppresses docker.
	skills2, _ := MatchSkills("Run tests in internal/work/worker.go and don't use docker", IntentTask, []string{"internal/work/worker.go"}, nil)
	for _, s := range skills2 {
		if s.Name == "docker" {
			t.Errorf("expected negated docker skill to be suppressed, got %+v", skills2)
		}
	}

	// 3. Genuine shell script prompt triggers bash.
	skills3, _ := MatchSkills("Fix shellcheck quoting in scripts/check-trailers.sh with jq", IntentTask, []string{"scripts/check-trailers.sh"}, nil)
	foundBash := false
	for _, s := range skills3 {
		if s.Name == "bash" {
			foundBash = true
		}
	}
	if !foundBash {
		t.Errorf("expected bash skill on .sh + shellcheck/jq prompt, got %+v", skills3)
	}

	// 4. Expanded skill surfaces match mermaid-syntax, fyr-work, and frontend-ui-modern.
	skillsMermaid, _ := MatchSkills("Add a Mermaid flowchart diagram to README.md", IntentTask, []string{"README.md"}, nil)
	if len(skillsMermaid) == 0 || skillsMermaid[0].Name != "mermaid-syntax" {
		t.Errorf("expected mermaid-syntax skill, got %+v", skillsMermaid)
	}
	skillsWork, _ := MatchSkills("Update fyr work kickoff parser in internal/work/kickoff.go", IntentTask, []string{"internal/work/kickoff.go", "internal/work/worker.go"}, nil)
	if len(skillsWork) < 2 || skillsWork[0].Name != "fyr-work" || skillsWork[1].Name != "golang" {
		t.Errorf("expected [fyr-work, golang], got %+v", skillsWork)
	}
}

func TestVetPromptClauseAndSecurityPrecision(t *testing.T) {
	// 1. Mid-sentence "will actually", "can actually", "that actually", "to actually" must NOT classify as CORRECTION.
	for _, p := range []string{
		"Update VetPrompt so the agent will actually execute the task",
		"Check if buildVetSlice can actually filter .sh files in hook.go",
		"Write a helper in vet.go that actually scores anchors",
	} {
		if got := ClassifyIntent(p, 1); got == IntentCorrection {
			t.Errorf("ClassifyIntent(%q) = CORRECTION, want non-CORRECTION", p)
		}
	}

	// 2. Meta/conditional release mentions ("write me a prompt ... when this is done ship it") must NOT classify as RELEASE.
	metaRelease := "Write me a prompt for Phase 1-6 and when this is done ship it and cut v8.3.0"
	if got := ClassifyIntent(metaRelease, 1); got == IntentRelease {
		t.Errorf("ClassifyIntent(%q) = RELEASE, want non-RELEASE", metaRelease)
	}

	// 3. Bare "token" or "fence" in non-security prompts must NOT set SecurityRequired.
	_, sec := MatchSkills("Reduce token usage in formatTurnContract in fence.go", IntentTask, []string{"internal/search/fence.go"}, nil)
	if sec {
		t.Errorf("expected SecurityRequired=false for token usage / fence.go prompt")
	}
}

func TestHookFyrProcessArtifact(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	brainDir := filepath.Join(t.TempDir(), "brain", "conv-vis-1")
	transcriptPath := filepath.Join(brainDir, ".system_generated", "logs", "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workStatusDir := filepath.Join(home, harness.StateDir, "work", "2026-11-10-120000-111")
	if err := os.MkdirAll(workStatusDir, 0o700); err != nil {
		t.Fatal(err)
	}
	workStatusJSON := `{"stamp":"2026-11-10-120000-111","updated_at":"2026-11-10T12:00:05Z","workers":[{"name":"w-fix","state":"PASS","model":"gemini-4-argon","step_count":4,"commits":1,"base_check":"PASS","judge_model":"claude-opus-5-5","judge_verdict":"PASS","last_step":"judge (claude-opus-5-5): PASS"}]}`
	if err := os.WriteFile(filepath.Join(workStatusDir, "status.json"), []byte(workStatusJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	inJSON := fmt.Sprintf(`{"conversationId":"conv-vis-1","workspacePaths":[%q],"transcriptPath":%q,"invocationNum":1,"lastUserInput":"Fix ReconcileProjectIndex in service.go and run go test ./..."}`, dir, transcriptPath)
	if err := RunHook(Options{Hook: "pre-invocation", Dir: dir, Home: home}, strings.NewReader(inJSON), &out); err != nil {
		t.Fatalf("RunHook pre-invocation: %v", err)
	}
	if !strings.Contains(out.String(), "status_line: 🧭 **fyr**") {
		t.Errorf("expected status_line in PreInvocation output, got:\n%s", out.String())
	}
	artifactPath := filepath.Join(brainDir, "fyr-process.md")
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("reading %s: %v", artifactPath, err)
	}
	text := string(data)
	for _, want := range []string{
		"# 🧭 `fyr` Live Process & Turn Telemetry",
		"flowchart LR",
		"Turn-by-Turn `fyr vet` History",
		"[VET_IMPROVED_PROMPT]",
		"## Active / Latest `fyr work` Wave (`2026-11-10-120000-111`)",
		"| `w-fix` | `PASS` | `gemini-4-argon` | `PASS` | `PASS (claude-opus-5-5)` | 4 | 1 |",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("fyr-process.md missing %q in:\n%s", want, text)
		}
	}
}
