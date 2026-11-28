package show

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	r "github.com/kesokaj/fyr/internal/metrics/report"
	"github.com/kesokaj/fyr/internal/search"
)

const evalLabelPrefix = "eval="

func (d Deps) populateHealthAndFallbacks(sum *Summary, o Options, all []Sess, trials []Trial, data *ShowData, since, now time.Time) error {
	home := ResolveMetricsHome(o, d.StorePath)
	if !o.SkipDeltas && o.Project == "" && o.Label == "" && home != "" && !hasEvalPassK(sum.A.PassK) && d.MergeDevEvalPassK != nil {
		d.MergeDevEvalPassK(sum, home, since, now)
	}
	if d.PopulateJudgeVerdictsHealth != nil {
		if err := d.PopulateJudgeVerdictsHealth(sum, o, data.Waves, since, now); err != nil {
			return err
		}
	}
	overBudget := CollectStepHealth(&sum.Health, sum, all, data.Steps, home)
	if err := d.populateModelFamilyHealth(&sum.Health, o, since, now); err != nil {
		return err
	}
	reconcileAutoModelRuns(sum, home, o, trials, data.Waves, since, now)
	if !o.SkipDeltas && home != "" {
		readModelsQuotaHealth(&sum.Health, home)
	}
	if o.Label == "" && !o.SkipDeltas {
		d.inspectProjectSearchDBs(sum, home, o, all, overBudget)
		if d.PopulateAutonomySignals != nil {
			d.PopulateAutonomySignals(sum, home, o, all)
		}
	}
	finalizeHealthSignals(sum, overBudget)
	if !o.SkipDeltas && home != "" && len(sum.Projects) > 0 {
		enrichProjectsFromBrainDB(sum.Projects, home, now)
	}
	redactProjectRowHomes(sum.Projects)
	return nil
}

func hasEvalPassK(m map[string]r.PassK) bool {
	for k := range m {
		if strings.HasPrefix(k, evalLabelPrefix) {
			return true
		}
	}
	return false
}

func redactProjectRowHomes(projects []r.ProjectRow) {
	for i := range projects {
		projects[i].Project = r.RedactHomePath(projects[i].Project)
	}
}

func finalizeHealthSignals(sum *Summary, overBudget map[string]bool) {
	sum.Health.SubagentsOverBudget = len(overBudget)
	sum.TriggerCoverage.SessionsOverKnee = sum.Health.SessionsOverKnee
	sum.TriggerCoverage.MainSessions = sum.Health.MainSessions
}

func reconcileAutoModelRuns(sum *Summary, home string, o Options, trials []Trial, waves []WaveRow, since, now time.Time) {
	dbTotal, dbByDay := countDBAutoModelRuns(trials, waves)
	total := dbTotal
	if home != "" {
		if runs, stateDay, ok := readAutoRunsHealth(sum, home, o, since, now); ok {
			onDay := dbByDay[stateDay]
			total = (dbTotal - onDay) + max(runs, onDay)
		}
	}
	sum.Health.AutoModelRuns = total
	sum.TriggerCoverage.AutoModelRuns = total
	if d := now.Sub(since); d > 0 && total > 0 {
		sum.TriggerCoverage.AutoModelRunsPerWeek = r.Round4(float64(total) / (d.Hours() / (24 * 7)))
	}
}

func countDBAutoModelRuns(trials []Trial, waves []WaveRow) (int, map[string]int) {
	total := 0
	byDay := map[string]int{}
	for _, tr := range trials {
		if strings.HasPrefix(tr.Label, "auto=") {
			total++
			if tr.Start > 0 {
				byDay[time.UnixMilli(NormalizeMilli(tr.Start)).UTC().Format("2006-01-02")]++
			}
		}
	}
	for _, w := range waves {
		if strings.HasPrefix(strings.TrimSpace(w.Worker), "auto-") {
			total++
			if w.UpdatedAt > 0 {
				byDay[time.UnixMilli(NormalizeMilli(w.UpdatedAt)).UTC().Format("2006-01-02")]++
			}
		}
	}
	return total, byDay
}

