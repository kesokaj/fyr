package isolate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/models"
)

func TestRunJudgePrintConfined(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Landlock judge confinement test runs on Linux")
	}
	if err := CheckAvailable(); err != nil {
		t.Fatalf("Landlock unavailable on Linux test host: %v", err)
	}
	tmp := t.TempDir()
	home := filepath.Join(tmp, "fyr-home")
	realHome := filepath.Join(tmp, "real-home")
	repoDir := filepath.Join(tmp, "repo")
	outsideDir := filepath.Join(tmp, "outside")
	binDir := filepath.Join(tmp, "bin")
	for _, d := range []string{home, realHome, repoDir, outsideDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	put(t, filepath.Join(realHome, TokenRel), "judge-oauth-token", 0o600)
	put(t, filepath.Join(repoDir, "README.md"), "repo-data\n", 0o644)

	agyPath := filepath.Join(binDir, "agy")
	script := `#!/bin/bash
set -e
printf '%s\n' "$@" > "$HOME/argv.txt"
[ -f "$HOME/.gemini/antigravity-cli/settings.json" ] || exit 11
grep -q '"allow":\[\]' "$HOME/.gemini/antigravity-cli/settings.json" || exit 12
[ -L "$HOME/.gemini/antigravity-cli/antigravity-oauth-token" ] || exit 13
[ "$(cat "$HOME/.gemini/antigravity-cli/antigravity-oauth-token")" = "judge-oauth-token" ] || exit 14
[ -z "${ANTIGRAVITY_LS_ADDRESS:-}" ] || exit 15
[ -z "${ANTIGRAVITY_CSRF_TOKEN:-}" ] || exit 16
[ -z "${GIT_DIR:-}" ] || exit 17
[ "$(cat "` + repoDir + `/README.md")" = "repo-data" ] || exit 18
if (printf 'escape' > "` + repoDir + `/evil.txt") 2>/dev/null; then exit 19; fi
if (printf 'escape' > "` + realHome + `/evil.txt") 2>/dev/null; then exit 20; fi
if (printf 'escape' > "` + outsideDir + `/evil.txt") 2>/dev/null; then exit 21; fi
printf 'ok' > "$HOME/ok.txt"
printf 'ok' > "$TMPDIR/ok.txt"
printf '{"verdict":"PASS","reason":"confined ok"}\n'
`
	put(t, agyPath, script, 0o755)
	t.Setenv("ANTIGRAVITY_LS_ADDRESS", "127.0.0.1:9999")
	t.Setenv("ANTIGRAVITY_CSRF_TOKEN", "leak")
	t.Setenv("GIT_DIR", "/bad/git")

	out, err := RunJudgePrint(context.Background(), JudgeExecOptions{
		Home:     home,
		RealHome: realHome,
		RepoDir:  repoDir,
		AgyPath:  agyPath,
		Model:    "claude-opus-5-5-high",
		Prompt:   "Evaluate change",
	})
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("RunJudgePrint failed: err=%v out=%+v", err, out)
	}
	if !strings.Contains(out.Stdout, `"PASS"`) {
		t.Errorf("unexpected stdout: %q", out.Stdout)
	}
	for _, p := range []string{
		filepath.Join(repoDir, "evil.txt"),
		filepath.Join(realHome, "evil.txt"),
		filepath.Join(outsideDir, "evil.txt"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("confined judge unexpectedly created %s", p)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(home, harness.StateDir))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "judge-") {
			t.Errorf("temp judge scratch dir %s not cleaned up", e.Name())
		}
	}
}

