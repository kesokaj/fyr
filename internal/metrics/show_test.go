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

// TestShowHistoricalColumn: an Antigravity ingest and the rows an older
// ingest left from Claude Code give a number per column, the historical one
// second; what Antigravity cannot record is null, not 0 (plan fyr D7).
func TestShowHistoricalColumn(t *testing.T) {
	ahome, root := agyFixture(t)
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	historicalRows(t, s, root)
	ingestT(t, s, Options{Home: ahome})

	sum, err := s.Show(ShowOptions{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := sum.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var j map[string]any
	if err := json.Unmarshal(buf.Bytes(), &j); err != nil {
		t.Fatal(err)
	}
	get := func(path string) any {
		var v any = j
		for _, k := range strings.Split(path, ".") {
			m, ok := v.(map[string]any)
			if !ok {
				return "<missing>"
			}
			v = m[k]
		}
		return v
	}
	want := map[string]any{
		"sessions.claude":                       1.0,
		"sessions.agy":                          1.0,
		"subagents.claude.verifier":             1.0,
		"subagents.agy.explorer":                1.0,
		"B.tool_calls.claude":                   5.0, // 4 main + 1 subagent
		"B.tool_calls.agy":                      4.0,
		"B.tool_calls_by_tool.claude.Bash":      3.0,
		"B.tool_calls_by_tool.agy.Bash":         1.0,
		"B.cost_usd.claude":                     0.5,
		"B.cost_usd.agy":                        nil,
		"B.compactions.claude":                  1.0,
		"B.compactions.agy":                     nil,
		"B.peak_context.agy":                    42000.0,
		"C.skills.claude.feature":               1.0,
		"C.skills.agy.golang":                   1.0,
		"C.forbidden.commit chained to test":    1.0,
		"C.first_reads.handoff":                 1.0,
		"C.first_reads.plan":                    1.0,
		"D.denials.claude":                      1.0,
		"D.denials.agy":                         nil,
		"D.interrupts.claude":                   1.0,
		"D.subagent_tool_calls.claude.verifier": 1.0,
	}
	for k, w := range want {
		if g := get(k); g != w {
			t.Errorf("%s = %v, want %v", k, g, w)
		}
	}
	for _, v := range []string{"claude", "agy"} {
		if sum.B.ActiveMin[v] > sum.B.WallMin[v]+1e-9 {
			t.Errorf("%s active %.3f min > wall %.3f min: both must count main sessions only", v, sum.B.ActiveMin[v], sum.B.WallMin[v])
		}
	}
	if sum.C.Forbidden["git add ."] != 1 { // a key with a dot: read directly
		t.Errorf("forbidden git add . = %d, want 1", sum.C.Forbidden["git add ."])
	}
	if r := sum.D.ToolErrorRate["agy"]; r == nil || *r != 0.25 {
		t.Errorf("agy tool error rate = %v, want 0.25", r)
	}
	if !strings.Contains(strings.Join(sum.Notes, "\n"), "agy: cost is not exposed") {
		t.Errorf("notes do not name agy's missing cost: %q", sum.Notes)
	}
	if strings.Join(sum.Columns, ",") != "agy,claude" {
		t.Errorf("columns %v, want agy then the historical claude", sum.Columns)
	}
	var text bytes.Buffer
	sum.WriteText(&text)
	for _, w := range []string{"A. outcome", "B. efficiency", "C. process adherence", "D. reliability", "n/a", "claude (historical)"} {
		if !strings.Contains(text.String(), w) {
			t.Errorf("text output lacks %q", w)
		}
	}
	if i, j := strings.Index(text.String(), " agy"), strings.Index(text.String(), "claude (historical)"); i < 0 || j < i {
		t.Errorf("the agy column does not come first:\n%s", text.String())
	}
}

// TestShowAgyOnly: a store with no historical rows has the one agy column;
// no claude key reaches the JSON and no claude column the text.
func TestShowAgyOnly(t *testing.T) {
	ahome, _ := agyFixture(t)
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, s, Options{Home: ahome})
	sum, err := s.Show(ShowOptions{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sum.Columns, ",") != "agy" {
		t.Errorf("columns %v, want agy alone", sum.Columns)
	}
	var j, text bytes.Buffer
	if err := sum.WriteJSON(&j); err != nil {
		t.Fatal(err)
	}
	sum.WriteText(&text)
	if strings.Contains(j.String(), `"claude"`) || strings.Contains(text.String(), "claude") {
		t.Errorf("an agy-only store shows a claude column:\n%s\n%s", j.String(), text.String())
	}
	if sum.Sessions["agy"] != 1 || sum.B.ToolCalls["agy"] != 4 {
		t.Errorf("sessions %v, tool calls %v; want 1 and 4", sum.Sessions, sum.B.ToolCalls)
	}
	for k := range sum.Numbers() {
		if strings.HasSuffix(k, ".claude") {
			t.Errorf("Numbers carries %s for an agy-only store", k)
		}
	}
}

// Labelled sandbox sessions are out of the default view, in with their label
// or --all-labels, and give pass^k.
func TestShowLabels(t *testing.T) {
	ahome, _ := agyFixture(t)
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, s, Options{Source: ahome, Label: "arm=lean", Outcome: "pass"})
	o := ShowOptions{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	if sum, _ := s.Show(o); sum.Sessions["agy"] != 0 {
		t.Errorf("default view shows labelled sessions: %v", sum.Sessions)
	}
	o.Label = "arm=lean"
	sum, _ := s.Show(o)
	p := sum.A.PassK["arm=lean"]
	if sum.Sessions["agy"] != 1 || p.K != 1 || p.Passed != 1 || p.PassK != 1 {
		t.Errorf("label view: sessions %v pass_k %v", sum.Sessions, sum.A.PassK)
	}
	// A store with no trials row: the session is its own graded trial.
	if p.N != 1 || p.C != 1 || p.Errors != 0 || p.Flip != 0 || p.Wilson95 != [2]float64{0.2065, 1} || p.PassAt["1"] != 1 || len(p.PassAt) != 1 || p.PassPow["1"] != 1 {
		t.Errorf("label view: pass_k %+v, want n 1, c 1, wilson95 [0.2065, 1], pass@1 and pass^1 = 1 alone", p)
	}
}

// A6 over trials (the eval-metrics plan D2, D13): a trial counts once
// however many sessions share its run, an error trial is counted apart and
// never graded, a session no trial claims is a graded trial of its own,
// and the rates come with pass@k, pass^k up to n, the Wilson interval and
// flip, each at four decimals; the text line and the JSON carry them.
func TestShowPassKTrials(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	base := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC).UnixMilli()
	for _, q := range []string{
		// Label eval=two: trial 1 passed with two sessions (a prompt2 row); trial 2 is an error with no session.
		`INSERT INTO trials(run, label, harness, status, cause, started_at, seconds) VALUES
			('s/two/1', 'eval=two', 'abc1234', 'pass', '', ` + fmt.Sprint(base) + `, 60),
			('s/two/2', 'eval=two', 'abc1234', 'error', 'setup', ` + fmt.Sprint(base+1000) + `, 5)`,
		`INSERT INTO sessions(id, vendor, label, outcome, run, started_at, ended_at) VALUES
			('A', 'agy', 'eval=two', 'pass', 's/two/1', ` + fmt.Sprint(base+100) + `, ` + fmt.Sprint(base+200) + `),
			('B', 'agy', 'eval=two', 'pass', 's/two/1', ` + fmt.Sprint(base+300) + `, ` + fmt.Sprint(base+400) + `)`,
		// Label eval=old: sessions an older ingest stored, no trials row: two graded trials, one passed.
		`INSERT INTO sessions(id, vendor, label, outcome, started_at, ended_at) VALUES
			('O1', 'agy', 'eval=old', 'pass', ` + fmt.Sprint(base+500) + `, ` + fmt.Sprint(base+600) + `),
			('O2', 'agy', 'eval=old', 'fail', ` + fmt.Sprint(base+700) + `, ` + fmt.Sprint(base+800) + `)`,
		// Label eval=six: six passed trials, so pass@5 and pass^5 exist.
		`INSERT INTO trials(run, label, harness, status, cause, started_at, seconds) VALUES
			('s/six/1', 'eval=six', 'abc1234', 'pass', '', ` + fmt.Sprint(base) + `, 1), ('s/six/2', 'eval=six', 'abc1234', 'pass', '', ` + fmt.Sprint(base) + `, 1),
			('s/six/3', 'eval=six', 'abc1234', 'pass', '', ` + fmt.Sprint(base) + `, 1), ('s/six/4', 'eval=six', 'abc1234', 'pass', '', ` + fmt.Sprint(base) + `, 1),
			('s/six/5', 'eval=six', 'abc1234', 'pass', '', ` + fmt.Sprint(base) + `, 1), ('s/six/6', 'eval=six', 'abc1234', 'fail', 'check', ` + fmt.Sprint(base) + `, 1)`,
		// Label eval=errs: error trials only, nothing graded; eval=none: one failed trial, no flip.
		`INSERT INTO trials(run, label, harness, status, cause, started_at, seconds) VALUES
			('s/errs/1', 'eval=errs', 'abc1234', 'error', 'clone', ` + fmt.Sprint(base) + `, 1),
			('s/none/1', 'eval=none', 'abc1234', 'fail', 'check', ` + fmt.Sprint(base) + `, 1)`,
		// Label eval=lost: a session whose run names no trial (the trial's write failed): its own graded trial.
		`INSERT INTO sessions(id, vendor, label, outcome, run, started_at, ended_at) VALUES
			('L', 'agy', 'eval=lost', 'pass', 's/lost/1', ` + fmt.Sprint(base+900) + `, ` + fmt.Sprint(base+950) + `)`,
		// A trial outside the window, and a subagent session with an outcome: neither counts.
		`INSERT INTO trials(run, label, harness, status, cause, started_at, seconds) VALUES ('s/two/9', 'eval=two', 'abc1234', 'fail', 'check', 1, 1)`,
		`INSERT INTO sessions(id, vendor, label, outcome, parent_id, started_at, ended_at) VALUES ('sub', 'agy', 'eval=old', 'fail', 'O1', ` + fmt.Sprint(base+500) + `, ` + fmt.Sprint(base+600) + `)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	window := ShowOptions{AllLabels: true, Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	sum, err := s.Show(window)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]PassK{
		"eval=two":  {K: 1, Passed: 1, PassK: 1, N: 1, C: 1, Errors: 1, PassAt: map[string]float64{"1": 1}, PassPow: map[string]float64{"1": 1}, Wilson95: [2]float64{0.2065, 1}},
		"eval=old":  {K: 2, Passed: 1, N: 2, C: 1, PassAt: map[string]float64{"1": 0.5}, PassPow: map[string]float64{"1": 0.5}, Wilson95: [2]float64{0.0945, 0.9055}, Flip: 1},
		"eval=six":  {K: 6, Passed: 5, N: 6, C: 5, PassAt: map[string]float64{"1": 0.8333, "3": 1, "5": 1}, PassPow: map[string]float64{"1": 0.8333, "3": 0.5, "5": 0.1667}, Wilson95: [2]float64{0.4365, 0.9699}, Flip: 1},
		"eval=errs": {Errors: 1},
		"eval=none": {K: 1, N: 1, PassAt: map[string]float64{"1": 0}, PassPow: map[string]float64{"1": 0}, Wilson95: [2]float64{0, 0.7935}},
		"eval=lost": {K: 1, Passed: 1, PassK: 1, N: 1, C: 1, PassAt: map[string]float64{"1": 1}, PassPow: map[string]float64{"1": 1}, Wilson95: [2]float64{0.2065, 1}},
	}
	if len(sum.A.PassK) != len(want) {
		t.Errorf("labels %v, want %d", sortedKeys(sum.A.PassK), len(want))
	}
	for l, w := range want {
		if got := sum.A.PassK[l]; fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", w) {
			t.Errorf("%s:\n got %+v\nwant %+v", l, got, w)
		}
	}
	// A non-number anywhere fails the encoder, which writes nothing: the
	// error is the check.
	var j bytes.Buffer
	if err := sum.WriteJSON(&j); err != nil {
		t.Fatalf("show --json: %v", err)
	}
	for _, w := range []string{`"n": 1`, `"c": 1`, `"errors": 1`, `"pass_at": {`, `"5": 0.1667`, `"wilson95": [`, `0.2065,`, `"flip": 1`} {
		if !strings.Contains(j.String(), w) {
			t.Errorf("show --json lacks %s:\n%s", w, j.String())
		}
	}
	var text bytes.Buffer
	sum.WriteText(&text)
	if !strings.Contains(text.String(), "pass^k eval=two: 1 of 1 passed, pass^1 = 1; n 1, c 1, errors 1, wilson95 [0.2065, 1.0000], flip 0") {
		t.Errorf("text lacks the trial line:\n%s", text.String())
	}
	// The default view (no label) and --project show no trial: a trial carries a label and belongs to no project root.
	if plain, _ := s.Show(ShowOptions{Since: window.Since, Now: window.Now}); len(plain.A.PassK) != 0 {
		t.Errorf("the default view shows pass_k %v", plain.A.PassK)
	}
	if proj, _ := s.Show(ShowOptions{AllLabels: true, Project: "/nowhere", Since: window.Since, Now: window.Now}); len(proj.A.PassK) != 0 {
		t.Errorf("--project shows pass_k %v", proj.A.PassK)
	}
	// --label keeps one label's trials.
	one, _ := s.Show(ShowOptions{Label: "eval=six", Since: window.Since, Now: window.Now})
	if len(one.A.PassK) != 1 || one.A.PassK["eval=six"].N != 6 {
		t.Errorf("--label eval=six: pass_k %v", one.A.PassK)
	}
}

// Wall and active time count main sessions only: a subagent's steps run
// inside its parent's wall time and would be counted twice (spec B2).
func TestShowActiveTimeMainOnly(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	base := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC).UnixMilli()
	min := int64(60000)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO sessions(id, vendor, started_at, ended_at) VALUES ('M', 'agy', ?, ?)`, []any{base, base + 2*min}},
		{`INSERT INTO sessions(id, vendor, parent_id, agent_type, started_at, ended_at) VALUES ('S', 'agy', 'M', 'explorer', ?, ?)`, []any{base, base + 4*min}},
		{`INSERT INTO steps(session_id, ext_id, ts, kind) VALUES ('M', 'm1', ?, 'assistant'), ('M', 'm2', ?, 'assistant')`, []any{base, base + 2*min}},
		{`INSERT INTO steps(session_id, ext_id, ts, kind) VALUES ('S', 's1', ?, 'assistant'), ('S', 's2', ?, 'assistant')`, []any{base, base + 4*min}},
	} {
		if _, err := s.db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := s.Show(ShowOptions{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if sum.B.WallMin["agy"] != 2 || sum.B.ActiveMin["agy"] != 2 {
		t.Fatalf("wall %.1f active %.1f, want 2 and 2", sum.B.WallMin["agy"], sum.B.ActiveMin["agy"])
	}
}

// A resumed or forked session replays earlier history into a new
// transcript: a tool call or a message seen under one id is counted once,
// whichever sessions repeat it (release verification of v2.6.0).
func TestShowCountsReplayedHistoryOnce(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	base := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC).UnixMilli()
	for _, q := range []string{
		`INSERT INTO sessions(id, vendor, started_at, ended_at) VALUES ('A', 'agy', ?, ?), ('B', 'agy', ?, ?)`,
	} {
		if _, err := s.db.Exec(q, base, base+60000, base+120000, base+180000); err != nil {
			t.Fatal(err)
		}
	}
	for _, sid := range []string{"A", "B"} { // B replays A's history
		s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES (?, 't1', ?, 'tool', 'Bash')`, sid, base+1000)
		s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tok_in, tok_out) VALUES (?, 'msg:m1', ?, 'assistant', 100, 10)`, sid, base+1000)
	}
	s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('B', 't2', ?, 'tool', 'Read')`, base+130000)
	sum, err := s.Show(ShowOptions{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if sum.B.ToolCalls["agy"] != 2 || sum.B.Tokens["agy"].In != 100 {
		t.Fatalf("tool calls %d, tokens in %d; want 2 and 100 (replayed history counted once)", sum.B.ToolCalls["agy"], sum.B.Tokens["agy"].In)
	}
}

// Replayed history is deduplicated within one label only: a sandbox
// transcript that reuses a real session's ids never hides the real session's
// steps under --all-labels (security review 6118921..c478d86, finding 3).
func TestShowReplayDedupeStaysWithinALabel(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	base := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC).UnixMilli()
	// The sandbox session starts first, so without the label in the key it
	// would claim the shared ids and the real session's steps would vanish.
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, label, started_at, ended_at) VALUES ('sbx', 'agy', 'arm=x', ?, ?)`, base, base+60000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, started_at, ended_at) VALUES ('real', 'agy', ?, ?)`, base+120000, base+180000); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"sbx", "real"} {
		s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES (?, 't1', ?, 'tool', 'Bash')`, sid, base+1000)
	}
	sum, err := s.Show(ShowOptions{AllLabels: true, Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if sum.B.ToolCalls["agy"] != 2 {
		t.Fatalf("tool calls %d; want 2 (one per label)", sum.B.ToolCalls["agy"])
	}
}

// A session belongs to the window it starts in, [since, until): two adjacent
// windows never count the same session, even one starting at the boundary
// or running across it (release review of v2.6.0, round 7).
func TestShowAdjacentWindowsShareNoSession(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	day := func(d, h int) int64 { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC).UnixMilli() }
	s.db.Exec(`INSERT INTO sessions(id, vendor, started_at, ended_at) VALUES
		('across', 'agy', ?, ?), ('boundary', 'agy', ?, ?), ('early', 'agy', ?, ?)`,
		day(4, 23), day(5, 1), day(5, 0), day(5, 2), day(3, 10), day(3, 11))
	split := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	before, _ := s.Show(ShowOptions{Since: split.AddDate(0, 0, -3), Now: split})
	after, _ := s.Show(ShowOptions{Since: split, Now: split.AddDate(0, 0, 3)})
	if before.Sessions["agy"] != 2 || after.Sessions["agy"] != 1 {
		t.Fatalf("before %d, after %d; want 2 (early, across) and 1 (boundary)", before.Sessions["agy"], after.Sessions["agy"])
	}
}

// The context target (plan 2026-10-23-session-sized-work, Phase 2): each main
// session's peak context, the count of those past the target (300,000 tokens
// unless set), both per vendor; a subagent's peak is its parent's time and is
// left out; replayed history counts under the session that first had it; a
// vendor with no main session carrying a context value in the window is null
// with a note, never 0; n in "k of n" is the sessions that have a peak.
// The claude rows are historical: their context is computed from usage at
// read time, the path only such rows take (plan fyr D7).
func TestShowContextTarget(t *testing.T) {
	ahome, _ := agyFixture(t) // C1: main, peak 42000; C2: a subagent
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, s, Options{Home: ahome})
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC).UnixMilli()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO sessions(id, vendor, started_at, ended_at) VALUES ('A', 'claude', ?, ?), ('B', 'claude', ?, ?), ('N', 'claude', ?, ?)`,
			[]any{base, base + 60000, base + 120000, base + 180000, base + 240000, base + 300000}},
		{`INSERT INTO sessions(id, vendor, parent_id, agent_type, started_at, ended_at) VALUES ('S', 'claude', 'A', 'reviewer', ?, ?)`, []any{base, base + 60000}},
		// A's one message: 300000 in + 50000 cache read = 350000, over the target.
		{`INSERT INTO steps(session_id, ext_id, ts, kind, tok_in, cache_read) VALUES ('A', 'msg:m1', ?, 'assistant', 300000, 50000)`, []any{base + 1000}},
		// B replays A's m1 (a fork) and adds one message of its own at 1000.
		{`INSERT INTO steps(session_id, ext_id, ts, kind, tok_in, cache_read) VALUES ('B', 'msg:m1', ?, 'assistant', 300000, 50000), ('B', 'msg:m2', ?, 'assistant', 1000, 0)`, []any{base + 1000, base + 130000}},
		// The subagent's context is the largest of all and must not appear.
		{`INSERT INTO steps(session_id, ext_id, ts, kind, tok_in) VALUES ('S', 'msg:m9', ?, 'assistant', 900000)`, []any{base + 2000}},
		// N has no model call: no peak, not in n.
		{`INSERT INTO steps(session_id, ext_id, ts, kind) VALUES ('N', 'u1', ?, 'user')`, []any{base + 241000}},
	} {
		if _, err := s.db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	window := ShowOptions{Since: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	sum, err := s.Show(window)
	if err != nil {
		t.Fatal(err)
	}
	if sum.B.ContextTarget != 300000 {
		t.Errorf("default context target %d, want 300000", sum.B.ContextTarget)
	}
	wantPeaks := map[string][]SessionPeak{
		"claude": {{Session: "A", Date: "2026-10-01", Peak: 350000}, {Session: "B", Date: "2026-10-01", Peak: 1000}},
		"agy":    {{Session: "C1", Date: "2026-10-07", Peak: 42000}},
	}
	for v, want := range wantPeaks {
		if got := sum.B.SessionPeaks[v]; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s session peaks %v, want %v", v, got, want)
		}
	}
	if p := sum.B.SessionsOverContextTarget["claude"]; p == nil || *p != 1 {
		t.Errorf("claude over target = %v, want 1", p)
	}
	if p := sum.B.SessionsOverContextTarget["agy"]; p == nil || *p != 0 {
		t.Errorf("agy over target = %v, want 0 (a number: Antigravity records context)", p)
	}
	var buf bytes.Buffer
	if err := sum.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"context_target": 300000`, `"sessions_over_context_target": {`, `"session_peaks": {`, `"session": "A"`, `"peak": 350000`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("show --json lacks %s", want)
		}
	}
	var text bytes.Buffer
	sum.WriteText(&text)
	if !strings.Contains(text.String(), "over context target 300000") || !strings.Contains(text.String(), "1 of 2") {
		t.Errorf("text table lacks the over-target row as k of n:\n%s", text.String())
	}

	// A lower target moves both counts; the target is reported back.
	window.ContextTarget = 40000
	low, err := s.Show(window)
	if err != nil {
		t.Fatal(err)
	}
	if low.B.ContextTarget != 40000 || *low.B.SessionsOverContextTarget["claude"] != 1 || *low.B.SessionsOverContextTarget["agy"] != 1 {
		t.Errorf("target 40000: %d, claude %v, agy %v; want 40000, 1, 1", low.B.ContextTarget, *low.B.SessionsOverContextTarget["claude"], *low.B.SessionsOverContextTarget["agy"])
	}

	// A window with no Antigravity session: null, named in notes, never 0.
	only, err := s.Show(ShowOptions{Since: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := only.B.SessionsOverContextTarget["agy"]; !ok || p != nil {
		t.Errorf("agy over target with no agy session = %v (present %v), want a null entry", p, ok)
	}
	if !strings.Contains(strings.Join(only.Notes, "\n"), "agy: no main session in the window carries a context value") {
		t.Errorf("notes do not name agy's missing context value: %q", only.Notes)
	}
	if _, ok := only.B.SessionPeaks["agy"]; ok {
		t.Errorf("agy has session peaks in a window without agy sessions: %v", only.B.SessionPeaks["agy"])
	}
}
