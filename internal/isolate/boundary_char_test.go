package isolate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/isolate/acp"
)

func writeCharFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestFingerprintWithBoundaryMatrix(t *testing.T) {
	t.Parallel()
	t.Run("missing watched paths produce empty map", func(t *testing.T) {
		fp := fingerprintWith(t.TempDir(), false, nil)
		if len(fp) != 0 {
			t.Fatalf("empty home fingerprint = %v, want empty map", fp)
		}
	})

	t.Run("regular files symlinks sandbox skips and work fixed files", func(t *testing.T) {
		home := t.TempDir()
		stateDir := filepath.FromSlash(harness.StateDir)

		stateSHA := writeCharFile(t, filepath.Join(home, stateDir, "state.json"), `{"v":1}`)
		autoJSONSHA := writeCharFile(t, filepath.Join(home, stateDir, "auto.json"), `{"enabled":true}`)
		writeCharFile(t, filepath.Join(home, stateDir, "auto", "run.log"), "skipped")
		writeCharFile(t, filepath.Join(home, stateDir, "eval", "trial.txt"), "skipped")
		writeCharFile(t, filepath.Join(home, stateDir, "work", "sandbox-1", "tmp.txt"), "skipped")
		workCfgSHA := writeCharFile(t, filepath.Join(home, stateDir, "work", "config.json"), `{"ok":1}`)
		hooksSHA := writeCharFile(t, filepath.Join(home, ".gemini", "config", "hooks.json"), `{}`)
		settingsSHA := writeCharFile(t, filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"), `{"k":1}`)

		targetFile := filepath.Join(t.TempDir(), "linked-target.txt")
		targetSHA := writeCharFile(t, targetFile, "linked-content")
		if err := os.Symlink(targetFile, filepath.Join(home, stateDir, "link.txt")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := os.Symlink(filepath.Join(home, "nonexistent-target"), filepath.Join(home, stateDir, "dangling.lnk")); err != nil {
			t.Fatal(err)
		}

		fp := fingerprintWith(home, false, nil)
		wantPresent := map[string]string{
			filepath.Join(stateDir, "state.json"):                       "sha256 " + stateSHA,
			filepath.Join(stateDir, "auto.json"):                        "sha256 " + autoJSONSHA,
			filepath.Join(stateDir, "work", "config.json"):              "sha256 " + workCfgSHA,
			filepath.Join(".gemini", "config", "hooks.json"):            "sha256 " + hooksSHA,
			filepath.FromSlash(".gemini/antigravity-cli/settings.json"): "sha256 " + settingsSHA,
			filepath.Join(stateDir, "link.txt"):                         "sha256 " + targetSHA,
			filepath.Join(stateDir, "dangling.lnk"):                     "-> ",
		}
		for k, sub := range wantPresent {
			got, ok := fp[k]
			if !ok || !strings.Contains(got, sub) {
				t.Errorf("fp[%q] = (%q, %v), want substring %q", k, got, ok, sub)
			}
		}
		if strings.Contains(fp[filepath.Join(stateDir, "dangling.lnk")], "sha256 ") {
			t.Errorf("dangling symlink unexpectedly had sha256: %q", fp[filepath.Join(stateDir, "dangling.lnk")])
		}
		for _, skipped := range []string{
			filepath.Join(stateDir, "auto", "run.log"),
			filepath.Join(stateDir, "eval", "trial.txt"),
			filepath.Join(stateDir, "work", "sandbox-1", "tmp.txt"),
		} {
			if _, ok := fp[skipped]; ok {
				t.Errorf("sandbox path %q should be skipped", skipped)
			}
		}
	})

	t.Run("skipStores false vs true and brainWatch callback", func(t *testing.T) {
		home := t.TempDir()
		stateDir := filepath.FromSlash(harness.StateDir)
		brainPath := filepath.Join(home, stateDir, "brain.db")

		brainSHA := writeCharFile(t, brainPath, "raw-brain-bytes")
		writeCharFile(t, filepath.Join(home, stateDir, "brain.db-wal"), "wal")
		writeCharFile(t, filepath.Join(home, stateDir, "search.db"), "search")
		writeCharFile(t, filepath.Join(home, stateDir, "metrics.db"), "metrics")
		writeCharFile(t, filepath.Join(home, stateDir, "dream.log"), "log")
		writeCharFile(t, filepath.Join(home, stateDir, "models.json"), `{}`)
		writeCharFile(t, filepath.Join(home, stateDir, ".fyr-42"), "tmp")
		writeCharFile(t, filepath.Join(home, stateDir, "state.json"), `{"installed":true}`)
		writeCharFile(t, filepath.Join(home, stateDir, "reports", "latest-daily-digest.json"), `{}`)
		writeCharFile(t, filepath.Join(home, stateDir, "reports", "2026-11-28-daily-metrics.html"), "<html>")
		writeCharFile(t, filepath.Join(home, stateDir, "reports", ".fyr-99"), "tmp")
		writeCharFile(t, filepath.Join(home, stateDir, "reports", "unexpected.txt"), "watch-me")

		fpAll := fingerprintWith(home, false, nil)
		for _, rel := range []string{
			"brain.db", "brain.db-wal", "search.db", "metrics.db", "dream.log",
			"models.json", ".fyr-42", "state.json",
			filepath.Join("reports", "latest-daily-digest.json"),
			filepath.Join("reports", "2026-11-28-daily-metrics.html"),
			filepath.Join("reports", ".fyr-99"),
			filepath.Join("reports", "unexpected.txt"),
		} {
			if _, ok := fpAll[filepath.Join(stateDir, rel)]; !ok {
				t.Errorf("skipStores=false missing %q", rel)
			}
		}

		fpSkip := fingerprintWith(home, true, nil)
		for _, skipped := range []string{
			"brain.db", "brain.db-wal", "search.db", "metrics.db", "dream.log",
			"models.json", ".fyr-42", "reports",
			filepath.Join("reports", "latest-daily-digest.json"),
			filepath.Join("reports", "2026-11-28-daily-metrics.html"),
			filepath.Join("reports", ".fyr-99"),
		} {
			if _, ok := fpSkip[filepath.Join(stateDir, skipped)]; ok {
				t.Errorf("skipStores=true should omit %q", skipped)
			}
		}
		for _, kept := range []string{"state.json", filepath.Join("reports", "unexpected.txt")} {
			if _, ok := fpSkip[filepath.Join(stateDir, kept)]; !ok {
				t.Errorf("skipStores=true must still watch %q", kept)
			}
		}

		var seenPath string
		fpWatchV1 := fingerprintWith(home, true, func(p string) (string, bool) {
			seenPath = p
			return "scope-digest-v1", true
		})
		fpWatchV2 := fingerprintWith(home, true, func(p string) (string, bool) {
			return "scope-digest-v2", true
		})
		brainKey := filepath.Join(stateDir, "brain.db")
		if seenPath != brainPath {
			t.Errorf("brainWatch path = %q, want %q", seenPath, brainPath)
		}
		if !strings.Contains(fpWatchV1[brainKey], "scope-digest-v1") {
			t.Errorf("brainWatch v1 entry = %q, want scope-digest-v1", fpWatchV1[brainKey])
		}
		if fpWatchV1[brainKey] == fpWatchV2[brainKey] {
			t.Errorf("brainWatch digest change did not alter fingerprint: %q", fpWatchV1[brainKey])
		}
		if _, ok := fpWatchV1[filepath.Join(stateDir, "brain.db-wal")]; ok {
			t.Errorf("regular brain.db-wal should remain skipped with brainWatch")
		}

		fpFallback := fingerprintWith(home, true, func(string) (string, bool) { return "", false })
		if !strings.Contains(fpFallback[brainKey], "sha256 "+brainSHA) {
			t.Errorf("brainWatch fallback entry = %q, want raw sha256 %s", fpFallback[brainKey], brainSHA)
		}
	})
}

func TestFirstCommandBinBoundaryMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		seg  string
		want string
	}{
		{name: "simple command", seg: "go test ./...", want: "go"},
		{name: "absolute path", seg: "/usr/local/bin/git status", want: "git"},
		{name: "leading env assignments", seg: "FOO=1 BAR='a b' /usr/bin/python3 script.py", want: "python3"},
		{name: "env assignment only", seg: "FOO=1 BAR=2", want: ""},
		{name: "invalid env ident treated as bin", seg: "1BAD=1 echo hi", want: "1BAD=1"},
		{name: "env wrapper basic", seg: "env go test", want: "go"},
		{name: "env wrapper -i and assign", seg: "env -i FOO=1 /bin/sh -c true", want: "sh"},
		{name: "env wrapper dash", seg: "env - FOO=1 git status", want: "git"},
		{name: "env wrapper double dash stops flags", seg: "env -- -dashbin arg", want: "-dashbin"},
		{name: "env wrapper unknown flag fails closed", seg: "env -u PATH go test", want: ""},
		{name: "quoted env is not wrapper", seg: "'env' go test", want: "env"},
		{name: "single quoted path", seg: "'/usr/bin/go' build", want: "go"},
		{name: "double quoted path", seg: "\"/bin/ls\" -la", want: "ls"},
		{name: "ansi single quoted", seg: "$'git' log", want: "git"},
		{name: "backslash escaped char", seg: `g\it status`, want: "git"},
		{name: "unterminated single quote", seg: "'unterminated", want: ""},
		{name: "unterminated double quote", seg: "\"unterminated", want: ""},
		{name: "trailing backslash", seg: `echo\`, want: ""},
		{name: "empty quoted command", seg: `"" arg`, want: ""},
		{name: "attached redir prefix", seg: ">out.txt go build", want: "go"},
		{name: "detached redir and fd redir", seg: "> out.txt 2>&1 /usr/bin/make test", want: "make"},
		{name: "dangling redir fails closed", seg: ">", want: ""},
		{name: "consecutive redirs fail closed", seg: "> >foo", want: ""},
		{name: "pipe in segment returns first cmd", seg: "echo hi | wc -l", want: "echo"},
		{name: "env assign with pipe returns first cmd", seg: "FOO=1 echo hi | wc -l", want: "echo"},
		{name: "empty input", seg: "", want: ""},
		{name: "whitespace only", seg: "   \t\n  ", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := acp.FirstCommandBin(tc.seg); got != tc.want {
				t.Errorf("acp.FirstCommandBin(%q) = %q, want %q", tc.seg, got, tc.want)
			}
		})
	}
}
