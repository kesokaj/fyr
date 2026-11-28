package isolate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunWorkerDraftsOnlyAllowsDirtyNonGitAndGlobal(t *testing.T) {
	realHome := t.TempDir()
	tokPath := filepath.Join(realHome, filepath.FromSlash(TokenRel))
	if err := os.MkdirAll(filepath.Dir(tokPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokPath, []byte("test-login-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	fakeSelf := filepath.Join(binDir, "fyr")
	selfScript := "#!/usr/bin/env bash\n" +
		"if [ \"${1:-}\" = \"install\" ]; then\n" +
		"  home=\"\"\n" +
		"  shift\n" +
		"  while [ $# -gt 0 ]; do\n" +
		"    if [ \"$1\" = \"--home\" ]; then home=\"$2\"; shift 2; else shift; fi\n" +
		"  done\n" +
		"  mkdir -p \"$home/.local/bin\"\n" +
		"  printf '#!/bin/sh\\nexit 0\\n' > \"$home/.local/bin/fyr\"\n" +
		"  chmod 0755 \"$home/.local/bin/fyr\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(fakeSelf, []byte(selfScript), 0o755); err != nil {
		t.Fatal(err)
	}

	fakeAgy := filepath.Join(binDir, "agy")
	draftJSON := `{"kind":"finding","body":"drafts-only note","quote":"none"}`
	agyScript := "#!/usr/bin/env bash\n" +
		"if [ \"${1:-}\" = \"models\" ]; then\n" +
		"  printf 'gemini-4-flash-medium\\tGemini 4 Flash (Medium)\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ -e .git ]; then\n" +
		"  echo 'unexpected .git in drafts-only workspace' >&2\n" +
		"  exit 3\n" +
		"fi\n" +
		"mkdir -p .agents/scratch/agents\n" +
		"printf '%s\\n' '" + draftJSON + "' > .agents/scratch/agents/auto-drafts.jsonl\n" +
		"exit 0\n"
	if err := os.WriteFile(fakeAgy, []byte(agyScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dirtyGit := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "t"},
		{"config", "user.email", "t@example.invalid"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dirtyGit}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dirtyGit, "README.md"), []byte("v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dirtyGit, "add", "README.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v (%s)", err, out)
	}
	if out, err := exec.Command("git", "-C", dirtyGit, "commit", "-q", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v (%s)", err, out)
	}
	if err := os.WriteFile(filepath.Join(dirtyGit, "dirty.txt"), []byte("uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	nonGit := t.TempDir()
	if err := os.WriteFile(filepath.Join(nonGit, "notes.txt"), []byte("plain folder\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	kickoffDir := t.TempDir()
	kDrafts := filepath.Join(kickoffDir, "auto-retro-lite-202611011200.md")
	if err := os.WriteFile(kDrafts, []byte("Files:\n\nTask: review session digest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kEdit := filepath.Join(kickoffDir, "auto-edit-202611011200.md")
	if err := os.WriteFile(kEdit, []byte("Files: README.md\n\nTask: edit readme\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		root string
	}{
		{"dirty-git", dirtyGit},
		{"non-git", nonGit},
		{"global-workspace", "__global__"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			// Non-DraftsOnly MUST fail closed at preflight on dirty/non-git/global roots.
			_, err := RunWorker(context.Background(), WorkerOptions{
				Root:           tc.root,
				Home:           home,
				RealHome:       realHome,
				Self:           fakeSelf,
				Timeout:        5 * time.Minute,
				Auto:           true,
				DraftsOnly:     false,
				TestRO:         []string{binDir},
				TestUnconfined: CheckAvailable() != nil,
			}, kEdit)
			if err == nil {
				t.Fatalf("expected non-DraftsOnly RunWorker to refuse %s (%s)", tc.name, tc.root)
			}

			// DraftsOnly succeeds in an isolated scratch workspace without touching root.
			res, err := RunWorker(context.Background(), WorkerOptions{
				Root:           tc.root,
				Home:           home,
				RealHome:       realHome,
				Self:           fakeSelf,
				Timeout:        5 * time.Minute,
				Model:          "gemini-4-flash-medium",
				Auto:           true,
				DraftsOnly:     true,
				TestRO:         []string{binDir},
				TestUnconfined: CheckAvailable() != nil,
			}, kDrafts)
			if err != nil {
				t.Fatalf("DraftsOnly RunWorker(%s): %v", tc.name, err)
			}
			if len(res) != 1 || res[0].Verdict != "PASS" || res[0].Drafts != 1 {
				t.Fatalf("DraftsOnly result = %+v, want Verdict=PASS Drafts=1", res)
			}
			collected, err := os.ReadFile(filepath.Join(res[0].DraftsDir, "auto-drafts.jsonl"))
			if err != nil || !strings.Contains(string(collected), "drafts-only note") {
				t.Fatalf("collected drafts = %q, err = %v", string(collected), err)
			}
			if tc.root != "__global__" {
				if _, err := os.Stat(filepath.Join(tc.root, ".agents")); !os.IsNotExist(err) {
					t.Errorf("DraftsOnly run polluted root %s with .agents", tc.root)
				}
			}
		})
	}
}
