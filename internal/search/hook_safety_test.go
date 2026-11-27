package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestHookHardDeadlineFallsBackToAllow(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()

	cases := []struct {
		name      string
		hook      string
		delay     time.Duration
		panicVal  any
		retErr    error
		wantEmpty bool
	}{
		{name: "slow_pre_tool_use", hook: "pre-tool-use", delay: 400 * time.Millisecond},
		{name: "slow_stop", hook: "stop", delay: 400 * time.Millisecond},
		{name: "slow_pre_invocation", hook: "pre-invocation", delay: 400 * time.Millisecond, wantEmpty: true},
		{name: "panic_pre_tool_use", hook: "pre-tool-use", panicVal: "boom"},
		{name: "err_stop", hook: "stop", retErr: errors.New("boom")},
	}

	reqBody, _ := json.Marshal(map[string]any{
		"conversationId": "conv-deadline",
		"workspacePaths": []string{dir},
		"invocationNum":  1,
		"lastUserInput":  "Inspect service.go",
		"toolCall":       map[string]any{"name": "read_file", "args": map[string]any{"Path": "service.go"}},
	})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan struct{})
			canceled := make(chan bool, 1)
			t.Cleanup(func() { <-done })

			var out bytes.Buffer
			start := time.Now()
			err := Search(Options{
				Dir:          dir,
				Home:         home,
				Hook:         tc.hook,
				HookIn:       bytes.NewReader(reqBody),
				hookDeadline: 80 * time.Millisecond,
				hookDispatch: func(ctx context.Context, _ Options, _ string, _ string, _ HookInput, _ io.Writer) error {
					defer close(done)
					if tc.delay > 0 {
						select {
						case <-ctx.Done():
							canceled <- true
						case <-time.After(tc.delay):
							canceled <- false
						}
					}
					if tc.panicVal != nil {
						panic(tc.panicVal)
					}
					return tc.retErr
				},
			}, &out)
			elapsed := time.Since(start)
			if tc.delay > 0 {
				if wasCanceled := <-canceled; !wasCanceled {
					t.Fatalf("Search --hook %s did not cancel worker context on deadline", tc.hook)
				}
			}
			if err != nil {
				t.Fatalf("Search --hook %s returned error %v, want fail-open nil", tc.hook, err)
			}
			if elapsed >= 250*time.Millisecond {
				t.Fatalf("Search --hook %s took %v, want < 250ms hard deadline", tc.hook, elapsed)
			}
			if tc.wantEmpty {
				var resp PreInvocationOutput
				if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
					t.Fatalf("unmarshal PreInvocationOutput: %v (raw=%q)", err, out.String())
				}
				if len(resp.InjectSteps) != 0 {
					t.Fatalf("expected empty fail-open PreInvocationOutput on timeout, got %+v", resp)
				}
				return
			}
			var dec HookDecisionOutput
			if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
				t.Fatalf("unmarshal HookDecisionOutput: %v (raw=%q)", err, out.String())
			}
			if dec.Decision != "allow" {
				t.Fatalf("HookDecisionOutput = %+v, want Decision=\"allow\"", dec)
			}
		})
	}
}

func TestGlobalNoHooksSwitch(t *testing.T) {
	dir := setupHookRepo(t)
	home := t.TempDir()
	stateDir := filepath.Join(home, harness.StateDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "no-hooks"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	brainDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "conv-global-off")
	transcript := filepath.Join(brainDir, ".system_generated", "logs", "transcript.jsonl")

	// pre-invocation is disabled and writes neither search.db nor fyr-process.md.
	preIn, _ := json.Marshal(map[string]any{
		"conversationId": "conv-global-off",
		"workspacePaths": []string{dir},
		"transcriptPath": transcript,
		"invocationNum":  1,
		"lastUserInput":  "Investigate ReconcileProjectIndex in service.go",
	})
	var preOut bytes.Buffer
	if err := Search(Options{Dir: dir, Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(preIn)}, &preOut); err != nil {
		t.Fatalf("pre-invocation with global no-hooks: %v", err)
	}
	var preResp PreInvocationOutput
	if err := json.Unmarshal(preOut.Bytes(), &preResp); err != nil {
		t.Fatalf("unmarshal PreInvocationOutput: %v", err)
	}
	if len(preResp.InjectSteps) != 0 {
		t.Fatalf("expected 0 injectSteps under global no-hooks, got %+v", preResp.InjectSteps)
	}

	// pre-tool-use and stop return allow without creating search.db or fyr-process.md.
	for _, ev := range []struct {
		hook string
		body map[string]any
	}{
		{
			hook: "pre-tool-use",
			body: map[string]any{
				"conversationId": "conv-global-off",
				"workspacePaths": []string{dir},
				"toolCall":       map[string]any{"name": "read_file", "args": map[string]any{"Path": "service.go"}},
			},
		},
		{
			hook: "pre-tool-use",
			body: map[string]any{
				"conversationId": "conv-global-off",
				"workspacePaths": []string{dir},
				"toolCall":       map[string]any{"name": "edit_file", "args": map[string]any{"Path": "service.go"}},
			},
		},
		{
			hook: "stop",
			body: map[string]any{
				"conversationId":    "conv-global-off",
				"workspacePaths":    []string{dir},
				"transcriptPath":    transcript,
				"terminationReason": "COMPLETE",
				"fullyIdle":         true,
			},
		},
	} {
		raw, _ := json.Marshal(ev.body)
		var out bytes.Buffer
		if err := Search(Options{Dir: dir, Home: home, Hook: ev.hook, HookIn: bytes.NewReader(raw)}, &out); err != nil {
			t.Fatalf("%s with global no-hooks: %v", ev.hook, err)
		}
		var dec HookDecisionOutput
		if err := json.Unmarshal(out.Bytes(), &dec); err != nil {
			t.Fatalf("%s unmarshal: %v", ev.hook, err)
		}
		if dec.Decision != "allow" {
			t.Fatalf("%s Decision = %q, want allow", ev.hook, dec.Decision)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, ".agents", "scratch", "search.db")); err == nil {
		t.Fatalf("search.db should not be created under global no-hooks")
	}
	if _, err := os.Stat(filepath.Join(brainDir, "fyr-process.md")); err == nil {
		t.Fatalf("fyr-process.md should not be created under global no-hooks")
	}
}

