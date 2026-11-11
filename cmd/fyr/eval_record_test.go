package main

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
)

// TestEvalVerbRecord is the trial record's golden path over the fake agy
// with --runs 2 and one ERROR (SPEC-fyr-eval § The testing record): each
// trial's line, the summary counting graded trials and errors, the title's
// k and errors, the header naming k, the model, agy, the harness and the
// suite's Wilson interval, the per-trial columns, and the per-row table.
func TestEvalVerbRecord(t *testing.T) {
	r := newEvalRig(t, "", false)
	mark := filepath.Join(r.ctl, "flaky-once")
	suite := evalSuite + "| `flaky` | `if test -e '" + mark + "'; then true; else touch '" + mark + "'; exit 1; fi` | say ok |  | `grep -q 'agy says ok' \"$OUT\"` |  | named |\n"
	r.mustRun(strings.NewReader(suite), "record", "add", "guide", "--id", "GUIDE-eval", "--title", "suite", "--home", r.home)
	r.mustRun(nil, "record", "export", "--home", r.home)
	r.git("add", "-A")
	r.git("commit", "-qm", "the suite")
	// Sessions 1 and 2 are ok's, 3 is flaky's second trial: the header
	// names the models the store holds for the run's trials.
	r.templateModel("1", "gemini-x", "C1")
	r.templateModel("2", "gemini-x", "C2")
	r.templateModel("3", "gemini-y", "C3")
	out, code := r.eval("--runs", "2", "ok", "flaky")
	if code != 1 {
		t.Fatalf("exit %d, want 1 for the ERROR trial:\n%s", code, out)
	}
	session := `\d+s \(\d+s session\)  \S+/`
	for _, line := range []string{
		`eval: ok#1  PASS  agy exit 0  ` + session + `ok/1`,
		`eval: ok#2  PASS  agy exit 0  ` + session + `ok/2`,
		`eval: flaky#1  ERROR \(setup; setup failed\)  agy exit -1  \d+s  \S+/flaky/1`,
		`eval: flaky#2  PASS  agy exit 0  ` + session + `flaky/2`,
	} {
		if !regexp.MustCompile(`(?m)^` + line + `$`).MatchString(out) {
			t.Errorf("no line %s:\n%s", line, out)
		}
	}
	m := regexp.MustCompile(`(?m)^eval: 3 of 3 PASS, 1 error; record (TEST-\S+)$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("summary: want 3 of 3 PASS, 1 error:\n%s", out)
	}
	got := r.mustRun(nil, "record", "get", m[1], "--home", r.home)
	key := harnessKey(harness.Source{Dir: r.harness})
	for _, want := range []string{
		" PASS at " + gitShort(r.project) + " (k=2, 1 error)\n",
		"- runs per row: 2; estimator: c/n over graded trials; pass^k = C(c,k)/C(n,k)\n",
		"- model: mixed (gemini-x, gemini-y); agy: agy-fake 9.9; harness: " + key + "\n",
		"- interval: Wilson 95%, per row below and over rows for the suite: [0.34, 1.00]\n",
		"| row | trial | status | cause | reasons | agy exit | seconds | session s | sandbox |\n",
		"| `ok` | 1 | PASS | - | - | 0 | ",
		"| `flaky` | 1 | ERROR | setup | setup failed | -1 | ",
		"| row | n | c | errors | wilson95 | expect |\n|---|---|---|---|---|---|\n| `ok` | 2 | 2 | 0 | [0.34, 1.00] | - |\n| `flaky` | 1 | 1 | 1 | [0.21, 1.00] | - |\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the record lacks %q:\n%s", want, got)
		}
	}
	if !regexp.MustCompile("\\| `flaky` \\| 1 \\| ERROR \\| setup \\| setup failed \\| -1 \\| \\d+ \\| - \\| `").MatchString(got) {
		t.Errorf("the ERROR trial's session seconds are not -:\n%s", got)
	}
	if strings.Contains(got, "- agy: ") {
		t.Errorf("the old agy line is still in the header:\n%s", got)
	}
}

// TestEvalTally: the counts the title, summary and row table read: graded
// trials and passes, errors apart, rows in the order first run; the
// suite's interval is over rows with a graded trial, each scored c/n, and
// a run with none has no interval; the title's suffix shows only past one
// run per row or with an error.
func TestEvalTally(t *testing.T) {
	res := func(row, status string) eval.Result { return eval.Result{Row: row, Status: status} }
	tl := tallyOf([]eval.Result{res("a", eval.StatusPass), res("b", eval.StatusError), res("a", eval.StatusFail), res("b", eval.StatusError)})
	if tl.pass != 1 || tl.graded != 2 || tl.errors != 2 || len(tl.rows) != 2 || tl.rows[0].row != "a" || tl.rows[1].row != "b" {
		t.Fatalf("tally %+v", tl)
	}
	if b := tl.rows[1]; b.n != 0 || b.c != 0 || b.errors != 2 || b.interval() != "n/a" {
		t.Errorf("row b %+v, interval %s; want n 0, 2 errors, n/a", b, b.interval())
	}
	if got := tl.suiteInterval(); got != "[0.05, 0.95]" {
		t.Errorf("suite interval %s, want [0.05, 0.95] (one row at 1/2)", got)
	}
	if got := tallyOf([]eval.Result{res("b", eval.StatusError)}).suiteInterval(); got != "n/a" {
		t.Errorf("suite interval with no graded trial: %s, want n/a", got)
	}
	for want, c := range map[string]struct{ runs, errors int }{"": {1, 0}, " (k=3, 0 errors)": {3, 0}, " (k=1, 2 errors)": {1, 2}} {
		if got := titleSuffix(c.runs, c.errors); got != want {
			t.Errorf("titleSuffix(%d, %d) = %q, want %q", c.runs, c.errors, got, want)
		}
	}
	for want, models := range map[string][]string{"unknown": nil, "gemini-a": {"gemini-a"}, "mixed (gemini-a, gemini-b)": {"gemini-a", "gemini-b"}} {
		if got := modelName(models); got != want {
			t.Errorf("modelName(%v) = %q, want %q", models, got, want)
		}
	}
}