func TestRunJudgePrintTimeoutUnconfinedAndTokenRules(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	repoDir := filepath.Join(tmp, "repo")
	binDir := filepath.Join(tmp, "bin")
	for _, d := range []string{home, repoDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	slowAgy := filepath.Join(binDir, "slow-agy")
	put(t, slowAgy, "#!/bin/sh\nsleep 30\n", 0o755)
	start := time.Now()
	_, err := RunJudgePrint(context.Background(), JudgeExecOptions{
		Home:       home,
		RepoDir:    repoDir,
		AgyPath:    slowAgy,
		Prompt:     "slow",
		Timeout:    200 * time.Millisecond,
		Unconfined: true,
	})
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("timeout did not fire promptly: err=%v elapsed=%s", err, time.Since(start))
	}

	okAgy := filepath.Join(binDir, "ok-agy")
	put(t, okAgy, "#!/bin/sh\nprintf '{\"verdict\":\"PASS\",\"reason\":\"ok\"}\\n'\n", 0o755)

	if runtime.GOOS == "linux" {
		prev := testSkipLandlock
		testSkipLandlock = true
		t.Cleanup(func() { testSkipLandlock = prev })
		if _, err := RunJudgePrint(context.Background(), JudgeExecOptions{
			Home: home, RepoDir: repoDir, AgyPath: okAgy, Prompt: "p", Unconfined: false,
		}); err == nil {
			t.Error("Unconfined=false succeeded with Landlock disabled")
		}
		out, err := RunJudgePrint(context.Background(), JudgeExecOptions{
			Home: home, RepoDir: repoDir, AgyPath: okAgy, Prompt: "p", Unconfined: true,
		})
		if err != nil || out.ExitCode != 0 {
			t.Errorf("Unconfined=true failed: err=%v out=%+v", err, out)
		}
	}

	missingReal := filepath.Join(tmp, "missing-real")
	if err := os.MkdirAll(missingReal, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := RunJudgePrint(context.Background(), JudgeExecOptions{
		Home: home, RealHome: missingReal, RepoDir: repoDir, AgyPath: okAgy, Prompt: "p", Unconfined: true,
	}); err == nil {
		t.Error("expected missing RealHome token to fail closed")
	}
}

func TestWorkerFinishChecksJudge(t *testing.T) {
	r := newRig(t)
	c := r.config("judge-worker")
	c.TestUnconfined = true
	b := r.prepared(c)

	baseHead, err := headCommit(b.App())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", b.App(), "checkout", "-q", "-b", "work/judge-test").CombinedOutput(); err != nil {
		t.Fatalf("checkout branch: %v\n%s", err, out)
	}
	put(t, filepath.Join(b.App(), "added.go"), "package added\n\nfunc Hello() string { return \"hi\" }\n", 0o644)
	for _, args := range [][]string{{"add", "added.go"}, {"commit", "-qm", "add Hello"}} {
		cmd := exec.Command("git", append([]string{"-C", b.App(), "-c", "user.name=w", "-c", "user.email=w@localhost"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	oldJudge := JudgeExecFunc
	oldList := models.ListModelsFunc
	oldUsage := models.UsageProbeFunc
	t.Cleanup(func() {
		JudgeExecFunc = oldJudge
		models.ListModelsFunc = oldList
		models.UsageProbeFunc = oldUsage
	})
	models.ListModelsFunc = func(context.Context, string) ([]string, error) {
		return []string{"claude-opus-5-5-high", "gemini-4-carbon-high"}, nil
	}
	models.UsageProbeFunc = func(context.Context, string) (string, error) {
		return "Claude and GPT models\tWeekly Limit Remaining\t60%\tx\nClaude and GPT models\tFive Hour Limit Remaining\t80%\tx\n", nil
	}

	makeWorker := func(judgeCrit string) *worker {
		w := &worker{
			o: WorkerOptions{Root: r.src, Home: filepath.Join(r.tmp, "work-home"), RealHome: r.real, TestUnconfined: true},
			p: prepared{
				k:    Kickoff{Name: "judge-test", Files: []string{"added.go"}, Judge: judgeCrit},
				base: baseHead,
				agy:  r.agy,
			},
			b:   b,
			ran: true,
		}
		b.reasons = nil
		return w
	}

	var gotModel, gotPrompt string
	JudgeExecFunc = func(_ context.Context, opts JudgeExecOptions) (JudgeExecOutput, error) {
		gotModel, gotPrompt = opts.Model, opts.Prompt
		return JudgeExecOutput{Stdout: `{"verdict":"PASS","reason":"Hello added"}`, ExitCode: 0}, nil
	}
	wPass := makeWorker("Add Hello() string in added.go")
	runWorkerJudge(context.Background(), wPass)
	if wPass.b.Has(ReasonJudgeFailed) || gotModel != "claude-opus-5-5-high" || !strings.Contains(gotPrompt, "BEGIN DATA") || !strings.Contains(gotPrompt, "+func Hello()") {
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
		JudgeExecFunc = func(context.Context, JudgeExecOptions) (JudgeExecOutput, error) {
			return tc.out, nil
		}
		w := makeWorker("Add Hello() string in added.go")
		runWorkerJudge(context.Background(), w)
		if !w.b.Has(ReasonJudgeFailed) {
			t.Errorf("%s: expected ReasonJudgeFailed, got reasons=%v", tc.name, w.b.Reasons())
		}
	}

	called := false
	JudgeExecFunc = func(context.Context, JudgeExecOptions) (JudgeExecOutput, error) {
		called = true
		return JudgeExecOutput{Stdout: `{"verdict":"PASS","reason":"ok"}`}, nil
	}
	wPrior := makeWorker("Add Hello() string in added.go")
	wPrior.b.Fail(ReasonTimedOut)
	if wPrior.p.k.Judge != "" && wPrior.ran && !wPrior.unsafe() && len(wPrior.b.Reasons()) == 0 {
		runWorkerJudge(context.Background(), wPrior)
	}
	if called {
		t.Errorf("JudgeExecFunc should not run when prior reasons exist")
	}
}
