package metrics

import (
	"path/filepath"
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
	all, err := s.sessionsIn(showQuery(o, since, now))
	if err != nil {
		return nil, err
	}
	trials, err := s.trialsIn(o, since, now)
	if err != nil {
		return nil, err
	}
	claimed, err := s.trialRuns()
	if err != nil {
		return nil, err
	}

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
	sum.finish(all, trials, claimed)
	if err := s.populateSecondBrainAndProjects(sum, o, all, trials, since, now); err != nil {
		return nil, err
	}
	return sum, nil
}

// showQuery is the query of the sessions in the window [since, now) that
// pass the options' label and project filters, with its arguments.
func showQuery(o ShowOptions, since, now time.Time) (string, []any) {
	q := `SELECT id, vendor, COALESCE(project_root,''), COALESCE(parent_id,''), COALESCE(agent_type,''),
		COALESCE(label,''), COALESCE(outcome,''), COALESCE(run,''), COALESCE(started_at,0), COALESCE(ended_at,0), cost_usd
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
	return q + ` ORDER BY started_at`, args
}

// sessionsIn runs a sessions query and reads its rows.
func (s *Store) sessionsIn(q string, args []any) ([]sess, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	var all []sess
	for rows.Next() {
		var x sess
		if err := rows.Scan(&x.id, &x.vendor, &x.root, &x.parent, &x.agent, &x.label, &x.outcome, &x.run, &x.start, &x.end, &x.cost); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, x)
	}
	rows.Close()
	return all, nil
}

func (s *Store) stepsOf(id string) ([]stp, error) {
	rows, err := s.db.Query(`SELECT ext_id, COALESCE(kind,''), COALESCE(tool,''), COALESCE(skill,''), COALESCE(target_kind,''),
		COALESCE(cmd_hash,''), COALESCE(ts,0), COALESCE(started_ms,0), COALESCE(ended_ms,0), COALESCE(wait_ms,0),
		COALESCE(is_error,0), COALESCE(denied,0), COALESCE(tok_in,0), COALESCE(tok_out,0),
		COALESCE(cache_read,0), COALESCE(cache_write,0), COALESCE(context,0)
		FROM steps WHERE session_id = ? ORDER BY ts, ext_id`, id)
	if err != nil {
		return nil, err
	}
	var out []stp
	idx := map[string]int{}
	for rows.Next() {
		var x stp
		if err := rows.Scan(&x.ext, &x.kind, &x.tool, &x.skill, &x.kindOf, &x.hash, &x.ts, &x.started, &x.ended, &x.wait,
			&x.isErr, &x.denied, &x.in, &x.out, &x.cr, &x.cw, &x.ctx); err != nil {
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
	vf := func() map[string]float64 { return map[string]float64{} }
	vm := func() map[string]map[string]int { return map[string]map[string]int{"agy": {}} }
	return &Summary{
		Window:    map[string]string{"since": since.UTC().Format("2006-01-02"), "until": now.UTC().Format("2006-01-02")},
		Sessions:  vi(),
		Subagents: vm(),
		A: Outcome{DoDClosed: map[string]Closed{}, Verifier: vi(), ReviewFindings: map[string]int{"high": 0, "medium": 0, "low": 0},
			PassK: map[string]PassK{}},
		B: Efficiency{ToolCalls: vi(), ToolCallsByTool: vm(), Turns: vi(), Tokens: map[string]*Tokens{},
			CostUSD: map[string]*float64{}, WallMin: vf(), ActiveMin: vf(),
			ModelMin: vf(), ToolMin: vf(), WaitMin: vf(), IdleMin: vf(),
			PeakContext: map[string]int64{}, Compactions: map[string]*int{},
			SessionsOverContextTarget: map[string]*int{}, SessionPeaks: map[string][]SessionPeak{}},
		C: Adherence{SQLiteVerbs: vi(), GrepCalls: vi(), SearchGrepRatio: map[string]*float64{},
			Forbidden:  map[string]int{"git add -A": 0, "git add .": 0, "git push --force": 0, "--no-verify": 0, "commit chained to test": 0},
			FirstReads: vi(), Skills: vm()},
		D: Reliability{ToolErrorRate: map[string]*float64{}, Denials: map[string]*int{}, Interrupts: map[string]*int{},
			RetryLoops: vi(), SubagentToolCalls: vm()},
	}
}
