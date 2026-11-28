package metrics

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func openExpectTestStore(t *testing.T) (*Store, func(string, ...any)) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return st, exec
}

func TestExpectMatchesFilteringAndPooling(t *testing.T) {
	t.Parallel()
	st, exec := openExpectTestStore(t)

	empty, err := st.ExpectMatches(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("ExpectMatches(nil) = %v, %v; want empty map", empty, err)
	}

	exec(`INSERT INTO trials(run, label, harness, status, cause, started_at, seconds, expect) VALUES
		('run-ok', 'eval=ok', 'h1', 'pass', '', 1000, 5, 'Read,fyr record'),
		('run-fail', 'eval=fail', 'h1', 'fail', 'chk', 1000, 5, 'Grep,fyr record'),
		('run-empty-steps', 'eval=e', 'h1', 'pass', '', 1000, 5, 'Read,Edit'),
		('run-err', 'eval=err', 'h1', 'error', 'boom', 1000, 5, 'Read'),
		('run-subonly', 'eval=sub', 'h1', 'pass', '', 1000, 5, 'Read'),
		('run-nosess', 'eval=no', 'h1', 'pass', '', 1000, 5, 'Read'),
		('run-noexpect', 'eval=ne', 'h1', 'pass', '', 1000, 5, ''),
		('run-unlisted', 'eval=un', 'h1', 'pass', '', 1000, 5, 'Read')`)

	exec(`INSERT INTO sessions(id, vendor, run, parent_id, started_at) VALUES
		('m1', 'agy', 'run-ok', NULL, 1000),
		('m2', 'agy', 'run-ok', NULL, 1001),
		('m3', 'agy', 'run-fail', NULL, 1000),
		('m4', 'agy', 'run-empty-steps', NULL, 1000),
		('m5', 'agy', 'run-err', NULL, 1000),
		('s1', 'agy', 'run-subonly', 'm1', 1000),
		('m6', 'agy', 'run-noexpect', NULL, 1000),
		('m7', 'agy', 'run-unlisted', NULL, 1000)`)

	exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES
		('m1', 'e1', 10, 'tool', 'Read'),
		('m1', 'e2', 20, 'assistant', ''),
		('m2', 'e1', 10, 'tool', ''),
		('m3', 'e1', 10, 'tool', 'Bash'),
		('m5', 'e1', 10, 'tool', 'Read'),
		('s1', 'e1', 10, 'tool', 'Read'),
		('m6', 'e1', 10, 'tool', 'Read'),
		('m7', 'e1', 10, 'tool', 'Read')`)

	exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES
		('m1', 'e2', 0, 'fyr record add --title'),
		('m2', 'e1', 0, 'fyr record add --title'),
		('m3', 'e1', 0, 'fyr recorder')`)

	got, err := st.ExpectMatches([]string{
		"run-ok", "run-ok", "run-fail", "run-empty-steps",
		"run-err", "run-subonly", "run-nosess", "run-noexpect",
	})
	if err != nil {
		t.Fatalf("ExpectMatches: %v", err)
	}
	want := map[string]ExpectMatch{
		"run-ok":          {M: 2, N: 2},
		"run-fail":        {M: 0, N: 2},
		"run-empty-steps": {M: 0, N: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExpectMatches = %+v, want %+v", got, want)
	}
}

func TestExpectMatchesChunkedRuns(t *testing.T) {
	t.Parallel()
	st, exec := openExpectTestStore(t)
	total := showBatchChunk + 5
	runs := make([]string, 0, total+1)
	for i := 0; i < total; i++ {
		run := fmt.Sprintf("chunk-run-%03d", i)
		sid := fmt.Sprintf("chunk-sess-%03d", i)
		runs = append(runs, run)
		exec(`INSERT INTO trials(run, label, harness, status, cause, started_at, seconds, expect) VALUES (?, 'eval=c', 'h1', 'pass', '', 1000, 1, 'Read')`, run)
		exec(`INSERT INTO sessions(id, vendor, run, parent_id, started_at) VALUES (?, 'agy', ?, NULL, 1000)`, sid, run)
		exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES (?, 'e1', 10, 'tool', 'Read')`, sid)
	}
	runs = append(runs, runs[0])

	got, err := st.ExpectMatches(runs)
	if err != nil {
		t.Fatalf("ExpectMatches chunked: %v", err)
	}
	if len(got) != total {
		t.Fatalf("len(got) = %d, want %d", len(got), total)
	}
	for _, r := range runs {
		if got[r] != (ExpectMatch{M: 1, N: 1}) {
			t.Fatalf("got[%s] = %+v, want {1, 1}", r, got[r])
		}
	}
}
