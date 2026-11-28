package guard

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCheckInteractiveBashGuard(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		cmd         string
		wantDenySub string
		wantRewrite bool
	}{
		{name: "vim_denied", cmd: "vim main.go", wantDenySub: "[INTERACTIVE_STDIN]"},
		{name: "sudo_interactive", cmd: "sudo ls", wantDenySub: "[INTERACTIVE_STDIN]"},
		{name: "sudo_noninteractive", cmd: "sudo -n true"},
		{name: "read_prompt_denied", cmd: `read -p "continue?" ans`, wantDenySub: "[INTERACTIVE_STDIN]"},
		{name: "ssh_missing_batch", cmd: "ssh user@host ls", wantDenySub: "[INTERACTIVE_STDIN]"},
		{name: "ssh_tty_denied", cmd: "ssh -t -o BatchMode=yes user@host ls", wantDenySub: "[INTERACTIVE_STDIN]"},
		{name: "ssh_batch_ok", cmd: "ssh -o BatchMode=yes user@host hostname"},
		{name: "git_commit_editor", cmd: "git commit", wantDenySub: "[INTERACTIVE_STDIN]"},
		{name: "git_commit_msg_ok", cmd: `git commit -m "ok"`},
		{name: "git_rebase_interactive", cmd: "git rebase -i HEAD~2", wantDenySub: "[INTERACTIVE_STDIN]"},
		{name: "git_add_patch", cmd: "git add -p", wantDenySub: "[INTERACTIVE_STDIN]"},
		{name: "nested_sh_c_vim", cmd: `bash -lc "vim a.go"`, wantDenySub: "[INTERACTIVE_STDIN]"},
		{name: "git_fetch_rewritten", cmd: "git fetch origin", wantRewrite: true},
		{name: "git_fetch_prompt_set", cmd: "GIT_TERMINAL_PROMPT=0 git fetch origin"},
		{name: "env_split_vim", cmd: `env -S "vim a.go"`, wantDenySub: "[INTERACTIVE_STDIN]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reason, ow := CheckInteractiveBashGuard(map[string]any{"CommandLine": tc.cmd})
			if tc.wantDenySub != "" {
				if !strings.Contains(reason, tc.wantDenySub) {
					t.Fatalf("cmd=%q reason=%q, want substring %q", tc.cmd, reason, tc.wantDenySub)
				}
				if ow != nil {
					t.Fatalf("cmd=%q got unexpected overwrite %v", tc.cmd, ow)
				}
				return
			}
			if reason != "" {
				t.Fatalf("cmd=%q unexpected deny: %q", tc.cmd, reason)
			}
			if tc.wantRewrite {
				got, _ := ow["CommandLine"].(string)
				if !strings.HasPrefix(got, "export GIT_TERMINAL_PROMPT=0; ") {
					t.Fatalf("cmd=%q overwrite=%q, want GIT_TERMINAL_PROMPT prefix", tc.cmd, got)
				}
			} else if ow != nil {
				t.Fatalf("cmd=%q unexpected overwrite: %v", tc.cmd, ow)
			}
		})
	}
}

func TestHasShellExpansionOutsideSingleQuotes(t *testing.T) {
	t.Parallel()
	if !HasShellExpansionOutsideSingleQuotes(`echo "$(id)"`) {
		t.Fatal("expected command substitution inside double quotes to be detected")
	}
	if !HasShellExpansionOutsideSingleQuotes("echo `id`") {
		t.Fatal("expected backtick expansion to be detected")
	}
	if HasShellExpansionOutsideSingleQuotes(`echo '$(id)'`) {
		t.Fatal("single-quoted expansion should not trigger")
	}
}

func TestParseWorkerFilesHeaderAndCoordinatorMode(t *testing.T) {
	t.Parallel()
	prompt := "Files:\n- internal/hooks/guard/a.go\n* `internal/hooks/telemetry/b.go`\n- <none>\nNext line"
	got, ok := ParseWorkerFilesHeader(prompt)
	if !ok {
		t.Fatal("expected Files: header to be detected")
	}
	want := []string{"internal/hooks/guard/a.go", "internal/hooks/telemetry/b.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("ParseWorkerFilesHeader = %v, want %v", got, want)
	}

	root := t.TempDir()
	if IsCoordinatorOnlyMode("", root) {
		t.Fatal("empty temp root should not be coordinator-only")
	}
	scratch := filepath.Join(root, ".agents", "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scratch, "coordinator-only"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsCoordinatorOnlyMode("", root) {
		t.Fatal("expected coordinator-only marker to activate mode")
	}
}

