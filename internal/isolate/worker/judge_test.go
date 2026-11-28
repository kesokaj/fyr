package worker

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWorkerFinishChecksJudge(t *testing.T) {
	t.Parallel()
	b := newFakeBox(t)
	for _, args := range [][]string{{"init", "-q"}} {
		if out, err := exec.Command("git", append([]string{"-C", b.App()}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeTestFile(t, filepath.Join(b.App(), "README.md"), "demo\n", 0o644)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", append([]string{"-C", b.App(), "-c", "user.name=w", "-c", "user.email=w@localhost"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	baseHead, err := headCommit(b.App())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", b.App(), "checkout", "-q", "-b", "work/judge-test").CombinedOutput(); err != nil {
		t.Fatalf("checkout branch: %v\n%s", err, out)
	}
	writeTestFile(t, filepath.Join(b.App(), "added.go"), "package added\n\nfunc Hello() string { return \"hi\" }\n", 0o644)
	for _, args := range [][]string{{"add", "added.go"}, {"commit", "-qm", "add Hello"}} {
		cmd := exec.Command("git", append([]string{"-C", b.App(), "-c", "user.name=w", "-c", "user.email=w@localhost"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	listFn := func(context.Context, string) ([]string, error) {
		return []string{"claude-opus-5-5-high", "gemini-4-carbon-high"}, nil
	}
	usageFn := func(context.Context, string) (string, error) {
		return "Claude and GPT models\tWeekly Limit Remaining\t60%\tx\nClaude and GPT models\tFive Hour Limit Remaining\t80%\tx\n", nil
	}

	workHome := t.TempDir()
	realHome := t.TempDir()
	deps := testDeps()
	makeWorker := func(judgeCrit string) *worker {
		w := &worker{
			deps: deps,
			o:    Options{Root: b.App(), Home: workHome, RealHome: realHome, TestUnconfined: true, Judge: JudgeOptions{ListModels: listFn, UsageProbe: usageFn}},
			p: prepared{
				k:    Kickoff{Name: "judge-test", Files: []string{"added.go"}, Judge: judgeCrit},
				base: baseHead,
				agy:  "/bin/true",
			},
			b:   b,
			ran: true,
		}
		b.reasons = nil
		return w
	}

	var gotModel, gotPrompt, gotOS string
	wPass := makeWorker("Add Hello() string in added.go")
	wPass.o.Judge.HostOS = "linux"
	wPass.o.Judge.ExecFunc = func(_ context.Context, opts JudgeExecOptions) (JudgeExecOutput, error) {
		gotModel, gotPrompt, gotOS = opts.Model, opts.Prompt, opts.HostOS
		return JudgeExecOutput{Stdout: `{"verdict":"PASS","reason":"Hello added"}`, ExitCode: 0}, nil
	}
	runWorkerJudge(context.Background(), wPass)
	if gotOS != "linux" {
		t.Fatalf("JudgeOptions.HostOS = %q, want linux", gotOS)
	}
	if wPass.b.Has(ReasonJudgeFailed) || gotModel != "claude-opus-5-5-high" || !strings.Contains(gotPrompt, "Do NOT call any tools") || !strings.Contains(gotPrompt, "BEGIN DATA") || !strings.Contains(gotPrompt, "+func Hello()") {
		t.Fatalf("worker judge PASS mismatch: reasons=%v model=%q prompt=\n%s", wPass.b.Reasons(), gotModel, gotPrompt)
	}

	for _, tc := range []struct {
		name string
		out  JudgeExecOutput
	}{
		{"fail verdict", JudgeExecOutput{Stdout: `{"verdict":"FAIL","reason":"missing behavior"}`, ExitCode: 0}},
		{"non-json output", JudgeExecOutput{Stdout: `VERDICT: PASS`, ExitCode: 0}},
		{"nonzero exit", JudgeExecOutput{Stdout: `{"verdict":"PASS","reason":"ok"}`, ExitCode: 1}},
	} {
		w := makeWorker("Add Hello() string in added.go")
		w.o.Judge.ExecFunc = func(context.Context, JudgeExecOptions) (JudgeExecOutput, error) {
			return tc.out, nil
		}
		runWorkerJudge(context.Background(), w)
		if !w.b.Has(ReasonJudgeFailed) {
			t.Errorf("%s: expected ReasonJudgeFailed, got reasons=%v", tc.name, w.b.Reasons())
		}
	}

	called := false
	wPrior := makeWorker("Add Hello() string in added.go")
	wPrior.o.Judge.ExecFunc = func(context.Context, JudgeExecOptions) (JudgeExecOutput, error) {
		called = true
		return JudgeExecOutput{Stdout: `{"verdict":"PASS","reason":"ok"}`}, nil
	}
	wPrior.b.Fail(ReasonTimedOut)
	if wPrior.p.k.Judge != "" && wPrior.ran && !wPrior.unsafe() && len(wPrior.b.Reasons()) == 0 {
		runWorkerJudge(context.Background(), wPrior)
	}
	if called {
		t.Errorf("JudgeExecFunc should not run when prior reasons exist")
	}

	wEnv := makeWorker("Add Hello() string in added.go")
	wEnv.o.Judge.ExecFunc = func(context.Context, JudgeExecOptions) (JudgeExecOutput, error) {
		return JudgeExecOutput{
			Stdout: `{"conversation_id":"c1","status":"SUCCESS","duration_seconds":1.2,"num_turns":1,"structured_output":{"verdict":"PASS","reason":"envelope ok"}}`,
		}, nil
	}
	runWorkerJudge(context.Background(), wEnv)
	if wEnv.b.Has(ReasonJudgeFailed) {
		t.Errorf("envelope PASS failed: reasons=%v", wEnv.b.Reasons())
	}

	b.notes.Reset()
	wErr := makeWorker("Add Hello() string in added.go")
	wErr.o.Judge.ExecFunc = func(context.Context, JudgeExecOptions) (JudgeExecOutput, error) {
		return JudgeExecOutput{Stderr: "Error: --json-schema failed\n", ExitCode: 1}, nil
	}
	runWorkerJudge(context.Background(), wErr)
	if !wErr.b.Has(ReasonJudgeFailed) || !strings.Contains(b.notes.String(), "Error: --json-schema failed") {
		t.Errorf("expected stderr note on nonzero exit, reasons=%v out=%q", wErr.b.Reasons(), b.notes.String())
	}
}

func TestParseJudgeVerdict(t *testing.T) {
	t.Parallel()
	t.Run("accepts agy envelope and bare JSON", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name, raw, wantV, wantR string
		}{
			{
				name:  "envelope with structured_output and telemetry",
				raw:   `{"conversation_id":"c1","status":"SUCCESS","duration_seconds":1.5,"num_turns":1,"json_schema":{"type":"object"},"usage":{"input":10},"structured_output":{"verdict":"PASS","reason":"  all good  "}}`,
				wantV: "PASS", wantR: "all good",
			},
			{
				name:  "envelope response fallback when structured_output absent",
				raw:   `{"conversation_id":"c2","status":"SUCCESS","response":"{\"verdict\":\"PASS\",\"reason\":\"from response\"}"}`,
				wantV: "PASS", wantR: "from response",
			},
			{
				name:  "envelope response fallback when structured_output null",
				raw:   `{"conversation_id":"c3","status":"SUCCESS","structured_output":null,"response":"{\"verdict\":\"FAIL\",\"reason\":\"missing test\"}"}`,
				wantV: "FAIL", wantR: "missing test",
			},
			{
				name:  "bare verdict JSON",
				raw:   `{"verdict":"PASS","reason":"ok"}`,
				wantV: "PASS", wantR: "ok",
			},
		}
		for _, tc := range cases {
			v, r, err := ParseJudgeVerdict(tc.raw)
			if err != nil || v != tc.wantV || r != tc.wantR {
				t.Errorf("%s: got (%q, %q, %v), want (%q, %q, nil)", tc.name, v, r, err, tc.wantV, tc.wantR)
			}
		}
	})

	t.Run("rejects error status and invalid payloads", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name, raw, wantSub string
		}{
			{
				name:    "non-SUCCESS status with error message even if response set",
				raw:     `{"status":"ERROR","error":"rate limited","response":"{\"verdict\":\"PASS\",\"reason\":\"ok\"}"}`,
				wantSub: "judge agy status ERROR: rate limited",
			},
			{
				name:    "non-SUCCESS status without error message",
				raw:     `{"status":"FAILED","response":"{\"verdict\":\"PASS\",\"reason\":\"ok\"}"}`,
				wantSub: "judge agy status FAILED: unsuccessful status",
			},
			{
				name:    "empty status with non-empty error fails closed",
				raw:     `{"error":"rate limited","response":"{\"verdict\":\"PASS\",\"reason\":\"ok\"}"}`,
				wantSub: "judge agy error: rate limited",
			},
			{
				name:    "SUCCESS status with non-empty error fails closed",
				raw:     `{"status":"SUCCESS","error":"quota exceeded","structured_output":{"verdict":"PASS","reason":"ok"}}`,
				wantSub: "judge agy status SUCCESS: quota exceeded",
			},
			{
				name:    "SUCCESS envelope missing structured_output and response",
				raw:     `{"conversation_id":"c4","status":"SUCCESS","structured_output":null,"response":""}`,
				wantSub: "empty structured_output",
			},
			{
				name:    "SUCCESS envelope with denied_actions",
				raw:     `{"conversation_id":"c5","status":"SUCCESS","response":"","denied_actions":[{"action":"command","display_name":"Bash"}]}`,
				wantSub: "judge attempted disallowed tool action(s): Bash",
			},
			{
				name:    "bare JSON with unknown inner field",
				raw:     `{"verdict":"PASS","reason":"ok","extra":1}`,
				wantSub: "unknown field",
			},
			{
				name:    "envelope structured_output with unknown inner field",
				raw:     `{"status":"SUCCESS","structured_output":{"verdict":"PASS","reason":"ok","extra":1}}`,
				wantSub: "unknown field",
			},
			{
				name:    "trailing tokens after bare JSON",
				raw:     `{"verdict":"PASS","reason":"ok"} {"trailing":true}`,
				wantSub: "trailing tokens",
			},
			{
				name:    "empty input",
				raw:     "   \n\t ",
				wantSub: "empty judge response",
			},
			{
				name:    "invalid verdict value",
				raw:     `{"verdict":"MAYBE","reason":"unsure"}`,
				wantSub: "invalid judge verdict",
			},
		}
		for _, tc := range cases {
			_, _, err := ParseJudgeVerdict(tc.raw)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("%s: got err=%v, want substring %q", tc.name, err, tc.wantSub)
			}
		}
	})
}

func setupLargeDiffRepo(t *testing.T) (repo, baseHead string) {
	t.Helper()
	repo = t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=w", "-c", "user.email=w@localhost"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "-q")
	writeTestFile(t, filepath.Join(repo, "big.txt"), "base\n", 0o644)
	runGit("add", "big.txt")
	runGit("commit", "-qm", "init")
	baseHead = runGit("rev-parse", "HEAD")
	payload := strings.Repeat("a", (96<<10)-1) + strings.Repeat("─", 20000) + "\n"
	writeTestFile(t, filepath.Join(repo, "big.txt"), payload, 0o644)
	runGit("add", "big.txt")
	runGit("commit", "-qm", "large diff")
	return repo, baseHead
}

func TestWorkerJudgePromptCapOver140KiB(t *testing.T) {
	t.Parallel()
	repo, baseHead := setupLargeDiffRepo(t)
	ctx := context.Background()
	diff, err := workerDiffForJudge(ctx, JudgeDeps{}, repo, baseHead)
	if err != nil {
		t.Fatalf("workerDiffForJudge: %v", err)
	}
	if len(diff) > (96<<10)+64 || !strings.Contains(diff, "[diff truncated at 96 KiB]") || !utf8.ValidString(diff) {
		t.Fatalf("diff cap failed: len=%d valid=%v", len(diff), utf8.ValidString(diff))
	}
	prompt := buildWorkerJudgePrompt(strings.Repeat("k", 30<<10), diff)
	if len(prompt) >= 120<<10 || len(prompt) >= 128<<10 || !utf8.ValidString(prompt) {
		t.Fatalf("prompt cap failed: len=%d valid=%v", len(prompt), utf8.ValidString(prompt))
	}
	hugePrompt := buildWorkerJudgePrompt(strings.Repeat("q", 130<<10), diff)
	if len(hugePrompt) >= 120<<10 || len(hugePrompt) >= 128<<10 || !utf8.ValidString(hugePrompt) {
		t.Fatalf("huge criterion prompt cap failed: len=%d valid=%v", len(hugePrompt), utf8.ValidString(hugePrompt))
	}
	if _, err := RunJudgePrint(ctx, JudgeDeps{}, JudgeExecOptions{Home: t.TempDir(), Prompt: strings.Repeat("x", 120<<10)}); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("RunJudgePrint oversized prompt error = %v, want too large refusal", err)
	}
}