// TestEvalTallyFromSuite: an indicator row stays in the record's row table
// but out of the suite's interval, and each row takes its own pooled expect
// match, printed "m of n"; a row with no match prints -.
func TestEvalTallyFromSuite(t *testing.T) {
	res := func(row, status string) eval.Result { return eval.Result{Row: row, Status: status} }
	tl := tallyOf([]eval.Result{res("a", eval.StatusPass), res("ind", eval.StatusFail)})
	if got := tl.suiteInterval(); got != "[0.09, 0.91]" {
		t.Fatalf("before the kinds: %s, want [0.09, 0.91] (two rows, mean 1/2)", got)
	}
	tl.fromSuite(map[string]eval.Row{"a": {Kind: eval.KindCapability}, "ind": {Kind: eval.KindIndicator}},
		map[string]metrics.ExpectMatch{"a": {M: 1, N: 2}})
	if got := tl.suiteInterval(); got != "[0.21, 1.00]" {
		t.Errorf("the indicator row counted: %s, want [0.21, 1.00] (one row at 1)", got)
	}
	if got := tl.rows[0].expect.String(); got != "1 of 2" {
		t.Errorf("row a's expect: %s", got)
	}
	if got := tl.rows[1].expect.String(); got != "-" {
		t.Errorf("row ind's expect: %s", got)
	}
}

// TestEveryVerbSurvivesTheVerbList: an expect entry names a normalised verb,
// so every verb the binary has must survive metrics.Verb; one the allowlist
// lacks reduces to bare fyr and an expect on it can never be found.
func TestEveryVerbSurvivesTheVerbList(t *testing.T) {
	for name := range verbs {
		got := metrics.Verb("fyr " + name)
		if len(got) != 1 || got[0] != "fyr "+name {
			t.Errorf("fyr %s normalises to %q", name, got)
		}
	}
}

// TestEvalVerbRecordSuiteColumns: a long-header suite through the whole
// verb, so what recordEval reads from the suite and the store reaches the
// record: the expect match of the row's stored session (a run_command of
// git status, so 2 of 3), - for a row with no list, and an indicator row
// in the row table but out of the suite's interval and in the title's count.
func TestEvalVerbRecordSuiteColumns(t *testing.T) {
	r := newEvalRig(t, "", false)
	suite := "The suite.\n\n| id | setup | prompt | prompt2 | check | agent | grants | version | kind | cluster | expect |\n|---|---|---|---|---|---|---|---|---|---|---|\n" +
		"| `ex` |  | say ok |  | `grep -q 'agy says ok' \"$OUT\"` |  | named |  | regression |  | Bash,git status,Grep |\n" +
		"| `ind` |  | say ok |  | `false` |  | named |  | indicator |  |  |\n"
	r.mustRun(strings.NewReader(suite), "record", "add", "guide", "--id", "GUIDE-eval", "--title", "suite", "--home", r.home)
	r.mustRun(nil, "record", "export", "--home", r.home)
	r.git("add", "-A")
	r.git("commit", "-qm", "the suite")
	r.templateModel("1", "gemini-x", "C1")
	r.templateModel("2", "gemini-x", "C2")
	out, code := r.eval("ex", "ind")
	if code != 1 {
		t.Fatalf("exit %d, want 1 for the FAIL:\n%s", code, out)
	}
	m := regexp.MustCompile(`(?m)^eval: 1 of 2 PASS, 0 errors; record (TEST-\S+)$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("summary:\n%s", out)
	}
	got := r.mustRun(nil, "record", "get", m[1], "--home", r.home)
	for _, want := range []string{"Eval — 1 of 2 PASS at ", "- interval: Wilson 95%, per row below and over rows for the suite: [0.21, 1.00]\n",
		"| `ex` | 1 | 1 | 0 | [0.21, 1.00] | 2 of 3 |\n", "| `ind` | 1 | 0 | 0 | [0.00, 0.79] | - |\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("the record lacks %q:\n%s", want, got)
		}
	}
	view := r.mustRun(nil, "metrics", "evals", "--home", r.home)
	for _, want := range []string{"2 of 3", "suite: regression: 1 row, 1 graded trial; pass rate 1.00", "suite: indicator: 1 row, 1 graded trial"} {
		if !strings.Contains(view, want) {
			t.Errorf("the view lacks %q:\n%s", want, view)
		}
	}
}