func TestGitGuardAndIndexState(t *testing.T) {
	t.Parallel()
	if !IsRefusedAutoInitDir("/") {
		t.Fatal("root directory must be refused for auto-init")
	}
	dir := t.TempDir()
	if IsRefusedAutoInitDir(dir) {
		t.Fatalf("fresh temp dir %s should not be refused", dir)
	}
	if err := EnsureHookProjectReady(dir); err != nil {
		t.Fatalf("EnsureHookProjectReady: %v", err)
	}
	root, commonDir, ok := FindGitRootFast(dir)
	if !ok || root == "" || commonDir == "" {
		t.Fatalf("FindGitRootFast(%s) = (%q, %q, %v)", dir, root, commonDir, ok)
	}
	if !ScratchIgnoredFast(root, commonDir) {
		t.Fatal("expected .agents/scratch/* to be ignored after EnsureHookProjectReady")
	}
	if !PatternCoversRootScratch(".agents/scratch/*") || PatternCoversRootScratch("README.md") {
		t.Fatal("PatternCoversRootScratch classification mismatch")
	}

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := ComputeGoTreeState(dir, nil)
	if err != nil {
		t.Fatalf("ComputeGoTreeState: %v", err)
	}
	if state.GoFileCount != 1 || state.TreeSHA256 == "" || !slices.Contains(state.DirtyGo, "main.go") {
		t.Fatalf("unexpected GoTreeState: %+v", state)
	}

	var hdr [12]byte
	copy(hdr[0:4], "DIRC")
	binary.BigEndian.PutUint32(hdr[4:8], 2)
	binary.BigEndian.PutUint32(hdr[8:12], 0)
	entries, err := ParseGitIndex(hdr[:])
	if err != nil || len(entries) != 0 {
		t.Fatalf("ParseGitIndex valid empty = (%v, %v)", entries, err)
	}
	if _, err := ParseGitIndex([]byte("NOPE")); err == nil {
		t.Fatal("ParseGitIndex should reject invalid header")
	}
}

func TestAskQuestionGuardAndEscapeClause(t *testing.T) {
	t.Parallel()
	if !HasAskQuestionEscapeClause("Found a PRD.md conflict with R3") {
		t.Fatal("expected PRD conflict escape clause to allow")
	}
	if !HasAskQuestionEscapeClause("Quota exhausted at 0%") {
		t.Fatal("expected quota exhaustion escape clause to allow")
	}
	if HasAskQuestionEscapeClause("Should I name this helper foo or bar?") {
		t.Fatal("routine naming question must not satisfy escape clause")
	}

	var buf bytes.Buffer
	writeDec := func(w io.Writer, dec, reason string) error {
		return json.NewEncoder(w).Encode(map[string]any{"decision": dec, "reason": reason})
	}
	err := HandlePreToolAskQuestionCtx(
		context.Background(),
		AskInput{
			LastUserInput: "Files: internal/a.go\nImplement task",
			ToolCallArgs:  map[string]any{"question": "Pick style?"},
		},
		nil, "",
		AskDeps{WriteDecision: writeDec},
		&buf,
	)
	if err != nil {
		t.Fatalf("HandlePreToolAskQuestionCtx: %v", err)
	}
	if !strings.Contains(buf.String(), `"decision":"deny"`) || !strings.Contains(buf.String(), "[KICKOFF_GUARD]") {
		t.Fatalf("expected KICKOFF_GUARD deny, got %s", buf.String())
	}
}

