package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ShowOptions filter what show and report read (spec § Verbs).
type ShowOptions struct {
	Since     time.Time // zero: 30 days before Now
	Now       time.Time // zero: time.Now()
	Project   string
	Label     string
	AllLabels bool
	// ContextTarget is the peak context, in tokens, a main session should
	// stay under; 0 means DefaultContextTarget (plan session-sized-work D4).
	ContextTarget int64
}

// DefaultContextTarget is the context target when none is given (plan
// session-sized-work D4). It was the auto-compact window the installer wrote
// into Claude Code's settings until ADR-062; the number stays.
const DefaultContextTarget = 300000

// activeGap is the longest pause still counted as active time (spec B2).
const activeGap = 5 * time.Minute

// historical is the column of the rows an ingest before ADR-062 read from
// Claude Code: kept in the store and shown only when the window holds them
// (plan fyr D7).
const historical = "claude"

// Summary is `show --json` (spec § Output). A metric the CLI cannot produce
// is nil for its column and named in Notes, never 0.
type Summary struct {
	Window map[string]string `json:"window"`
	// Columns are the summary's columns: "agy", then "claude" only when the
	// window holds historical rows.
	Columns   []string                  `json:"columns"`
	Sessions  map[string]int            `json:"sessions"`
	Subagents map[string]map[string]int `json:"subagents"`
	A         Outcome                   `json:"A"`
	B         Efficiency                `json:"B"`
	C         Adherence                 `json:"C"`
	D         Reliability               `json:"D"`
	Daily     []Day                     `json:"daily"`
	Notes     []string                  `json:"notes"`

	accs map[string]*acc
	// seen holds the vendor and id of every tool call and message already
	// counted: a resumed or forked session replays earlier history into a
	// new transcript, and the replay is not new work.
	seen map[string]bool
}

type Outcome struct {
	DoDClosed      map[string]Closed `json:"dod_closed"`
	Verifier       map[string]int    `json:"verifier"`
	ReviewFindings map[string]int    `json:"review_findings"`
	EscapedBugs    int               `json:"escaped_bugs"`
	Corrections    int               `json:"corrections"`
	PassK          map[string]PassK  `json:"pass_k"`
}

type Closed struct {
	Closed    int `json:"closed"`
	Ambiguous int `json:"ambiguous"`
	Of        int `json:"of"`
}

type PassK struct {
	K      int `json:"k"`
	Passed int `json:"passed"`
	PassK  int `json:"pass_k"`
}