func readAutoRunsHealth(sum *Summary, home string, o Options, since, now time.Time) (int, string, bool) {
	data, ok := r.ReadBoundedRegularFile(home, harness.StateDir, "auto", "runs.json")
	if !ok {
		return 0, "", false
	}
	var st struct {
		Date      string                     `json:"date"`
		ModelRuns int                        `json:"model_runs"`
		LastRun   time.Time                  `json:"last_run"`
		Verdict   string                     `json:"verdict"`
		OpenAsks  int                        `json:"open_asks"`
		Projects  map[string]json.RawMessage `json:"projects"`
	}
	if json.Unmarshal(data, &st) != nil {
		return 0, "", false
	}
	sum.Health.AutoRunsPresent = true
	sum.Health.AutoOpenAsks = max(0, st.OpenAsks)
	sum.Health.AutoLastVerdict = search.Printable(strings.TrimSpace(st.Verdict))
	sum.Health.AutoProjectCount = len(st.Projects)
	if o.Label != "" || o.Project != "" || !autoStateInWindow(st.LastRun, st.Date, since, now) {
		return 0, "", false
	}
	day := strings.TrimSpace(st.Date)
	if !st.LastRun.IsZero() {
		day = st.LastRun.UTC().Format("2006-01-02")
	}
	return max(0, st.ModelRuns), day, true
}

func autoStateInWindow(lastRun time.Time, date string, since, now time.Time) bool {
	if !lastRun.IsZero() {
		return !lastRun.Before(since) && lastRun.Before(now)
	}
	if strings.TrimSpace(date) != "" {
		d, err := time.Parse("2006-01-02", strings.TrimSpace(date))
		return err == nil && d.Add(24*time.Hour).After(since) && d.Before(now)
	}
	return false
}

func readModelsQuotaHealth(h *r.HealthSignals, home string) {
	data, ok := r.ReadBoundedRegularFile(home, harness.StateDir, "models.json")
	if !ok {
		return
	}
	var cat struct {
		QuotaUpdatedAt string        `json:"quota_updated_at"`
		Pools          []r.PoolQuota `json:"pools"`
	}
	if json.Unmarshal(data, &cat) != nil {
		return
	}
	h.QuotaUpdatedAt = search.Printable(strings.TrimSpace(cat.QuotaUpdatedAt))
	for _, pool := range cat.Pools {
		h.PoolQuotas = append(h.PoolQuotas, r.PoolQuota{Name: search.Printable(pool.Name), Five: pool.Five, Week: pool.Week})
	}
}

// DistinctProjectRoots returns sorted unique project roots across all sessions.
func DistinctProjectRoots(o Options, home string, all []Sess, isSandbox func(root, home string) bool) []string {
	if o.Project != "" {
		return []string{filepath.Clean(o.Project)}
	}
	set := map[string]bool{}
	for _, x := range all {
		if root := strings.TrimSpace(x.Root); root != "" {
			clean := filepath.Clean(root)
			if !o.IncludeSandboxes && isSandbox != nil && isSandbox(clean, home) {
				continue
			}
			set[clean] = true
		}
	}
	out := make([]string, 0, len(set))
	for root := range set {
		out = append(out, root)
	}
	sort.Strings(out)
	return out
}

func indexSessionsByID(all []Sess) (map[string]bool, map[string]string) {
	main, sub := map[string]bool{}, map[string]string{}
	for _, x := range all {
		if x.Parent == "" {
			main[x.ID] = true
		} else {
			sub[x.ID] = x.Agent
		}
	}
	return main, sub
}

func querySearchDBSessionMeta(db *sql.DB, knownMain map[string]bool, knownSub map[string]string, overBudget map[string]bool) int {
	cols := r.TableCols(db, "session_meta")
	if len(cols) == 0 {
		return 0
	}
	compactions := 0
	if cols["generation_id"] {
		_ = db.QueryRow(`SELECT COALESCE(SUM(CASE WHEN generation_id > 0 THEN generation_id ELSE 0 END), 0) FROM session_meta`).Scan(&compactions)
	}
	if toolCol := pickFirstCol(cols, "tool_calls", "tool_count"); toolCol != "" && cols["conversation_id"] {
		scanSessionMetaBudgets(db, cols, toolCol, knownMain, knownSub, overBudget)
	}
	return compactions
}

func scanSessionMetaBudgets(db *sql.DB, cols map[string]bool, toolCol string, knownMain map[string]bool, knownSub map[string]string, overBudget map[string]bool) {
	q := fmt.Sprintf(`SELECT conversation_id, COALESCE(%s, 0), %s, %s FROM session_meta`,
		toolCol, pickColExpr(cols, "agent_type", "agent_name", "subagent"), pickColExpr(cols, "parent_id", "parent_conversation_id"))
	rows, err := db.Query(q)
	if err != nil {
		return
	}
	defer rows.Close()
	var found []string
	for rows.Next() {
		var id, agent, parent string
		var tools int
		if rows.Scan(&id, &tools, &agent, &parent) != nil || id == "" || id == "default" || knownMain[id] {
			continue
		}
		if capLimit, ok := subagentBudgetCap(id, agent, parent, knownSub, len(knownMain) > 0); ok && tools > capLimit {
			found = append(found, id)
		}
	}
	if rows.Err() != nil {
		return
	}
	for _, id := range found {
		overBudget[id] = true
	}
}

