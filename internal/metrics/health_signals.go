package metrics

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func (s *Store) populateHealthAndFallbacks(sum *Summary, o ShowOptions, all []sess, trials []trial, data *showData, since, now time.Time) error {
	home := resolveMetricsHome(o, s.path)
	if !o.skipDeltas && o.Project == "" && o.Label == "" && home != "" && !hasEvalPassK(sum.A.PassK) {
		mergeDevEvalPassK(sum, home, since, now)
	}
	if err := s.populateJudgeVerdictsHealth(sum, o, data.waves, since, now); err != nil {
		return err
	}
	overBudget := collectStepHealth(&sum.Health, sum, all, data.steps, home)
	if err := s.populateModelFamilyHealth(&sum.Health, o, since, now); err != nil {
		return err
	}
	reconcileAutoModelRuns(sum, home, o, trials, data.waves, since, now)
	if !o.skipDeltas && home != "" {
		readModelsQuotaHealth(&sum.Health, home)
	}
	if o.Label == "" && !o.skipDeltas {
		inspectProjectSearchDBs(sum, home, o, all, overBudget)
		populateAutonomySignals(sum, home, o, all)
	}
	finalizeHealthSignals(sum, overBudget)
	if !o.skipDeltas && home != "" && len(sum.Projects) > 0 {
		enrichProjectsFromBrainDB(sum.Projects, home, now)
	}
	redactProjectRowHomes(sum.Projects)
	return nil
}

func hasEvalPassK(m map[string]PassK) bool {
	for k := range m {
		if strings.HasPrefix(k, evalPrefix) {
			return true
		}
	}
	return false
}

func redactProjectRowHomes(projects []ProjectRow) {
	for i := range projects {
		projects[i].Project = redactHomePath(projects[i].Project)
	}
}

func finalizeHealthSignals(sum *Summary, overBudget map[string]bool) {
	sum.Health.SubagentsOverBudget = len(overBudget)
	sum.TriggerCoverage.SessionsOverKnee = sum.Health.SessionsOverKnee
	sum.TriggerCoverage.MainSessions = sum.Health.MainSessions
}

func reconcileAutoModelRuns(sum *Summary, home string, o ShowOptions, trials []trial, waves []waveRow, since, now time.Time) {
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
		sum.TriggerCoverage.AutoModelRunsPerWeek = round4(float64(total) / (d.Hours() / (24 * 7)))
	}
}

func countDBAutoModelRuns(trials []trial, waves []waveRow) (int, map[string]int) {
	total := 0
	byDay := map[string]int{}
	for _, tr := range trials {
		if strings.HasPrefix(tr.label, "auto=") {
			total++
			if tr.start > 0 {
				byDay[time.UnixMilli(normalizeMilli(tr.start)).UTC().Format("2006-01-02")]++
			}
		}
	}
	for _, w := range waves {
		if strings.HasPrefix(strings.TrimSpace(w.worker), "auto-") {
			total++
			if w.updatedAt > 0 {
				byDay[time.UnixMilli(normalizeMilli(w.updatedAt)).UTC().Format("2006-01-02")]++
			}
		}
	}
	return total, byDay
}

func readAutoRunsHealth(sum *Summary, home string, o ShowOptions, since, now time.Time) (int, string, bool) {
	data, ok := readBoundedRegularFile(home, harness.StateDir, "auto", "runs.json")
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
	sum.Health.AutoLastVerdict = printable(strings.TrimSpace(st.Verdict))
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

func readModelsQuotaHealth(h *HealthSignals, home string) {
	data, ok := readBoundedRegularFile(home, harness.StateDir, "models.json")
	if !ok {
		return
	}
	var cat struct {
		QuotaUpdatedAt string      `json:"quota_updated_at"`
		Pools          []PoolQuota `json:"pools"`
	}
	if json.Unmarshal(data, &cat) != nil {
		return
	}
	h.QuotaUpdatedAt = printable(strings.TrimSpace(cat.QuotaUpdatedAt))
	for _, pool := range cat.Pools {
		h.PoolQuotas = append(h.PoolQuotas, PoolQuota{Name: printable(pool.Name), Five: pool.Five, Week: pool.Week})
	}
}

func distinctProjectRoots(o ShowOptions, home string, all []sess) []string {
	if o.Project != "" {
		return []string{filepath.Clean(o.Project)}
	}
	set := map[string]bool{}
	for _, x := range all {
		if r := strings.TrimSpace(x.root); r != "" {
			clean := filepath.Clean(r)
			if !o.IncludeSandboxes && isSandboxProjectRoot(clean, home) {
				continue
			}
			set[clean] = true
		}
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func indexSessionsByID(all []sess) (map[string]bool, map[string]string) {
	main, sub := map[string]bool{}, map[string]string{}
	for _, x := range all {
		if x.parent == "" {
			main[x.id] = true
		} else {
			sub[x.id] = x.agent
		}
	}
	return main, sub
}

func querySearchDBSessionMeta(db *sql.DB, knownMain map[string]bool, knownSub map[string]string, overBudget map[string]bool) int {
	cols := tableCols(db, "session_meta")
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
		return subagentCapForRole(ka), true
	}
	lowID, lowAgent := strings.ToLower(id), strings.ToLower(strings.TrimSpace(agent))
	if isBuilderOrSelfRole(lowAgent) || strings.Contains(lowID, "self") || isBuilderLikeID(lowID) {
		return 120, true
	}
	if parent != "" || lowAgent != "" || isSubagentLikeID(lowID) || !hasKnownMain {
		return 40, true
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

func enrichProjectsFromBrainDB(projects []ProjectRow, home string, now time.Time) {
	db, cleanup, ok := openCopySQLiteFile(home, home, harness.StateDir, "brain.db")
	if !ok {
		return
	}
	defer cleanup()
	cols := tableCols(db, "records")
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
