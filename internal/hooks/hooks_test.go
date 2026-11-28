package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/hooks/telemetry"
	"github.com/kesokaj/fyr/internal/symgraph"
	"github.com/kesokaj/fyr/internal/vet"
)

func TestNormalizeHookEvent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"session-start", "session-start"},
		{"SessionStart", "session-start"},
		{"pre-invocation", "pre-invocation"},
		{"preInvocation", "pre-invocation"},
		{"pre-tool-use", "pre-tool-use"},
		{"post_tool_use", "post-tool-use"},
		{"STOP", "stop"},
		{"unknown", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := NormalizeHookEvent(tc.in); got != tc.want {
			t.Errorf("NormalizeHookEvent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCombinePreInvocationMessage(t *testing.T) {
	t.Parallel()
	if got := combinePreInvocationMessage("", ""); got != "" {
		t.Errorf("both empty = %q", got)
	}
	if got := combinePreInvocationMessage("sys", ""); got != "sys" {
		t.Errorf("sys only = %q", got)
	}
	if got := combinePreInvocationMessage("", "banner"); got != "banner" {
		t.Errorf("banner only = %q", got)
	}
	if got := combinePreInvocationMessage("banner\nsys", "banner"); got != "banner\nsys" {
		t.Errorf("duplicate banner not deduplicated: %q", got)
	}
	if got := combinePreInvocationMessage("sys", "banner"); got != "banner\nsys" {
		t.Errorf("combined = %q, want banner\\nsys", got)
	}
}

func TestParseAndNormalizeHookInputAliases(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"workspace_paths": ["/repo/a"],
		"working_directory": "/repo/a/sub",
		"user_prompt": "fix bug",
		"invocation_num": 3,
		"is_continuation": true,
		"artifact_directory_path": "/home/u/.gemini/brain/conv-42/art",
		"tool_call": {"tool_name": "runCommand", "tool_input": "{\"CommandLine\":\"git status\"}"}
	}`)
	got, err := ParseAndNormalizeHookInput(Options{}, raw)
	if err != nil {
		t.Fatalf("ParseAndNormalizeHookInput: %v", err)
	}
	if got.ConversationID != "conv-42" {
		t.Errorf("ConversationID = %q, want conv-42", got.ConversationID)
	}
	if got.Cwd != "/repo/a/sub" || got.LastUserInput != "fix bug" || got.InvocationNum != 3 || !got.ContinuationActive {
		t.Errorf("aliases not applied: %+v", got)
	}
	if got.ToolCall.Name != "runCommand" || got.ToolCall.Args["CommandLine"] != "git status" {
		t.Errorf("tool aliases not applied: %+v", got.ToolCall)
	}
	if _, err := ParseAndNormalizeHookInput(Options{}, []byte("{bad")); err == nil {
		t.Error("expected error on invalid JSON")
	}
}

func TestCheckStatelessPreToolSafety(t *testing.T) {
	t.Parallel()
	subCtx := withHookSubagentMeta(context.Background(), hookSubagentMeta{AgentName: "builder", ParentConversationID: "p1"})
	deny := checkStatelessPreToolSafety(subCtx, Options{}, ".", HookInput{
		ToolCall: HookToolCall{Name: "ask_question"},
	})
	if !strings.Contains(deny, "[KICKOFF_GUARD]") {
		t.Errorf("expected KICKOFF_GUARD denial for subagent ask_question, got %q", deny)
	}
	allowMain := checkStatelessPreToolSafety(context.Background(), Options{}, ".", HookInput{
		ToolCall: HookToolCall{Name: "ask_question"},
	})
	if allowMain != "" {
		t.Errorf("expected allow for main ask_question, got %q", allowMain)
	}
	denyOverride := checkStatelessPreToolSafety(context.Background(), Options{}, ".", HookInput{
		ToolCall: HookToolCall{
			Name: "invoke_subagent",
			Args: map[string]any{"SubagentType": "reviewer", "Model": "flash"},
		},
	})
	if !strings.Contains(denyOverride, "[MODEL_OVERRIDE]") {
		t.Errorf("expected MODEL_OVERRIDE denial, got %q", denyOverride)
	}
	allowInherit := checkStatelessPreToolSafety(context.Background(), Options{}, ".", HookInput{
		ToolCall: HookToolCall{
			Name: "invoke_subagent",
			Args: map[string]any{"SubagentType": "reviewer", "Model": "inherit"},
		},
	})
	if allowInherit != "" {
		t.Errorf("expected allow for inherit override, got %q", allowInherit)
	}
}

func TestWatchdogRolesAndExemptBash(t *testing.T) {
	t.Parallel()
	if !IsBuilderSubagentRole("workflow-builder-1") || !IsBuilderSubagentRole("self") {
		t.Error("expected builder/self roles to be builder roles")
	}
	if IsBuilderSubagentRole("reviewer") || !IsAuditSubagentRole("reviewer") {
		t.Error("expected reviewer to be audit role")
	}
	if w, c := SubagentBudgetCap("builder"); w != 90 || c != 120 {
		t.Errorf("builder cap = (%d,%d), want (90,120)", w, c)
	}
	if w, c := SubagentBudgetCap("security"); w != 25 || c != 40 {
		t.Errorf("security cap = (%d,%d), want (25,40)", w, c)
	}
	if !IsWatchdogExemptBash(map[string]any{"CommandLine": "git status && git checkout --detach HEAD"}) {
		t.Error("expected git status + detach to be watchdog-exempt")
	}
	if IsWatchdogExemptBash(map[string]any{"CommandLine": "git push origin main"}) {
		t.Error("git push must not be watchdog-exempt")
	}
}

func TestTimeoutRecorderBudgetPositive(t *testing.T) {
	t.Parallel()
	b := TimeoutRecorderBudget()
	if b < 45*time.Millisecond {
		t.Fatalf("TimeoutRecorderBudget() = %v, want >= 45ms", b)
	}
	if DefaultHookDeadline > 200*time.Millisecond && b < 400*time.Millisecond {
		t.Fatalf("under -race, TimeoutRecorderBudget() = %v, want >= 400ms", b)
	}
}

func TestSessionStartTodoAndDailyAlert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	todo := filepath.Join(dir, "TODO.md")
	content := "# TODO\n\n## Now\n- first item\n<!-- comment -->\n- second item\n\n## Next\n- later\n"
	if err := os.WriteFile(todo, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got := extractTodoNow(Deps{}, todo, 4)
	if len(got) != 2 || got[0] != "- first item" || got[1] != "- second item" {
		t.Fatalf("extractTodoNow = %v", got)
	}
	brief := sessionDailyDigestBrief{
		Day:     "2026-11-28",
		Verdict: "regressing",
		Signals: []struct {
			SignalID string `json:"signal_id"`
			Severity string `json:"severity"`
			Rule     string `json:"rule"`
		}{
			{SignalID: "s1", Severity: "high", Rule: "pre_invocation_timeouts"},
			{SignalID: "s2", Severity: "low", Rule: "ignored"},
		},
		Actions: []struct {
			SignalID       string `json:"signal_id"`
			Status         string `json:"status"`
			InstallPending bool   `json:"install_pending"`
			RevertCmd      string `json:"revert_cmd"`
		}{
			{SignalID: "act-1", Status: "still_regressing"},
		},
	}
	alert := buildSessionDailyAlert(Deps{}, brief)
	if !strings.Contains(alert, "pre_invocation_timeouts") || !strings.Contains(alert, "act-1[still_regressing]") {
		t.Fatalf("buildSessionDailyAlert = %q", alert)
	}
	cap := telemetry.SessionStartCapsule{
		Surface: "SESSION_START", Date: "2026-11-28",
		Plan: strings.Repeat("p", 300), Now: []string{"a", "b", "c"},
	}
	if rendered := FitSessionStartCapsule(&cap, 120); len(rendered) > 120 {
		t.Errorf("FitSessionStartCapsule exceeded cap: len=%d", len(rendered))
	}
}

func TestLookupWorkerRecordsDedupAndValidity(t *testing.T) {
	t.Parallel()
	o := Options{
		BrainTurnContextCtx: func(_ context.Context, _, _, _ string, _ []string) vet.TurnBrainContext {
			return vet.TurnBrainContext{
				Bugs: []vet.MatchedRecord{{ID: "BUG-1", Kind: "bug", Title: "crash"}},
				Memories: []vet.MatchedRecord{
					{ID: "MEM-LOW", Kind: "memory", Validity: 0.2, Title: "stale"},
					{ID: "MEM-OK", Kind: "memory", Validity: 0.9, Title: "fresh"},
				},
			}
		},
		BrainRefsForDir: func(_, _ string) []symgraph.LinkedRecord {
			return []symgraph.LinkedRecord{
				{ID: "BUG-1", Title: "dup"},
				{ID: "ADR-2", Title: "arch"},
				{ID: "EXTRA-3", Title: "capped"},
			}
		},
	}
	got := LookupWorkerRecords(o, "/repo", "fix bug", []string{"a.go"})
	if len(got) != 3 {
		t.Fatalf("LookupWorkerRecords len = %d, want 3: %+v", len(got), got)
	}
	if got[0].ID != "BUG-1" || got[1].ID != "MEM-OK" || got[2].ID != "ADR-2" || got[2].Kind != "adr" {
		t.Fatalf("LookupWorkerRecords = %+v", got)
	}
}

func TestRunHookInvalidEventAndFallback(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := RunHook(Deps{}, Options{Hook: "bogus"}, strings.NewReader("{}"), &buf); err == nil {
		t.Fatal("expected usage error for unknown hook event")
	}
	buf.Reset()
	err := RunHook(Deps{}, Options{
		Hook:         "pre-tool-use",
		HookDeadline: 20 * time.Millisecond,
		HookDispatch: func(_ context.Context, _ Options, _, _ string, _ HookInput, _ io.Writer) error {
			return context.DeadlineExceeded
		},
	}, strings.NewReader(`{"toolCall":{"name":"read_file"}}`), &buf)
	if err != nil {
		t.Fatalf("RunHook fail-open returned err: %v", err)
	}
	var dec HookDecisionOutput
	if err := json.Unmarshal(buf.Bytes(), &dec); err != nil || dec.Decision != "allow" {
		t.Fatalf("expected fail-open allow, got %q (err=%v)", buf.String(), err)
	}
}
