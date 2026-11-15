package models

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestModelsPackageDeps(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == "modernc.org/sqlite" ||
				strings.HasPrefix(p, "github.com/kesokaj/fyr/internal/work") ||
				strings.HasPrefix(p, "github.com/kesokaj/fyr/internal/sandbox") {
				t.Errorf("%s imports forbidden package %q", e.Name(), p)
			}
		}
	}
}

func TestRootEnvStripsAntigravityVars(t *testing.T) {
	t.Setenv("ANTIGRAVITY_LS_ADDRESS", "localhost:1234")
	t.Setenv("ANTIGRAVITY_CSRF_TOKEN", "tok")
	t.Setenv("GIT_DIR", "/tmp/bad-git")
	t.Setenv("FYR_REPO", "/tmp/bad-repo")
	t.Setenv("SSH_CONNECTION", "192.168.5.181 1234 192.168.5.10 22")

	env := RootEnv()
	var hasSSH, hasPWD bool
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTIGRAVITY_") || strings.HasPrefix(kv, "GIT_") || kv == "FYR_REPO=/tmp/bad-repo" {
			t.Errorf("RootEnv leaked %q", kv)
		}
		if kv == "SSH_CONNECTION=192.168.5.181 1234 192.168.5.10 22" {
			hasSSH = true
		}
		if kv == "PWD=/" {
			hasPWD = true
		}
	}
	if !hasSSH || !hasPWD {
		t.Errorf("RootEnv missing expected vars: hasSSH=%v hasPWD=%v", hasSSH, hasPWD)
	}
}

func TestRunAgyDetachesControllingTTY(t *testing.T) {
	if os.Getenv("FYR_TEST_IN_PTY") == "1" {
		runAgyInPTYChild(t)
		return
	}
	scriptBin, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script binary not available for PTY check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.CommandContext(ctx, scriptBin, "-q", "/dev/null", os.Args[0], "-test.run=^TestRunAgyDetachesControllingTTY$")
	} else {
		quoted := "'" + strings.ReplaceAll(os.Args[0], "'", "'\"'\"'") + "'"
		sub := quoted + " -test.run=^TestRunAgyDetachesControllingTTY$"
		cmd = exec.CommandContext(ctx, scriptBin, "-q", "-e", "-c", sub, "/dev/null")
	}
	cmd.Env = append(os.Environ(), "FYR_TEST_IN_PTY=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PTY child failed: %v\n%s", err, out)
	}
}

func runAgyInPTYChild(t *testing.T) {
	t.Helper()
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("precondition: /dev/tty not attached: %v", err)
	}
	_ = tty.Close()

	oldTimeout := ModelsTimeout
	ModelsTimeout = 400 * time.Millisecond
	t.Cleanup(func() { ModelsTimeout = oldTimeout })

	fakeAgy := writeFakePTYScript(t)
	ctx := context.Background()
	if out, err := RunAgyModels(ctx, fakeAgy); err != nil || !strings.Contains(out, "gemini-4-carbon-high") {
		t.Fatalf("RunAgyModels: out=%q err=%v", out, err)
	}
	if out, err := RunUsage(ctx, fakeAgy); err != nil || !strings.Contains(out, "Gemini Models") {
		t.Fatalf("RunUsage: out=%q err=%v", out, err)
	}
	if out := RunAgyChangelog(ctx, fakeAgy); !strings.Contains(out, "Added Gemini 4 Carbon") {
		t.Fatalf("RunAgyChangelog: out=%q", out)
	}
}

func writeFakePTYScript(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agy")
	script := `#!/bin/sh
if [ -r /dev/tty ]; then
  head -c 1 /dev/tty >/dev/null 2>&1 || true
fi
case "$1" in
  models) printf 'gemini-4-carbon-high\tGemini 4 Carbon (High)\n' ;;
  changelog) printf '1.5.8:\n· Added Gemini 4 Carbon\n' ;;
  -p)
    if [ "$2" = "/usage" ]; then
      printf 'Gemini Models\tWeekly Limit Remaining\t80%%\tx\nGemini Models\tFive Hour Limit Remaining\t90%%\tx\n'
    else
      printf 'VERDICT: PASS — clean\n'
    fi
    ;;
esac
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}