type Tokens struct {
	In         int64 `json:"in"`
	Out        int64 `json:"out"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

type Efficiency struct {
	ToolCalls       map[string]int            `json:"tool_calls"`
	ToolCallsByTool map[string]map[string]int `json:"tool_calls_by_tool"`
	Turns           map[string]int            `json:"turns"`
	Tokens          map[string]*Tokens        `json:"tokens"`
	CostUSD         map[string]*float64       `json:"cost_usd"`
	WallMin         map[string]float64        `json:"wall_min"`
	ActiveMin       map[string]float64        `json:"active_min"`
	PeakContext     map[string]int64          `json:"peak_context"`
	Compactions     map[string]*int           `json:"compactions"`
	// ContextTarget is the target the next two fields are measured against.
	ContextTarget int64 `json:"context_target"`
	// SessionsOverContextTarget counts the main sessions whose peak passed
	// the target; nil, named in Notes, for a vendor with no main session in
	// the window that carries a context value (spec B3).
	SessionsOverContextTarget map[string]*int `json:"sessions_over_context_target"`
	// SessionPeaks is every main session's peak context, largest first,
	// for the vendors that have one; no content, no path (plan D6, D16).
	SessionPeaks map[string][]SessionPeak `json:"session_peaks"`
}

// SessionPeak is one main session's peak context: the session's id, the UTC
// day it started and the largest context one of its model calls saw.
type SessionPeak struct {
	Session string `json:"session"`
	Date    string `json:"date"`
	Peak    int64  `json:"peak"`
}

type Adherence struct {
	SQLiteVerbs     map[string]int            `json:"sqlite_verbs"`
	GrepCalls       map[string]int            `json:"grep_calls"`
	SearchGrepRatio map[string]*float64       `json:"search_grep_ratio"`
	Forbidden       map[string]int            `json:"forbidden"`
	FirstReads      map[string]int            `json:"first_reads"`
	Skills          map[string]map[string]int `json:"skills"`
}

type Reliability struct {
	ToolErrorRate     map[string]*float64       `json:"tool_error_rate"`
	Denials           map[string]*int           `json:"denials"`
	Interrupts        map[string]*int           `json:"interrupts"`
	RetryLoops        map[string]int            `json:"retry_loops"`
	SubagentToolCalls map[string]map[string]int `json:"subagent_tool_calls"`
}

// Day is one calendar day (UTC) of the window, per vendor: the report's
// trend charts read these.
type Day struct {
	Date        string             `json:"date"`
	Sessions    map[string]int     `json:"sessions"`
	ToolCalls   map[string]int     `json:"tool_calls"`
	Tokens      map[string]int64   `json:"tokens"`
	CostUSD     map[string]float64 `json:"cost_usd"`
	SQLiteVerbs map[string]int     `json:"sqlite_verbs"`
	GrepCalls   map[string]int     `json:"grep_calls"`
	Errors      map[string]int     `json:"errors"`
}

type sess struct {
	id, vendor, root, parent, agent, label, outcome string
	start, end                                      int64
	cost                                            *float64
}

type stp struct {
	ext, kind, tool, skill, kindOf, hash string
	ts                                   int64
	isErr, denied                        bool
	in, out, cr, cw, ctx                 int64
	verbs                                []string
}

// Show computes the summary for the options' window and filters.
func (s *Store) Show(o ShowOptions) (*Summary, error) {
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	since := o.Since
	if since.IsZero() {
		since = now.AddDate(0, 0, -30)
	}
	q := `SELECT id, vendor, COALESCE(project_root,''), COALESCE(parent_id,''), COALESCE(agent_type,''),
		COALESCE(label,''), COALESCE(outcome,''), COALESCE(started_at,0), COALESCE(ended_at,0), cost_usd
		FROM sessions WHERE COALESCE(started_at, ended_at, 0) >= ? AND COALESCE(started_at, ended_at, 0) < ?`
	// A session belongs to the window it starts in, [since, until): adjacent
	// windows never share one (retro compares two of them).
	args := []any{since.UnixMilli(), now.UnixMilli()}
	switch {
	case o.Label != "":
		q += ` AND label = ?`
		args = append(args, o.Label)
	case !o.AllLabels:
		q += ` AND label IS NULL`
	}
	if o.Project != "" {
		// The root or anything under it: a session started in a
		// subdirectory is the project's. An exact, case-sensitive prefix
		// comparison: LIKE ignores ASCII case, and a sibling sharing the
		// prefix never matches because the prefix ends in "/".
		p := filepath.Clean(o.Project)
		q += ` AND (project_root = ? OR substr(project_root, 1, length(?)) = ?)`
		args = append(args, p, p+"/", p+"/")
	}
	rows, err := s.db.Query(q+` ORDER BY started_at`, args...)
	if err != nil {
		return nil, err
	}
	var all []sess
	for rows.Next() {
		var x sess
		if err := rows.Scan(&x.id, &x.vendor, &x.root, &x.parent, &x.agent, &x.label, &x.outcome, &x.start, &x.end, &x.cost); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, x)
	}
	rows.Close()

	sum := newSummary(since, now)
	sum.B.ContextTarget = o.ContextTarget
	if sum.B.ContextTarget <= 0 {
		sum.B.ContextTarget = DefaultContextTarget
	}
	for _, x := range all {
		steps, err := s.stepsOf(x.id)
		if err != nil {
			return nil, err
		}
		sum.add(x, steps)
		if err := s.addEvents(sum, x); err != nil {
			return nil, err
		}
	}
	sum.finish(all)
	return sum, nil
}

func (s *Store) stepsOf(id string) ([]stp, error) {
	rows, err := s.db.Query(`SELECT ext_id, COALESCE(kind,''), COALESCE(tool,''), COALESCE(skill,''), COALESCE(target_kind,''),
		COALESCE(cmd_hash,''), COALESCE(ts,0), COALESCE(is_error,0), COALESCE(denied,0), COALESCE(tok_in,0), COALESCE(tok_out,0),
		COALESCE(cache_read,0), COALESCE(cache_write,0), COALESCE(context,0)
		FROM steps WHERE session_id = ? ORDER BY ts, ext_id`, id)
	if err != nil {
		return nil, err
	}
	var out []stp
	idx := map[string]int{}
	for rows.Next() {
		var x stp
		if err := rows.Scan(&x.ext, &x.kind, &x.tool, &x.skill, &x.kindOf, &x.hash, &x.ts, &x.isErr, &x.denied,
			&x.in, &x.out, &x.cr, &x.cw, &x.ctx); err != nil {
			rows.Close()
			return nil, err
		}
		idx[x.ext] = len(out)
		out = append(out, x)
	}
	rows.Close()
	vr, err := s.db.Query(`SELECT ext_id, verb FROM step_verbs WHERE session_id = ? ORDER BY ext_id, n`, id)
	if err != nil {
		return nil, err
	}
	defer vr.Close()
	for vr.Next() {
		var ext, verb string
		if err := vr.Scan(&ext, &verb); err != nil {
			return nil, err
		}
		if i, ok := idx[ext]; ok {
			out[i].verbs = append(out[i].verbs, verb)
		}
	}
	return out, vr.Err()
}

func newSummary(since, now time.Time) *Summary {
	vi := func() map[string]int { return map[string]int{} }
	vm := func() map[string]map[string]int { return map[string]map[string]int{"agy": {}} }
	return &Summary{
		Window:    map[string]string{"since": since.UTC().Format("2006-01-02"), "until": now.UTC().Format("2006-01-02")},
		Sessions:  vi(),
		Subagents: vm(),
		A: Outcome{DoDClosed: map[string]Closed{}, Verifier: vi(), ReviewFindings: map[string]int{"high": 0, "medium": 0, "low": 0},
			PassK: map[string]PassK{}},
		B: Efficiency{ToolCalls: vi(), ToolCallsByTool: vm(), Turns: vi(), Tokens: map[string]*Tokens{},
			CostUSD: map[string]*float64{}, WallMin: map[string]float64{}, ActiveMin: map[string]float64{},
			PeakContext: map[string]int64{}, Compactions: map[string]*int{},
			SessionsOverContextTarget: map[string]*int{}, SessionPeaks: map[string][]SessionPeak{}},
		C: Adherence{SQLiteVerbs: vi(), GrepCalls: vi(), SearchGrepRatio: map[string]*float64{},
			Forbidden:  map[string]int{"git add -A": 0, "git add .": 0, "git push --force": 0, "--no-verify": 0, "commit chained to test": 0},
			FirstReads: vi(), Skills: vm()},
		D: Reliability{ToolErrorRate: map[string]*float64{}, Denials: map[string]*int{}, Interrupts: map[string]*int{},
			RetryLoops: vi(), SubagentToolCalls: vm()},
	}
}

// totals per vendor that become rates in finish.
type acc struct{ tools, errs, sqlite, grep, denials, interrupts, compactions int }

func (s *Summary) acc(v string) *acc {
	if s.accs == nil {
		s.accs = map[string]*acc{}
	}
	if s.accs[v] == nil {
		s.accs[v] = &acc{}
	}
	return s.accs[v]
}

func (s *Summary) day(ts int64) *Day {
	d := time.UnixMilli(ts).UTC().Format("2006-01-02")
	for i := range s.Daily {
		if s.Daily[i].Date == d {
			return &s.Daily[i]
		}
	}
	s.Daily = append(s.Daily, Day{Date: d, Sessions: map[string]int{}, ToolCalls: map[string]int{}, Tokens: map[string]int64{},
		CostUSD: map[string]float64{}, SQLiteVerbs: map[string]int{}, GrepCalls: map[string]int{}, Errors: map[string]int{}})
	return &s.Daily[len(s.Daily)-1]
}

func isSQLiteVerb(v string) bool {
	return strings.HasPrefix(v, "sh-agent search") || strings.HasPrefix(v, "sh-agent index")
}

func isGrepVerb(v string) bool {
	p, _, _ := strings.Cut(v, " ")
	return p == "grep" || p == "rg"
}

// columns are s.Columns, or the agy column alone for a summary not yet
// finished.
func (s *Summary) columns() []string {
	if len(s.Columns) == 0 {
		return []string{"agy"}
	}
	return s.Columns
}

// label is a column's name in a table.
func label(v string) string {
	if v == historical {
		return historical + " (historical)"
	}
	return v
}

func (s *Summary) add(x sess, steps []stp) {
	v := x.vendor
	// A column's count maps exist before anything counts into them: newSummary
	// makes agy's, and the historical column's are made on its first row.
	for _, m := range []map[string]map[string]int{s.Subagents, s.B.ToolCallsByTool, s.C.Skills, s.D.SubagentToolCalls} {
		if m[v] == nil {
			m[v] = map[string]int{}
		}
	}
	a := s.acc(v)
	main := x.parent == ""
	if main {
		s.Sessions[v]++
		if x.start > 0 {
			s.day(x.start).Sessions[v]++
		}
		if x.end > x.start && x.start > 0 {
			s.B.WallMin[v] += float64(x.end-x.start) / 60000
		}
	} else {
		t := x.agent
		if t == "" {
			t = "unknown"
		}
		s.Subagents[v][t]++
	}
	if x.cost != nil {
		c := *x.cost
		if s.B.CostUSD[v] == nil {
			s.B.CostUSD[v] = new(float64)
		}
		*s.B.CostUSD[v] += c
		if x.start > 0 {
			s.day(x.start).CostUSD[v] += c
		}
	}
	if s.B.Tokens[v] == nil {
		s.B.Tokens[v] = &Tokens{}
	}
	tk := s.B.Tokens[v]
	var last, peak int64
	var lastHash string
	reads := 0
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	for _, st := range steps {
		if st.kind == "tool" || st.kind == "assistant" || st.kind == "generation" {
			key := v + "\x00" + x.label + "\x00" + st.ext // within one label: a sandbox never hides real steps
			if s.seen[key] {
				continue // replayed history, counted under the session that first had it
			}
			s.seen[key] = true
		}
		if st.ts > 0 {
			// main sessions only, as wall time: a subagent runs inside its parent
			if main && last > 0 && st.ts-last < activeGap.Milliseconds() {
				s.B.ActiveMin[v] += float64(st.ts-last) / 60000
			}
			last = st.ts
		}
		tk.In += st.in
		tk.Out += st.out
		tk.CacheRead += st.cr
		tk.CacheWrite += st.cw
		ctx := st.ctx
		if v == historical && st.kind == "assistant" {
			ctx = st.in + st.cr + st.cw
		}
		s.B.PeakContext[v] = max(s.B.PeakContext[v], ctx)
		peak = max(peak, ctx)
		if st.ts > 0 && st.in+st.out+st.cr+st.cw > 0 {
			s.day(st.ts).Tokens[v] += st.in + st.out + st.cr + st.cw
		}
		switch st.kind {
		case "user":
			s.B.Turns[v]++
		case "interrupt":
			a.interrupts++
		case "compaction":
			a.compactions++
		case "tool":
			if st.tool == "" {
				continue // a result whose call was never seen
			}
			a.tools++
			s.B.ToolCalls[v]++
			s.B.ToolCallsByTool[v][st.tool]++
			if !main {
				t := x.agent
				if t == "" {
					t = "unknown"
				}
				s.D.SubagentToolCalls[v][t]++
			}
			if st.ts > 0 {
				s.day(st.ts).ToolCalls[v]++
			}
			if st.isErr {
				a.errs++
				if st.ts > 0 {
					s.day(st.ts).Errors[v]++
				}
			}
			if st.denied {
				a.denials++
			}
			if st.skill != "" {
				s.C.Skills[v][st.skill]++
			}
			if st.tool == "Grep" {
				a.grep++
				if st.ts > 0 {
					s.day(st.ts).GrepCalls[v]++
				}
			}
			if main && st.kindOf != "" && reads < 3 && st.tool == "Read" {
				s.C.FirstReads[st.kindOf]++
				reads++
			}
			if st.hash != "" && st.hash == lastHash {
				s.D.RetryLoops[v]++
			}
			if st.tool == "Bash" {
				lastHash = st.hash
			}
			s.verbs(v, st)
		}
	}
	// A main session with at least one model call has a peak; a subagent runs
	// inside its parent's window and is not a session of its own here.
	if main && peak > 0 {
		s.B.SessionPeaks[v] = append(s.B.SessionPeaks[v], SessionPeak{Session: x.id, Date: sessionDay(x), Peak: peak})
	}
}

// sessionDay is the UTC day a session started, or ended when no start is
// recorded; "" when neither is.
func sessionDay(x sess) string {
	ts := x.start
	if ts == 0 {
		ts = x.end
	}
	if ts == 0 {
		return ""
	}
	return time.UnixMilli(ts).UTC().Format("2006-01-02")
}

func (s *Summary) verbs(v string, st stp) {
	a := s.acc(v)
	commit, test := false, false
	for _, verb := range st.verbs {
		w := strings.Fields(verb)
		switch {
		case isSQLiteVerb(verb):
			a.sqlite++
			if st.ts > 0 {
				s.day(st.ts).SQLiteVerbs[v]++
			}
		case isGrepVerb(verb):
			a.grep++
			if st.ts > 0 {
				s.day(st.ts).GrepCalls[v]++
			}
		}
		if len(w) >= 2 && w[0] == "git" {
			flags := " " + strings.Join(w[2:], " ") + " "
			switch w[1] {
			case "add":
				if strings.Contains(flags, " -A ") || strings.Contains(flags, " --all ") {
					s.C.Forbidden["git add -A"]++
				}
				if strings.Contains(flags, " . ") {
					s.C.Forbidden["git add ."]++
				}
			case "push":
				if strings.Contains(flags, " -f ") || strings.Contains(flags, " --force ") {
					s.C.Forbidden["git push --force"]++
				}
			case "commit":
				commit = true
			}
		}
		if strings.Contains(" "+verb+" ", " --no-verify ") {
			s.C.Forbidden["--no-verify"]++
		}
		if strings.HasPrefix(verb, "go test") {
			test = true
		}
	}
	if commit && test {
		s.C.Forbidden["commit chained to test"]++
	}
}

func (st *Store) addEvents(s *Summary, x sess) error {
	if x.parent != "" {
		return nil
	}
	rows, err := st.db.Query(`SELECT kind, value FROM events WHERE session_id = ?`, x.id)
	if err != nil {
		return err
	}
	defer rows.Close()
	commits, ambiguous, handoff := 0, 0, false
	for rows.Next() {
		var kind, value string
		if err := rows.Scan(&kind, &value); err != nil {
			return err
		}
		switch kind {
		case "commit":
			if value == "ambiguous" {
				ambiguous++
			} else {
				commits++
			}
		case "handoff":
			handoff = true
		case "verifier":
			s.A.Verifier[value]++
		case "review":
			for _, kv := range strings.Split(value, ",") {
				k, n, _ := strings.Cut(kv, "=")
				var c int
				fmt.Sscan(n, &c)
				s.A.ReviewFindings[k] += c
			}
		case "bug":
			s.A.EscapedBugs++
		case "correction":
			s.A.Corrections++
		}
	}
	c := s.A.DoDClosed[x.vendor]
	c.Of++
	switch {
	case commits > 0 && handoff:
		c.Closed++
	case ambiguous > 0 && handoff:
		c.Ambiguous++
	}
	s.A.DoDClosed[x.vendor] = c
	return rows.Err()
}

func (s *Summary) finish(all []sess) {
	labels := map[string]*PassK{}
	for _, x := range all {
		if x.parent != "" || x.label == "" || x.outcome == "" {
			continue
		}
		p := labels[x.label]
		if p == nil {
			p = &PassK{}
			labels[x.label] = p
		}
		p.K++
		if x.outcome == "pass" {
			p.Passed++
		}
	}
	for l, p := range labels {
		if p.Passed == p.K {
			p.PassK = 1
		}
		s.A.PassK[l] = *p
	}
	s.Columns = []string{"agy"}
	if s.Sessions[historical] > 0 || len(s.Subagents[historical]) > 0 {
		s.Columns = append(s.Columns, historical)
	}
	for _, v := range s.Columns {
		s.finishContext(v)
		a := s.acc(v)
		if s.Sessions[v] == 0 && len(s.Subagents[v]) == 0 {
			continue
		}
		if a.tools > 0 {
			r := float64(a.errs) / float64(a.tools)
			s.D.ToolErrorRate[v] = &r
		}
		s.C.SQLiteVerbs[v] = a.sqlite
		s.C.GrepCalls[v] = a.grep
		if a.grep > 0 {
			r := float64(a.sqlite) / float64(a.grep)
			s.C.SearchGrepRatio[v] = &r
		}
		if v == historical {
			d, i, c := a.denials, a.interrupts, a.compactions
			s.D.Denials[v], s.D.Interrupts[v], s.B.Compactions[v] = &d, &i, &c
			if s.B.CostUSD[v] == nil {
				s.Notes = append(s.Notes, label(v)+": no session in the window has ended with a cost line")
			}
		} else {
			s.D.Denials[v], s.D.Interrupts[v], s.B.Compactions[v] = nil, nil, nil
			s.Notes = append(s.Notes, "agy: denials, interrupts and compactions are not recorded by the CLI")
			if s.B.CostUSD[v] == nil {
				s.Notes = append(s.Notes, "agy: cost is not exposed by the CLI")
			}
		}
	}
	sort.Slice(s.Daily, func(i, j int) bool { return s.Daily[i].Date < s.Daily[j].Date })
	for _, c := range s.A.DoDClosed {
		if c.Ambiguous > 0 {
			s.Notes = append(s.Notes, "some sessions overlap another in the same project; their commits are counted as ambiguous, not closed")
			break
		}
	}
}

// finishContext orders v's session peaks, largest first, and counts those
// past the target; with no peak the count is nil and a note says so.
func (s *Summary) finishContext(v string) {
	peaks := s.B.SessionPeaks[v]
	if len(peaks) == 0 {
		delete(s.B.SessionPeaks, v)
		s.B.SessionsOverContextTarget[v] = nil
		s.Notes = append(s.Notes, v+": no main session in the window carries a context value; sessions over context target is n/a")
		return
	}
	sort.Slice(peaks, func(i, j int) bool {
		if peaks[i].Peak != peaks[j].Peak {
			return peaks[i].Peak > peaks[j].Peak
		}
		return peaks[i].Session < peaks[j].Session
	})
	over := 0
	for _, p := range peaks {
		if p.Peak > s.B.ContextTarget {
			over++
		}
	}
	s.B.SessionsOverContextTarget[v] = &over
}

// overTarget renders the over-target count as "k of n", n the main sessions
// with a peak, or "n/a".
func (s *Summary) overTarget(v string) string {
	p := s.B.SessionsOverContextTarget[v]
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d of %d", *p, len(s.B.SessionPeaks[v]))
}

// WriteJSON prints the summary as one indented object.
func (s *Summary) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// safeMap is a count map for the terminal: a key holding a character the
// BUG-152 filter replaces (a row an older build stored raw) prints as "?".
func safeMap(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		if printable(k) != k {
			k = "?"
		}
		out[k] += v
	}
	return out
}

// WriteText prints the summary as one short table per catalogue group, a
// column per Columns entry; "n/a" is a metric the CLI cannot produce.
func (s *Summary) WriteText(w io.Writer) {
	fmt.Fprintf(w, "metrics %s .. %s\n", s.Window["since"], s.Window["until"])
	cols := s.columns()
	width := 14
	for _, v := range cols {
		width = max(width, len(label(v)))
	}
	row := func(name string, f func(v string) string) {
		fmt.Fprintf(w, "  %-28s", name)
		for _, v := range cols {
			fmt.Fprintf(w, " %*s", width, f(v))
		}
		fmt.Fprintln(w)
	}
	head := func(title string) {
		fmt.Fprintf(w, "\n%s\n  %-28s", title, "")
		for _, v := range cols {
			fmt.Fprintf(w, " %*s", width, label(v))
		}
		fmt.Fprintln(w)
	}
	n := func(m map[string]int) func(string) string {
		return func(v string) string { return fmt.Sprint(m[v]) }
	}
	pf := func(m map[string]*float64, format string) func(string) string {
		return func(v string) string {
			if m[v] == nil {
				return "n/a"
			}
			return fmt.Sprintf(format, *m[v])
		}
	}
	pi := func(m map[string]*int) func(string) string {
		return func(v string) string {
			if m[v] == nil {
				return "n/a"
			}
			return fmt.Sprint(*m[v])
		}
	}
	head("sessions")
	row("main sessions", n(s.Sessions))
	row("subagents", func(v string) string {
		t := 0
		for _, c := range s.Subagents[v] {
			t += c
		}
		return fmt.Sprint(t)
	})
	head("A. outcome")
	row("DoD closed (ambiguous) / of", func(v string) string {
		c := s.A.DoDClosed[v]
		return fmt.Sprintf("%d (%d) / %d", c.Closed, c.Ambiguous, c.Of)
	})
	fmt.Fprintf(w, "  verifier %v; review findings %v; escaped bugs %d; corrections %d\n",
		safeMap(s.A.Verifier), safeMap(s.A.ReviewFindings), s.A.EscapedBugs, s.A.Corrections)
	for _, l := range sortedKeys(s.A.PassK) {
		p := s.A.PassK[l]
		fmt.Fprintf(w, "  pass^k %s: %d of %d passed, pass^%d = %d\n", l, p.Passed, p.K, p.K, p.PassK)
	}
	head("B. efficiency")
	row("tool calls", n(s.B.ToolCalls))
	row("user turns", n(s.B.Turns))
	row("tokens in+out", func(v string) string {
		if t := s.B.Tokens[v]; t != nil {
			return fmt.Sprint(t.In + t.Out)
		}
		return "0"
	})
	row("cache read", func(v string) string {
		if t := s.B.Tokens[v]; t != nil {
			return fmt.Sprint(t.CacheRead)
		}
		return "0"
	})
	row("cost USD", pf(s.B.CostUSD, "%.2f"))
	row("wall / active min", func(v string) string { return fmt.Sprintf("%.0f / %.0f", s.B.WallMin[v], s.B.ActiveMin[v]) })
	row("peak context", func(v string) string { return fmt.Sprint(s.B.PeakContext[v]) })
	row(fmt.Sprintf("over context target %d", s.B.ContextTarget), s.overTarget)
	row("compactions", pi(s.B.Compactions))
	head("C. process adherence")
	row("sh-agent search|index", n(s.C.SQLiteVerbs))
	row("grep/rg/Grep", n(s.C.GrepCalls))
	row("search : grep", pf(s.C.SearchGrepRatio, "%.3f"))
	fmt.Fprintf(w, "  forbidden shapes %v\n  first reads %v\n", safeMap(s.C.Forbidden), safeMap(s.C.FirstReads))
	for _, v := range cols {
		if len(s.C.Skills[v]) > 0 {
			fmt.Fprintf(w, "  skills (%s) %v\n", label(v), safeMap(s.C.Skills[v]))
		}
	}
	head("D. reliability")
	row("tool error rate", pf(s.D.ToolErrorRate, "%.3f"))
	row("denials", pi(s.D.Denials))
	row("interrupts", pi(s.D.Interrupts))
	row("retry loops", n(s.D.RetryLoops))
	for _, v := range cols {
		if len(s.D.SubagentToolCalls[v]) > 0 {
			fmt.Fprintf(w, "  subagent tool calls (%s) %v\n", label(v), safeMap(s.D.SubagentToolCalls[v]))
		}
	}
	for _, note := range s.Notes {
		fmt.Fprintln(w, "note: "+note)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
