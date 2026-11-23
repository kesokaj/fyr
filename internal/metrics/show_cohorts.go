package metrics

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *Store) populateCohorts(sum *Summary, o ShowOptions, all []sess, data *showData, since, now time.Time) error {
	var convVets map[string][2]int
	if data != nil && data.sbAcc != nil {
		convVets = data.sbAcc.byConv
	}
	var steps map[string][]stp
	if data != nil {
		steps = data.steps
	}
	sum.AgentRoles = buildAgentRoles(all, steps, convVets)
	if o.SinceTag == "" || o.skipDeltas {
		return nil
	}
	tc, err := s.buildTagCohort(o, all, data, since, now)
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

func buildAgentRoles(all []sess, stepsByID map[string][]stp, convVets map[string][2]int) []AgentRoleMetrics {
	accs := map[string]*roleAcc{}
	seen := map[string]bool{}
	for _, x := range all {
		if x.vendor == historical {
			continue
		}
		role := normalizeAgentRole(x.agent)
		a := accs[role]
		if a == nil {
			a = &roleAcc{role: role}
			accs[role] = a
		}
		a.sessions++
		accumulateRoleSteps(a, x, stepsByID[x.id], seen)
		if cv, ok := convVets[x.id]; ok {
			a.turnVets += cv[0]
			a.timedOut += cv[1]
		}
	}
	return finalizeAgentRoles(accs)
}

func normalizeAgentRole(agent string) string {
	r := printable(strings.TrimSpace(agent))
	if r == "" {
		return "main"
	}
	return r
}

func accumulateRoleSteps(a *roleAcc, x sess, steps []stp, seen map[string]bool) {
	for _, st := range steps {
		if st.kind == "tool" || st.kind == "assistant" || st.kind == "generation" {
			key := x.vendor + "\x00" + x.label + "\x00" + st.ext
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		if st.kind != "tool" || st.tool == "" {
			continue
		}
		a.tools++
		if st.isErr && !st.denied {
			a.errs++
		}
		if st.denied {
			a.denials++
		}
		if st.tool == "Grep" {
			a.grep++
		}
		for i, v := range st.verbs {
			if isSQLiteVerb(v) {
				a.sqlite++
			} else if st.verbN(i) == 0 && isGrepVerb(v) {
				a.grep++
			}
		}
	}
}

func finalizeAgentRoles(accs map[string]*roleAcc) []AgentRoleMetrics {
	if len(accs) == 0 {
		return nil
	}
	out := make([]AgentRoleMetrics, 0, len(accs))
	for _, a := range accs {
		m := AgentRoleMetrics{
			Role: a.role, Sessions: a.sessions, ToolCalls: a.tools, ToolErrors: a.errs, HookDenials: a.denials,
			SQLiteVerbs: a.sqlite, GrepCalls: a.grep, TurnVets: a.turnVets, TimedOutTurns: a.timedOut,
		}
		if a.tools > 0 {
			r := round4(float64(a.errs) / float64(a.tools))
			m.ToolErrorRate = &r
		}
		if a.grep > 0 {
			r := round4(float64(a.sqlite) / float64(a.grep))
			m.SearchGrepRatio = &r
		}
		if a.turnVets > 0 {
			r := round4(float64(a.timedOut) * 100.0 / float64(a.turnVets))
			m.TimeoutRatePct = &r
		}
		out = append(out, m)
	}
	sortAgentRoles(out)
	return out
}

func sortAgentRoles(roles []AgentRoleMetrics) {
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

func (s *Store) buildTagCohort(o ShowOptions, postAll []sess, postData *showData, cutoff, now time.Time) (*TagCohort, error) {
	preAll, err := s.sessionsIn(beforeCutoffShowQuery(o, cutoff))
	if err != nil {
		return nil, err
	}
	preData, err := s.loadShowData(preAll)
	if err != nil {
		return nil, err
	}
	var preSB, postSB *secondBrainAcc
	if postData != nil {
		postSB = postData.sbAcc
	}
	if o.Label == "" {
		where, args := beforeCutoffProjectWhere("created_at", o, cutoff)
		if preSB, err = s.querySecondBrainWhere(where, args); err != nil {
			return nil, err
		}
	}
	cutDay := cutoff.UTC().Format("2006-01-02")
	nowDay := now.UTC().Format("2006-01-02")
	home := resolveMetricsHome(o, s.path)
	pre := summarizeCohort(preAll, preData.steps, preSB, earliestSessionDay(preAll, o, home), cutDay, o, home)
	post := summarizeCohort(postAll, postData.steps, postSB, cutDay, nowDay, o, home)
	return &TagCohort{
		Tag:    printable(o.SinceTag),
		Cutoff: cutoff.UTC().Format(time.RFC3339),
		Pre:    pre,
		Post:   post,
	}, nil
}

func beforeCutoffShowQuery(o ShowOptions, cutoff time.Time) (string, []any) {
	rawTS := `COALESCE(started_at, ended_at, 0)`
	expr := `(CASE WHEN ` + rawTS + ` >= 100000000 AND ` + rawTS + ` < 100000000000 THEN ` + rawTS + ` * 1000 ELSE ` + rawTS + ` END)`
	w := expr + ` > 0 AND ` + expr + ` < ? AND vendor != ?`
	args := []any{cutoff.UnixMilli(), historical}
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

func earliestSessionDay(all []sess, o ShowOptions, home string) string {
	var minTS int64
	for _, x := range all {
		if o.Project == "" && !o.IncludeSandboxes && isSandboxProjectRoot(x.root, home) {
			continue
		}
		ts := x.start
		if ts == 0 {
			ts = x.end
		}
		ts = normalizeMilli(ts)
		if ts > 0 && (minTS == 0 || ts < minTS) {
			minTS = ts
		}
	}
	if minTS == 0 {
		return ""
	}
	return time.UnixMilli(minTS).UTC().Format("2006-01-02")
}

func summarizeCohort(all []sess, stepsByID map[string][]stp, sbAcc *secondBrainAcc, sinceStr, untilStr string, o ShowOptions, home string) CohortMetrics {
	cm := CohortMetrics{Since: sinceStr, Until: untilStr}
	seen := map[string]bool{}
	for _, x := range all {
		if x.vendor == historical || (o.Project == "" && !o.IncludeSandboxes && isSandboxProjectRoot(x.root, home)) {
			continue
		}
		if x.parent == "" {
			cm.Sessions++
		}
		accumulateCohortSteps(&cm, x, stepsByID[x.id], seen)
	}
	if cm.GrepCalls > 0 {
		r := round4(float64(cm.SQLiteVerbs) / float64(cm.GrepCalls))
		cm.SearchGrepRatio = &r
	}
	if sbAcc != nil {
		cm.TurnVets = sbAcc.sb.TurnVets
		cm.PreInvocationTimedOut = sbAcc.sb.TimedOutTurns
		cm.PreInvocationTimeoutPct = sbAcc.sb.TimeoutRatePct
		cm.PreInvocationP50MS = sbAcc.sb.P50DurationMS
		cm.PreInvocationP95MS = sbAcc.sb.P95DurationMS
	}
	return cm
}

func accumulateCohortSteps(cm *CohortMetrics, x sess, steps []stp, seen map[string]bool) {
	var tracker bashReadTracker
	for _, st := range steps {
		if st.kind == "compaction" {
			tracker.isUnguardedSkip(st)
			continue
		}
		if st.kind == "tool" || st.kind == "assistant" || st.kind == "generation" {
			key := x.vendor + "\x00" + x.label + "\x00" + st.ext
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		if st.kind != "tool" || st.tool == "" {
			continue
		}
		cm.ToolCalls++
		if tracker.isUnguardedSkip(st) {
			cm.BashReadSkips++
		}
		if st.tool == "Grep" {
			cm.GrepCalls++
		}
		for i, v := range st.verbs {
			if isSQLiteVerb(v) {
				cm.SQLiteVerbs++
			} else if st.verbN(i) == 0 && isGrepVerb(v) {
				cm.GrepCalls++
			}
		}
	}
}

func tagCohortSection(tc *TagCohort) reportSection {
	tag := printable(tc.Tag)
	t := table{
		Head: []string{"Metric", "Pre-tag (< " + tag + ")", "Post-tag (>= " + tag + ")"},
		Rows: [][]string{
			{"Sessions", fmt.Sprint(tc.Pre.Sessions), fmt.Sprint(tc.Post.Sessions)},
			{"Tool calls", fmt.Sprint(tc.Pre.ToolCalls), fmt.Sprint(tc.Post.ToolCalls)},
			{"Search : grep ratio", fmtCohortSearchGrep(tc.Pre), fmtCohortSearchGrep(tc.Post)},
			{"Bash read bypasses", fmt.Sprint(tc.Pre.BashReadSkips), fmt.Sprint(tc.Post.BashReadSkips)},
			{"PreInvocation timeout %", fmtPctRatio(tc.Pre.PreInvocationTimeoutPct, tc.Pre.PreInvocationTimedOut, tc.Pre.TurnVets), fmtPctRatio(tc.Post.PreInvocationTimeoutPct, tc.Post.PreInvocationTimedOut, tc.Post.TurnVets)},
			{"PreInvocation latency (p50 / p95)", fmtP50P95MS(tc.Pre.PreInvocationP50MS, tc.Pre.PreInvocationP95MS), fmtP50P95MS(tc.Post.PreInvocationP50MS, tc.Post.PreInvocationP95MS)},
		},
	}
	return reportSection{
		Title: "Tag Cohort (" + tag + ")",
		Note:  fmt.Sprintf("Pre-tag history (< %s) vs post-tag window (>= %s).", tc.Cutoff, tc.Cutoff),
		Table: t,
	}
}

func agentRolesSection(roles []AgentRoleMetrics) reportSection {
	t := table{Head: []string{"Role", "Sessions", "Tool calls", "Tool error %", "Hook denials", "Search : grep", "PreInvocation timeouts"}}
	for _, r := range roles {
		t.Rows = append(t.Rows, []string{
			printable(r.Role),
			fmt.Sprint(r.Sessions),
			fmt.Sprint(r.ToolCalls),
			fmtScaledFloatPtr(r.ToolErrorRate, 100.0, "%.2f%%"),
			fmt.Sprint(r.HookDenials),
			fmtRatioCounts(r.SearchGrepRatio, r.SQLiteVerbs, r.GrepCalls),
			fmtPctRatio(r.TimeoutRatePct, r.TimedOutTurns, r.TurnVets),
		})
	}
	return reportSection{
		Title: "Agent Roles",
		Note:  "Per-role activity across filtered sessions (empty agent_type normalized to main; hook denials excluded from tool errors).",
		Table: t,
	}
}

func fmtCohortSearchGrep(c CohortMetrics) string {
	return fmtRatioCounts(c.SearchGrepRatio, c.SQLiteVerbs, c.GrepCalls)
}

func fmtRatioCounts(ratio *float64, num, den int) string {
	if ratio == nil {
		return fmt.Sprintf("n/a (%d : %d)", num, den)
	}
	return fmt.Sprintf("%.2f (%d : %d)", *ratio, num, den)
}

func fmtScaledFloatPtr(p *float64, scale float64, format string) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf(format, *p*scale)
}
