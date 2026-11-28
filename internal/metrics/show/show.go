package show

import (
	"database/sql"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	r "github.com/kesokaj/fyr/internal/metrics/report"
)

// Show computes the summary for the options' window and filters.
func Show(deps Deps, o Options) (*Summary, error) {
	now, since := resolveWindow(o)
	q, args := ShowQuery(o, since, now)
	all, err := SessionsIn(deps.DB, q, args)
	if err != nil {
		return nil, err
	}
	trials, claimed, err := loadShowTrials(deps, o, since, now)
	if err != nil {
		return nil, err
	}
	data, err := LoadShowData(deps.DB, all)
	if err != nil {
		return nil, err
	}
	sum := buildBaseSummary(o, all, trials, claimed, data, since, now, deps.Est)
	if err := deps.populateSummaryExtras(sum, o, all, trials, data, since, now); err != nil {
		return nil, err
	}
	return sum, nil
}

func resolveWindow(o Options) (now, since time.Time) {
	now = o.Now
	if now.IsZero() {
		now = time.Now()
	}
	since = o.Since
	if since.IsZero() {
		since = now.AddDate(0, 0, -30)
	}
	return now, since
}

func loadShowTrials(deps Deps, o Options, since, now time.Time) ([]Trial, map[string]bool, error) {
	var trials []Trial
	var err error
	if deps.TrialsIn != nil {
		if trials, err = deps.TrialsIn(o, since, now); err != nil {
			return nil, nil, err
		}
	}
	var claimed map[string]bool
	if deps.TrialRuns != nil {
		if claimed, err = deps.TrialRuns(); err != nil {
			return nil, nil, err
		}
	}
	return trials, claimed, nil
}

func buildBaseSummary(o Options, all []Sess, trials []Trial, claimed map[string]bool, data *ShowData, since, now time.Time, est Estimators) *Summary {
	sum := NewSummary(since, now)
	sum.B.ContextTarget = o.ContextTarget
	if sum.B.ContextTarget <= 0 {
		sum.B.ContextTarget = DefaultContextTarget
	}
	for _, x := range all {
		sum.Add(x, data.Steps[x.ID])
		sum.AddSessionEvents(x, data.Events[x.ID])
	}
	sum.Finish(all, trials, claimed, est)
	return sum
}

func (d Deps) populateSummaryExtras(sum *Summary, o Options, all []Sess, trials []Trial, data *ShowData, since, now time.Time) error {
	home := ResolveMetricsHome(o, d.StorePath)
	if o.Home == "" {
		o.Home = home
	}
	PopulateAdherenceVerbs(sum, all, data.Steps)
	steps := []func() error{
		func() error { return d.populateSecondBrainAndProjects(sum, o, all, trials, data, since, now) },
		func() error { return callOptErr(d.PopulateSessionRatings, sum, o, since, now) },
		func() error { return callOptErr(d.PopulateBackgroundRuns, sum, o, since, now) },
		func() error { return d.populateOrchestrationSummary(sum, o, since, now) },
		func() error { return d.populateHealthAndFallbacks(sum, o, all, trials, data, since, now) },
		func() error { return d.populateTelemetryAndCohorts(sum, home, o, all, data, since, now) },
		func() error { return d.populateEvalsDeltasDiagnostics(sum, home, o, since, now) },
	}
	for _, fn := range steps {
		if err := fn(); err != nil {
			return err
		}
	}
	PopulateRuleTelemetry(sum)
	return nil
}

func callOptErr(fn func(*Summary, Options, time.Time, time.Time) error, sum *Summary, o Options, since, now time.Time) error {
	if fn == nil {
		return nil
	}
	return fn(sum, o, since, now)
}

func (d Deps) populateTelemetryAndCohorts(sum *Summary, home string, o Options, all []Sess, data *ShowData, since, now time.Time) error {
	sum.TriggerCoverage.OrchestrationCalls = sum.Health.OrchestrationCalls
	sum.TriggerCoverage.DeterministicGates = sum.Health.DeterministicGates
	sum.TriggerCoverage.ReadLedgerBlocks = dedupLedgerSaves(sum)
	d.populateSIMSummary(sum, home, o)
	if err := d.populateSkillTelemetry(sum, o, all, data, since, now); err != nil {
		return err
	}
	return d.populateCohorts(sum, o, all, data, since, now)
}

func (d Deps) populateEvalsDeltasDiagnostics(sum *Summary, home string, o Options, since, now time.Time) error {
	if strings.TrimSpace(o.Project) == "" && o.Label == "" && d.LoadEvalRows != nil {
		ev, err := d.LoadEvalRows(home, since, now)
		if err != nil {
			return err
		}
		sum.EvalRows = ev
	}
	if d.PopulateWindowDeltas != nil {
		if err := d.PopulateWindowDeltas(sum, o, since, now); err != nil {
			return err
		}
	}
	if d.PopulateDiagnostics != nil {
		return d.PopulateDiagnostics(sum, o, now)
	}
	return nil
}