func TestWorkflowGuardAndWatchdogPreamble(t *testing.T) {
	t.Parallel()
	makeDeps := func(fast bool) WorkflowDeps {
		return WorkflowDeps{
			EffectiveSessionModel:  func() string { return "gemini-2.5-flash" },
			NeedsGuardedProUpgrade: func(_, _ string) bool { return fast },
			IsModelGuardedSubagent: func(name string) bool { return name == "reviewer" || name == "security" },
			FormatModelTag:         func(_, raw string) string { return raw },
			SanitizePrintable:      func(s string) string { return s },
			WriteDecision: func(w io.Writer, dec, reason string) error {
				return json.NewEncoder(w).Encode(map[string]any{"decision": dec, "reason": reason})
			},
			WriteDecisionOverwrite: func(w io.Writer, dec, reason string, ow map[string]any) {
				_ = json.NewEncoder(w).Encode(map[string]any{"decision": dec, "reason": reason, "overwrite": ow})
			},
		}
	}

	var denyBuf bytes.Buffer
	_ = HandlePreToolWorkflow(WorkflowInput{
		ToolArgs: map[string]any{"script": `agent("a", "b", "c", type_name="reviewer", model="flash")`},
	}, makeDeps(false), &denyBuf)
	if !strings.Contains(denyBuf.String(), `"decision":"deny"`) || !strings.Contains(denyBuf.String(), "[MODEL_OVERRIDE]") {
		t.Fatalf("expected MODEL_OVERRIDE deny, got %s", denyBuf.String())
	}

	var upgBuf bytes.Buffer
	_ = HandlePreToolWorkflow(WorkflowInput{
		ToolArgs: map[string]any{"script": `agent("a", "b", "c", type_name="reviewer", model="inherit")`},
	}, makeDeps(true), &upgBuf)
	if !strings.Contains(upgBuf.String(), "[AUTO_MODEL_UPGRADE]") || !strings.Contains(upgBuf.String(), `model=\"pro\"`) {
		t.Fatalf("expected AUTO_MODEL_UPGRADE rewrite, got %s", upgBuf.String())
	}

	var litBuf bytes.Buffer
	_ = HandlePreToolWorkflow(WorkflowInput{
		ToolArgs: map[string]any{"script": `msg = 'agent(type_name="reviewer", model="flash")'`},
	}, makeDeps(true), &litBuf)
	if !strings.Contains(litBuf.String(), `"decision":"allow"`) {
		t.Fatalf("string literal should be allowed, got %s", litBuf.String())
	}

	var pathBuf bytes.Buffer
	_ = HandlePreToolWorkflow(WorkflowInput{
		Dir:      t.TempDir(),
		ToolArgs: map[string]any{"script_path": "../escape.py"},
	}, makeDeps(false), &pathBuf)
	if !strings.Contains(pathBuf.String(), `"decision":"deny"`) {
		t.Fatalf("parent traversal script_path must be denied, got %s", pathBuf.String())
	}

	if got := MatchWatchdogPromptPreamble("You are a senior code reviewer.\nCheck diff."); got != "reviewer" {
		t.Fatalf("MatchWatchdogPromptPreamble = %q, want reviewer", got)
	}
	if got := MatchWatchdogPromptPreamble("Regular task prompt"); got != "" {
		t.Fatalf("MatchWatchdogPromptPreamble regular = %q, want empty", got)
	}
	if pos := SkipPythonWSAndComments("  # note\n  x", 0); pos != 11 {
		t.Fatalf("SkipPythonWSAndComments = %d, want 11", pos)
	}
}

func testWorkflowDeps(fast bool) WorkflowDeps {
	return WorkflowDeps{
		EffectiveSessionModel:  func() string { return "gemini-2.5-flash" },
		NeedsGuardedProUpgrade: func(_, _ string) bool { return fast },
		IsModelGuardedSubagent: func(name string) bool { return name == "reviewer" || name == "security" },
		FormatModelTag:         func(_, raw string) string { return raw },
		SanitizePrintable:      func(s string) string { return s },
		WriteDecision: func(w io.Writer, dec, reason string) error {
			return json.NewEncoder(w).Encode(map[string]any{"decision": dec, "reason": reason})
		},
		WriteDecisionOverwrite: func(w io.Writer, dec, reason string, ow map[string]any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"decision": dec, "reason": reason, "overwrite": ow})
		},
	}
}

func TestWorkflowGuardNilCallbacksFailClosed(t *testing.T) {
	t.Parallel()
	script := `agent("a", "b", "c", type_name="reviewer", model="flash")`
	in := WorkflowInput{ToolArgs: map[string]any{"script": script}}

	depsNoWriter := testWorkflowDeps(false)
	depsNoWriter.WriteDecision = nil
	var errBuf bytes.Buffer
	if err := HandlePreToolWorkflow(in, depsNoWriter, &errBuf); err == nil {
		t.Fatal("expected error when WriteDecision is nil")
	}

	cases := []struct {
		name         string
		mutate       func(*WorkflowDeps)
		want         string
		checkInspect bool
	}{
		{"nil_needs_pro_upgrade", func(d *WorkflowDeps) { d.NeedsGuardedProUpgrade = nil }, "NeedsGuardedProUpgrade", true},
		{"nil_is_guarded_subagent", func(d *WorkflowDeps) { d.IsModelGuardedSubagent = nil }, "IsModelGuardedSubagent", true},
		{"nil_write_decision_overwrite", func(d *WorkflowDeps) { d.WriteDecisionOverwrite = nil }, "WriteDecisionOverwrite", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps := testWorkflowDeps(false)
			tc.mutate(&deps)
			var buf bytes.Buffer
			if err := HandlePreToolWorkflow(WorkflowInput{ToolArgs: map[string]any{"script": "x = 1"}}, deps, &buf); err != nil {
				t.Fatalf("HandlePreToolWorkflow error: %v", err)
			}
			if !strings.Contains(buf.String(), `"decision":"deny"`) || !strings.Contains(buf.String(), tc.want) {
				t.Fatalf("expected fail-closed deny mentioning %q, got %s", tc.want, buf.String())
			}
			if tc.checkInspect {
				deny, patches := inspectWorkflowScript("", script, "gemini-2.5-flash", true, deps)
				if deny == "" || !strings.Contains(deny, tc.want) || len(patches) != 0 {
					t.Fatalf("inspectWorkflowScript = (%q, %v), want deny mentioning %q", deny, patches, tc.want)
				}
			}
		})
	}
}
