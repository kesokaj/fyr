package models

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProbeWorkDir(t *testing.T) {
	dir := probeWorkDir()
	want := filepath.Join(os.TempDir(), "fyr-agy-probes")
	if dir != want {
		t.Fatalf("probeWorkDir() = %q, want %q", dir, want)
	}
	if dir == "/" {
		t.Fatal("probeWorkDir() must never return /")
	}
	st, err := os.Lstat(dir)
	if err != nil {
		t.Fatalf("Lstat(%q): %v", dir, err)
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		t.Fatalf("probeWorkDir %q is not a regular directory: mode=%v", dir, st.Mode())
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("probeWorkDir %q permissions %v expose group/other bits", dir, st.Mode().Perm())
	}
}

func TestProbeWorkDirUsedByAgyProbes(t *testing.T) {
	oldTimeout := ModelsTimeout
	ModelsTimeout = 2 * time.Second
	t.Cleanup(func() { ModelsTimeout = oldTimeout })

	wantDir := probeWorkDir()
	wantReal, err := filepath.EvalSymlinks(wantDir)
	if err != nil {
		wantReal = filepath.Clean(wantDir)
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
	for _, line := range lines {
		_, got, ok := strings.Cut(line, ":")
		if !ok || got != wantReal {
			t.Errorf("probe ran in %q, want %q (line %q)", got, wantReal, line)
		}
	}
}
