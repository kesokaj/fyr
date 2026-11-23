package metrics

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func (s *Store) queryWaveRows(o ShowOptions, since, now time.Time) ([]waveRow, error) {
	where, args := windowAndProjectWhere("updated_at", o, since, now)
	rows, err := s.db.Query(`SELECT stamp, worker, project_root, state, model, base_check, judge_model, judge_verdict, updated_at FROM work_waves WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []waveRow
	for rows.Next() {
		var w waveRow
		if err := rows.Scan(&w.stamp, &w.worker, &w.projectRoot, &w.state, &w.model, &w.baseCheck, &w.judgeModel, &w.judgeVerdict, &w.updatedAt); err != nil {
			return nil, err
		}
		if waveMatchesLabel(w.stamp, o) {
			out = append(out, w)
		}
	}
	return out, rows.Err()
}

func summarizeWorkWaves(waves []waveRow) WorkSummary {
	var ws WorkSummary
	stamps := map[string]bool{}
	for _, w := range waves {
		if w.stamp != "" {
			stamps[w.stamp] = true
		}
		ws.Workers++
		addWaveWorkerCounts(&ws, w.state, w.model, w.baseCheck, w.judgeModel, w.judgeVerdict)
	}
	ws.Waves = len(stamps)
	return ws
}

func addWaveWorkerCounts(ws *WorkSummary, state, workerModel, baseCheck, judgeModel, judgeVerdict string) {
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
	if strings.TrimSpace(judgeModel) != "" && sameModelFamily(workerModel, judgeModel) {
		ws.JudgeFallback++
	}
}

func sameModelFamily(workerModel, judgeModel string) bool {
	wf := classifyModelFamily(workerModel)
	jf := classifyModelFamily(judgeModel)
	if wf == "" || jf == "" || wf != jf {
		return false
	}
	if wf == "other" {
		return strings.EqualFold(strings.TrimSpace(workerModel), strings.TrimSpace(judgeModel))
	}
	return true
}

func (s *Store) populateOrchestrationSummary(sum *Summary, o ShowOptions, since, now time.Time) error {
	if o.Label != "" {
		return nil
	}
	rows, err := s.queryOrchestrationRunsForShow(o, since, now)
	if err != nil {
		return err
	}
	sum.Orchestration = summarizeOrchestrationRuns(rows)
	return nil
}

func (s *Store) queryOrchestrationRunsForShow(o ShowOptions, since, now time.Time) ([]OrchestrationRunRow, error) {
	if len(tableCols(s.db, "orchestration_runs")) == 0 {
		return nil, nil
	}
	conds := []string{`ts >= ?`, `ts < ?`}
	args := []any{since.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339)}
	if o.Project != "" {
		p := filepath.Clean(o.Project)
		conds = append(conds, `(project_root = ? OR substr(project_root, 1, length(?)) = ?)`)
		args = append(args, p, p+"/", p+"/")
	}
	q := `SELECT id, ts, kind, COALESCE(project_root, ''), COALESCE(repo_id, ''), status, COALESCE(halt_code, ''), units, defect_rounds, passed, failed, duration_ms, COALESCE(worker_model, ''), COALESCE(judge_model, ''), COALESCE(verdict, '') FROM orchestration_runs WHERE ` + strings.Join(conds, ` AND `) + ` ORDER BY ts DESC, id DESC`
	return scanOrchestrationRuns(s.db, q, args)
}

func summarizeOrchestrationRuns(rows []OrchestrationRunRow) *OrchestrationSummary {
	if len(rows) == 0 {
		return nil
	}
	out := &OrchestrationSummary{
		Runs:     len(rows),
		ByKind:   map[string]int{},
		ByStatus: map[string]int{},
		Halts:    map[string]int{},
	}
	var durSum int64
	for _, r := range rows {
		if k := printable(strings.TrimSpace(r.Kind)); k != "" {
			out.ByKind[k]++
		}
		if st := printable(strings.TrimSpace(r.Status)); st != "" {
			out.ByStatus[st]++
		}
		out.Units += max(0, r.Units)
		out.DefectRounds += max(0, r.DefectRounds)
		out.Succeeded += max(0, r.Passed)
		out.Failed += max(0, r.Failed)
		durSum += max(int64(0), r.DurationMS)
		switch normJudgeVerdict(r.Verdict) {
		case "PASS":
			out.JudgeOK++
		case "FAIL":
			out.JudgeFailed++
		}
		if hc := printable(strings.TrimSpace(r.HaltCode)); hc != "" {
			out.Halts[hc]++
		}
	}
	out.MeanDurationMS = round4(float64(durSum) / float64(len(rows)))
	if len(out.Halts) == 0 {
		out.Halts = nil
	}
	return out
}

func populateSIMSummary(sum *Summary, home string, o ShowOptions) {
	if strings.TrimSpace(home) == "" || o.Label != "" || o.skipDeltas {
		return
	}
	db, cleanup, ok := openCopySQLiteFile(home, home, harness.StateDir, "brain.db")
	if !ok {
		return
	}
	defer cleanup()
	if !brainHasSIMTables(db) {
		return
	}
	sum.SIM = querySIMSummaryDB(db)
}

func querySIMSummaryDB(db *sql.DB) *SIMSummary {
	out := &SIMSummary{
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

func scanSIMProblemsSummary(db *sql.DB, out *SIMSummary) {
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

func scanSIMAttemptsSummary(db *sql.DB, out *SIMSummary) {
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
		if v := printable(strings.ToLower(strings.TrimSpace(verdict))); v != "" {
			out.AttemptsByVerdict[v]++
		}
		if fs := printable(strings.ToLower(strings.TrimSpace(fieldSt))); fs != "" {
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
	name := printable(strings.TrimSpace(raw))
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

func (s *Store) populateSkillTelemetry(sum *Summary, o ShowOptions, all []sess, data *showData, since, now time.Time) error {
	if o.Label != "" {
		return nil
	}
	accs := map[string]*skillTelemetryAcc{}
	if err := s.collectSuggestedSkills(accs, o, since, now); err != nil {
		return err
	}
	loadedByConv := accumulateSessionSkillSteps(accs, all, data)
	if err := s.applySkillRatingsTelemetry(accs, loadedByConv, o, since, now); err != nil {
		return err
	}
	sum.SkillTelemetry = finalizeSkillTelemetry(accs)
	return nil
}

func (s *Store) collectSuggestedSkills(accs map[string]*skillTelemetryAcc, o ShowOptions, since, now time.Time) error {
	if !tableCols(s.db, "turn_vets")["suggested_skills"] {
		return nil
	}
	where, args := windowAndProjectWhere("created_at", o, since, now)
	rows, err := s.db.Query(`SELECT COALESCE(conversation_id, ''), COALESCE(suggested_skills, '') FROM turn_vets WHERE `+where+` AND TRIM(COALESCE(suggested_skills, '')) != ''`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var convID, raw string
		if err := rows.Scan(&convID, &raw); err != nil {
			return err
		}
		cid := strings.TrimSpace(convID)
		for _, sk := range parseSuggestedSkills(raw) {
			if a := getSkillAcc(accs, sk); a != nil {
				a.suggestedTurns++
				if cid != "" {
					a.suggestedConvs[cid] = true
				}
			}
		}
	}
	return rows.Err()
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
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
	return dedupTrimSkills(parts)
}

func dedupTrimSkills(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range in {
		name := printable(strings.TrimSpace(raw))
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func accumulateSessionSkillSteps(accs map[string]*skillTelemetryAcc, all []sess, data *showData) map[string]map[string]bool {
	loadedByConv := map[string]map[string]bool{}
	if data == nil {
		return loadedByConv
	}
	seen := map[string]bool{}
	for _, x := range all {
		if x.vendor == historical {
			continue
		}
		sessSkills, sessTools, sessErrs := scanSessionSkillLoads(x, data.steps[x.id], seen)
		if len(sessSkills) == 0 {
			continue
		}
		loadedByConv[x.id] = map[string]bool{}
		for sk, cnt := range sessSkills {
			a := getSkillAcc(accs, sk)
			if a == nil {
				continue
			}
			a.loaded += cnt
			a.sessions++
			a.toolCalls += sessTools
			a.toolErrs += sessErrs
			loadedByConv[x.id][sk] = true
			if a.suggestedConvs[x.id] {
				a.convertedConvs[x.id] = true
			}
		}
	}
	return loadedByConv
}

func scanSessionSkillLoads(x sess, steps []stp, seen map[string]bool) (map[string]int, int, int) {
	loads := map[string]int{}
	tools, errs := 0, 0
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
		tools++
		if st.isErr && !st.denied {
			errs++
		}
		if st.skill != "" {
			loads[st.skill]++
		}
	}
	return loads, tools, errs
}

func (s *Store) applySkillRatingsTelemetry(accs map[string]*skillTelemetryAcc, loadedByConv map[string]map[string]bool, o ShowOptions, since, now time.Time) error {
	ratings, err := s.SessionRatings(o.Project, since, now)
	if err != nil {
		return err
	}
	for _, r := range ratings {
		for sk := range loadedByConv[r.SessionID] {
			if a := getSkillAcc(accs, sk); a != nil {
				a.ratedSessions++
				a.outcomeSum += r.OutcomeScore
			}
		}
		switch r.SkillAction {
		case "rewrite-skill":
			if a := getSkillAcc(accs, r.TargetSkill); a != nil {
				a.rewriteCount++
			} else {
				for sk := range loadedByConv[r.SessionID] {
					if a := getSkillAcc(accs, sk); a != nil {
						a.rewriteCount++
					}
				}
			}
		case "new-skill":
			if a := getSkillAcc(accs, r.TargetSkill); a != nil {
				a.newSkillCount++
			}
		}
	}
	return nil
}

func finalizeSkillTelemetry(accs map[string]*skillTelemetryAcc) []SkillTelemetry {
	if len(accs) == 0 {
		return nil
	}
	out := make([]SkillTelemetry, 0, len(accs))
	for _, a := range accs {
		row := SkillTelemetry{
			Skill:             a.skill,
			Suggested:         a.suggestedTurns,
			Loaded:            a.loaded,
			Converted:         len(a.convertedConvs),
			Sessions:          a.sessions,
			RatedSessions:     a.ratedSessions,
			ToolCalls:         a.toolCalls,
			ToolErrors:        a.toolErrs,
			RewriteSkillCount: a.rewriteCount,
			NewSkillCount:     a.newSkillCount,
		}
		if len(a.suggestedConvs) > 0 {
			pct := round4(float64(len(a.convertedConvs)) * 100.0 / float64(len(a.suggestedConvs)))
			row.ConversionRatePct = &pct
		}
		if a.ratedSessions > 0 {
			mo := round4(float64(a.outcomeSum) / float64(a.ratedSessions))
			row.MeanOutcomeScore = &mo
		}
		if a.toolCalls > 0 {
			er := round4(float64(a.toolErrs) * 100.0 / float64(a.toolCalls))
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

func populateRuleTelemetry(sum *Summary) {
	if sum == nil {
		return
	}
	var out []RuleTelemetry
	forbKeys := make([]string, 0, len(sum.C.Forbidden))
	for k, n := range sum.C.Forbidden {
		if n > 0 {
			forbKeys = append(forbKeys, k)
		}
	}
	sort.Strings(forbKeys)
	for _, k := range forbKeys {
		out = append(out, RuleTelemetry{Rule: k, Kind: "forbidden", Count: sum.C.Forbidden[k], Severity: "high"})
	}
	if sum.Health.ClampedSaves > 0 {
		out = append(out, RuleTelemetry{Rule: "hook_block:clamped", Kind: "hook_block", Count: sum.Health.ClampedSaves, Severity: "info"})
	}
	if ledger := dedupLedgerSaves(sum); ledger > 0 {
		out = append(out, RuleTelemetry{Rule: "hook_block:ledger", Kind: "hook_block", Count: ledger, Severity: "info"})
	}
	if sum.Health.HookBlocksGuards > 0 {
		out = append(out, RuleTelemetry{Rule: "hook_block:guard", Kind: "hook_block", Count: sum.Health.HookBlocksGuards, Severity: "medium"})
	}
	if sk := sum.C.BashReadSkips["agy"]; sk > 0 {
		out = append(out, RuleTelemetry{Rule: "bash_read_bypass", Kind: "adherence", Count: sk, Severity: "medium"})
	}
	if sum.Diagnostics != nil {
		for _, sig := range sum.Diagnostics.Signals {
			out = append(out, RuleTelemetry{Rule: printable(sig.Rule), Kind: "diagnostic", Count: 1, Severity: printable(sig.Severity)})
		}
	}
	if len(out) > 0 {
		sum.RuleTelemetry = out
	}
}
