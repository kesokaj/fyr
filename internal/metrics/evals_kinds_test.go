package metrics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// kindsStore holds a store and writes trials and tool steps into it.
type kindsStore struct {
	t *testing.T
	s *Store
	i int
}

func newKindsStore(t *testing.T) *kindsStore {
	t.Helper()
	return &kindsStore{t: t, s: openT(t, filepath.Join(t.TempDir(), "metrics.db"))}
}

// trial stores one trial of row and returns its run.
func (k *kindsStore) trial(row, kind, expect, status string) string {
	k.t.Helper()
	k.i++
	run := fmt.Sprintf("s/%s/%d", row, k.i)
	cause := map[string]string{"fail": "check", "error": "setup"}[status]
	if err := k.s.RecordTrial(Trial{Run: run, Label: "eval=" + row, Harness: "aaa1111", Status: status, Cause: cause,
		StartedAt: evalsNow.Add(-24 * time.Hour).Add(time.Duration(k.i) * time.Second), Seconds: 30, Kind: kind, Expect: expect}); err != nil {
		k.t.Fatal(err)
	}
	return run
}

// session stores a session of run; each call is an Antigravity tool name
// and, for run_command, the command line, mapped as ingest maps them.
func (k *kindsStore) session(id, row, run, parent string, calls ...[2]string) {
	k.t.Helper()
	exec := func(q string, args ...any) {
		k.t.Helper()
		if _, err := k.s.db.Exec(q, args...); err != nil {
			k.t.Fatal(err)
		}
	}
	start := evalsNow.Add(-24 * time.Hour).UnixMilli()
	exec(`INSERT INTO sessions(id, vendor, label, outcome, run, parent_id, started_at, ended_at) VALUES (?, 'agy', ?, 'pass', ?, NULLIF(?, ''), ?, ?)`,
		id, "eval="+row, run, parent, start, start+1000)
	for i, c := range calls {
		ext := fmt.Sprintf("%s-%d", id, i)
		exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES (?, ?, ?, 'tool', ?)`, id, ext, start+int64(i), agyTools[c[0]])
		for n, v := range Verb(c[1]) {
			exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES (?, ?, ?, ?)`, id, ext, n, v)
		}
	}
}

// The Phase 5b golden: `expect: Read,fyr record` on a session holding
// view_file and `fyr record get` shows 2 of 2, through the same tool map
// and verb reduction ingest uses.
func TestEvalRowsExpectGolden(t *testing.T) {
	k := newKindsStore(t)
	run := k.trial("r", "", "Read,fyr record", "pass")
	k.session("a", "r", run, "", [2]string{"view_file", ""}, [2]string{"run_command", "fyr record get PLAN-2026-10-31-x --home h"})
	e, err := k.s.Evals(EvalOptions{Now: evalsNow})
	if err != nil {
		t.Fatal(err)
	}
	var text bytes.Buffer
	e.WriteText(&text)
	if e.Rows[0].Expect == nil || e.Rows[0].Expect.String() != "2 of 2" || !strings.Contains(text.String(), "2 of 2") {
		t.Errorf("expect = %+v, view:\n%s", e.Rows[0].Expect, text.String())
	}
}

// The match, arm by arm: any order; a verb equal to the entry or led by it
// word by word, never by a prefix of a word; a tool name never matched as
// a verb's prefix of its own; each entry counted once however often used.
func TestMatchExpect(t *testing.T) {
	tools := map[string]bool{"Read": true, "agy:browser": true}
	verbs := map[string]bool{"fyr record add --title": true, "go test -race": true, "git": true}
	cases := []struct {
		expect string
		want   string
	}{
		{"Read", "1 of 1"},
		{"fyr record,Read", "2 of 2"},
		{"go test -race", "1 of 1"},
		{"git", "1 of 1"},
		{"agy:browser", "1 of 1"},
		{"fyr rec", "0 of 1"},
		{"go test -ra", "0 of 1"},
		{"Grep", "0 of 1"},
		{"read", "0 of 1"},
		{"Grep,Read,git push", "1 of 3"},
	}
	for _, c := range cases {
		if got := matchExpect(c.expect, tools, verbs).String(); got != c.want {
			t.Errorf("%q: %s, want %s", c.expect, got, c.want)
		}
	}
}

// What counts: the matches pool over a line's graded trials; two main
// sessions of one trial are one trial's use; a subagent's tools, an ERROR,
// a trial with no session and a trial with no expect count nothing; a
// trial's own stored list is read, so a list that changed under one version
// pools what each trial asked.
func TestEvalRowsExpectCounts(t *testing.T) {
	k := newKindsStore(t)
	one := k.trial("r", "", "Read,Grep", "pass")
	k.session("one", "r", one, "", [2]string{"view_file", ""})
	k.session("one-b", "r", one, "", [2]string{"grep_search", ""})
	two := k.trial("r", "", "Read,Grep", "fail")
	k.session("two", "r", two, "", [2]string{"view_file", ""})
	k.session("two-sub", "r", two, "two", [2]string{"grep_search", ""})
	three := k.trial("r", "", "Read", "pass")
	k.session("three", "r", three, "", [2]string{"view_file", ""})
	errored := k.trial("r", "", "Read", "error")
	k.session("errored", "r", errored, "", [2]string{"view_file", ""})
	k.trial("r", "", "Read", "fail") // no session
	bare := k.trial("r", "", "", "pass")
	k.session("bare", "r", bare, "", [2]string{"view_file", ""})
	k.trial("none", "", "", "pass")
	e, err := k.s.Evals(EvalOptions{Now: evalsNow})
	if err != nil {
		t.Fatal(err)
	}
	if e.Rows[0].Row != "none" || e.Rows[0].Expect != nil {
		t.Errorf("a row with no expect: %+v", e.Rows[0].Expect)
	}
	if got := e.Rows[1].Expect; got == nil || got.String() != "4 of 5" {
		t.Errorf("pooled: %+v, want 2 + 1 + 1 found of 2 + 2 + 1 asked", got)
	}
	if cells := e.Table(); cells[0][len(cells[0])-3] != "-" || cells[1][len(cells[1])-3] != "4 of 5" {
		t.Errorf("the expect cell: %q, %q", cells[0], cells[1])
	}
}

