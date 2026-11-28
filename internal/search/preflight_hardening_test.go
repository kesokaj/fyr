package search

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/vet"
)

func TestRelayGuardIgnoresRepoNoHooksAndFailsClosedOnBadPayload(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	t.Setenv("FYR_RELAY", "1")
	if err := os.MkdirAll(filepath.Join(dir, ".agents", "scratch"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agents", "scratch", "no-hooks"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"conversationId": "conv-relay-nohooks",
		"workspacePaths": []string{dir},
		"toolCall": map[string]any{
			"name": "run_command",
			"args": map[string]any{"CommandLine": "git push origin main", "Cwd": dir},
		},
	})
	var out bytes.Buffer
	if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(payload)}, &out); err != nil {
		t.Fatal(err)
	}
	var dec HookDecisionOutput
	if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
		t.Fatal(err)
	}
	if dec.Decision != "deny" || !strings.Contains(dec.Reason, "[RELAY_GUARD]") {
		t.Fatalf("repo no-hooks bypassed RELAY_GUARD: %+v", dec)
	}

	out.Reset()
	if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: strings.NewReader("{bad-json")}, &out); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
		t.Fatal(err)
	}
	if dec.Decision != "deny" || !strings.Contains(dec.Reason, "[RELAY_GUARD]") {
		t.Fatalf("malformed pre-tool-use payload under FYR_RELAY=1 must fail closed: %+v", dec)
	}
}

func TestRelayGuardWrappersAliasesPlumbingAndSudoShortFlags(t *testing.T) {
	t.Setenv("FYR_RELAY", "1")
	mustDeny := []string{
		`env -S "git push origin main"`,
		`env --split-string="git tag v1.0.0"`,
		`timeout 10 git push origin main`,
		`nohup git push`,
		`nice -n 5 git tag v1`,
		`stdbuf -oL git push`,
		`setsid git push`,
		`sh -c "git push origin main"`,
		`bash -lc "git tag -a v9 -m x"`,
		`eval git push origin main`,
		`echo origin | xargs git push`,
		`git -c alias.p=push p origin main`,
		`git -C . -c alias.p=push p origin main`,
		`git --git-dir .git -c alias.p=push p`,
		`git -calias.t=tag t v1`,
		`git --config-env=alias.p=X p`,
		`GIT_CONFIG_PARAMETERS="'alias.p=push'" git p`,
		`GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=alias.p git p`,
		`env -S "A=1 GIT_CONFIG_PARAMETERS='alias.p=push' git p"`,
		`env --split-string="A=1 GIT_CONFIG_COUNT=1 git p"`,
		`git send-pack origin HEAD:main`,
		`git-send-pack origin HEAD:main`,
		`git update-ref refs/tags/v9.9.0 HEAD`,
		`git update-ref --stdin`,
		`sh +e -c "git push origin main"`,
		`bash +o pipefail -c "git tag v1"`,
		`sh -c -e "git push origin main"`,
		`bash -c -- "git push origin main"`,
		`sh -c - "git push origin main"`,
		`sudo -n git push origin main`,
		`eval eval eval eval eval eval eval git push`,
	}
	for _, cmd := range mustDeny {
		if reason, _ := inspectBashCommandTree(cmd); !strings.Contains(reason, "[RELAY_GUARD]") {
			t.Errorf("FYR_RELAY=1 cmd %q: got reason=%q, want [RELAY_GUARD]", cmd, reason)
		}
	}
	for _, okCmd := range []string{"git tag --list", "git tag -l 'v9*'", "git status", "sudo -n ls", `bash -c ""`, `sh ""`} {
		if reason, _ := inspectBashCommandTree(okCmd); reason != "" {
			t.Errorf("benign cmd %q unexpectedly denied: %q", okCmd, reason)
		}
	}
	for _, badInteractive := range []string{"sudo -uadmin ls", "sudo -R/bin ls", `bash -c "" && vim foo`} {
		if reason, _ := inspectBashCommandTree(badInteractive); !strings.Contains(reason, "[INTERACTIVE_STDIN]") {
			t.Errorf("%q must deny [INTERACTIVE_STDIN]: got %q", badInteractive, reason)
		}
	}
}

