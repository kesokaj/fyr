package show

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	r "github.com/kesokaj/fyr/internal/metrics/report"
	"github.com/kesokaj/fyr/internal/search"
)

func (d Deps) populateCohorts(sum *Summary, o Options, all []Sess, data *ShowData, since, now time.Time) error {
	var convVets map[string][2]int
	if data != nil && data.SBAcc != nil {
		convVets = data.SBAcc.ByConv
	}
	var steps map[string][]Stp
	if data != nil {
		steps = data.Steps
	}
	sum.AgentRoles = BuildAgentRoles(all, steps, convVets)
	if o.SinceTag == "" || o.SkipDeltas {
		return nil
	}
	tc, err := d.buildTagCohort(o, all, data, since, now)
	if err != nil {
		return err
	}
	sum.TagCohort = tc
	return nil
}

type roleAcc struct {
	role                           string
	sessions, tools, errs, denials int
	sqlite, grep                   int
	turnVets, timedOut             int
}

// BuildAgentRoles aggregates per-role session, tool, search, and turn-vet metrics.
func BuildAgentRoles(all []Sess, stepsByID map[string][]Stp, convVets map[string][2]int) []r.AgentRoleMetrics {
	accs := map[string]*roleAcc{}
	seen := map[string]bool{}
	for _, x := range all {
		if x.Vendor == Historical {
			continue
		}
		role := normalizeAgentRole(x.Agent)
		a := accs[role]
		if a == nil {
			a = &roleAcc{role: role}
			accs[role] = a
		}
		a.sessions++
		accumulateRoleSteps(a, x, stepsByID[x.ID], seen)
		if cv, ok := convVets[x.ID]; ok {
			a.turnVets += cv[0]
			a.timedOut += cv[1]
		}
	}
	return finalizeAgentRoles(accs)
}

func normalizeAgentRole(agent string) string {
	role := search.Printable(strings.TrimSpace(agent))
	if role == "" {
		return "main"
	}
	return role
}

func countStepSearchVerbs(st Stp) (sqliteCnt, grepCnt int) {
	if st.Tool == "Grep" {
		grepCnt++
	}
	firstVerb := st.FirstNonCDVerbIdx()
	for i, v := range st.Verbs {
		if IsSQLiteVerb(v) {
			sqliteCnt++
		} else if i == firstVerb && IsGrepVerb(v) {
			grepCnt++
		}
	}
	return sqliteCnt, grepCnt
}

func accumulateRoleSteps(a *roleAcc, x Sess, steps []Stp, seen map[string]bool) {
	for _, st := range steps {
		if isReplayedStep(x, st, seen) || st.Kind != "tool" || st.Tool == "" {
			continue
		}
		a.tools++
		if st.IsErr && !st.Denied {
			a.errs++
		}
		if st.Denied {
			a.denials++
		}
		sq, gr := countStepSearchVerbs(st)
		a.sqlite += sq
		a.grep += gr
	}
}

func finalizeAgentRoles(accs map[string]*roleAcc) []r.AgentRoleMetrics {
	if len(accs) == 0 {
		return nil
	}
	out := make([]r.AgentRoleMetrics, 0, len(accs))
	for _, a := range accs {
		m := r.AgentRoleMetrics{
			Role: a.role, Sessions: a.sessions, ToolCalls: a.tools, ToolErrors: a.errs, HookDenials: a.denials,
			SQLiteVerbs: a.sqlite, GrepCalls: a.grep, TurnVets: a.turnVets, TimedOutTurns: a.timedOut,
		}
		if a.tools > 0 {
			rate := r.Round4(float64(a.errs) / float64(a.tools))
			m.ToolErrorRate = &rate
		}
		if a.grep > 0 {
			ratio := r.Round4(float64(a.sqlite) / float64(a.grep))
			m.SearchGrepRatio = &ratio
		}
		if a.turnVets > 0 {
			pct := r.Round4(float64(a.timedOut) * 100.0 / float64(a.turnVets))
			m.TimeoutRatePct = &pct
		}
		out = append(out, m)
	}
	sortAgentRoles(out)
	return out
}

func sortAgentRoles(roles []r.AgentRoleMetrics) {
	sort.Slice(roles, func(i, j int) bool {
		if (roles[i].Role == "main") != (roles[j].Role == "main") {
			return roles[i].Role == "main"
		}
		if roles[i].Sessions != roles[j].Sessions {
			return roles[i].Sessions > roles[j].Sessions
		}
		if roles[i].ToolCalls != roles[j].ToolCalls {
			return roles[i].ToolCalls > roles[j].ToolCalls
		}
		return roles[i].Role < roles[j].Role
	})
}