func TestNaturalLanguagePromptGetsPivotsViaFTSFallback(t *testing.T) {
	dir, home := setupHookRepo(t), t.TempDir()
	writeFile(t, dir, "internal/events/outbound.go", `package events

// sendOutboundEvent delivers payload bytes and sleeps with bounded jittered
// exponential backoff whenever transient 503 responses break downstream delivery.
func sendOutboundEvent(endpoint string, attempt int) error {
	return nil
}
`)
	writeFile(t, dir, "docs/outbound.md", `# Outbound Dispatch

## Dispatch Timing

Use jittered backoff delays when transient 503 responses interrupt downstream delivery.
`)
	commitAll(t, dir, "add outbound domain docs and code")
	if err := Index(Options{Dir: dir, Home: home}, &bytes.Buffer{}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	nlPrompt := "how should we handle transient 503 responses with jittered backoff?"
	req, _ := json.Marshal(map[string]any{
		"conversationId": "conv-nl-fts",
		"workspacePaths": []string{dir},
		"invocationNum":  1,
		"lastUserInput":  nlPrompt,
	})
	var out bytes.Buffer
	if err := Search(Options{Home: home, Hook: "pre-invocation", HookIn: bytes.NewReader(req)}, &out); err != nil {
		t.Fatalf("pre-invocation nl prompt: %v", err)
	}
	var resp PreInvocationOutput
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal pre-invocation output: %v", err)
	}
	if len(resp.InjectSteps) == 0 || resp.InjectSteps[0].SystemMessage == nil {
		t.Fatalf("expected non-empty pre-invocation capsule via FTS5 fallback, got %+v", resp)
	}
	sys := resp.InjectSteps[0].SystemMessage.SystemMessage
	if !strings.Contains(sys, "sendOutboundEvent") && !strings.Contains(sys, "Dispatch Timing") {
		t.Fatalf("expected FTS5 fallback capsule to cite sendOutboundEvent or Dispatch Timing, got:\n%s", sys)
	}

	sc, err := prepareHookScope(dir)
	if err != nil {
		t.Fatalf("prepareHookScope: %v", err)
	}
	defer sc.close()
	res := sc.buildVetSlice(Options{Dir: dir, Home: home}, nlPrompt, SessionState{}, false)
	if !res.Confident || res.Pivots == 0 || len(res.Files) == 0 {
		t.Fatalf("expected buildVetSlice Pivots > 0 and Confident=true via FTS5 fallback, got %+v", res)
	}

	for _, offTopic := range []string{
		"yes ok",
		"please explain general relativity and thermodynamics",
	} {
		off := sc.buildVetSlice(Options{Dir: dir, Home: home}, offTopic, SessionState{}, false)
		if off.Pivots != 0 || len(off.Files) != 0 {
			t.Fatalf("off-topic prompt %q leaked pivots/files: %+v", offTopic, off)
		}
	}
}

func TestEnsureHookGitCheckoutRefusesHomeWithoutGit(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	if _, _, err := ensureHookGitCheckout(fakeHome); err == nil {
		t.Fatalf("ensureHookGitCheckout(%s) succeeded, want refusal on HOME without auto-initializing .git", fakeHome)
	}
	if _, err := os.Lstat(filepath.Join(fakeHome, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected no .git directory created inside refused HOME %s, got stat err=%v", fakeHome, err)
	}
}

func TestUnwrapEnvOrExec(t *testing.T) {
	cases := []struct {
		name string
		kind string
		toks []string
		want []string
	}{
		{name: "env flags and vars", kind: "env", toks: []string{"-i", "-0", "--ignore-environment", "--null", "-u", "FOO", "--unset", "BAR", "--unset=BAZ", "-C", "/tmp", "--chdir", "/var", "--chdir=/opt", "A=1", "vim", "a.go"}, want: []string{"vim", "a.go"}},
		{name: "env stops at double dash", kind: "env", toks: []string{"-i", "--", "-u", "git", "status"}, want: []string{"-u", "git", "status"}},
		{name: "exec flags and -a", kind: "exec", toks: []string{"-c", "-l", "-a", "myname", "nvim", "f.go"}, want: []string{"nvim", "f.go"}},
		{name: "exec double dash", kind: "exec", toks: []string{"-l", "--", "less", "f.go"}, want: []string{"less", "f.go"}},
		{name: "command and time -p", kind: "command", toks: []string{"-p", "git", "status"}, want: []string{"git", "status"}},
		{name: "time double dash", kind: "time", toks: []string{"-p", "--", "sudo", "id"}, want: []string{"sudo", "id"}},
		{name: "only flags returns nil", kind: "env", toks: []string{"-i", "FOO=bar"}, want: nil},
	}
	for _, tc := range cases {
		got := unwrapEnvOrExec(tc.kind, tc.toks)
		if len(got) != len(tc.want) {
			t.Errorf("%s: unwrapEnvOrExec(%q, %v) = %v, want %v", tc.name, tc.kind, tc.toks, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: unwrapEnvOrExec(%q, %v)[%d] = %q, want %q", tc.name, tc.kind, tc.toks, i, got[i], tc.want[i])
			}
		}
	}
}
