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

// evalsNow is the fixture's clock: the default window is the 30 days
// before it.
var evalsNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// evalsFixture is a store holding one row of each flag and the arms around
// them (TestEvalRows names each):
//   - sat: three passes under each of two keys, one session each, a
//     subagent beside one of them;
//   - broke: two fails and one error trial under one key;
//   - flaky: two passes in the window and a fail before it, all under one
//     key, and a pass under a second key;
//   - old: two sessions stored before migration 4 (run NULL), both passed.
func evalsFixture(t *testing.T) *Store {
	t.Helper()
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	day := evalsNow.Add(-24 * time.Hour)
	trial := func(row string, i int, harness, status string, at time.Time) string {
		t.Helper()
		run := fmt.Sprintf("2026-10-30-100000/%s/%d", row, i)
		cause := map[string]string{"pass": "", "fail": "check", "error": "setup"}[status]
		if err := s.RecordTrial(Trial{Run: run, Label: "eval=" + row, Harness: harness, Status: status, Cause: cause,
			StartedAt: at, Seconds: 30}); err != nil {
			t.Fatal(err)
		}
		return run
	}
	session := func(id, label, outcome, run, parent string, at time.Time, tools ...string) {
		t.Helper()
		start := at.UnixMilli()
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := s.db.Exec(q, args...); err != nil {
				t.Fatal(err)
			}
		}
		exec(`INSERT INTO sessions(id, vendor, label, outcome, run, parent_id, started_at, ended_at) VALUES (?, 'agy', ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)`,
			id, label, outcome, run, parent, start, start+20000)
		exec(`INSERT INTO steps(session_id, ext_id, ts, kind) VALUES (?, ?, ?, 'user')`, id, id+"-u", start)
		exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tok_in, tok_out, cache_read, cache_write, context) VALUES (?, ?, ?, 'generation', 100, 10, 40, 10, 2000)`, id, id+"-g", start+1000)
		for i, tool := range tools {
			exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, is_error, cmd_hash) VALUES (?, ?, ?, 'tool', ?, ?, ?)`,
				id, fmt.Sprintf("%s-t%d", id, i), start+int64(2000+1000*i), strings.TrimSuffix(tool, "!"), strings.HasSuffix(tool, "!"), map[bool]string{true: "h"}[strings.HasPrefix(tool, "Bash")])
		}
	}
	for i := 1; i <= 6; i++ {
		key := "aaa1111"
		if i > 3 {
			key = "bbb2222"
		}
		run := trial("sat", i, key, "pass", day)
		tools := []string{"Read", "Bash", "Bash!"}
		if i == 6 {
			tools = []string{"Edit", "Bash", "Bash!"}
		}
		session(fmt.Sprintf("sat-%d", i), "eval=sat", "pass", run, "", day, tools...)
	}
	session("sat-sub", "eval=sat", "pass", "2026-10-30-100000/sat/1", "sat-1", day, "Read", "Read", "Read")
	for i := 1; i <= 2; i++ {
		run := trial("broke", i, "aaa1111", "fail", day)
		session(fmt.Sprintf("broke-%d", i), "eval=broke", "fail", run, "", day, "Read")
	}
	trial("broke", 3, "aaa1111", "error", day)
	trial("flaky", 1, "aaa1111", "fail", evalsNow.AddDate(0, 0, -40))
	trial("flaky", 2, "aaa1111", "pass", day)
	trial("flaky", 3, "aaa1111", "pass", day)
	trial("flaky", 4, "bbb2222", "pass", day)
	session("old-1", "eval=old", "pass", "", "", day, "Read")
	session("old-2", "eval=old", "pass", "", "", day, "Read", "Grep")
	// old's mean output is (10 + 10 + 1) / 2 = 10.5 tokens: shown whole.
	if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tok_out) VALUES ('old-2', 'old-2-g2', ?, 'generation', 1)`, day.UnixMilli()+500); err != nil {
		t.Fatal(err)
	}
	// A real session and a kit's label are not suite rows.
	session("real", "", "", "", "", day, "Read")
	session("kit", "kit=x", "pass", "", "", day, "Read")
	return s
}

// TestEvalRows pins the view over the fixture: every flag from the whole
// history, n/a where a rate cannot be had, the means over main sessions
// only, the suite lines, and no NaN in the JSON.
func TestEvalRows(t *testing.T) {
	s := evalsFixture(t)
	e, err := s.Evals(EvalOptions{Now: evalsNow})
	if err != nil {
		t.Fatal(err)
	}
	var text bytes.Buffer
	e.WriteText(&text)
	want := `evals 2026-10-01 to 2026-10-31