// ExpectMatches keeps to the runs asked for: a line under --harness never
// pools another key's trial.
func TestExpectMatchesRunsOnly(t *testing.T) {
	k := newKindsStore(t)
	a := k.trial("r", "", "Read", "pass")
	k.session("a", "r", a, "", [2]string{"view_file", ""})
	b := k.trial("r", "", "Read", "pass")
	k.session("b", "r", b, "", [2]string{"view_file", ""})
	got, err := k.s.ExpectMatches([]string{a})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[a] != (ExpectMatch{M: 1, N: 1}) {
		t.Errorf("%+v", got)
	}
}

// The kind-dependent suite lines: regression and adversarial rows count in
// the suite's rate and again in their own line; an indicator row is left
// out of every suite number, its errors and trajectory too, and reported
// on its own line; the newest trial names a line's kind; NULL is capability.
func TestEvalRowsKinds(t *testing.T) {
	k := newKindsStore(t)
	k.trial("cap", "", "", "pass") // NULL: stored before P8, or the default unset
	k.trial("cap", "capability", "", "fail")
	k.trial("reg", "regression", "", "pass")
	k.trial("reg", "regression", "", "pass")
	k.trial("adv", "adversarial", "", "fail")
	ind := k.trial("ind", "indicator", "", "fail")
	k.session("i1", "ind", ind, "", [2]string{"view_file", ""})
	k.session("i2", "ind", ind, "", [2]string{"view_file", ""})
	k.trial("ind", "indicator", "", "error")
	k.trial("moved", "capability", "", "pass")
	k.trial("moved", "regression", "", "pass")
	e, err := k.s.Evals(EvalOptions{Now: evalsNow})
	if err != nil {
		t.Fatal(err)
	}
	var text bytes.Buffer
	e.WriteText(&text)
	got := text.String()[strings.Index(text.String(), "suite:"):]
	want := `suite: 4 rows graded, 7 graded trials, 0 errors; pass rate 0.62 [0.22, 0.91] over rows; C_out 0.75
suite: regression: 2 rows, 4 graded trials; pass rate 1.00 [0.34, 1.00] over rows
suite: adversarial: 1 row, 1 graded trial; pass rate 0.00 [0.00, 0.79] over rows
suite: indicator: 1 row, 1 graded trial; pass rate 0.00 [0.00, 0.79] over rows, left out of the rate above
suite: trajectory similarity n/a: no row has two sessions
`
	if got != want {
		t.Errorf("suite lines:\n%s\nwant:\n%s", got, want)
	}
	kinds := map[string]string{}
	for _, r := range e.Rows {
		kinds[r.Row] = r.Kind
	}
	if fmt.Sprint(kinds) != "map[adv:adversarial cap:capability ind:indicator moved:regression reg:regression]" {
		t.Errorf("kinds: %v", kinds)
	}
	var js bytes.Buffer
	if err := e.WriteJSON(&js); err != nil {
		t.Fatal(err)
	}
	var back EvalRows
	if err := json.Unmarshal(js.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if back.Suite.Regression == nil || back.Suite.Regression.Rows != 2 || back.Suite.Indicator == nil || back.Suite.Indicator.Trials != 1 {
		t.Errorf("the JSON's kinds: %s", js.String())
	}
}

// A store with no row of a kind prints no line for it and no JSON key, and
// a kind whose rows only errored has no graded row.
func TestEvalRowsKindAbsent(t *testing.T) {
	k := newKindsStore(t)
	k.trial("cap", "", "", "pass")
	k.trial("reg", "regression", "", "error")
	e, err := k.s.Evals(EvalOptions{Now: evalsNow})
	if err != nil {
		t.Fatal(err)
	}
	if e.Suite.Regression != nil || e.Suite.Adversarial != nil || e.Suite.Indicator != nil {
		t.Errorf("%+v", e.Suite)
	}
	if ls := e.Suite.lines(); len(ls) != 2 {
		t.Errorf("lines: %q", ls)
	}
}

// A bumped version starts a new line, each with its own kind and match.
func TestEvalRowsBumpedVersion(t *testing.T) {
	k := newKindsStore(t)
	for i, v := range []int{1, 2} {
		run := fmt.Sprintf("s/r/v%d", v)
		if err := k.s.RecordTrial(Trial{Run: run, Label: "eval=r", Harness: "aaa1111", Status: "pass", RowVersion: v,
			Kind: []string{"capability", "regression"}[i], Expect: []string{"Read", "Grep"}[i], StartedAt: evalsNow.Add(-time.Hour), Seconds: 1}); err != nil {
			t.Fatal(err)
		}
		k.session(run, "r", run, "", [2]string{"view_file", ""})
	}
	e, err := k.s.Evals(EvalOptions{Now: evalsNow})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range e.Rows {
		got = append(got, fmt.Sprintf("v%d %s %s", r.Version, r.Kind, r.Expect))
	}
	if strings.Join(got, "; ") != "v1 capability 1 of 1; v2 regression 0 of 1" {
		t.Errorf("%q", got)
	}
}
