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

// TestShowBothCLIs: the Claude and Antigravity fixtures in one store give
// per-vendor numbers, and what Antigravity cannot record is null, not 0.
func TestShowBothCLIs(t *testing.T) {
	chome, _ := claudeFixture(t)
	ahome, _ := agyFixture(t)
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, s, Options{Home: chome, HomeGiven: true, Vendor: "claude"})
	ingestT(t, s, Options{Home: ahome, HomeGiven: true, Vendor: "agy"})

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
	var text bytes.Buffer
	sum.WriteText(&text)
	for _, w := range []string{"A. outcome", "B. efficiency", "C. process adherence", "D. reliability", "n/a"} {
		if !strings.Contains(text.String(), w) {
			t.Errorf("text output lacks %q", w)
		}
	}
}

// Labelled sandbox sessions are out of the default view, in with their label
// or --all-labels, and give pass^k.
func TestShowLabels(t *testing.T) {
	chome, _ := claudeFixture(t)
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, s, Options{Source: chome, Label: "arm=lean", Outcome: "pass", Vendor: "claude"})
	o := ShowOptions{Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	if sum, _ := s.Show(o); sum.Sessions["claude"] != 0 {
		t.Errorf("default view shows labelled sessions: %v", sum.Sessions)
	}
	o.Label = "arm=lean"
	sum, _ := s.Show(o)
	if sum.Sessions["claude"] != 1 || sum.A.PassK["arm=lean"] != (PassK{K: 1, Passed: 1, PassK: 1}) {
		t.Errorf("label view: sessions %v pass_k %v", sum.Sessions, sum.A.PassK)
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

// A resumed or forked Claude session replays earlier history into a new
// transcript: a tool call or a message seen under one id is counted once,
// whichever sessions repeat it (release verification of v2.6.0).
func TestShowCountsReplayedHistoryOnce(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	base := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC).UnixMilli()
	for _, q := range []string{
		`INSERT INTO sessions(id, vendor, started_at, ended_at) VALUES ('A', 'claude', ?, ?), ('B', 'claude', ?, ?)`,
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
	if sum.B.ToolCalls["claude"] != 2 || sum.B.Tokens["claude"].In != 100 {
		t.Fatalf("tool calls %d, tokens in %d; want 2 and 100 (replayed history counted once)", sum.B.ToolCalls["claude"], sum.B.Tokens["claude"].In)
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
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, label, started_at, ended_at) VALUES ('sbx', 'claude', 'arm=x', ?, ?)`, base, base+60000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, started_at, ended_at) VALUES ('real', 'claude', ?, ?)`, base+120000, base+180000); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"sbx", "real"} {
		s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES (?, 't1', ?, 'tool', 'Bash')`, sid, base+1000)
	}
	sum, err := s.Show(ShowOptions{AllLabels: true, Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Now: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if sum.B.ToolCalls["claude"] != 2 {
		t.Fatalf("tool calls %d; want 2 (one per label)", sum.B.ToolCalls["claude"])
	}
}

// A session belongs to the window it starts in, [since, until): two adjacent
// windows never count the same session, even one starting at the boundary
// or running across it (release review of v2.6.0, round 7).
func TestShowAdjacentWindowsShareNoSession(t *testing.T) {
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	day := func(d, h int) int64 { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC).UnixMilli() }
	s.db.Exec(`INSERT INTO sessions(id, vendor, started_at, ended_at) VALUES
		('across', 'claude', ?, ?), ('boundary', 'claude', ?, ?), ('early', 'claude', ?, ?)`,
		day(4, 23), day(5, 1), day(5, 0), day(5, 2), day(3, 10), day(3, 11))
	split := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	before, _ := s.Show(ShowOptions{Since: split.AddDate(0, 0, -3), Now: split})
	after, _ := s.Show(ShowOptions{Since: split, Now: split.AddDate(0, 0, 3)})
	if before.Sessions["claude"] != 2 || after.Sessions["claude"] != 1 {
		t.Fatalf("before %d, after %d; want 2 (early, across) and 1 (boundary)", before.Sessions["claude"], after.Sessions["claude"])
	}
}

// The context target (plan 2026-10-23-session-sized-work, Phase 2): each main
// session's peak context, the count of those past the target (300,000 tokens
// unless set), both per vendor; a subagent's peak is its parent's time and is
// left out; replayed history counts under the session that first had it; a
// vendor with no main session carrying a context value in the window is null
// with a note, never 0; n in "k of n" is the sessions that have a peak.
func TestShowContextTarget(t *testing.T) {
	ahome, _ := agyFixture(t) // C1: main, peak 42000; C2: a subagent
	s := openT(t, filepath.Join(t.TempDir(), "metrics.db"))
	ingestT(t, s, Options{Home: ahome, HomeGiven: true, Vendor: "agy"})
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