func (d Deps) buildTagCohort(o Options, postAll []Sess, postData *ShowData, cutoff, now time.Time) (*r.TagCohort, error) {
	q, args := beforeCutoffShowQuery(o, cutoff)
	preAll, err := SessionsIn(d.DB, q, args)
	if err != nil {
		return nil, err
	}
	preData, err := LoadShowData(d.DB, preAll)
	if err != nil {
		return nil, err
	}
	var preSB, postSB *SecondBrainAcc
	if postData != nil {
		postSB = postData.SBAcc
	}
	if o.Label == "" {
		where, wargs := beforeCutoffProjectWhere("created_at", o, cutoff)
		if preSB, err = d.querySecondBrainWhere(where, wargs); err != nil {
			return nil, err
		}
	}
	cutDay := cutoff.UTC().Format("2006-01-02")
	nowDay := now.UTC().Format("2006-01-02")
	home := ResolveMetricsHome(o, d.StorePath)
	pre := d.summarizeCohort(preAll, preData.Steps, preSB, d.earliestSessionDay(preAll, o, home), cutDay, o, home)
	post := d.summarizeCohort(postAll, postData.Steps, postSB, cutDay, nowDay, o, home)
	return &r.TagCohort{
		Tag:    search.Printable(o.SinceTag),
		Cutoff: cutoff.UTC().Format(time.RFC3339),
		Pre:    pre,
		Post:   post,
	}, nil
}

func beforeCutoffShowQuery(o Options, cutoff time.Time) (string, []any) {
	rawTS := `COALESCE(started_at, ended_at, 0)`
	expr := `(CASE WHEN ` + rawTS + ` >= 100000000 AND ` + rawTS + ` < 100000000000 THEN ` + rawTS + ` * 1000 ELSE ` + rawTS + ` END)`
	w := expr + ` > 0 AND ` + expr + ` < ? AND vendor != ?`
	args := []any{cutoff.UnixMilli(), Historical}
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
	q := `SELECT id, vendor, COALESCE(project_root,''), COALESCE(parent_id,''), COALESCE(agent_type,''),
		COALESCE(model,''), COALESCE(label,''), COALESCE(outcome,''), COALESCE(run,''), COALESCE(started_at,0), COALESCE(ended_at,0), cost_usd
		FROM sessions WHERE ` + w + ` ORDER BY started_at`
	return q, args
}

func (d Deps) isSandboxRoot(root, home string) bool {
	return d.Roots.IsSandboxProjectRoot != nil && d.Roots.IsSandboxProjectRoot(root, home)
}

func (d Deps) earliestSessionDay(all []Sess, o Options, home string) string {
	var minTS int64
	for _, x := range all {
		if o.Project == "" && !o.IncludeSandboxes && d.isSandboxRoot(x.Root, home) {
			continue
		}
		ts := x.Start
		if ts == 0 {
			ts = x.End
		}
		ts = NormalizeMilli(ts)
		if ts > 0 && (minTS == 0 || ts < minTS) {
			minTS = ts
		}
	}
	if minTS == 0 {
		return ""
	}
	return time.UnixMilli(minTS).UTC().Format("2006-01-02")
}

func (d Deps) summarizeCohort(all []Sess, stepsByID map[string][]Stp, sbAcc *SecondBrainAcc, sinceStr, untilStr string, o Options, home string) r.CohortMetrics {
	cm := r.CohortMetrics{Since: sinceStr, Until: untilStr}
	seen := map[string]bool{}
	for _, x := range all {
		if x.Vendor == Historical || (o.Project == "" && !o.IncludeSandboxes && d.isSandboxRoot(x.Root, home)) {
			continue
		}
		if x.Parent == "" {
			cm.Sessions++
		}
		accumulateCohortSteps(&cm, x, stepsByID[x.ID], seen)
	}
	if cm.GrepCalls > 0 {
		ratio := r.Round4(float64(cm.SQLiteVerbs) / float64(cm.GrepCalls))
		cm.SearchGrepRatio = &ratio
	}
	if sbAcc != nil {
		cm.TurnVets = sbAcc.SB.TurnVets
		cm.PreInvocationTimedOut = sbAcc.SB.TimedOutTurns
		cm.PreInvocationTimeoutPct = sbAcc.SB.TimeoutRatePct
		cm.PreInvocationP50MS = sbAcc.SB.P50DurationMS
		cm.PreInvocationP95MS = sbAcc.SB.P95DurationMS
	}
	return cm
}

func accumulateCohortSteps(cm *r.CohortMetrics, x Sess, steps []Stp, seen map[string]bool) {
	var tracker BashReadTracker
	for _, st := range steps {
		if st.Kind == "compaction" {
			tracker.IsUnguardedSkip(st)
			continue
		}
		if isReplayedStep(x, st, seen) || st.Kind != "tool" || st.Tool == "" {
			continue
		}
		cm.ToolCalls++
		if tracker.IsUnguardedSkip(st) {
			cm.BashReadSkips++
		}
		sq, gr := countStepSearchVerbs(st)
		cm.SQLiteVerbs += sq
		cm.GrepCalls += gr
	}
}