row    v  n  c  errors  pass@1  pass^3  wilson95      flip  turns  tool calls  tokens in/out/cache  session s  active s  tool err %  retries  peak ctx  last        flags
broke  1  2  0  1       0.00    n/a     [0.00, 0.66]  0     1.0    1.0         100/10/50            20.0       2.0       0.0         0.0      2k        2026-10-30  broken
flaky  1  3  3  0       1.00    1.00    [0.44, 1.00]  0     -      -           -                    -          -         -           -        -         2026-10-30  unstable
old    1  2  2  0       1.00    n/a     [0.34, 1.00]  0     1.0    1.5         100/11/50            20.0       2.5       0.0         0.0      2k        2026-10-30  history
sat    1  6  6  0       1.00    1.00    [0.61, 1.00]  0     1.0    3.0         100/10/50            20.0       4.0       33.3        1.0      2k        2026-10-30  saturated
suite: 4 rows graded, 13 graded trials, 1 error; pass rate 0.75 [0.30, 0.95] over rows; C_out 1.00
suite: trajectory similarity 0.80 over 3 rows with two or more sessions
`
	if text.String() != want {
		t.Errorf("the view:\n%s\nwant:\n%s", text.String(), want)
	}
	var js bytes.Buffer
	if err := e.WriteJSON(&js); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(js.String(), "NaN") || strings.Contains(js.String(), "Inf") {
		t.Errorf("a value that is not a number:\n%s", js.String())
	}
	var back EvalRows
	if err := json.Unmarshal(js.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	broke := back.Rows[0]
	if broke.Row != "broke" || broke.N != 2 || broke.Errors != 1 || broke.PassPow3 != nil || broke.Means == nil || broke.Means.Sessions != 2 {
		t.Errorf("broke: %+v", broke)
	}
	sat := back.Rows[3]
	if sat.Means == nil || sat.Means.Sessions != 6 || sat.Means.ToolErrorRate == nil || !near(*sat.Means.ToolErrorRate, 0.3333) {
		t.Errorf("sat's means count its main sessions only: %+v", sat.Means)
	}
}

// --harness keeps the trials of one key exactly; a session stored before
// migration 4 is under harness unknown; the flags still read every key.
func TestEvalRowsHarness(t *testing.T) {
	s := evalsFixture(t)
	e, err := s.Evals(EvalOptions{Now: evalsNow, Harness: "bbb2222"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range e.Rows {
		got = append(got, fmt.Sprintf("%s %d/%d %v", r.Row, r.C, r.N, r.Flags))
	}
	if strings.Join(got, "; ") != "flaky 1/1 [unstable]; sat 3/3 [saturated]" {
		t.Errorf("--harness bbb2222: %q", got)
	}
	e, err = s.Evals(EvalOptions{Now: evalsNow, Harness: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Rows) != 1 || e.Rows[0].Row != "old" {
		t.Errorf("--harness unknown: %+v", e.Rows)
	}
	if e, err = s.Evals(EvalOptions{Now: evalsNow, Harness: "aaa111"}); err != nil || len(e.Rows) != 0 {
		t.Errorf("a key's prefix matched: %+v, %v", e, err)
	}
}

// The flag rules at their edges (PLAN.md D16): one key with three passes is
// not saturated, two keys with a third holding a fail are not either, and
// a row whose only graded trials are under harness unknown is history
// even when an error trial has a known key.
func TestHistoryFlags(t *testing.T) {
	k := rowKey{label: "eval=r"}
	tr := func(harness, status string) evalTrial { return evalTrial{key: k, harness: harness, status: status} }
	cases := []struct {
		name   string
		trials []evalTrial
		want   string
	}{
		{"one key, three passes", []evalTrial{tr("a", "pass"), tr("a", "pass"), tr("a", "pass")}, ""},
		{"two keys, three passes each", []evalTrial{tr("a", "pass"), tr("a", "pass"), tr("a", "pass"), tr("b", "pass"), tr("b", "pass"), tr("b", "pass")}, "saturated"},
		{"two keys at three, a third key failed", []evalTrial{tr("a", "pass"), tr("a", "pass"), tr("a", "pass"), tr("b", "pass"), tr("b", "pass"), tr("b", "pass"), tr("c", "fail")}, ""},
		{"one fail", []evalTrial{tr("a", "fail")}, "broken"},
		{"unknown only, an error under a key", []evalTrial{tr("unknown", "pass"), tr("a", "error")}, "history"},
		{"errors only", []evalTrial{tr("a", "error")}, ""},
		{"one key both ways", []evalTrial{tr("a", "pass"), tr("a", "fail"), tr("b", "fail")}, "unstable"},
	}
	for _, c := range cases {
		got := strings.Join(historyFlags(c.trials)[k], ",")
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A store with no suite trial prints the window and one line, and its JSON
// has an empty list of rows, not null.
func TestEvalRowsEmpty(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	e, err := s.Evals(EvalOptions{Now: evalsNow})
	if err != nil {
		t.Fatal(err)
	}
	var text, js bytes.Buffer
	e.WriteText(&text)
	if text.String() != "evals 2026-10-01 to 2026-10-31\nno eval trial in the window: fyr eval writes them\n" {
		t.Errorf("text: %q", text.String())
	}
	if err := e.WriteJSON(&js); err != nil || !strings.Contains(js.String(), `"rows": []`) {
		t.Errorf("json: %v\n%s", err, js.String())
	}
}

// TestEvalRowsVersionDefault: a trial or session stored before the suite's
// version column (NULL) reads as version 1, so a row's history does not
// split when fyr eval starts writing the default (ADR-085); another
// version is its own line.
func TestEvalRowsVersionDefault(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	day := evalsNow.Add(-24 * time.Hour)
	for i, v := range []int{0, 1, 2} {
		if err := s.RecordTrial(Trial{Run: fmt.Sprintf("s/r/%d", i+1), Label: "eval=r", Harness: "aaa1111", Status: "pass",
			StartedAt: day, Seconds: 30, RowVersion: v}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, label, outcome, started_at, ended_at) VALUES ('old', 'agy', 'eval=r', 'pass', ?, ?)`,
		day.UnixMilli(), day.UnixMilli()+1000); err != nil {
		t.Fatal(err)
	}
	e, err := s.Evals(EvalOptions{Now: evalsNow})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range e.Rows {
		got = append(got, fmt.Sprintf("%s v%d n%d", r.Row, r.Version, r.N))
	}
	if strings.Join(got, "; ") != "r v1 n3; r v2 n1" {
		t.Errorf("lines = %q, want r v1 n3; r v2 n1", got)
	}
}
