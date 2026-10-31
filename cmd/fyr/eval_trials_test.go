package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
)

// exported is every row of table in home's metrics store, read through
// fyr metrics export.
func exported(t *testing.T, home, table string) []map[string]any {
	t.Helper()
	file := filepath.Join(t.TempDir(), "export.jsonl")
	var buf bytes.Buffer
	if err := run([]string{"metrics", "export", file, "--home", home}, &buf); err != nil {
		t.Fatalf("metrics export: %v\n%s", err, buf.String())
	}
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, file)), "\n") {
		var rec struct {
			Table string         `json:"table"`
			Row   map[string]any `json:"row"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("export line %q: %v", line, err)
		}
		if rec.Table == table {
			rows = append(rows, rec.Row)
		}
	}
	return rows
}

func str(v any) string { s, _ := v.(string); return s }

// TestEvalVerbRuns: --runs 3 runs the row three times under one stamp, each
// trial in its own sandbox with its own trials row, its session carrying
// its run; pass^k counts three trials.
func TestEvalVerbRuns(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	r.template("1", "C1")
	r.template("2", "C2")
	r.template("3", "C3")
	out, code := r.eval("--runs", "3", "ok")
	if code != 0 {
		t.Fatalf("eval --runs 3 ok: exit %d\n%s", code, out)
	}
	m := regexp.MustCompile(`(?m)^eval: ok#1  PASS  agy exit 0  \d+s \(\d+s session\)  (\S+)/ok/1$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no line for trial 1:\n%s", out)
	}
	for _, i := range []string{"2", "3"} {
		if !strings.Contains(out, "  "+m[1]+"/ok/"+i+"\n") {
			t.Errorf("trial %s is not under the same stamp %s:\n%s", i, m[1], out)
		}
	}
	if s := recordIDLine.FindStringSubmatch(out); s == nil || s[1] != "3" || s[2] != "3" {
		t.Errorf("summary: want 3 of 3 PASS:\n%s", out)
	}
	runs := map[any]bool{}
	for _, tr := range exported(t, r.home, "trials") {
		runs[tr["run"]] = true
	}
	stamp := filepath.Base(m[1])
	for _, i := range []string{"1", "2", "3"} {
		if !runs[stamp+"/ok/"+i] {
			t.Errorf("no trials row %s/ok/%s: %v", stamp, i, runs)
		}
	}
	sessions := exported(t, r.home, "sessions")
	seen := map[any]bool{}
	for _, x := range sessions {
		if !runs[x["run"]] || seen[x["run"]] {
			t.Errorf("session %v: run %v, want one trial's own", x["id"], x["run"])
		}
		seen[x["run"]] = true
	}
	if len(runs) != 3 || len(sessions) != 3 {
		t.Errorf("%d trials and %d sessions, want 3 and 3", len(runs), len(sessions))
	}
	if k, passed := r.passK("eval=ok"); k != 3 || passed != 3 {
		t.Errorf("pass^k of eval=ok: k %d passed %d, want 3 and 3", k, passed)
	}
}

// TestEvalVerbErrorTrial: a trial whose setup fails is an ERROR with cause
// setup: no session ran, yet it has a trials row, with no session seconds,
// and pass^k counts it under errors, never graded (the eval-metrics plan
// D6, D13).
func TestEvalVerbErrorTrial(t *testing.T) {
	r := newEvalRig(t, evalSuite+"| `err` | `exit 1` | say ok |  | `true` |  | named |\n", true)
	out, code := r.eval("--runs", "2", "err")
	if code != 1 || strings.Count(out, "  ERROR (setup; setup failed)  agy exit -1  ") != 2 || !strings.Contains(out, "eval: err#2  ") {
		t.Fatalf("exit %d, want 1 with two ERROR lines:\n%s", code, out)
	}
	if n, err := os.ReadFile(filepath.Join(r.rec, "agy.n")); err == nil {
		t.Errorf("agy ran %s times after a failed setup", n)
	}
	trials := exported(t, r.home, "trials")
	if len(trials) != 2 {
		t.Fatalf("%d trials rows, want 2: %v", len(trials), trials)
	}
	for _, tr := range trials {
		if tr["status"] != "error" || tr["cause"] != "setup" || tr["session_seconds"] != nil {
			t.Errorf("the trial row: %v, want error, setup, no session seconds", tr)
		}
	}
	if got := exported(t, r.home, "sessions"); len(got) != 0 {
		t.Errorf("%d sessions stored for a trial that ran none", len(got))
	}
	if a := a6(t, r.home, "eval=err"); a.N != 0 || a.Errors != 2 {
		t.Errorf("A6 of eval=err: n %d errors %d, want 0 and 2", a.N, a.Errors)
	}
}