func TestKickoffGuardHeadlessNoEscapeAndSubagentWorkerHint(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	escapeQ := map[string]any{
		"questions": []any{map[string]any{"question": "PRD.md conflict: amend R4?", "options": []any{"A", "B"}}},
	}
	invoke := func(body map[string]any) HookDecisionOutput {
		raw, _ := json.Marshal(body)
		var out bytes.Buffer
		if err := Search(Options{Home: home, Hook: "pre-tool-use", HookIn: bytes.NewReader(raw)}, &out); err != nil {
			t.Fatal(err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatal(err)
		}
		return dec
	}
	t.Run("FYR_RELAY=1 blocks ask_question even with escape clause", func(t *testing.T) {
		t.Setenv("FYR_RELAY", "1")
		d := invoke(map[string]any{
			"conversationId": "c-relay-esc", "workspacePaths": []string{dir},
			"toolCall": map[string]any{"name": "ask_question", "args": escapeQ},
		})
		if d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
			t.Fatalf("relay ask_question with escape must deny: %+v", d)
		}
	})
	t.Run("subagent blocks ask_question even with escape clause", func(t *testing.T) {
		d := invoke(map[string]any{
			"conversationId": "c-sub-esc", "parentConversationId": "c-parent", "workspacePaths": []string{dir},
			"toolCall": map[string]any{"name": "ask_question", "args": escapeQ},
		})
		if d.Decision != "deny" || !strings.Contains(d.Reason, "[KICKOFF_GUARD]") {
			t.Fatalf("subagent ask_question with escape must deny: %+v", d)
		}
	})
	if hasAskQuestionEscapeClause("Should we replant the tree?") {
		t.Error("replant should not trigger replan escape")
	}
	if hasAskQuestionEscapeClause("This code block mentions PRD.md") {
		t.Error("bare 'block' with PRD.md should not trigger conflict escape")
	}
	if role, guarded := isGuardedSubagentCall(map[string]any{"TypeName": "self", "Role": "Security Review Worker"}); !guarded || role != "security" {
		t.Errorf("Security Review Worker: got (%q, %v), want (security, true)", role, guarded)
	}
	if role, guarded := isGuardedSubagentCall(map[string]any{"TypeName": "self", "Role": "Security Fix Review"}); !guarded || role != "security" {
		t.Errorf("Security Fix Review: got (%q, %v), want (security, true)", role, guarded)
	}
	if role, guarded := isGuardedSubagentCall(map[string]any{"TypeName": "self", "Role": "Docs Reviewer"}); !guarded || role != "reviewer" {
		t.Errorf("Docs Reviewer: got (%q, %v), want (reviewer, true)", role, guarded)
	}
	if role, guarded := isGuardedSubagentCall(map[string]any{"TypeName": "self", "Role": "Judge Worker"}); !guarded || role != "judge" {
		t.Errorf("Judge Worker: got (%q, %v), want (judge, true)", role, guarded)
	}
}

func TestReadLedgerBashStrictSedAndPipeFilterAllowlists(t *testing.T) {
	mustIgnore := []string{
		"sed -n -e '1,10p' -e'1w out.txt' a.go",
		"sed -n -e '1,10p' -e'1e ls' a.go",
		"sed --sandbox '1,10p' a.go",
		"cat a.go | awk -fscript.awk",
		"cat a.go | awk -E script.awk",
		"cat a.go | awk '{print}' other.go",
		`cat a.go | awk -- '{print > "out.txt"}'`,
		`cat a.go | awk -- 'BEGIN{system("id")}'`,
		"cat a.go | sort -uo out.txt",
		"cat a.go | sort --compress-program=sh",
		"cat a.go | sort other.go",
		"cat a.go | rg --pre sh pat",
		"cat a.go | grep -f pats.txt",
		"cat a.go | grep pat other.go",
		"cat a.go | grep --color pat other.go",
		"cat a.go | jq -f prog.jq",
		"cat a.go | jq --rawfile x /etc/passwd '.'",
		"cat a.go | jq --slurpfile x /etc/passwd '.'",
		"cat a.go | jq -L /tmp '.'",
		"cat a.go | uniq other.go",
		"cat a.go | wc -l other.go",
		"cat a.go | cut -f1 other.go",
	}
	for _, cmd := range mustIgnore {
		if got := ParseBashFileReads(cmd); len(got) != 0 {
			t.Errorf("ParseBashFileReads(%q) = %+v, want empty", cmd, got)
		}
	}
	if got := ParseBashFileReads("sed -ne'1,10p' a.go"); len(got) != 1 || got[0].Path != "a.go" || got[0].StartLine != 1 || got[0].EndLine != 10 {
		t.Errorf("ParseBashFileReads(sed -ne'1,10p' a.go) = %+v, want 1..10", got)
	}
}

func TestInferSliceVerifyRejectsUnsafePathsAndSetsSubtaskTrust(t *testing.T) {
	if got := vet.InferSliceVerify("builder", []string{"scripts/a;rm -rf /.sh"}); got != "" {
		t.Errorf("unsafe .sh verify = %q, want empty", got)
	}
	if got := vet.InferSliceVerify("builder", []string{"-evil.sh"}); got != "" {
		t.Errorf("leading-dash .sh verify = %q, want empty", got)
	}
	if got := vet.InferSliceVerify("builder", []string{"/tmp/abs.sh"}); got != "" {
		t.Errorf("absolute .sh verify = %q, want empty", got)
	}
	if got := vet.InferSliceVerify("builder", []string{"internal/a;id/x.go"}); got != "" {
		t.Errorf("unsafe .go verify = %q, want empty", got)
	}
	if got := vet.InferSliceVerify("builder", []string{"scripts/check.sh"}); got != "bash -n scripts/check.sh" {
		t.Errorf("safe .sh verify = %q", got)
	}
	e := buildPreInvocationEnrichment(VetResult{
		Intent:   IntentFeature,
		Gate:     GateEnrichAndProceed,
		Subtasks: []SubtaskSlice{{ID: "S1", Role: "builder", Files: []string{"a.go"}}},
	}, SliceResult{}, TurnBrainContext{}, nil, 0)
	if e == nil || e.Trust == "" {
		t.Fatalf("Subtasks-only enrichment must set Trust banner: %+v", e)
	}
	fileAsDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if isRegularDirOrAbsent(filepath.Join(fileAsDir, "child")) {
		t.Errorf("ENOTDIR path must NOT pass isRegularDirOrAbsent")
	}
}