// ResolveMetricsHome returns o.Home if set; otherwise it infers the home
// directory ONLY when storePath ends with <sep><StateDir>/metrics.db.
func ResolveMetricsHome(o Options, storePath string) string {
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

// ShowWhere builds the WHERE clause and arguments for sessions matching o in [since, now).
func ShowWhere(o Options, since, now time.Time) (string, []any) {
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

// ShowQuery returns the sessions query and arguments for o in [since, now).
func ShowQuery(o Options, since, now time.Time) (string, []any) {
	where, args := ShowWhere(o, since, now)
	q := `SELECT id, vendor, COALESCE(project_root,''), COALESCE(parent_id,''), COALESCE(agent_type,''),
		COALESCE(model,''), COALESCE(label,''), COALESCE(outcome,''), COALESCE(run,''), COALESCE(started_at,0), COALESCE(ended_at,0), cost_usd
		FROM sessions WHERE ` + where + ` ORDER BY started_at`
	return q, args
}

// SessionsIn executes a sessions query and scans its rows.
func SessionsIn(db *sql.DB, q string, args []any) ([]Sess, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	var all []Sess
	for rows.Next() {
		var x Sess
		if err := rows.Scan(&x.ID, &x.Vendor, &x.Root, &x.Parent, &x.Agent, &x.Model, &x.Label, &x.Outcome, &x.Run, &x.Start, &x.End, &x.Cost); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, x)
	}
	rows.Close()
	return all, rows.Err()
}

// StepsOf loads one session's steps and attached Bash verbs in order.
func StepsOf(db *sql.DB, id string) ([]Stp, error) {
	rows, err := db.Query(`SELECT ext_id, COALESCE(kind,''), COALESCE(tool,''), COALESCE(skill,''), COALESCE(target_kind,''),
		COALESCE(cmd_hash,''), COALESCE(ts,0), COALESCE(started_ms,0), COALESCE(ended_ms,0), COALESCE(wait_ms,0),
		COALESCE(is_error,0), COALESCE(denied,0), COALESCE(tok_in,0), COALESCE(tok_out,0),
		COALESCE(cache_read,0), COALESCE(cache_write,0), COALESCE(context,0)
		FROM steps WHERE session_id = ? ORDER BY ts, ext_id`, id)
	if err != nil {
		return nil, err
	}
	var out []Stp
	idx := map[string]int{}
	for rows.Next() {
		var x Stp
		if err := rows.Scan(&x.Ext, &x.Kind, &x.Tool, &x.Skill, &x.KindOf, &x.Hash, &x.TS, &x.Started, &x.Ended, &x.Wait,
			&x.IsErr, &x.Denied, &x.In, &x.Out, &x.CR, &x.CW, &x.Ctx); err != nil {
			rows.Close()
			return nil, err
		}
		idx[x.Ext] = len(out)
		out = append(out, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return attachSessionVerbs(db, id, out, idx)
}

func attachSessionVerbs(db *sql.DB, id string, out []Stp, idx map[string]int) ([]Stp, error) {
	vr, err := db.Query(`SELECT ext_id, n, verb FROM step_verbs WHERE session_id = ? ORDER BY ext_id, n`, id)
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
			out[i].Verbs = append(out[i].Verbs, verb)
			out[i].VerbNs = append(out[i].VerbNs, n)
		}
	}
	return out, vr.Err()
}

// NewSummary allocates an initialized empty Summary for [since, now).
func NewSummary(since, now time.Time) *Summary {
	vi := func() map[string]int { return map[string]int{} }
	vf := func() map[string]float64 { return map[string]float64{} }
	vm := func() map[string]map[string]int { return map[string]map[string]int{"agy": {}} }
	pk := map[string]r.PassK{}
	vt := map[string]*r.Tokens{}
	return &Summary{
		Window:    map[string]string{"since": since.UTC().Format("2006-01-02"), "until": now.UTC().Format("2006-01-02")},
		Sessions:  vi(),
		Subagents: vm(),
		A: r.Outcome{DoDClosed: map[string]r.Closed{}, Verifier: vi(), ReviewFindings: map[string]int{"high": 0, "medium": 0, "low": 0},
			PassK: pk},
		B: r.Efficiency{ToolCalls: vi(), ToolCallsByTool: vm(), Turns: vi(), Tokens: vt,
			CostUSD: map[string]*float64{}, WallMin: vf(), ActiveMin: vf(),
			ModelMin: vf(), ToolMin: vf(), WaitMin: vf(), IdleMin: vf(),
			PeakContext: map[string]int64{}, Compactions: map[string]*int{},
			SessionsOverContextTarget: map[string]*int{}, SessionPeaks: map[string][]r.SessionPeak{}},
		C: r.Adherence{SQLiteVerbs: vi(), GrepCalls: vi(), SearchGrepRatio: map[string]*float64{},
			Forbidden:  map[string]int{"git add -A": 0, "git add .": 0, "git push --force": 0, "--no-verify": 0, "commit chained to test": 0},
			FirstReads: vi(), Skills: vm()},
		D: r.Reliability{ToolErrorRate: map[string]*float64{}, Denials: map[string]*int{}, Interrupts: map[string]*int{},
			RetryLoops: vi(), SubagentToolCalls: vm()},
	}
}

func dedupLedgerSaves(s *Summary) int {
	if s == nil {
		return 0
	}
	return max(s.Health.HookBlocksLedger, s.E.LedgerSaved)
}
