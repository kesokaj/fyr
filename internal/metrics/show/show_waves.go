package show

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	r "github.com/kesokaj/fyr/internal/metrics/report"
	"github.com/kesokaj/fyr/internal/search"
)

func (d Deps) waveMatchesLabel(stamp string, o Options) bool {
	var lbl string
	if d.Roots.DecodeWaveStamp != nil {
		lbl, _ = d.Roots.DecodeWaveStamp(stamp)
	}
	if o.Label != "" {
		return lbl == o.Label
	}
	if o.AllLabels {
		return true
	}
	return lbl == ""
}

func (d Deps) queryWaveRows(o Options, since, now time.Time) ([]WaveRow, error) {
	where, args := WindowAndProjectWhere("updated_at", o, since, now)
	rows, err := d.DB.Query(`SELECT stamp, worker, project_root, state, model, base_check, judge_model, judge_verdict, updated_at FROM work_waves WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WaveRow
	for rows.Next() {
		var w WaveRow
		if err := rows.Scan(&w.Stamp, &w.Worker, &w.ProjectRoot, &w.State, &w.Model, &w.BaseCheck, &w.JudgeModel, &w.JudgeVerdict, &w.UpdatedAt); err != nil {
			return nil, err
		}
		if d.waveMatchesLabel(w.Stamp, o) {
			out = append(out, w)
		}
	}
	return out, rows.Err()
}

// SummarizeWorkWaves rolls up wave rows into a WorkSummary.
func SummarizeWorkWaves(waves []WaveRow) r.WorkSummary {
	var ws r.WorkSummary
	stamps := map[string]bool{}
	for _, w := range waves {
		if w.Stamp != "" {
			stamps[w.Stamp] = true
		}
		ws.Workers++
		AddWaveWorkerCounts(&ws, w.State, w.Model, w.BaseCheck, w.JudgeModel, w.JudgeVerdict)
	}
	ws.Waves = len(stamps)
	return ws
}

// AddWaveWorkerCounts tallies one wave worker row into ws.
func AddWaveWorkerCounts(ws *r.WorkSummary, state, workerModel, baseCheck, judgeModel, judgeVerdict string) {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "PASS":
		ws.Passed++
	case "FAIL":
		ws.Failed++
	}
	bc := strings.ToUpper(strings.TrimSpace(baseCheck))
	if bc == "PASS" {
		ws.BaseCheckPassed++
	} else if strings.Contains(bc, "FAIL") {
		ws.BaseCheckFailed++
	}
	jv := strings.ToUpper(strings.TrimSpace(judgeVerdict))
	if jv == "PASS" {
		ws.JudgePassed++
	} else if strings.Contains(jv, "FAIL") {
		ws.JudgeFailed++
	}
	if strings.TrimSpace(judgeModel) != "" && SameModelFamily(workerModel, judgeModel) {
		ws.JudgeFallback++
	}
}

// SameModelFamily reports whether workerModel and judgeModel belong to the same provider family.
func SameModelFamily(workerModel, judgeModel string) bool {
	wf := ClassifyModelFamily(workerModel)
	jf := ClassifyModelFamily(judgeModel)
	if wf == "" || jf == "" || wf != jf {
		return false
	}
	if wf == "other" {
		return strings.EqualFold(strings.TrimSpace(workerModel), strings.TrimSpace(judgeModel))
	}
	return true
}

// NormJudgeVerdict normalizes judge verdict text to "PASS", "FAIL", or "".
func NormJudgeVerdict(raw string) string {
	v := strings.ToUpper(strings.TrimSpace(raw))
	switch {
	case v == "PASS":
		return "PASS"
	case strings.Contains(v, "FAIL"):
		return "FAIL"
	default:
		return ""
	}
}

func (d Deps) populateOrchestrationSummary(sum *Summary, o Options, since, now time.Time) error {
	if o.Label != "" || d.ScanOrchestrationRuns == nil {
		return nil
	}
	rows, err := d.queryOrchestrationRunsForShow(o, since, now)
	if err != nil {
		return err
	}
	sum.Orchestration = summarizeOrchestrationRuns(rows)
	return nil
}

func (d Deps) queryOrchestrationRunsForShow(o Options, since, now time.Time) ([]OrchestrationRunRow, error) {
	if len(r.TableCols(d.DB, "orchestration_runs")) == 0 {
		return nil, nil
	}
	conds := []string{`ts >= ?`, `ts < ?`}
	args := []any{since.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339)}
	if o.Project != "" {
		p := filepath.Clean(o.Project)
		var rid string
		if d.Roots.ResolveRepoID != nil {
			rid = d.Roots.ResolveRepoID(p)
		}
		if rid != "" {
			conds = append(conds, `(repo_id = ? OR project_root = ? OR substr(project_root, 1, length(?)) = ?)`)
			args = append(args, rid, p, p+"/", p+"/")
		} else {
			conds = append(conds, `(project_root = ? OR substr(project_root, 1, length(?)) = ?)`)
			args = append(args, p, p+"/", p+"/")
		}
	}
	q := `SELECT id, ts, kind, COALESCE(project_root, ''), COALESCE(repo_id, ''), status, COALESCE(halt_code, ''), units, defect_rounds, passed, failed, duration_ms, COALESCE(worker_model, ''), COALESCE(judge_model, ''), COALESCE(verdict, '') FROM orchestration_runs WHERE ` + strings.Join(conds, ` AND `) + ` ORDER BY ts DESC, id DESC`
	return d.ScanOrchestrationRuns(d.DB, q, args)
}

func summarizeOrchestrationRuns(rows []OrchestrationRunRow) *r.OrchestrationSummary {
	if len(rows) == 0 {
		return nil
	}
	out := &r.OrchestrationSummary{
		Runs:     len(rows),
		ByKind:   map[string]int{},
		ByStatus: map[string]int{},
		Halts:    map[string]int{},
	}
	var durSum int64
	for _, row := range rows {
		if k := search.Printable(strings.TrimSpace(row.Kind)); k != "" {
			out.ByKind[k]++
		}
		if st := search.Printable(strings.TrimSpace(row.Status)); st != "" {
			out.ByStatus[st]++
		}
		out.Units += max(0, row.Units)
		out.DefectRounds += max(0, row.DefectRounds)
		out.Succeeded += max(0, row.Passed)
		out.Failed += max(0, row.Failed)
		durSum += max(int64(0), row.DurationMS)
		switch NormJudgeVerdict(row.Verdict) {
		case "PASS":
			out.JudgeOK++
		case "FAIL":
			out.JudgeFailed++
		}
		if hc := search.Printable(strings.TrimSpace(row.HaltCode)); hc != "" {
			out.Halts[hc]++
		}
	}
	out.MeanDurationMS = r.Round4(float64(durSum) / float64(len(rows)))
	if len(out.Halts) == 0 {
		out.Halts = nil
	}
	return out
}

func (d Deps) populateSIMSummary(sum *Summary, home string, o Options) {
	if strings.TrimSpace(home) == "" || o.Label != "" || o.SkipDeltas || d.BrainHasSIMTables == nil {
		return
	}
	db, cleanup, ok := r.OpenCopySQLiteFile(home, home, harness.StateDir, "brain.db")
	if !ok {
		return
	}
	defer cleanup()
	if !d.BrainHasSIMTables(db) {
		return
	}
	sum.SIM = querySIMSummaryDB(db)
}

func querySIMSummaryDB(db *sql.DB) *r.SIMSummary {
	out := &r.SIMSummary{
		Available:         true,
		AttemptsByVerdict: map[string]int{},
		FieldStatusCounts: map[string]int{},
	}
	scanSIMProblemsSummary(db, out)
	scanSIMAttemptsSummary(db, out)
	if len(out.AttemptsByVerdict) == 0 {
		out.AttemptsByVerdict = nil
	}
	if len(out.FieldStatusCounts) == 0 {
		out.FieldStatusCounts = nil
	}
	return out
}

func scanSIMProblemsSummary(db *sql.DB, out *r.SIMSummary) {
	rows, err := db.Query(`SELECT COALESCE(status, ''), COALESCE(mechanism_class, '') FROM sim_problems`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var status, mech string
		if rows.Scan(&status, &mech) != nil {
			continue
		}
		st := strings.ToLower(strings.TrimSpace(status))
		mc := strings.ToLower(strings.TrimSpace(mech))
		switch st {
		case "active":
			out.ActiveProblems++
		case "resolved":
			out.ResolvedProblems++
		case "merged":
			out.MergedProblems++
		}
		if mc == "model_workaround" || strings.Contains(mc, "workaround") {
			out.Workarounds++
			if st == "active" {
				out.ActiveWorkarounds++
			}
		}
	}
}

func scanSIMAttemptsSummary(db *sql.DB, out *r.SIMSummary) {
	rows, err := db.Query(`SELECT COALESCE(eval_verdict, ''), COALESCE(field_status, '') FROM sim_attempts`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var verdict, fieldSt string
		if rows.Scan(&verdict, &fieldSt) != nil {
			continue
		}
		out.Attempts++
		if v := search.Printable(strings.ToLower(strings.TrimSpace(verdict))); v != "" {
			out.AttemptsByVerdict[v]++
		}
		if fs := search.Printable(strings.ToLower(strings.TrimSpace(fieldSt))); fs != "" {
			out.FieldStatusCounts[fs]++
		}
	}
}

type skillTelemetryAcc struct {
	skill          string
	suggestedTurns int
	suggestedConvs map[string]bool
	convertedConvs map[string]bool
	loaded         int
	sessions       int
	ratedSessions  int
	outcomeSum     int
	toolCalls      int
	toolErrs       int
	rewriteCount   int
	newSkillCount  int
}

func getSkillAcc(m map[string]*skillTelemetryAcc, raw string) *skillTelemetryAcc {
	name := search.Printable(strings.TrimSpace(raw))
	if name == "" {
		return nil
	}
	a := m[name]
	if a == nil {
		a = &skillTelemetryAcc{
			skill:          name,
			suggestedConvs: map[string]bool{},
			convertedConvs: map[string]bool{},
		}
		m[name] = a
	}
	return a
}

func (d Deps) populateSkillTelemetry(sum *Summary, o Options, all []Sess, data *ShowData, since, now time.Time) error {
	if o.Label != "" {
		return nil
	}
	accs := map[string]*skillTelemetryAcc{}
	if err := d.collectSuggestedSkills(accs, o, since, now); err != nil {
		return err
	}
	loadedByConv := accumulateSessionSkillSteps(accs, all, data)
	if err := d.applySkillRatingsTelemetry(accs, loadedByConv, o, since, now); err != nil {
		return err
	}
	sum.SkillTelemetry = finalizeSkillTelemetry(accs)
	return nil
}

func (d Deps) collectSuggestedSkills(accs map[string]*skillTelemetryAcc, o Options, since, now time.Time) error {
	if !r.TableCols(d.DB, "turn_vets")["suggested_skills"] {
		return nil
	}
	where, args := suggestedSkillsWhere(o, since, now)
	filterSandboxes := o.Project == "" && !o.IncludeSandboxes
	home := ResolveMetricsHome(o, d.StorePath)
	rows, err := d.DB.Query(`SELECT COALESCE(project_root, ''), COALESCE(conversation_id, ''), COALESCE(suggested_skills, '') FROM turn_vets WHERE `+where+` AND TRIM(COALESCE(suggested_skills, '')) != ''`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var root, convID, raw string
		if err := rows.Scan(&root, &convID, &raw); err != nil {
			return err
		}
		if filterSandboxes && d.isEphemeralTurnVetRoot(root, home) {
			continue
		}
		recordSuggestedSkillsRow(accs, raw, strings.TrimSpace(convID))
	}
	return rows.Err()
}

func suggestedSkillsWhere(o Options, since, now time.Time) (string, []any) {
	where, args := WindowAndProjectWhere("created_at", o, since, now)
	if o.Label == "" && !o.AllLabels {
		where += excludeLabeledTurnVetsSQL
	}
	return where, args
}

func recordSuggestedSkillsRow(accs map[string]*skillTelemetryAcc, raw, cid string) {
	for _, sk := range parseSuggestedSkills(raw) {
		a := getSkillAcc(accs, sk)
		if a == nil {
			continue
		}
		a.suggestedTurns++
		if cid != "" {
			a.suggestedConvs[cid] = true
		}
	}
}

func parseSuggestedSkills(raw string) []string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	var items []string
	if strings.HasPrefix(s, "[") {
		if json.Unmarshal([]byte(s), &items) == nil {
			return dedupTrimSkills(items)
		}
	}
	parts := strings.FieldsFunc(s, func(ch rune) bool { return ch == ',' || ch == ';' || ch == '\n' })
	return dedupTrimSkills(parts)
}

func dedupTrimSkills(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range in {
		name := search.Printable(strings.TrimSpace(raw))
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func accumulateSessionSkillSteps(accs map[string]*skillTelemetryAcc, all []Sess, data *ShowData) map[string]map[string]bool {
	loadedByConv := map[string]map[string]bool{}
	if data == nil {
		return loadedByConv
	}
	seen := map[string]bool{}
	for _, x := range all {
		if x.Vendor == Historical {
			continue
		}
		sessSkills, sessTools, sessErrs := scanSessionSkillLoads(x, data.Steps[x.ID], seen)
		if len(sessSkills) == 0 {
			continue
		}
		loadedByConv[x.ID] = map[string]bool{}
		for sk, cnt := range sessSkills {
			a := getSkillAcc(accs, sk)
			if a == nil {
				continue
			}
			a.loaded += cnt
			a.sessions++
			a.toolCalls += sessTools
			a.toolErrs += sessErrs
			loadedByConv[x.ID][sk] = true
			if a.suggestedConvs[x.ID] {
				a.convertedConvs[x.ID] = true
			}
		}
	}
	return loadedByConv
}

func scanSessionSkillLoads(x Sess, steps []Stp, seen map[string]bool) (map[string]int, int, int) {
	loads := map[string]int{}
	tools, errs := 0, 0
	for _, st := range steps {
		if isReplayedStep(x, st, seen) || st.Kind != "tool" || st.Tool == "" {
			continue
		}
		tools++
		if st.IsErr && !st.Denied {
			errs++
		}
		if st.Skill != "" {
			loads[st.Skill]++
		}
	}
	return loads, tools, errs
}

func (d Deps) applySkillRatingsTelemetry(accs map[string]*skillTelemetryAcc, loadedByConv map[string]map[string]bool, o Options, since, now time.Time) error {
	if d.SessionRatings == nil {
		return nil
	}
	ratings, err := d.SessionRatings(o.Project, since, now)
	if err != nil {
		return err
	}
	for _, row := range ratings {
		loaded := loadedByConv[row.SessionID]
		applyRatingOutcomeToSkills(accs, loaded, row.OutcomeScore)
		applyRatingActionToSkills(accs, loaded, row.SkillAction, row.TargetSkill)
	}
	return nil
}

func applyRatingOutcomeToSkills(accs map[string]*skillTelemetryAcc, loaded map[string]bool, outcomeScore int) {
	for sk := range loaded {
		if a := getSkillAcc(accs, sk); a != nil {
			a.ratedSessions++
			a.outcomeSum += outcomeScore
		}
	}
}

func applyRatingActionToSkills(accs map[string]*skillTelemetryAcc, loaded map[string]bool, action, target string) {
	if action == "new-skill" {
		if a := getSkillAcc(accs, target); a != nil {
			a.newSkillCount++
		}
		return
	}
	if action != "rewrite-skill" {
		return
	}
	if a := getSkillAcc(accs, target); a != nil {
		a.rewriteCount++
		return
	}
	for sk := range loaded {
		if a := getSkillAcc(accs, sk); a != nil {
			a.rewriteCount++
		}
	}
}

func finalizeSkillTelemetry(accs map[string]*skillTelemetryAcc) []r.SkillTelemetry {
	if len(accs) == 0 {
		return nil
	}
	out := make([]r.SkillTelemetry, 0, len(accs))
	for _, a := range accs {
		row := r.SkillTelemetry{
			Skill: a.skill, Suggested: a.suggestedTurns, Loaded: a.loaded,
			Converted: len(a.convertedConvs), Sessions: a.sessions,
			RatedSessions: a.ratedSessions, ToolCalls: a.toolCalls,
			ToolErrors: a.toolErrs, RewriteSkillCount: a.rewriteCount,
			NewSkillCount: a.newSkillCount,
		}
		if len(a.suggestedConvs) > 0 {
			pct := r.Round4(float64(len(a.convertedConvs)) * 100.0 / float64(len(a.suggestedConvs)))
			row.ConversionRatePct = &pct
		}
		if a.ratedSessions > 0 {
			mo := r.Round4(float64(a.outcomeSum) / float64(a.ratedSessions))
			row.MeanOutcomeScore = &mo
		}
		if a.toolCalls > 0 {
			er := r.Round4(float64(a.toolErrs) * 100.0 / float64(a.toolCalls))
			row.ToolErrorRatePct = &er
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Loaded != out[j].Loaded {
			return out[i].Loaded > out[j].Loaded
		}
		if out[i].Suggested != out[j].Suggested {
			return out[i].Suggested > out[j].Suggested
		}
		if out[i].RewriteSkillCount != out[j].RewriteSkillCount {
			return out[i].RewriteSkillCount > out[j].RewriteSkillCount
		}
		return out[i].Skill < out[j].Skill
	})
	return out
}

// PopulateRuleTelemetry summarizes forbidden shapes, hook blocks, bypasses, and diagnostics into sum.RuleTelemetry.
func PopulateRuleTelemetry(sum *Summary) {
	if sum == nil {
		return
	}
	var out []r.RuleTelemetry
	forbKeys := make([]string, 0, len(sum.C.Forbidden))
	for k, n := range sum.C.Forbidden {
		if n > 0 {
			forbKeys = append(forbKeys, k)
		}
	}
	sort.Strings(forbKeys)
	for _, k := range forbKeys {
		out = append(out, r.RuleTelemetry{Rule: k, Kind: "forbidden", Count: sum.C.Forbidden[k], Severity: "high"})
	}
	if sum.Health.ClampedSaves > 0 {
		out = append(out, r.RuleTelemetry{Rule: "hook_block:clamped", Kind: "hook_block", Count: sum.Health.ClampedSaves, Severity: "info"})
	}
	if ledger := dedupLedgerSaves(sum); ledger > 0 {
		out = append(out, r.RuleTelemetry{Rule: "hook_block:ledger", Kind: "hook_block", Count: ledger, Severity: "info"})
	}
	if sum.Health.HookBlocksGuards > 0 {
		out = append(out, r.RuleTelemetry{Rule: "hook_block:guard", Kind: "hook_block", Count: sum.Health.HookBlocksGuards, Severity: "medium"})
	}
	if sk := sum.C.BashReadSkips["agy"]; sk > 0 {
		out = append(out, r.RuleTelemetry{Rule: "bash_read_bypass", Kind: "adherence", Count: sk, Severity: "medium"})
	}
	if sum.Diagnostics != nil {
		for _, sig := range sum.Diagnostics.Signals {
			out = append(out, r.RuleTelemetry{Rule: search.Printable(sig.Rule), Kind: "diagnostic", Count: 1, Severity: search.Printable(sig.Severity)})
		}
	}
	if len(out) > 0 {
		sum.RuleTelemetry = out
	}
}
