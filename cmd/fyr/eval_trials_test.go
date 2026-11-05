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

	"github.com/kesokaj/fyr/internal/brain"
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
	if err := os.MkdirAll(filepath.Join(repo, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "skills", "f"), []byte("x\n"), 0o644); err != nil {
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
	if got := harnessKey(harness.Source{Dir: filepath.Join(repo, "skills")}); got != short+" (dirty: unknown)" {
		t.Errorf("below the top level: %q, want %q", got, short+" (dirty: unknown)")
	}
	if err := os.WriteFile(filepath.Join(repo, "skills", "f"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := harnessKey(harness.Source{Dir: repo}); got != short+" (dirty)" {
		t.Errorf("dirty: %q, want %q", got, short+" (dirty)")
	}
}

// TestHarnessKeyReadsInstalledDirs is PLAN.md item 53: the dirty mark looks
// only at what `fyr install` installs. A run's testing record changes the
// checkout's trail, and the next run of the same harness keeps its key; a
// change under an installed directory, tracked or not, still marks it.
func TestHarnessKeyReadsInstalledDirs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git here")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
		t.Skipf("the temporary directory is inside a checkout: %s", out)
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@localhost"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, s string) {
		t.Helper()
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("context/AGENTS.md", "c\n")
	write("skills/s/SKILL.md", "s\n")
	write(".agents/trail.jsonl", "{}\n")
	git("add", "-A")
	git("commit", "-qm", "c")
	short := gitShort(repo)
	write(".agents/trail.jsonl", "{}\n{\"id\":\"TEST-1\"}\n")
	write("PLAN.md", "untracked\n")
	if got := harnessKey(harness.Source{Dir: repo}); got != short {
		t.Errorf("after a run wrote its testing record: %q, want the clean %q", got, short)
	}
	for _, rel := range []string{"skills/s/SKILL.md", "rules/new.md", "workflows/w.md"} {
		write(rel, "changed\n")
		if got := harnessKey(harness.Source{Dir: repo}); got != short+" (dirty)" {
			t.Errorf("after writing %s: %q, want %q", rel, got, short+" (dirty)")
		}
		if rel == "skills/s/SKILL.md" {
			write(rel, "s\n")
		} else if err := os.Remove(filepath.Join(repo, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
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

// evalLongSuite is evalSuite under the long header (ADR-085): ok sets all
// four optional cells, two leaves them empty.
const evalLongSuite = "The suite of the verb tests.\n\n" +
	"| id | setup | prompt | prompt2 | check | agent | grants | version | kind | cluster | expect |\n" +
	"|---|---|---|---|---|---|---|---|---|---|---|\n" +
	"| `ok` |  | say ok |  | `grep -q 'agy says ok' \"$OUT\"` |  | named | 2 | regression | greet | Read, fyr record get |\n" +
	"| `two` |  | first |  | `grep -q 'agy says ok' \"$OUT2\"` |  | named |  |  |  |  |\n"

// TestEvalVerbListLong: --list prints each row's version and kind after its
// grants, the defaults for an empty cell.
func TestEvalVerbListLong(t *testing.T) {
	r := newEvalRig(t, evalLongSuite, false)
	out, code := r.eval("--list")
	if code != 0 || out != "ok  -  named  v2  regression\ntwo  -  named  v1  capability\n" {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// TestEvalTrialSuiteFields: each trial's row carries its row's version,
// kind and cluster with the defaults applied, and expect as written or
// NULL (ADR-085); a seven-column suite stores the defaults.
func TestEvalTrialSuiteFields(t *testing.T) {
	for name, c := range map[string]struct {
		suite string
		want  map[string][4]any // row → row_version, kind, cluster, expect
	}{
		"long": {evalLongSuite, map[string][4]any{
			"ok": {float64(2), "regression", "greet", "Read,fyr record get"},
		}},
		"seven columns": {evalSuite, map[string][4]any{
			"ok": {float64(1), "capability", "ok", nil},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newEvalRig(t, c.suite, true)
			r.template("1", "C1")
			if out, code := r.eval("ok"); code != 0 {
				t.Fatalf("eval ok: exit %d\n%s", code, out)
			}
			trials := exported(t, r.home, "trials")
			if len(trials) != 1 {
				t.Fatalf("%d trials rows, want 1: %v", len(trials), trials)
			}
			tr, w := trials[0], c.want["ok"]
			got := [4]any{tr["row_version"], tr["kind"], tr["cluster"], tr["expect"]}
			if got != w {
				t.Errorf("row_version, kind, cluster, expect = %v, want %v", got, w)
			}
		})
	}
}

// TestEvalSuiteFile pins Phase 6 box 1 (D15): --suite-file reads a suite
// outside every checkout, refuses one inside a checkout, and labels its
// trials and ingested sessions eval=holdout/<id>.
func TestEvalSuiteFile(t *testing.T) {
	r := newEvalRig(t, evalSuite, true)
	r.template("1", "C1")
	suiteBody := "| id | setup | prompt | prompt2 | check | agent | grants | version | kind | cluster | expect |\n" +
		"|---|---|---|---|---|---|---|---|---|---|---|\n" +
		"| `secret` |  | say ok |  | `grep -q 'agy says ok' \"$OUT\"` |  | named | 1 | regression | hold | Read |\n"
	inside := filepath.Join(r.project, "holdout.md")
	if err := os.WriteFile(inside, []byte(suiteBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := r.eval("--suite-file", inside, "secret"); code != 2 || !strings.Contains(out, "inside a git checkout") {
		t.Fatalf("inside checkout: exit %d\n%s", code, out)
	}
	outside := filepath.Join(t.TempDir(), "holdout.md")
	if err := os.WriteFile(outside, []byte(suiteBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, code := r.eval("--suite-file", outside, "--list"); code != 0 || out != "secret  -  named  v1  regression\n" {
		t.Fatalf("--list: exit %d\n%s", code, out)
	}
	out, code := r.eval("--suite-file", outside, "secret")
	if code != 0 || !strings.Contains(out, "eval: 1 of 1 PASS, 0 errors; record TEST-") {
		t.Fatalf("run: exit %d\n%s", code, out)
	}
	if k, passed := r.passK("eval=holdout/secret"); k != 1 || passed != 1 {
		t.Errorf("passK(eval=holdout/secret) = %d, %d; want 1, 1", k, passed)
	}
	trials := exported(t, r.home, "trials")
	if len(trials) != 1 || trials[0]["label"] != "eval=holdout/secret" || trials[0]["cluster"] != "hold" {
		t.Errorf("trials = %v, want label eval=holdout/secret cluster hold", trials)
	}
}

// TestEvalVerbBrainWatch pins BUG-504 and ADR-506 end to end through runEval
// and brain.SnapshotScopes: concurrent writes to brain.db in o.Root or another
// project's scope (plus metrics.db) during a trial pass check c, whereas a
// leaked write to the trial clone's scope in brain.db fails check c.
func TestEvalVerbBrainWatch(t *testing.T) {
	t.Run("concurrent unwatched writes pass", func(t *testing.T) {
		r := newEvalRig(t, evalSuite, true)
		r.template("1", "C1")
		if _, err := brain.Add(brain.Options{Home: r.real, Dir: r.project}, brain.AddInput{
			Kind: "change", Title: "Before trial", Body: strings.NewReader("body"),
		}); err != nil {
			t.Fatal(err)
		}
		oldRun := evalRun
		t.Cleanup(func() { evalRun = oldRun })
		evalRun = func(ctx context.Context, o eval.Options, rows []eval.Row) ([]eval.Result, error) {
			origWatch := o.BrainWatch
			calls := 0
			o.BrainWatch = func(dbPath, scratch string, scopes []string) (string, bool) {
				calls++
				if calls == 2 {
					if _, err := brain.Add(brain.Options{Home: r.real, Dir: r.project}, brain.AddInput{
						Kind: "bug", Title: "Concurrent session write", Status: "open", Body: strings.NewReader("body"),
					}); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(r.real, harness.StateDir, "metrics.db"), []byte("ingested"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				return origWatch(dbPath, scratch, scopes)
			}
			return oldRun(ctx, o, rows)
		}
		if out, code := r.eval("ok"); code != 0 || !strings.Contains(out, "eval: 1 of 1 PASS") {
			t.Fatalf("concurrent unwatched writes: exit %d\n%s", code, out)
		}
	})

	t.Run("leaked clone scope write fails check c", func(t *testing.T) {
		r := newEvalRig(t, evalSuite, true)
		r.template("1", "C1")
		if _, err := brain.Add(brain.Options{Home: r.real, Dir: r.project}, brain.AddInput{
			Kind: "change", Title: "Before trial", Body: strings.NewReader("body"),
		}); err != nil {
			t.Fatal(err)
		}
		oldRun := evalRun
		t.Cleanup(func() { evalRun = oldRun })
		evalRun = func(ctx context.Context, o eval.Options, rows []eval.Row) ([]eval.Result, error) {
			origWatch := o.BrainWatch
			calls := 0
			o.BrainWatch = func(dbPath, scratch string, scopes []string) (string, bool) {
				calls++
				if calls == 2 && len(scopes) > 0 {
					if _, err := brain.Add(brain.Options{Home: r.real, Dir: scopes[0]}, brain.AddInput{
						Kind: "change", Title: "Leaked trial write", Body: strings.NewReader("body"),
					}); err != nil {
						t.Fatal(err)
					}
				}
				return origWatch(dbPath, scratch, scopes)
			}
			return oldRun(ctx, o, rows)
		}
		if out, code := r.eval("ok"); code != 1 || !strings.Contains(out, "FAIL (config; real config changed)") {
			t.Fatalf("leaked clone write: exit %d\n%s", code, out)
		}
	})
}
