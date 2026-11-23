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

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/gate"
	iso "github.com/kesokaj/fyr/internal/isolate"
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

func queryJudgeVerdictsT(t *testing.T, home, repo string) []metrics.JudgeVerdictRow {
	t.Helper()
	st, err := metrics.Open(metrics.Path(home))
	if err != nil {
		t.Fatalf("metrics.Open: %v", err)
	}
	defer st.Close()
	rows, err := st.JudgeVerdicts(repo, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("JudgeVerdicts: %v", err)
	}
	return rows
}

func setTestHomeWithAuth(t *testing.T, home string, stageAuth bool) {
	t.Helper()
	if os.Getenv("GOCACHE") == "" {
		if cacheDir, err := os.UserCacheDir(); err == nil {
			goCache := filepath.Join(cacheDir, "go-build")
			if st, err := os.Stat(goCache); err == nil && st.IsDir() {
				t.Setenv("GOCACHE", goCache)
			}
		}
	}
	t.Setenv("HOME", home)
	if !stageAuth {
		return
	}
	p := filepath.Join(home, filepath.FromSlash(iso.TokenRel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	if err := os.WriteFile(p, []byte("cli-test-auth\n"), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}
}

func TestVerbGateJudge(t *testing.T) {
	repo := initCLITestRepo(t)
	home := t.TempDir()
	binDir := t.TempDir()
	setTestHomeWithAuth(t, home, true)
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
	rows := queryJudgeVerdictsT(t, home, repo)
	if len(rows) != 1 || rows[0].Verdict != "PASS" {
		t.Fatalf("metrics rows after PASS (%d rows) rows=%+v", len(rows), rows)
	}

	writeFakeCLIJudgeAgy(t, binDir, `{"verdict":"FAIL","reason":"missing edge case"}`)
	outStr, errStr, code = runCLIWithErr("gate", "feat", "--home", home, "--dir", repo, "--judge", "verify add")
	if code != 1 || !strings.Contains(outStr, "[judge]") {
		t.Fatalf("gate --judge FAIL failed: code=%d out=%q err=%q", code, outStr, errStr)
	}
	rows = queryJudgeVerdictsT(t, home, repo)
	if len(rows) != 2 || (rows[0].Verdict != "FAIL" && rows[1].Verdict != "FAIL") {
		t.Fatalf("metrics rows after FAIL: len=%d rows=%+v", len(rows), rows)
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
	rows = queryJudgeVerdictsT(t, home, repo)
	if len(rows) != 3 {
		t.Fatalf("metrics rows after land PASS (%d rows) rows=%+v", len(rows), rows)
	}
}

func hasJudgeAttribution(rows []metrics.JudgeVerdictRow, model, sess string) bool {
	for _, r := range rows {
		if r.WorkerModel == model && r.WorkerSessionID == sess {
			return true
		}
	}
	return false
}

func commitBranchFilesT(t *testing.T, repo, branch string, files map[string]string) {
	t.Helper()
	runGitCLI(t, repo, "checkout", "-b", branch, "main")
	for rel, body := range files {
		writeCLITestFile(t, filepath.Join(repo, filepath.FromSlash(rel)), body)
		runGitCLI(t, repo, "add", rel)
	}
	runGitCLI(t, repo, "commit", "-m", "commit "+branch)
	runGitCLI(t, repo, "checkout", "main")
}

func TestV910VerbGateLandWorkerFlagsAndDetail(t *testing.T) {
	repo := initCLITestRepo(t)
	home, binDir := t.TempDir(), t.TempDir()
	setTestHomeWithAuth(t, home, true)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	writeFakeCLIJudgeAgy(t, binDir, `{"verdict":"PASS","reason":"looks good"}`)

	commitBranchFilesT(t, repo, "feat-w", map[string]string{
		"w.go":      "package main\n\nfunc wVal() int { return 9 }\n",
		"w_test.go": "package main\n\nimport \"testing\"\n\nfunc TestWVal(t *testing.T) {\n\tif wVal() != 9 {\n\t\tt.Fatal()\n\t}\n}\n",
	})
	commitBranchFilesT(t, repo, "feat-fail", map[string]string{
		"fail_test.go": "package main\n\nimport \"testing\"\n\nfunc TestBoom(t *testing.T) {\n\tt.Fatal(\"boom-detail\")\n}\n",
	})

	outStr, errStr, code := runCLIWithErr("gate", "feat-w", "--home", home, "--dir", repo,
		"--judge", "check worker", "--worker-model", "gemini-4-carbon-high", "--worker-session", "conv-gate-1")
	if code != 0 {
		t.Fatalf("gate with worker flags failed: code=%d out=%q err=%q", code, outStr, errStr)
	}
	if rows := queryJudgeVerdictsT(t, home, repo); !hasJudgeAttribution(rows, "gemini-4-carbon-high", "conv-gate-1") {
		t.Fatalf("gate verdict missing worker attribution: %+v", rows)
	}

	// Failing land (verify failure & lease failure) populates LandReport.Detail.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"land", "feat-fail", "--home", home, "--dir", repo, "--json"}, "verify"},
		{[]string{"land", "feat-w", "--home", home, "--dir", repo, "--lease", "docs/**", "--json"}, "outside lease"},
	} {
		out, _, c := runCLIWithErr(tc.args...)
		var rep gate.LandReport
		if c != 1 || json.Unmarshal([]byte(out), &rep) != nil || rep.OK || !strings.Contains(rep.Detail, tc.want) {
			t.Fatalf("land fail %v: code=%d detail=%q want %q out=%s", tc.args, c, rep.Detail, tc.want, out)
		}
	}

	outStr, errStr, code = runCLIWithErr("land", "feat-w", "--home", home, "--dir", repo,
		"--judge", "check land", "--worker-model", "claude-opus-5-5-high", "--worker-session", "conv-land-2", "--json")
	var landRep gate.LandReport
	if code != 0 || json.Unmarshal([]byte(outStr), &landRep) != nil || !landRep.OK {
		t.Fatalf("land with worker flags failed: code=%d out=%q err=%q", code, outStr, errStr)
	}
	if rows := queryJudgeVerdictsT(t, home, repo); !hasJudgeAttribution(rows, "claude-opus-5-5-high", "conv-land-2") {
		t.Fatalf("land verdict missing worker attribution: %+v", rows)
	}

	emptyHome := t.TempDir()
	setTestHomeWithAuth(t, emptyHome, false)
	if _, errStr, code = runCLIWithErr("gate", "feat-fail", "--home", emptyHome, "--dir", repo, "--judge", "check auth"); code != 2 || !strings.Contains(errStr, "OAuth") {
		t.Fatalf("gate --judge without OAuth file: code=%d err=%q, want exit 2 OAuth refusal", code, errStr)
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

func TestV910CloseStdinFailFast(t *testing.T) {
	home := t.TempDir()
	repo := initCLITestRepo(t)
	if outStr, errStr, code := runCLIWithErr("verify", "--home", home, "--dir", repo); code != 0 {
		t.Fatalf("verify: code=%d out=%q err=%q", code, outStr, errStr)
	}

	// 1. Help first 6 lines must document reading stdin when --body-file is omitted.
	helpOut, _, code := runCLIWithErr("close", "--help")
	if code != 0 {
		t.Fatalf("close --help exit=%d", code)
	}
	headLines := strings.Split(helpOut, "\n")
	if len(headLines) > 6 {
		headLines = headLines[:6]
	}
	if !strings.Contains(strings.Join(headLines, "\n"), "stdin") {
		t.Errorf("close --help first 6 lines lack stdin note:\n%s", strings.Join(headLines, "\n"))
	}

	// 2. Idle open pipe (like `sleep 4 | fyr close`) fails fast (< 50ms) with exit 2.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	origStdin := stdin
	t.Cleanup(func() { stdin = origStdin })
	stdin = pr
	t0 := time.Now()
	_, errStr, code := runCLIWithErr("close", "--home", home, "--dir", repo, "--title", "idle-pipe")
	elapsed := time.Since(t0)
	if code != 2 || !strings.Contains(errStr, "no body on stdin") || !strings.Contains(errStr, "--body-file") {
		t.Fatalf("idle pipe close: code=%d err=%q elapsed=%s", code, errStr, elapsed)
	}
	if elapsed >= 50*time.Millisecond {
		t.Errorf("idle pipe close took %s, want < 50ms", elapsed)
	}

	// 3. /dev/null (char device) and empty reader fail fast with exit 2.
	if devNull, err := os.Open(os.DevNull); err == nil {
		defer devNull.Close()
		stdin = devNull
		_, errStr, code = runCLIWithErr("close", "--home", home, "--dir", repo, "--title", "dev-null")
		if code != 2 || !strings.Contains(errStr, "no body on stdin") {
			t.Errorf("/dev/null close: code=%d err=%q", code, errStr)
		}
	}
	stdin = strings.NewReader("")
	_, errStr, code = runCLIWithErr("close", "--home", home, "--dir", repo, "--title", "empty-reader")
	if code != 2 || !strings.Contains(errStr, "no body on stdin") {
		t.Errorf("empty reader close: code=%d err=%q", code, errStr)
	}

	// 4. Populated pipe preserves ALL bytes (including first byte!) and writes handoff.
	pr2, pw2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr2.Close()
	const wantBody = "First-byte preserved handoff summary.\n"
	if _, err := pw2.WriteString(wantBody); err != nil {
		t.Fatal(err)
	}
	pw2.Close()
	stdin = pr2
	outStr, errStr, code := runCLIWithErr("close", "--home", home, "--dir", repo, "--title", "piped-ok")
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(outStr), "HANDOFF-") {
		t.Fatalf("piped close: code=%d out=%q err=%q", code, outStr, errStr)
	}
	recs, _, gerr := brain.Get(brain.Options{Home: home, Dir: repo}, []string{strings.TrimSpace(outStr)})
	if gerr != nil || len(recs) != 1 || !strings.HasPrefix(recs[0].Body, wantBody) {
		t.Fatalf("stored handoff body corrupted: err=%v recs=%+v", gerr, recs)
	}

	// 5. Oversized stdin and oversized --body-file (> brain.MaxBodyBytes) both exit 2.
	oversized := strings.Repeat("x", brain.MaxBodyBytes+1)
	stdin = strings.NewReader(oversized)
	_, errStr, code = runCLIWithErr("close", "--home", home, "--dir", repo, "--title", "too-big-stdin")
	if code != 2 || !strings.Contains(errStr, "exceeds") {
		t.Errorf("oversized stdin close: code=%d err=%q", code, errStr)
	}
	stdin = strings.NewReader("")
	bigFile := filepath.Join(t.TempDir(), "big.md")
	writeCLITestFile(t, bigFile, oversized)
	_, errStr, code = runCLIWithErr("close", "--home", home, "--dir", repo, "--title", "too-big-file", "--body-file", bigFile)
	if code != 2 || !strings.Contains(errStr, "exceeds") {
		t.Errorf("oversized --body-file close: code=%d err=%q", code, errStr)
	}
}
