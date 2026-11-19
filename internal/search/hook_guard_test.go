package search

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
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
	callHook := func(invNum int, extra map[string]any, userPrompt string) PreInvocationOutput {
		payload := map[string]any{
			"conversationId": "conv-midturn",
			"workspacePaths": []string{dir},
			"transcriptPath": transcript,
			"invocationNum":  invNum,
			"lastUserInput":  userPrompt,
		}
		for k, v := range extra {
			payload[k] = v
		}
		inJSON, _ := json.Marshal(payload)
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

	// 1. Invocation 0 of Turn 1 (0-indexed in agy) injects capsule + turn contract with nil Metadata (BUG-523).
	r1 := callHook(0, nil, prompt)
	if len(r1.InjectSteps) != 1 || r1.InjectSteps[0].SystemMessage == nil {
		t.Fatalf("turn 1 inv 0: expected 1 injectStep, got %+v", r1)
	}
	if !strings.Contains(r1.InjectSteps[0].SystemMessage.SystemMessage, "[FYR_TURN_CONTRACT]") {
		t.Errorf("turn 1 inv 0 missing [FYR_TURN_CONTRACT]")
	}
	if r1.InjectSteps[0].SystemMessage.Metadata != nil {
		t.Errorf("turn 1 inv 0 Metadata must stay nil (BUG-523), got %+v", r1.InjectSteps[0].SystemMessage.Metadata)
	}

	// 2. Invocation 1 with stepIdx > initialNumSteps (mid-turn tool loop, same prompt, no compaction) skips re-injection and keeps turn_count=1.
	r2 := callHook(1, map[string]any{"initialNumSteps": 0, "stepIdx": 2}, prompt)
	if len(r2.InjectSteps) != 0 {
		t.Errorf("turn 1 inv 1 (mid-turn): expected 0 injectSteps, got %+v", r2.InjectSteps)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, turnCount := st.currentGeneration("conv-midturn")
	st.close()
	if turnCount != 1 {
		t.Errorf("after mid-turn inv 1, turn_count = %d, want 1", turnCount)
	}

	// 3. SD67: New user turn (invocationNum >= 1, but stepIdx == initialNumSteps, executionNum == 0, continuationActive == false) with the SAME prompt runs in full and increments turn_count to 2.
	r3 := callHook(2, map[string]any{"initialNumSteps": 4, "stepIdx": 4, "executionNum": 0, "continuationActive": false}, prompt)
	if len(r3.InjectSteps) != 1 || r3.InjectSteps[0].SystemMessage == nil {
		t.Fatalf("turn 2 inv 2 (same prompt, new turn): expected 1 injectStep, got %+v", r3)
	}
	st2, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, turnCount2 := st2.currentGeneration("conv-midturn")
	st2.close()
	if turnCount2 != 2 {
		t.Errorf("after turn 2 inv 2, turn_count = %d, want 2", turnCount2)
	}
}

func TestSD59PureGoIgnoreCommonDirAndIndexChecks(t *testing.T) {
	// 1. ignoreFileCoversScratch handles leading /, /**, and unrelated ! negations.
	giDir := t.TempDir()
	giPath := filepath.Join(giDir, ".gitignore")
	if err := os.WriteFile(giPath, []byte("/.agents/scratch/**\n!*.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !ignoreFileCoversScratch(giPath) {
		t.Fatalf("expected /.agents/scratch/** + !*.md to cover scratch")
	}
	if err := os.WriteFile(giPath, []byte("/.agents/**\n!.agents/scratch/search.db\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ignoreFileCoversScratch(giPath) {
		t.Fatalf("expected explicit !.agents/scratch/search.db negation to fail cover check")
	}

	// 2. resolveGitCommonDirFast rejects forged .git -> victim commonDir back-links.
	victim := setupHookRepo(t)
	attacker := t.TempDir()
	dotGit := filepath.Join(attacker, ".git")
	if err := os.WriteFile(dotGit, []byte("gitdir: "+filepath.Join(victim, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cd, ok := resolveGitCommonDirFast(dotGit, attacker); ok {
		t.Fatalf("forged direct .git link unexpectedly accepted: %q", cd)
	}
	fakeGD := filepath.Join(t.TempDir(), "fake-wt")
	if err := os.MkdirAll(fakeGD, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(fakeGD, "gitdir"), []byte(dotGit+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(fakeGD, "commondir"), []byte(filepath.Join(victim, ".git")+"\n"), 0o644)
	_ = os.WriteFile(dotGit, []byte("gitdir: "+fakeGD+"\n"), 0o644)
	if cd, ok := resolveGitCommonDirFast(dotGit, attacker); ok {
		t.Fatalf("forged fake-wt -> victim commondir unexpectedly accepted: %q", cd)
	}

	// 3. guardProject catches tracked companion via readGitIndex and runs zero git subprocesses (PATH="").
	repo := setupHookRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("/.agents/scratch/**\n!.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".agents", "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	wal := filepath.Join(repo, ".agents", "scratch", "search.db-wal")
	if err := os.WriteFile(wal, []byte("wal"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", "-f", ".agents/scratch/search.db-wal").CombinedOutput(); err != nil {
		t.Fatalf("git add -f wal: %v\n%s", err, out)
	}
	fresh := setupHookRepo(t)
	t.Setenv("PATH", "")
	if err := guardProject(repo); err == nil || !strings.Contains(err.Error(), "is tracked") {
		t.Fatalf("guardProject with tracked -wal = %v, want tracked error", err)
	}
	if err := ensureHookScratchIgnored(fresh, nil); err != nil {
		t.Fatalf("ensureHookScratchIgnored with PATH=\"\" failed: %v", err)
	}
	if err := guardProject(fresh); err != nil {
		t.Fatalf("guardProject after ensureHookScratchIgnored failed: %v", err)
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

func TestHookInputNormalizationAndProjectDirResolution(t *testing.T) {
	raw := []byte(`{
		"artifact_directory_path": "/home/dev/.gemini/antigravity-cli/brain/conv-from-artifact",
		"tool_call": {
			"tool_name": "CORTEX_STEP_TYPE_VIEW_FILE",
			"arguments": "{\"filePath\":\"pkg/a.go\",\"startLine\":5,\"lineCount\":10}"
		}
	}`)
	in, err := parseAndNormalizeHookInput(Options{}, raw)
	if err != nil {
		t.Fatalf("parseAndNormalizeHookInput: %v", err)
	}
	if in.ConversationID != "conv-from-artifact" {
		t.Fatalf("ConversationID = %q, want conv-from-artifact", in.ConversationID)
	}
	if got := normalizeHookToolName(in.ToolCall.Name); got != "read_file" {
		t.Fatalf("normalizeHookToolName(%q) = %q, want read_file", in.ToolCall.Name, got)
	}
	if p := extractToolTargetPath(in.ToolCall.Args); p != "pkg/a.go" {
		t.Fatalf("extractToolTargetPath = %q, want pkg/a.go", p)
	}
	if s, e := extractLineRangeArgs(in.ToolCall.Args); s != 5 || e != 14 {
		t.Fatalf("extractLineRangeArgs = (%d, %d), want (5, 14)", s, e)
	}

	// When cwd/o.Dir is one checkout (e.g. ~/.gemini dotfiles) and absolutePath targets another checkout,
	// resolveToolProjectDir routes to target's checkout and still honors .agents/scratch/no-hooks there.
	otherRepo := setupHookRepo(t)
	targetRepo := setupHookRepo(t)
	targetFile := filepath.Join(targetRepo, "service.go")
	if got := resolveToolProjectDir(otherRepo, HookInput{}, targetFile); CanonicalizePath("", got) != CanonicalizePath("", targetRepo) {
		t.Fatalf("resolveToolProjectDir = %q, want %q", got, targetRepo)
	}
	if err := os.MkdirAll(filepath.Join(targetRepo, ".agents", "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetRepo, ".agents", "scratch", "no-hooks"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(map[string]any{
		"conversationId": "conv-disabled-target",
		"toolCall":       map[string]any{"name": "view_file", "args": map[string]any{"absolutePath": targetFile}},
	})
	var out bytes.Buffer
	if err := Search(Options{Dir: otherRepo, Home: t.TempDir(), Hook: "pre-tool-use", HookIn: bytes.NewReader(req)}, &out); err != nil {
		t.Fatalf("pre-tool-use with target no-hooks: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetRepo, ".agents", "scratch", "search.db")); err == nil {
		t.Fatalf("expected targetRepo search.db not to be created when no-hooks marker is present")
	}
}

func TestValidateCommonDirForRootLinkedWorktreeNoGit(t *testing.T) {
	mainRepo := setupHookRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "-C", mainRepo, "worktree", "add", "-b", "wt-branch", wt).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	for _, gi := range []string{
		filepath.Join(wt, ".gitignore"),
		filepath.Join(wt, ".agents", ".gitignore"),
		filepath.Join(wt, ".agents", "scratch", ".gitignore"),
	} {
		_ = os.Remove(gi)
	}
	commonDir := filepath.Join(mainRepo, ".git")

	t.Setenv("PATH", "")
	if !validateCommonDirForRoot(wt, commonDir) {
		t.Fatalf("validateCommonDirForRoot(%q, %q) = false, want true", wt, commonDir)
	}
	if err := ensureHookScratchIgnored(wt, nil); err != nil {
		t.Fatalf("ensureHookScratchIgnored(%q) with PATH=\"\" failed: %v", wt, err)
	}
	excludeBytes, err := os.ReadFile(filepath.Join(commonDir, "info", "exclude"))
	if err != nil || !strings.Contains(string(excludeBytes), ".agents/scratch/*") {
		t.Fatalf("expected commonDir info/exclude to contain .agents/scratch/*, err=%v content=%q", err, string(excludeBytes))
	}
	if err := guardProject(wt); err != nil {
		t.Fatalf("guardProject(%q) failed: %v", wt, err)
	}
}

func TestValidateCommonDirForRootRejectsUnregisteredCommonDir(t *testing.T) {
	victim := setupHookRepo(t)
	victimCommon := filepath.Join(victim, ".git")
	attacker := t.TempDir()
	fakeGD := filepath.Join(t.TempDir(), "outside", "wt1")
	if err := os.MkdirAll(fakeGD, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeGD, "gitdir"), []byte(filepath.Join(attacker, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeGD, "commondir"), []byte(victimCommon+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(attacker, ".git"), []byte("gitdir: "+fakeGD+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", "")
	if validateCommonDirForRoot(attacker, victimCommon) {
		t.Fatalf("validateCommonDirForRoot unexpectedly accepted unregistered commondir outside worktrees/")
	}
	if err := writePatternsToValidatedExclude(attacker, victimCommon, []string{".agents/scratch/*"}); err == nil {
		t.Fatalf("writePatternsToValidatedExclude unexpectedly succeeded for unregistered commondir")
	}
	if err := ensureHookScratchIgnored(attacker, nil); err == nil {
		t.Fatalf("ensureHookScratchIgnored unexpectedly succeeded for unregistered commondir")
	}
	if data, err := os.ReadFile(filepath.Join(victimCommon, "info", "exclude")); err == nil && strings.Contains(string(data), ".agents/scratch/*") {
		t.Fatalf("victim info/exclude was modified despite refusal: %q", string(data))
	}

	// Missing HEAD inside an otherwise matching commonDir must also be refused.
	noHeadCommon := filepath.Join(t.TempDir(), "common")
	wtGD := filepath.Join(noHeadCommon, "worktrees", "wt1")
	wtRoot := t.TempDir()
	if err := os.MkdirAll(wtGD, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtGD, "gitdir"), []byte(filepath.Join(wtRoot, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtRoot, ".git"), []byte("gitdir: "+wtGD+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if validateCommonDirForRoot(wtRoot, noHeadCommon) {
		t.Fatalf("validateCommonDirForRoot accepted commonDir without regular HEAD")
	}
}
