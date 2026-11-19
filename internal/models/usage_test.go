package models

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func TestProbeWorkDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wantRoot := filepath.Join(home, harness.StateDir, "sandbox")

	dir, cleanup, err := probeWorkDir("")
	if err != nil {
		t.Fatalf("probeWorkDir() error = %v", err)
	}
	if dir == "/" || dir == os.TempDir() || filepath.Dir(dir) != wantRoot {
		cleanup()
		t.Fatalf("probeWorkDir() = %q, want under %q", dir, wantRoot)
	}
	if !strings.HasPrefix(filepath.Base(dir), "fyr-agy-probes-") {
		cleanup()
		t.Fatalf("probeWorkDir() = %q, want prefix fyr-agy-probes-", dir)
	}
	st, err := os.Lstat(dir)
	if err != nil {
		cleanup()
		t.Fatalf("Lstat(%q): %v", dir, err)
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		cleanup()
		t.Fatalf("probeWorkDir %q is not a regular directory: mode=%v", dir, st.Mode())
	}
	if st.Mode().Perm()&0o077 != 0 {
		cleanup()
		t.Fatalf("probeWorkDir %q permissions %v expose group/other bits", dir, st.Mode().Perm())
	}
	dir2, cleanup2, err2 := probeWorkDir("")
	if err2 != nil {
		cleanup()
		t.Fatalf("second probeWorkDir() error = %v", err2)
	}
	defer cleanup2()
	if dir2 == dir {
		cleanup()
		t.Fatalf("two probeWorkDir calls shared path %q", dir)
	}
	cleanup()
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("cleanup left probeWorkDir %q behind (err=%v)", dir, statErr)
	}

	symHome := t.TempDir()
	symState := filepath.Join(symHome, harness.StateDir)
	if err := os.MkdirAll(symState, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(symState, "sandbox")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", symHome)
	if _, _, err := probeWorkDir(""); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlinked sandbox to fail closed, got err=%v", err)
	}
}

func TestProbeWorkDirUsedByAgyProbes(t *testing.T) {
	oldTimeout := ModelsTimeout
	ModelsTimeout = 2 * time.Second
	t.Cleanup(func() { ModelsTimeout = oldTimeout })

	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, harness.StateDir, "sandbox")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootReal = filepath.Clean(root)
	}

	cwdLog := filepath.Join(t.TempDir(), "cwds.txt")
	fakeAgy := filepath.Join(t.TempDir(), "agy")
	script := `#!/bin/sh
here="$(pwd -P)"
if [ "$here" = "/" ]; then
  echo "refusing root cwd /" >&2
  exit 9
fi
printf '%s:%s\n' "$1" "$here" >> "` + cwdLog + `"
case "$1" in
  models) printf 'gemini-4-carbon-high\tGemini 4 Carbon (High)\n' ;;
  changelog) printf '1.5.8:\n· Added Gemini 4 Carbon\n' ;;
  -p)
    if [ "$2" = "/usage" ]; then
      printf 'Gemini Models\tWeekly Limit Remaining\t80%%\tx\nGemini Models\tFive Hour Limit Remaining\t90%%\tx\n'
    fi
    ;;
esac
`
	if err := os.WriteFile(fakeAgy, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if out, err := RunAgyModels(ctx, fakeAgy); err != nil || !strings.Contains(out, "gemini-4-carbon-high") {
		t.Fatalf("RunAgyModels failed: out=%q err=%v", out, err)
	}
	if out := RunAgyChangelog(ctx, fakeAgy); !strings.Contains(out, "Added Gemini 4 Carbon") {
		t.Fatalf("RunAgyChangelog failed: out=%q", out)
	}
	if out, err := RunUsage(ctx, fakeAgy); err != nil || !strings.Contains(out, "Gemini Models") {
		t.Fatalf("RunUsage failed: out=%q err=%v", out, err)
	}

	raw, err := os.ReadFile(cwdLog)
	if err != nil {
		t.Fatalf("read cwd log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 probe invocations, got %d:\n%s", len(lines), string(raw))
	}
	seen := make(map[string]bool, 3)
	for _, line := range lines {
		_, got, ok := strings.Cut(line, ":")
		if !ok || filepath.Dir(got) != rootReal || !strings.HasPrefix(filepath.Base(got), "fyr-agy-probes-") {
			t.Errorf("probe ran in %q, want %s/fyr-agy-probes-* (line %q)", got, rootReal, line)
			continue
		}
		if seen[got] {
			t.Errorf("probeWorkDir reused %q across calls", got)
		}
		seen[got] = true
		if _, statErr := os.Stat(got); !os.IsNotExist(statErr) {
			t.Errorf("probeWorkDir %q not removed after probe return (err=%v)", got, statErr)
		}
	}
}
