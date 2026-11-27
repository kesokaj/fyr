package search

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
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

	// 3. SD67 / F-002: New user turn (invocationNum == 1, stepIdx == initialNumSteps, executionNum == 0, continuationActive == false) with the SAME prompt runs in full and increments turn_count to 2.
	r3 := callHook(1, map[string]any{"initialNumSteps": 4, "stepIdx": 4, "executionNum": 0, "continuationActive": false}, prompt)
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
		t.Errorf("after turn 2 inv 2, turn_count = %d, want 2", turnCount2)
	}
}

func TestSD59PureGoIgnoreCommonDirAndIndexChecks(t *testing.T) {
	// 1. ignoreFileScratchState handles leading /, /**, and unrelated ! negations.
	giDir := t.TempDir()
	giPath := filepath.Join(giDir, ".gitignore")
	if err := os.WriteFile(giPath, []byte("/.agents/scratch/**\n!*.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ignoreFileScratchState(giPath, patternCoversRootScratch) != 1 {
		t.Fatalf("expected /.agents/scratch/** + !*.md to cover scratch")
	}
	if err := os.WriteFile(giPath, []byte("/.agents/**\n!.agents/scratch/search.db\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ignoreFileScratchState(giPath, patternCoversRootScratch) == 1 {
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

func TestResolveGitWorktreeDir(t *testing.T) {
	t.Run("regular dotgit dir", func(t *testing.T) {
		root := t.TempDir()
		dotGit := filepath.Join(root, ".git")
		if err := os.MkdirAll(dotGit, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := resolveGitWorktreeDir(root); got != dotGit {
			t.Errorf("resolveGitWorktreeDir = %q, want %q", got, dotGit)
		}
	})
	t.Run("missing or symlinked dotgit", func(t *testing.T) {
		root := t.TempDir()
		if got := resolveGitWorktreeDir(root); got != "" {
			t.Errorf("missing .git = %q, want empty", got)
		}
		target := filepath.Join(t.TempDir(), "realgit")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, ".git")); err != nil {
			t.Fatal(err)
		}
		if got := resolveGitWorktreeDir(root); got != "" {
			t.Errorf("symlink .git = %q, want empty", got)
		}
	})
	t.Run("dotgit file variants", func(t *testing.T) {
		wtTarget := filepath.Join(t.TempDir(), "wt-meta")
		if err := os.MkdirAll(wtTarget, 0o755); err != nil {
			t.Fatal(err)
		}
		rootAbs := t.TempDir()
		if err := os.WriteFile(filepath.Join(rootAbs, ".git"), []byte("gitdir: "+wtTarget+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := resolveGitWorktreeDir(rootAbs); got != wtTarget {
			t.Errorf("absolute gitdir = %q, want %q", got, wtTarget)
		}
		rootRel := t.TempDir()
		relTarget := filepath.Join(rootRel, ".git-wt", "wt1")
		if err := os.MkdirAll(relTarget, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rootRel, ".git"), []byte("gitdir: .git-wt/wt1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := resolveGitWorktreeDir(rootRel); got != relTarget {
			t.Errorf("relative gitdir = %q, want %q", got, relTarget)
		}
		symTarget := filepath.Join(t.TempDir(), "sym-wt")
		if err := os.Symlink(wtTarget, symTarget); err != nil {
			t.Fatal(err)
		}
		badCases := []struct {
			name, body string
		}{
			{"no prefix", wtTarget},
			{"empty gitdir", "gitdir:   "},
			{"multiline", "gitdir: " + wtTarget + "\nextra"},
			{"missing dir", "gitdir: " + filepath.Join(t.TempDir(), "absent")},
			{"symlink target", "gitdir: " + symTarget},
		}
		for _, tc := range badCases {
			badRoot := t.TempDir()
			if err := os.WriteFile(filepath.Join(badRoot, ".git"), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := resolveGitWorktreeDir(badRoot); got != "" {
				t.Errorf("%s: got %q, want empty", tc.name, got)
			}
		}
	})
}

func testCheckTurnOrMidTurnInput(dir string, in HookInput, rawPrompt string) (SessionState, bool, error) {
	st, _, err := openLedgerStore(dir)
	if err != nil {
		return SessionState{}, false, err
	}
	defer st.close()
	return checkTurnOrMidTurnWithStore(context.Background(), st, in, rawPrompt)
}

func TestAdvanceOrSkipTurnConsecutiveIdenticalPromptVsF002MidTurn(t *testing.T) {
	dir := setupHookRepo(t)
	prompt := "continue"
	// Turn 1: inv 1, initialNumSteps=4, stepIdx=4 (fresh user turn).
	st1, skip1, err := testCheckTurnOrMidTurnInput(dir, HookInput{
		ConversationID: "conv-ident", InvocationNum: 1, InitialNumSteps: 4, StepIdx: 4,
	}, prompt)
	if err != nil || skip1 || st1.TurnCount != 1 {
		t.Fatalf("turn 1: state=%+v skip=%v err=%v", st1, skip1, err)
	}
	// Turn 2: consecutive IDENTICAL prompt, inv 2 > lastInvNum(1), but explicit new-turn cue
	// (ContinuationActive=false, ExecutionNum=0, InitialNumSteps=8, StepIdx=8) MUST NOT skip.
	st2, skip2, err := testCheckTurnOrMidTurnInput(dir, HookInput{
		ConversationID: "conv-ident", InvocationNum: 2, InitialNumSteps: 8, StepIdx: 8,
	}, prompt)
	if err != nil || skip2 || st2.TurnCount != 2 {
		t.Fatalf("consecutive identical prompt new turn: state=%+v skip=%v err=%v, want skip=false turn=2", st2, skip2, err)
	}
	// F-002 mid-turn continuation: inv 3 > lastInvNum(2) with InitialNumSteps=0 MUST still skip.
	st3, skip3, err := testCheckTurnOrMidTurnInput(dir, HookInput{
		ConversationID: "conv-ident", InvocationNum: 3, InitialNumSteps: 0, StepIdx: 0,
	}, prompt)
	if err != nil || !skip3 || st3.TurnCount != 2 {
		t.Fatalf("F-002 mid-turn inv=3 (InitialNumSteps=0): state=%+v skip=%v err=%v, want skip=true turn=2", st3, skip3, err)
	}
}

func TestScanTranscriptCheckpointTailJumpPartialLineAndStrictJSON(t *testing.T) {
	// 1. Tail-scan jump landing mid-line right before a fake CHECKPOINT JSON object must NOT trigger.
	tr1 := filepath.Join(t.TempDir(), "tail.jsonl")
	fakeTail := `{"type":"CHECKPOINT"}` + "\n" + `{"type":"assistant","text":"ok"}` + "\n"
	// Craft file so curBytes - maxCheckpointScanBytes lands inside an assistant line right before fakeTail.
	padLen := maxCheckpointScanBytes - len(fakeTail)
	var b bytes.Buffer
	b.WriteString(`{"type":"assistant","text":"`)
	b.Write(bytes.Repeat([]byte("a"), 1024))
	b.WriteString(fakeTail[:len(fakeTail)-1]) // still inside same line -> will end with \n after pad
	// Place a fake {"type":"CHECKPOINT"} right at scanStart (mid-line, preceded by non-newline 'x').
	b.Reset()
	b.WriteString(`{"type":"assistant","text":"`)
	b.Write(bytes.Repeat([]byte("x"), 2048))
	// scanStart will be len(content) - maxCheckpointScanBytes; put fake CHECKPOINT starting at scanStart.
	b.WriteString(`{"type":"CHECKPOINT"}` + "\n")
	b.Write(bytes.Repeat([]byte(`{"type":"assistant","text":"pad"}`+"\n"), (padLen/34)+1))
	// Exact construction: prefix (2048 bytes, no newline) + {"type":"CHECKPOINT"}\n + remaining bytes = maxCheckpointScanBytes - len(`{"type":"CHECKPOINT"}\n`).
	b.Reset()
	prefix := `{"type":"assistant","text":"` + strings.Repeat("x", 2048)
	fakeLine := `{"type":"CHECKPOINT"}` + "\n"
	tailLine := `{"type":"assistant","text":"z"}` + "\n"
	rem := maxCheckpointScanBytes - len(fakeLine)
	reps := rem / len(tailLine)
	extra := rem - reps*len(tailLine)
	b.WriteString(prefix)
	b.WriteString(strings.Repeat("y", extra))
	b.WriteString(fakeLine)
	for i := 0; i < reps; i++ {
		b.WriteString(tailLine)
	}
	if err := os.WriteFile(tr1, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, cps, ok := scanTranscriptCheckpoint(tr1, 0)
	if !ok || cps != 0 {
		t.Fatalf("tail-jump partial line fake CHECKPOINT: cps=%d ok=%v, want 0, true", cps, ok)
	}

	// 2. Malformed/unparseable JSON line containing "type":"CHECKPOINT" must NOT trigger.
	tr2 := filepath.Join(t.TempDir(), "badjson.jsonl")
	if err := os.WriteFile(tr2, []byte(`{"type":"CHECKPOINT", "broken": `+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, cps2, ok2 := scanTranscriptCheckpoint(tr2, 0)
	if !ok2 || cps2 != 0 {
		t.Fatalf("malformed JSON CHECKPOINT: cps=%d ok=%v, want 0, true", cps2, ok2)
	}

	// 3. Valid complete CHECKPOINT line after tail jump MUST still trigger.
	tr3 := filepath.Join(t.TempDir(), "realcp.jsonl")
	var b3 bytes.Buffer
	b3.Write(bytes.Repeat([]byte(`{"type":"assistant","text":"`+strings.Repeat("p", 100)+`"}`+"\n"), 6000))
	b3.WriteString(`{"type":"CHECKPOINT","summary":"ok"}` + "\n")
	if err := os.WriteFile(tr3, b3.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, cps3, ok3 := scanTranscriptCheckpoint(tr3, 0)
	if !ok3 || cps3 != 1 {
		t.Fatalf("valid CHECKPOINT after tail jump: cps=%d ok=%v, want 1, true", cps3, ok3)
	}
}

func TestDetectDirtyPathsFastNonRegularAndAlreadyDeletedOrOversized(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	writeFile(t, dir, "gone.go", "package sample\nfunc Gone() {}\n")
	writeFile(t, dir, "symmed.go", "package sample\nfunc Symmed() {}\n")
	writeFile(t, dir, "huge.bin", strings.Repeat("A", maxFileBytes+128))
	// Backdate huge.bin before commitAll so its index entry is deterministically
	// non-racy (st.sec < indexTime.Unix()) and exercises refreshTracked's stat-clean fast path.
	past := time.Now().Add(-5 * time.Second)
	if err := os.Chtimes(filepath.Join(dir, "huge.bin"), past, past); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "add gone symmed huge")
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// 1. Oversized tracked file (never in files), even when mtime is touched (non-statClean), must produce 0 dirty deltas.
	if err := os.WriteFile(filepath.Join(dir, "huge.bin"), bytes.Repeat([]byte("B"), maxFileBytes+256), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := openStore(projectDB(dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	dirty0, _, ok0 := detectDirtyPathsFast(context.Background(), dir, st)
	if !ok0 || len(dirty0) != 0 {
		t.Fatalf("oversized tracked file produced dirty=%+v ok=%v, want empty, true", dirty0, ok0)
	}

	// 2. Replace tracked regular file with symlink and delete another tracked file.
	if err := os.Remove(filepath.Join(dir, "gone.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "symmed.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "service.go"), filepath.Join(dir, "symmed.go")); err != nil {
		t.Fatal(err)
	}
	dirty1, _, ok1 := detectDirtyPathsFast(context.Background(), dir, st)
	if !ok1 {
		t.Fatalf("detectDirtyPathsFast after rm+symlink returned ok=false")
	}
	gotDel := map[string]bool{}
	for _, e := range dirty1 {
		if e.state == "deleted" {
			gotDel[e.path] = true
		}
	}
	if !gotDel["gone.go"] || !gotDel["symmed.go"] {
		t.Fatalf("1st fast scan dirty=%+v, want deleted entries for both gone.go and symmed.go", dirty1)
	}
	if err := st.applyDirtyPaths(context.Background(), dir, dirty1, 10); err != nil {
		t.Fatalf("applyDirtyPaths: %v", err)
	}

	// 3. 2nd fast scan after deletions were reconciled must produce 0 dirty deltas.
	dirty2, _, ok2 := detectDirtyPathsFast(context.Background(), dir, st)
	if !ok2 || len(dirty2) != 0 {
		t.Fatalf("2nd fast scan after reconciling deleted/symlinked/oversized paths = %+v (ok=%v), want empty", dirty2, ok2)
	}
}

func TestRelayPostToolUseNotBypassedByGlobalNoHooks(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	stateDir := filepath.Join(home, harness.StateDir)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "no-hooks"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FYR_RELAY", "1")
	if hookBypassedForEvent(home, dir, "post-tool-use") {
		t.Fatal("hookBypassedForEvent returned true for post-tool-use during FYR_RELAY=1")
	}
	// RunHook end-to-end for pre-tool-use + post-tool-use must record tool error into watchdog_ring despite global no-hooks.
	prePayload, _ := json.Marshal(map[string]any{
		"conversationId": "conv-relay-post",
		"workspacePaths": []string{dir},
		"toolCall":       map[string]any{"id": "call-1", "name": "run_command", "args": map[string]any{"CommandLine": "false"}},
	})
	var preOut bytes.Buffer
	if err := RunHook(Options{Home: home, Dir: dir, Hook: "pre-tool-use"}, bytes.NewReader(prePayload), &preOut); err != nil {
		t.Fatalf("RunHook pre-tool-use: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"conversationId": "conv-relay-post",
		"workspacePaths": []string{dir},
		"toolCall":       map[string]any{"id": "call-1", "name": "run_command", "args": map[string]any{"CommandLine": "false"}},
		"error":          "exit status 1",
	})
	var out bytes.Buffer
	if err := RunHook(Options{Home: home, Dir: dir, Hook: "post-tool-use"}, bytes.NewReader(payload), &out); err != nil {
		t.Fatalf("RunHook post-tool-use: %v", err)
	}
	st, _, err := openLedgerStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	var n int
	if err := st.db.QueryRow(`SELECT count(*) FROM watchdog_ring WHERE conversation_id = 'conv-relay-post' AND is_error = 1`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("watchdog_ring error count = %d (err=%v), want 1 under FYR_RELAY=1 + global no-hooks", n, err)
	}
}

func TestGitCtxAndBrainTurnContextCtxCancellation(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gitCtx(cctx, dir, "status"); err == nil {
		t.Fatal("gitCtx with canceled context returned nil error")
	}
	// Verify startParallelTurnWork invokes BrainTurnContextCtx with a bounded ctx when provided.
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"conversationId": "conv-ctx-brain",
		"workspacePaths": []string{dir},
		"invocationNum":  1,
		"lastUserInput":  "Inspect ReconcileProjectIndex in service.go",
	})
	sawCtxWithDeadline := false
	fallbackCalled := false
	var out bytes.Buffer
	if err := Search(Options{
		Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(payload),
		BrainTurnContext: func(string, string, string, []string) TurnBrainContext {
			fallbackCalled = true
			return TurnBrainContext{}
		},
		BrainTurnContextCtx: func(ctx context.Context, d, h, prompt string, files []string) TurnBrainContext {
			if ctx != nil {
				if _, ok := ctx.Deadline(); ok {
					sawCtxWithDeadline = true
				}
			}
			return TurnBrainContext{}
		},
	}, &out); err != nil {
		t.Fatalf("Search pre-invocation: %v", err)
	}
	if !sawCtxWithDeadline {
		t.Fatal("expected BrainTurnContextCtx to be invoked with a deadline-bounded context")
	}
	if fallbackCalled {
		t.Fatal("BrainTurnContext fallback should not run when BrainTurnContextCtx is set")
	}
}

func TestRelayBashPreservesGitTerminalPromptOverwriteWithNoHooks(t *testing.T) {
	runBash := func(home, dir string) HookDecisionOutput {
		payload, _ := json.Marshal(map[string]any{
			"conversationId": "conv-relay-bash",
			"workspacePaths": []string{dir},
			"toolCall":       map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": "git fetch origin"}},
		})
		var out bytes.Buffer
		if err := RunHook(Options{Home: home, Dir: dir, Hook: "pre-tool-use"}, bytes.NewReader(payload), &out); err != nil {
			t.Fatalf("RunHook pre-tool-use: %v", err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("unmarshal decision: %v (raw=%q)", err, out.String())
		}
		return dec
	}

	// (a) Global no-hooks under FYR_RELAY=1 must still prepend GIT_TERMINAL_PROMPT=0.
	dir1, home1 := setupHookRepo(t), t.TempDir()
	stateDir := filepath.Join(home1, harness.StateDir)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "no-hooks"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Non-relay must-pass control: global no-hooks skips overwrite.
	t.Setenv("FYR_RELAY", "")
	if dec := runBash(home1, dir1); dec.Decision != "allow" || dec.Overwrite != nil {
		t.Fatalf("non-relay global no-hooks = %+v, want plain allow without Overwrite", dec)
	}
	t.Setenv("FYR_RELAY", "1")
	decGlobal := runBash(home1, dir1)
	if cmd, _ := decGlobal.Overwrite["CommandLine"].(string); decGlobal.Decision != "allow" || !strings.Contains(cmd, "GIT_TERMINAL_PROMPT=0") {
		t.Fatalf("relay + global no-hooks = %+v, want allow with GIT_TERMINAL_PROMPT=0 Overwrite", decGlobal)
	}

	// (b) Repo .agents/scratch/no-hooks under FYR_RELAY=1 skips bash read clamping but preserves Overwrite.
	dir2, home2 := setupHookRepo(t), t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir2, ".agents", "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, ".agents", "scratch", "no-hooks"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	decRepo := runBash(home2, dir2)
	if cmd, _ := decRepo.Overwrite["CommandLine"].(string); decRepo.Decision != "allow" || !strings.Contains(cmd, "GIT_TERMINAL_PROMPT=0") {
		t.Fatalf("relay + repo no-hooks = %+v, want allow with GIT_TERMINAL_PROMPT=0 Overwrite", decRepo)
	}
}

func TestBrainTurnContextCtxUsedAcrossHookPaths(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	type ctxKey struct{}
	wantCtxVal := "active-ctx"
	calls := 0
	sawCtxKey := 0
	ctxCallback := func(ctx context.Context, d, h, prompt string, files []string) TurnBrainContext {
		calls++
		if ctx != nil && ctx.Value(ctxKey{}) == wantCtxVal {
			sawCtxKey++
		}
		return TurnBrainContext{
			ActivePlan: &MatchedRecord{ID: "PLAN-CTX", Kind: "plan", Title: "Ctx Plan"},
			Bugs:       []MatchedRecord{{ID: "BUG-CTX", Kind: "bug", Status: "open", Title: "Ctx Bug"}},
		}
	}
	optsOnlyCtx := Options{Dir: dir, Home: home, BrainTurnContextCtx: ctxCallback}

	// 1. session-start (extractSessionPlan) uses BrainTurnContextCtx with active ctx.
	var ssOut bytes.Buffer
	if err := handleSessionStart(context.WithValue(context.Background(), ctxKey{}, wantCtxVal), optsOnlyCtx, dir, HookInput{}, &ssOut); err != nil {
		t.Fatalf("handleSessionStart: %v", err)
	}
	if !strings.Contains(ssOut.String(), "PLAN-CTX") || sawCtxKey != 1 {
		t.Fatalf("handleSessionStart did not invoke BrainTurnContextCtx with active ctx: sawCtxKey=%d out=%s", sawCtxKey, ssOut.String())
	}

	// 2. lookupWorkerRecords uses BrainTurnContextCtx.
	recs := lookupWorkerRecords(optsOnlyCtx, dir, "fix service", []string{"service.go"})
	if len(recs) == 0 || recs[0].ID != "BUG-CTX" {
		t.Fatalf("lookupWorkerRecords did not invoke BrainTurnContextCtx: recs=%+v", recs)
	}

	// 3. runVet uses BrainTurnContextCtx.
	var vetOut bytes.Buffer
	if err := Search(Options{Dir: dir, Home: home, Vet: "fix ReconcileProjectIndex in service.go", BrainTurnContextCtx: ctxCallback}, &vetOut); err != nil {
		t.Fatalf("Search --vet: %v", err)
	}
	if !strings.Contains(vetOut.String(), "BUG-CTX") {
		t.Fatalf("runVet did not invoke BrainTurnContextCtx: out=%s", vetOut.String())
	}

	// 4. handleParallelHookTurn uses BrainTurnContextCtx with active ctx.
	prevCalls := calls
	_, _ = handleParallelHookTurn(
		context.WithValue(context.Background(), ctxKey{}, wantCtxVal),
		optsOnlyCtx, nil, dir,
		HookInput{ConversationID: "conv-env"}, SessionState{TurnCount: 1},
		"fix ReconcileProjectIndex in service.go", "fix ReconcileProjectIndex in service.go",
		time.Time{},
	)
	if calls <= prevCalls || sawCtxKey != 2 {
		t.Fatalf("handleParallelHookTurn did not invoke BrainTurnContextCtx with active ctx: calls=%d->%d sawCtxKey=%d", prevCalls, calls, sawCtxKey)
	}
}

func TestZeroIndexedInvocationNumAndBypassedSkillDigests(t *testing.T) {
	dir := setupHookRepo(t)
	prompt := "fix ReconcileProjectIndex in service.go"

	// 1. 0-indexed InvocationNum=0 followed by InvocationNum=1 (same prompt, no new-turn cue) returns skip=true.
	st1, skip1, err := testCheckTurnOrMidTurnInput(dir, HookInput{ConversationID: "conv-zero-mid", InvocationNum: 0}, prompt)
	if err != nil || skip1 || st1.TurnCount != 1 {
		t.Fatalf("inv=0 first call: state=%+v skip=%v err=%v", st1, skip1, err)
	}
	st2, skip2, err := testCheckTurnOrMidTurnInput(dir, HookInput{ConversationID: "conv-zero-mid", InvocationNum: 1}, prompt)
	if err != nil || !skip2 || st2.TurnCount != 1 {
		t.Errorf("0-indexed inv=0 -> inv=1 mid-turn: state=%+v skip=%v err=%v, want skip=true turn=1", st2, skip2, err)
	}

	// 2. InvocationNum=0 -> InvocationNum=0 still returns skip=false.
	_, _, _ = testCheckTurnOrMidTurnInput(dir, HookInput{ConversationID: "conv-zero-repeat", InvocationNum: 0}, prompt)
	stRep, skipRep, err := testCheckTurnOrMidTurnInput(dir, HookInput{ConversationID: "conv-zero-repeat", InvocationNum: 0}, prompt)
	if err != nil || skipRep || stRep.TurnCount != 2 {
		t.Errorf("inv=0 -> inv=0: state=%+v skip=%v err=%v, want skip=false turn=2", stRep, skipRep, err)
	}

	// 3. InvocationNum=0 -> InvocationNum=1 with explicit new-turn cue still returns skip=false.
	_, _, _ = testCheckTurnOrMidTurnInput(dir, HookInput{ConversationID: "conv-zero-cue", InvocationNum: 0}, prompt)
	stCue, skipCue, err := testCheckTurnOrMidTurnInput(dir, HookInput{
		ConversationID: "conv-zero-cue", InvocationNum: 1, InitialNumSteps: 4, StepIdx: 4,
	}, prompt)
	if err != nil || skipCue || stCue.TurnCount != 2 {
		t.Errorf("inv=0 -> inv=1 with new-turn cue: state=%+v skip=%v err=%v, want skip=false turn=2", stCue, skipCue, err)
	}

	// 4. Bypassed (!-prefix) prompt omits Enrichment.SkillDigests while keeping Banner.Skills and Enrichment.AstSlice.
	slice := SliceResult{Capsule: "[Hop 0] service.go:1-5 (ReconcileProjectIndex)"}
	skills := []SkillMeta{{Name: "golang", Digest: "Use stdlib first; table-driven -race tests."}}
	bypRes := makeGuardVetResult(true, GatePassThrough, skills)
	bypBanner := buildPreInvocationBannerBaseWithMeta(bypRes, TurnBannerMeta{})
	bypEnrich := buildPreInvocationEnrichment(bypRes, slice, TurnBrainContext{}, nil, 0)
	if bypEnrich == nil || len(bypEnrich.SkillDigests) != 0 || bypEnrich.AstSlice == "" {
		t.Errorf("bypassed enrichment = %+v, want empty SkillDigests and non-empty AstSlice", bypEnrich)
	}
	if len(bypBanner.Skills) == 0 || bypBanner.Skills[0] != "golang" {
		t.Errorf("bypassed banner skills = %+v, want [golang]", bypBanner.Skills)
	}
	normRes := makeGuardVetResult(false, GateEnrichAndProceed, skills)
	normEnrich := buildPreInvocationEnrichment(normRes, slice, TurnBrainContext{}, nil, 0)
	if normEnrich == nil || len(normEnrich.SkillDigests) != 1 {
		t.Errorf("non-bypassed enrichment = %+v, want 1 SkillDigest", normEnrich)
	}
}

func makeGuardVetResult(b bool, gate VetGate, skills []SkillMeta) VetResult {
	r := VetResult{Gate: gate, Intent: IntentFeature, Skills: skills}
	r.Bypassed = b
	return r
}
