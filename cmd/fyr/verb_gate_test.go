package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/gate"
	"github.com/kesokaj/fyr/internal/metrics"
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

func writeFakeCLIJudgeAgy(t *testing.T, binDir, verdictJSON string) {
	t.Helper()
	q := "'" + strings.ReplaceAll(verdictJSON, "'", "'\\''") + "'"
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"models\" ]; then\n" +
		"  printf 'gemini-4-carbon-high\\tGemini 4 Carbon (High)\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"-p\" ] && [ \"$2\" = \"/usage\" ]; then\n" +
		"  printf 'Claude and GPT models\\tWeekly Limit Remaining\\t50%%\\tx\\nClaude and GPT models\\tFive Hour Limit Remaining\\t70%%\\tx\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"-p\" ]; then\n" +
		"  printf '%s\\n' " + q + "\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	writeCLITestFile(t, filepath.Join(binDir, "agy"), script)
	if err := os.Chmod(filepath.Join(binDir, "agy"), 0o755); err != nil {
		t.Fatalf("chmod agy: %v", err)
	}
}

func TestVerbGateJudge(t *testing.T) {
	repo := initCLITestRepo(t)
	home := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	runGitCLI(t, repo, "checkout", "-b", "feat")
	writeCLITestFile(t, filepath.Join(repo, "add.go"), "package main\n\nfunc add(a, b int) int { return a + b }\n")
	writeCLITestFile(t, filepath.Join(repo, "add_test.go"), "package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif add(1, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	runGitCLI(t, repo, "add", "add.go", "add_test.go")
	runGitCLI(t, repo, "commit", "-m", "feat add")
	runGitCLI(t, repo, "checkout", "main")

	if _, _, code := runCLIWithErr("gate", "feat", "--home", home, "--dir", repo, "--judge-model", "gemini-4-carbon-high"); code != 2 {
		t.Fatalf("gate --judge-model without --judge: code=%d, want 2", code)
	}
	if _, _, code := runCLIWithErr("land", "feat", "--home", home, "--dir", repo, "--judge-model", "gemini-4-carbon-high"); code != 2 {
		t.Fatalf("land --judge-model without --judge: code=%d, want 2", code)
	}

	writeFakeCLIJudgeAgy(t, binDir, `{"verdict":"PASS","reason":"add matches criterion"}`)
	outStr, errStr, code := runCLIWithErr("gate", "feat", "--home", home, "--dir", repo, "--judge", "verify add")
	if code != 0 || !strings.Contains(outStr, "gate: PASS (") || !strings.Contains(outStr, "judge ") {
		t.Fatalf("gate --judge PASS failed: code=%d out=%q err=%q", code, outStr, errStr)
	}
	rows, err := metrics.QueryJudgeVerdicts(home, repo, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil || len(rows) != 1 || rows[0].Verdict != "PASS" {
		t.Fatalf("metrics rows after PASS (%d rows) err=%v rows=%+v", len(rows), err, rows)
	}

	writeFakeCLIJudgeAgy(t, binDir, `{"verdict":"FAIL","reason":"missing edge case"}`)
	outStr, errStr, code = runCLIWithErr("gate", "feat", "--home", home, "--dir", repo, "--judge", "verify add")
	if code != 1 || !strings.Contains(outStr, "[judge]") {
		t.Fatalf("gate --judge FAIL failed: code=%d out=%q err=%q", code, outStr, errStr)
	}
	rows, err = metrics.QueryJudgeVerdicts(home, repo, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil || len(rows) != 2 || (rows[0].Verdict != "FAIL" && rows[1].Verdict != "FAIL") {
		t.Fatalf("metrics rows after FAIL: len=%d err=%v rows=%+v", len(rows), err, rows)
	}

	writeFakeCLIJudgeAgy(t, binDir, `{"verdict":"PASS","reason":"ready to land"}`)
	outStr, errStr, code = runCLIWithErr("land", "feat", "--home", home, "--dir", repo, "--judge", "verify add", "--json")
	if code != 0 {
		t.Fatalf("land --judge --json failed: code=%d out=%q err=%q", code, outStr, errStr)
	}
	var landRep gate.LandReport
	if err := json.Unmarshal([]byte(outStr), &landRep); err != nil || !landRep.OK {
		t.Fatalf("unmarshal LandReport failed: ok=%v err=%v out=%q", landRep.OK, err, outStr)
	}
	rows, err = metrics.QueryJudgeVerdicts(home, repo, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil || len(rows) != 3 {
		t.Fatalf("metrics rows after land PASS (%d rows) err=%v rows=%+v", len(rows), err, rows)
	}
}

func TestShortHashAndPrintVerifyLandText(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"", 12, ""},
		{"abc", 12, "abc"},
		{"0123456789abcdef", 12, "0123456789ab"},
		{"abc", 0, ""},
		{"abc", -1, ""},
	} {
		if got := shortHash(tc.in, tc.n); got != tc.want {
			t.Errorf("shortHash(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}

	for _, hash := range []string{"", "abc", strings.Repeat("a", 64)} {
		var buf bytes.Buffer
		printVerifyText(&buf, gate.VerifyReport{
			OK:         true,
			TreeSHA256: hash,
			Steps:      []gate.StepResult{{Name: "gofmt", OK: true, DurationMs: 5}},
		})
		want := "verify: PASS (" + shortHash(hash, 12) + ")"
		if !strings.Contains(buf.String(), want) || !strings.Contains(buf.String(), "verify gofmt:   PASS (5ms)") {
			t.Errorf("printVerifyText(hash=%q) = %q, want %q", hash, buf.String(), want)
		}
	}

	var failVerify bytes.Buffer
	printVerifyText(&failVerify, gate.VerifyReport{
		OK:    false,
		Steps: []gate.StepResult{{Name: "test", OK: false, DurationMs: 9, Detail: "boom"}},
	})
	if !strings.Contains(failVerify.String(), "FAIL (9ms)") || !strings.Contains(failVerify.String(), "boom") {
		t.Errorf("printVerifyText failure = %q", failVerify.String())
	}

	for _, commit := range []string{"", "def", "0123456789abcdef0123456789abcdef01234567"} {
		var buf bytes.Buffer
		printLandText(&buf, gate.LandReport{
			OK:         true,
			Branch:     "work/x",
			Target:     "main",
			LandCommit: commit,
		})
		want := "land: PASS work/x -> main (" + shortHash(commit, 12) + ")"
		if !strings.Contains(buf.String(), want) {
			t.Errorf("printLandText(commit=%q) = %q, want %q", commit, buf.String(), want)
		}
	}

	var failLand bytes.Buffer
	printLandText(&failLand, gate.LandReport{
		OK:     false,
		Gate:   &gate.GateReport{Findings: []gate.GateFinding{{Check: "lease", Path: "x.go", Line: 3, Detail: "out"}}},
		Verify: &gate.VerifyReport{Steps: []gate.StepResult{{Name: "vet", OK: false, DurationMs: 2, Detail: "bad"}}},
	})
	if !strings.Contains(failLand.String(), "[lease] x.go:3: out") || !strings.Contains(failLand.String(), "verify vet:") {
		t.Errorf("printLandText failure = %q", failLand.String())
	}
}