// TestEvalVerbUnrunTrials: a trial that never ran is exit 1 even when every
// trial that ran passed; the unrun count is rows × runs minus the results.
func TestEvalVerbUnrunTrials(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	old := evalRun
	evalRun = func(ctx context.Context, o eval.Options, rows []eval.Row) ([]eval.Result, error) {
		res, err := eval.Run(ctx, o, rows)
		return res[:len(res)-1], err
	}
	t.Cleanup(func() { evalRun = old })
	out, code := r.eval("--runs", "2", "ok")
	if s := recordIDLine.FindStringSubmatch(out); code != 1 || s == nil || s[1] != "1" || s[2] != "1" {
		t.Fatalf("exit %d, want 1 with 1 of 1 PASS and the second trial unrun:\n%s", code, out)
	}
}

// TestHarnessKey: each branch of the key the trials of a run share (the
// eval-metrics plan D15, D31): a release binary's embedded harness, a
// source outside git, a clean and a dirty checkout, and a tree whose
// dirtiness cannot be read (a directory below its top level).
func TestHarnessKey(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git here")
	}
	if got := harnessKey(harness.Source{Version: "v5.1.0"}); got != "embedded v5.1.0" {
		t.Errorf("embedded: %q", got)
	}
	if got := harnessKey(harness.Source{}); got != "embedded" {
		t.Errorf("embedded, no version: %q", got)
	}
	notGit := t.TempDir()
	if out, err := exec.Command("git", "-C", notGit, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
		t.Skipf("the temporary directory is inside a checkout: %s", out)
	}
	if got := harnessKey(harness.Source{Dir: notGit}); got != "unknown" {
		t.Errorf("not a checkout: %q", got)
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@localhost"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "sub", "f"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "c")
	short := gitShort(repo)
	if !regexp.MustCompile(`^[0-9a-f]{4,}$`).MatchString(short) {
		t.Fatalf("gitShort: %q", short)
	}
	if got := harnessKey(harness.Source{Dir: repo}); got != short {
		t.Errorf("clean: %q, want %q", got, short)
	}
	if got := harnessKey(harness.Source{Dir: filepath.Join(repo, "sub")}); got != short+" (dirty: unknown)" {
		t.Errorf("below the top level: %q, want %q", got, short+" (dirty: unknown)")
	}
	if err := os.WriteFile(filepath.Join(repo, "sub", "f"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := harnessKey(harness.Source{Dir: repo}); got != short+" (dirty)" {
		t.Errorf("dirty: %q, want %q", got, short+" (dirty)")
	}
}

// TestTrialText: a harness or cli value is made printable and cut to 120
// bytes on a rune boundary, so the trial's validation never refuses it.
func TestTrialText(t *testing.T) {
	if got := trialText("agy 1.4\x1b[31m"); got != "agy 1.4?[31m" {
		t.Errorf("escape: %q", got)
	}
	long := strings.Repeat("a", 119) + "é" + "tail" // é is two bytes: 119 + 2 > 120
	if got := trialText(long); got != strings.Repeat("a", 119) {
		t.Errorf("cut: %d bytes %q", len(got), got)
	}
	if got := trialText(strings.Repeat("b", 120)); len(got) != 120 {
		t.Errorf("exactly 120 bytes cut to %d", len(got))
	}
}
