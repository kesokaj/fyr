package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/gate"
)

func runCLIWithErr(args ...string) (stdoutStr, stderrStr string, code int) {
	var outBuf, errBuf bytes.Buffer
	prevErr := stderr
	stderr = &errBuf
	defer func() { stderr = prevErr }()
	err := run(args, &outBuf)
	return outBuf.String(), errBuf.String(), exitOf(err)
}

func runGitCLI(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func initCLITestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitCLI(t, repo, "init", "-b", "main")
	runGitCLI(t, repo, "config", "user.name", "Test Operator")
	runGitCLI(t, repo, "config", "user.email", "test@example.com")
	writeCLITestFile(t, filepath.Join(repo, ".gitignore"), ".agents/scratch/\n")
	writeCLITestFile(t, filepath.Join(repo, "go.mod"), "module example.com/clitest\n\ngo 1.24\n")
	writeCLITestFile(t, filepath.Join(repo, "main.go"), "package main\n\nfunc main() {}\n")
	writeCLITestFile(t, filepath.Join(repo, "main_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n")
	runGitCLI(t, repo, "add", ".")
	runGitCLI(t, repo, "commit", "-m", "initial")
	return repo
}

func writeCLITestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCLIVerifyGateLandClose(t *testing.T) {
	home := t.TempDir()
	repo := initCLITestRepo(t)

	// 1. close before verify -> exit 2 refusal.
	bodyPath := filepath.Join(t.TempDir(), "handoff.md")
	writeCLITestFile(t, bodyPath, "Session summary.\n")
	_, errStr, code := runCLIWithErr("close", "--home", home, "--dir", repo, "--title", "early-close", "--body-file", bodyPath)
	if code != 2 || !strings.Contains(errStr, "verify.stamp") {
		t.Fatalf("close without stamp: code=%d err=%q", code, errStr)
	}

	// 2. verify --json -> exit 0 + valid JSON.
	outStr, errStr, code := runCLIWithErr("verify", "--home", home, "--dir", repo, "--json")
	if code != 0 {
		t.Fatalf("verify --json failed: code=%d out=%s err=%s", code, outStr, errStr)
	}
	var vRep gate.VerifyReport
	if err := json.Unmarshal([]byte(outStr), &vRep); err != nil || !vRep.OK {
		t.Fatalf("invalid verify json: err=%v out=%s", err, outStr)
	}

	// 3. gate failure (out-of-lease) -> exit 1; gate usage (no arg) -> exit 2.
	outStr, _, code = runCLIWithErr("gate", "--home", home, "--dir", repo)
	if code != 2 || !strings.Contains(outStr, "usage: fyr gate") {
		t.Fatalf("bare gate: code=%d out=%q", code, outStr)
	}

	wt := filepath.Join(t.TempDir(), "wt-cli")
	runGitCLI(t, repo, "worktree", "add", "-b", "cli-branch", wt, "main")
	writeCLITestFile(t, filepath.Join(wt, "extra.go"), "package main\n\nfunc Extra() int { return 7 }\n")
	writeCLITestFile(t, filepath.Join(wt, "extra_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestExtra(t *testing.T) {\n\tif Extra() != 7 {\n\t\tt.Fatal()\n\t}\n}\n")
	runGitCLI(t, wt, "add", "extra.go", "extra_test.go")
	runGitCLI(t, wt, "commit", "-m", "add Extra")

	outStr, _, code = runCLIWithErr("gate", "--home", home, "--dir", repo, "--lease", "docs/**", "--json", wt)
	if code != 1 {
		t.Fatalf("out-of-lease gate: want exit 1, got %d out=%s", code, outStr)
	}
	outStr, errStr, code = runCLIWithErr("gate", "--home", home, "--dir", repo, "--lease", "extra*.go", wt)
	if code != 0 || !strings.Contains(outStr, "gate: PASS") {
		t.Fatalf("happy gate: code=%d out=%q err=%q", code, outStr, errStr)
	}

	// Remove worktree so branch can land cleanly.
	runGitCLI(t, repo, "worktree", "remove", wt)

	// 4. land --json -> exit 0, branch deleted.
	outStr, errStr, code = runCLIWithErr("land", "--home", home, "--dir", repo, "--lease", "extra*.go", "--json", "cli-branch")
	if code != 0 {
		t.Fatalf("land failed: code=%d out=%s err=%s", code, outStr, errStr)
	}
	var lRep gate.LandReport
	if err := json.Unmarshal([]byte(outStr), &lRep); err != nil || !lRep.OK || !lRep.BranchDel {
		t.Fatalf("bad land json: err=%v out=%s", err, outStr)
	}

	// 5. close after land -> exit 0, prints HANDOFF- ID.
	outStr, errStr, code = runCLIWithErr("close", "--home", home, "--dir", repo, "--title", "cli-done", "--body-file", bodyPath)
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(outStr), "HANDOFF-") {
		t.Fatalf("close failed: code=%d out=%q err=%q", code, outStr, errStr)
	}
}
