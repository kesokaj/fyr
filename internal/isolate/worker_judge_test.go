package isolate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
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
grep -qx -- '--output-format' "$HOME/argv.txt" || exit 22
[ "$(grep -A1 -x -- '--output-format' "$HOME/argv.txt" | sed -n '2p')" = "json" ] || exit 23
[ "$(pwd)" != "` + repoDir + `" ] || exit 24
! grep -qx -- '--mode' "$HOME/argv.txt" || exit 25
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
	t.Parallel()
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

	missingReal := filepath.Join(tmp, "missing-real")
	if err := os.MkdirAll(missingReal, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := RunJudgePrint(context.Background(), JudgeExecOptions{
		Home: home, RealHome: missingReal, RepoDir: repoDir, AgyPath: okAgy, Prompt: "p", Unconfined: true,
	}); err == nil {
		t.Error("expected missing RealHome token to fail closed")
	}
	if _, err := RunJudgePrint(context.Background(), JudgeExecOptions{Home: home, Prompt: strings.Repeat("x", 120<<10)}); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("RunJudgePrint oversized prompt error = %v, want too large refusal", err)
	}
}
