//go:build linux

package isolate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunJudgePrintLandlockDisabled(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	repoDir := filepath.Join(tmp, "repo")
	binDir := filepath.Join(tmp, "bin")
	for _, d := range []string{home, repoDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	okAgy := filepath.Join(binDir, "ok-agy")
	put(t, okAgy, "#!/bin/sh\nprintf '{\"verdict\":\"PASS\",\"reason\":\"ok\"}\\n'\n", 0o755)
	put(t, filepath.Join(home, filepath.FromSlash(TokenRel)), "landlock-test-auth", 0o600)

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
