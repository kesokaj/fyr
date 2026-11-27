package metrics

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// ShowOptions filter what show and report read (spec § Verbs).
type ShowOptions struct {
	Home             string
	Since            time.Time // zero: 30 days before Now
	Now              time.Time // zero: time.Now()
	SinceTag         string
	Project          string
	Label            string
	AllLabels        bool
	IncludeSandboxes bool
	// ContextTarget is the peak context, in tokens, a main session should
	// stay under; 0 means DefaultContextTarget (plan session-sized-work D4).
	ContextTarget int64

	skipDeltas bool
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

	data, err := s.loadShowData(all)
	if err != nil {
		return nil, err
	}
	sum := newSummary(since, now)
	sum.B.ContextTarget = o.ContextTarget
	if sum.B.ContextTarget <= 0 {
		sum.B.ContextTarget = DefaultContextTarget
	}
	for _, x := range all {
		sum.add(x, data.steps[x.id])
		sum.addSessionEvents(x, data.events[x.id])
	}
	sum.finish(all, trials, claimed)
	if err := s.populateSummaryExtras(sum, o, all, trials, data, since, now); err != nil {
		return nil, err
	}
	return sum, nil
}

func (s *Store) populateSummaryExtras(sum *Summary, o ShowOptions, all []sess, trials []trial, data *showData, since, now time.Time) error {
	home := resolveMetricsHome(o, s.path)
	if o.Home == "" {
		o.Home = home
	}
	populateAdherenceVerbs(sum, all, data.steps)
	if err := s.populateSecondBrainAndProjects(sum, o, all, trials, data, since, now); err != nil {
		return err
	}
	if err := s.populateSessionRatings(sum, o, since, now); err != nil {
		return err
	}
	if err := s.populateBackgroundRuns(sum, o, since, now); err != nil {
		return err
	}
	if err := s.populateOrchestrationSummary(sum, o, since, now); err != nil {
		return err
	}
	if err := s.populateHealthAndFallbacks(sum, o, all, trials, data, since, now); err != nil {
		return err
	}
	sum.TriggerCoverage.OrchestrationCalls = sum.Health.OrchestrationCalls
	sum.TriggerCoverage.DeterministicGates = sum.Health.DeterministicGates
	sum.TriggerCoverage.ReadLedgerBlocks = dedupLedgerSaves(sum)
	populateSIMSummary(sum, home, o)
	if err := s.populateSkillTelemetry(sum, o, all, data, since, now); err != nil {
		return err
	}
	if err := s.populateCohorts(sum, o, all, data, since, now); err != nil {
		return err
	}
	if strings.TrimSpace(o.Project) == "" && o.Label == "" {
		ev, err := ReportEvalsWithDevFallback(s, home, ShowOptions{Since: since, Now: now})
		if err != nil {
			return err
		}
		if ev != nil {
			sum.evalRows = ev.Rows
		}
	}
	if err := s.populateWindowDeltas(sum, o, since, now); err != nil {
		return err
	}
	if err := s.populateDiagnostics(sum, o, now); err != nil {
		return err
	}
	populateRuleTelemetry(sum)
	return nil
}

// resolveMetricsHome returns o.Home if set; otherwise it infers the home
// directory ONLY when s.path ends with <sep><StateDir>/metrics.db so synthetic
// unit tests using t.TempDir()/metrics.db never touch host $HOME.
func resolveMetricsHome(o ShowOptions, storePath string) string {
	if h := strings.TrimSpace(o.Home); h != "" {
		return filepath.Clean(h)
	}
	if strings.TrimSpace(storePath) == "" {
		return ""
	}
	clean := filepath.Clean(storePath)
	suffix := string(filepath.Separator) + filepath.Join(harness.StateDir, "metrics.db")
	if strings.HasSuffix(clean, suffix) && len(clean) > len(suffix) {
		return strings.TrimSuffix(clean, suffix)
	}
	return ""
}

// showWhere builds the WHERE clause and arguments for sessions matching o in [since, now).
func showWhere(o ShowOptions, since, now time.Time) (string, []any) {
	w := `COALESCE(started_at, ended_at, 0) >= ? AND COALESCE(started_at, ended_at, 0) < ?`
	args := []any{since.UnixMilli(), now.UnixMilli()}
	switch {
	case o.Label != "":
		w += ` AND label = ?`
		args = append(args, o.Label)
	case !o.AllLabels:
		w += ` AND label IS NULL`
	}
	if o.Project != "" {
		p := filepath.Clean(o.Project)
		w += ` AND (project_root = ? OR substr(project_root, 1, length(?)) = ?)`
		args = append(args, p, p+"/", p+"/")
	}
	return w, args
}

// showQuery is the query of the sessions in the window [since, now) that
// pass the options' label and project filters, with its arguments.
func showQuery(o ShowOptions, since, now time.Time) (string, []any) {
	where, args := showWhere(o, since, now)
	q := `SELECT id, vendor, COALESCE(project_root,''), COALESCE(parent_id,''), COALESCE(agent_type,''),
		COALESCE(model,''), COALESCE(label,''), COALESCE(outcome,''), COALESCE(run,''), COALESCE(started_at,0), COALESCE(ended_at,0), cost_usd
		FROM sessions WHERE ` + where + ` ORDER BY started_at`
	return q, args
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
		if err := rows.Scan(&x.id, &x.vendor, &x.root, &x.parent, &x.agent, &x.model, &x.label, &x.outcome, &x.run, &x.start, &x.end, &x.cost); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, x)
	}
	rows.Close()
	return all, rows.Err()
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	vr, err := s.db.Query(`SELECT ext_id, n, verb FROM step_verbs WHERE session_id = ? ORDER BY ext_id, n`, id)
	if err != nil {
		return nil, err
	}
	defer vr.Close()
	for vr.Next() {
		var ext, verb string
		var n int
		if err := vr.Scan(&ext, &n, &verb); err != nil {
			return nil, err
		}
		if i, ok := idx[ext]; ok {
			out[i].verbs = append(out[i].verbs, verb)
			out[i].verbNs = append(out[i].verbNs, n)
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
