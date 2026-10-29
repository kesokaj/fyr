package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

// oldStateHome is a home holding only the state directory from before the
// rename, with a marker file a move must carry.
func oldStateHome(t *testing.T) (home, old, cur string) {
	t.Helper()
	home = t.TempDir()
	old, cur = filepath.Join(home, harness.OldStateDir), filepath.Join(home, harness.StateDir)
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "feedback.jsonl"), []byte("kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, old, cur
}

// Every verb that writes under the state directory migrates the old one
// first, whatever the verb itself then answers; every read-only verb and mode
// leaves it, names it once on stderr, and creates no ~/.config/fyr (plan fyr
// D13, Interfaces § Migration).
func TestStateDirMigrationByVerb(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	t.Setenv("FYR_REPO", "")
	repo := checkoutRoot
	t.Chdir(t.TempDir()) // not a checkout: memory and search never reach this repository's own files
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "systemctl"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	zip := filepath.Join(t.TempDir(), "absent.zip")

	writing := [][]string{
		{"install", "--repo", repo},
		{"sync", "--repo", repo},
		{"uninstall"},
		{"adopt", "no-such-skill", "--repo", repo},
		{"index", "--global"},
		{"search", "--global", "anything"},
		{"memory", "add", "--type", "user", "--name", "x", "--description", "y"},
		{"metrics", "ingest"},
		{"dream", "--remove-timer"},
		{"update", "--repo", plainCopy(t)},
		{"update", "--from-zip", zip},
		// Its sandboxes go under the state directory, whatever it refuses.
		{"work", "absent.md"},
	}
	readOnly := [][]string{
		{"status", "--repo", repo},
		{"diff", "--repo", repo},
		{"list", "--repo", repo},
		{"render", "--repo", repo},
		{"version"},
		{"memory", "show"},
		{"metrics", "show"},
		{"metrics", "report"},
		{"metrics", "export", filepath.Join(t.TempDir(), "x.jsonl")},
		{"install", "--dry-run", "--repo", repo},
		{"sync", "--dry-run", "--repo", repo},
		{"uninstall", "--dry-run"},
		{"update", "--check", "--repo", plainCopy(t)},
		{"update", "--dry-run", "--repo", plainCopy(t)},
		{"update", "--from-zip", zip, "--dry-run"},
		{"dream", "--remove-timer", "--dry-run"},
		{"work", "--kill", "2026-10-29-170000-42"},
		// A record dry run says it writes nothing (IN33 of the 2026-10-27 audit).
		{"record", "import", "--dry-run"},
	}
	for _, args := range writing {
		t.Run("writes "+strings.Join(args[:min(2, len(args))], " "), func(t *testing.T) {
			home, old, cur := oldStateHome(t)
			var out, errOut bytes.Buffer
			stderr = &errOut
			t.Cleanup(func() { stderr = os.Stderr })
			_ = run(append(args, "--home", home), &out) // the verb's own answer is not the subject
			if _, err := os.Lstat(old); !os.IsNotExist(err) {
				t.Errorf("~/.config/sh-agent still there (%v)\nout:\n%s", err, out.String())
			}
			if b, err := os.ReadFile(filepath.Join(cur, "feedback.jsonl")); err != nil || string(b) != "kept\n" {
				t.Errorf("feedback.jsonl not carried to ~/.config/fyr: %q, %v", b, err)
			}
			if got := strings.Count(out.String(), "migrated ~/.config/sh-agent → ~/.config/fyr"); got != 1 {
				t.Errorf("the migration line printed %d times, want 1:\n%s", got, out.String())
			}
		})
	}
	for _, args := range readOnly {
		t.Run("reads "+strings.Join(args[:min(3, len(args))], " "), func(t *testing.T) {
			home, old, cur := oldStateHome(t)
			var out, errOut bytes.Buffer
			stderr = &errOut
			t.Cleanup(func() { stderr = os.Stderr })
			_ = run(append(args, "--home", home), &out)
			if _, err := os.Lstat(old); err != nil {
				t.Errorf("~/.config/sh-agent moved: %v", err)
			}
			if _, err := os.Lstat(cur); !os.IsNotExist(err) {
				t.Errorf("~/.config/fyr exists after a read-only verb (%v)\nout:\n%s", err, out.String())
			}
			if got := strings.Count(errOut.String(), "run fyr install to migrate ~/.config/sh-agent"); got != 1 {
				t.Errorf("the notice printed %d times on stderr, want 1:\n%s", got, errOut.String())
			}
			if strings.Contains(out.String(), "~/.config/sh-agent") {
				t.Errorf("the notice reached stdout, where a read-only verb's data goes:\n%s", out.String())
			}
		})
	}
}

// A refused migration stops a writing verb before it does anything: a
// symlinked old directory is the operator's arrangement, and a verb that ran
// on would start a second, empty state directory beside it.
func TestSymlinkedOldStateDirStopsAWritingVerb(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	home := t.TempDir()
	target := t.TempDir()
	old := filepath.Join(home, harness.OldStateDir)
	if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, old); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := run([]string{"install", "--repo", checkoutRoot, "--home", home}, &out)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("install over a symlinked ~/.config/sh-agent: err %v, want the refusal\n%s", err, out.String())
	}
	for _, rel := range []string{harness.StateDir, harness.AgyRoot, harness.BinPath} {
		if _, err := os.Lstat(filepath.Join(home, rel)); !os.IsNotExist(err) {
			t.Errorf("~/%s written before the refusal: %v", rel, err)
		}
	}
}