func subagentBudgetCap(id, agent, parent string, knownSub map[string]string, hasKnownMain bool) (int, bool) {
	if ka, ok := knownSub[id]; ok {
		return SubagentCapForRole(ka), true
	}
	lowID, lowAgent := strings.ToLower(id), strings.ToLower(strings.TrimSpace(agent))
	if lowAgent != "" {
		return SubagentCapForRole(lowAgent), true
	}
	if strings.Contains(lowID, "self") || isBuilderLikeID(lowID) || strings.Contains(lowID, "coder") || strings.Contains(lowID, "worker") {
		return BuilderSubagentCap, true
	}
	if parent != "" || isSubagentLikeID(lowID) || !hasKnownMain {
		return AuditSubagentCap, true
	}
	return 0, false
}

func isBuilderLikeID(low string) bool {
	return low == "builder" ||
		strings.HasPrefix(low, "builder-") ||
		strings.HasPrefix(low, "builder_") ||
		strings.Contains(low, "-builder-") ||
		strings.Contains(low, "_builder_") ||
		strings.HasSuffix(low, "-builder") ||
		strings.HasSuffix(low, "_builder")
}

func isSubagentLikeID(low string) bool {
	for _, tok := range []string{"reviewer", "security", "verifier", "explorer", "dreamer", "documentation", "research", "subagent", "sub-", "sub_"} {
		if strings.Contains(low, tok) {
			return true
		}
	}
	return false
}

func enrichProjectsFromBrainDB(projects []r.ProjectRow, home string, now time.Time) {
	db, cleanup, ok := r.OpenCopySQLiteFile(home, home, harness.StateDir, "brain.db")
	if !ok {
		return
	}
	defer cleanup()
	cols := r.TableCols(db, "records")
	if !cols["scope"] || !cols["kind"] || !cols["status"] || !cols["date"] || !cols["id"] || !cols["title"] || !cols["body"] {
		return
	}
	for i := range projects {
		proj := filepath.Clean(projects[i].Project)
		projects[i].AutoFindings = countBrainAutoFindings(db, cols, proj)
		projects[i].HandoffLagDays = queryBrainHandoffLag(db, proj, now)
	}
}

func countBrainAutoFindings(db *sql.DB, cols map[string]bool, proj string) int {
	scopeCond, args := `scope = ?`, []any{proj}
	if cols["project"] {
		scopeCond, args = `(scope = ? OR (scope = '' AND project = ?))`, append(args, proj)
	}
	provCond := `status = 'open'`
	if cols["provenance"] {
		provCond = `(status = 'open' OR (kind = 'proposal' AND status = 'queued')) AND provenance = 'derived'`
	}
	q := fmt.Sprintf(`SELECT COUNT(*) FROM records WHERE %s AND ((kind = 'review' AND (id LIKE 'REVIEW-%%auto%%' OR title LIKE '%%auto%%') AND body NOT LIKE '%%## Outcome%%') OR (kind IN ('bug', 'proposal') AND %s))`, scopeCond, provCond)
	var cnt int
	_ = db.QueryRow(q, args...).Scan(&cnt)
	return cnt
}

func queryBrainHandoffLag(db *sql.DB, proj string, now time.Time) *int {
	rows, err := db.Query(`SELECT date FROM records WHERE scope = ? AND kind = 'handoff'`, proj)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var newest time.Time
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil || len(raw) < 10 {
			continue
		}
		if t, err := time.ParseInLocation("2006-01-02", raw[:10], time.UTC); err == nil && (newest.IsZero() || t.After(newest)) {
			newest = t
		}
	}
	if rows.Err() != nil || newest.IsZero() {
		return nil
	}
	days := max(0, int(now.UTC().Sub(newest).Hours()/24))
	return &days
}

func pickFirstCol(cols map[string]bool, names ...string) string {
	for _, n := range names {
		if cols[n] {
			return n
		}
	}
	return ""
}

func pickColExpr(cols map[string]bool, names ...string) string {
	if c := pickFirstCol(cols, names...); c != "" {
		return fmt.Sprintf("COALESCE(%s, '')", c)
	}
	return "''"
}
