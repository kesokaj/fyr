package metrics

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type projVetAcc struct {
	turns, scoreSum int
	lastMS          int64
}

type projAcc struct {
	root                                                    string
	sessions, sqliteVerbs, grepCalls, vetTurns, vetScoreSum int
	corrections, escapedBugs                                int
	activeMS, tokens, lastMS                                int64
}

func (s *Store) populateSecondBrainAndProjects(sum *Summary, o ShowOptions, all []sess, trials []trial, data *showData, since, now time.Time) error {
	if o.Label == "" {
		sb, vetsByRoot, err := s.querySecondBrain(o, since, now)
		if err != nil {
			return err
		}
		sum.E = sb
		if o.Project == "" {
			sum.Projects = buildProjectRows(all, vetsByRoot, data.steps, data.events)
		}
	}
	waves, err := s.queryWaveRows(o, since, now)
	if err != nil {
		return err
	}
	data.waves = waves
	sum.F = summarizeWorkWaves(waves)
	populateTriggerCoverage(sum, o, all, trials, data, since, now)
	return nil
}

func windowAndProjectWhere(tsCol string, o ShowOptions, since, now time.Time) (string, []any) {
	expr := `(CASE WHEN ` + tsCol + ` >= 100000000 AND ` + tsCol + ` < 100000000000 THEN ` + tsCol + ` * 1000 ELSE ` + tsCol + ` END)`
	w := expr + ` >= ? AND ` + expr + ` < ?`
	args := []any{since.UnixMilli(), now.UnixMilli()}
	if o.Project != "" {
		p := filepath.Clean(o.Project)
		w += ` AND (project_root = ? OR substr(project_root, 1, length(?)) = ?)`
		args = append(args, p, p+"/", p+"/")
	}
	return w, args
}

func (s *Store) querySecondBrain(o ShowOptions, since, now time.Time) (SecondBrain, map[string]*projVetAcc, error) {
	where, args := windowAndProjectWhere("created_at", o, since, now)
	rows, err := s.db.Query(`SELECT project_root, gate, score, bypassed, pivots_count, ledger_saved, created_at FROM turn_vets WHERE `+where, args...)
	if err != nil {
		return SecondBrain{}, nil, err
	}
	defer rows.Close()
	var sb SecondBrain
	var scoreSum int
	byRoot := map[string]*projVetAcc{}
	for rows.Next() {
		var root, gate string
		var score, bypassed, pivots, ledger int
		var createdAt int64
		if err := rows.Scan(&root, &gate, &score, &bypassed, &pivots, &ledger, &createdAt); err != nil {
			return SecondBrain{}, nil, err
		}
		sb.TurnVets++
		scoreSum += score
		if gate == "VET_FIRST" {
			sb.VetFirstTurns++
		}
		if bypassed != 0 {
			sb.BypassedTurns++
		}
		sb.PivotsSeeded += pivots
		sb.LedgerSaved += ledger
		accumulateProjVet(byRoot, root, score, normalizeMilli(createdAt))
	}
	if err := rows.Err(); err != nil {
		return SecondBrain{}, nil, err
	}
	if sb.TurnVets > 0 {
		mean := round4(float64(scoreSum) / float64(sb.TurnVets))
		sb.MeanVetScore = &mean
	}
	return sb, byRoot, nil
}

func accumulateProjVet(byRoot map[string]*projVetAcc, root string, score int, tsMS int64) {
	r := strings.TrimSpace(root)
	if r == "" {
		return
	}
	r = filepath.Clean(r)
	pv := byRoot[r]
	if pv == nil {
		pv = &projVetAcc{}
		byRoot[r] = pv
	}
	pv.turns++
	pv.scoreSum += score
	pv.lastMS = max(pv.lastMS, tsMS)
}

func normalizeMilli(ts int64) int64 {
	if ts >= 100_000_000 && ts < 100_000_000_000 {
		return ts * 1000
	}
	return ts
}

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

func buildProjectRows(all []sess, vetsByRoot map[string]*projVetAcc, stepsByID map[string][]stp, eventsByID map[string][]sessEvent) []ProjectRow {
	byID, wtMap, _ := resolveSessionRoots(all)
	canon := buildRootCanonicalizerWithRoots(all, vetsByRoot, byID, wtMap)
	accs := map[string]*projAcc{}
	getAcc := func(raw string) *projAcc {
		r := canon(raw)
		if r == "" || isProbeBreakdownRoot(r) {
			return nil
		}
		if accs[r] == nil {
			accs[r] = &projAcc{root: r}
		}
		return accs[r]
	}
	for r, pv := range vetsByRoot {
		if p := getAcc(r); p != nil {
			p.vetTurns += pv.turns
			p.vetScoreSum += pv.scoreSum
			p.lastMS = max(p.lastMS, pv.lastMS)
		}
	}
	parentRoot := buildParentRootMap(all, byID, canon)
	seen := map[string]bool{}
	for _, x := range all {
		root := sessionProjectRoot(x, byID, parentRoot)
		if p := getAcc(root); p != nil {
			accumulateProjectSession(p, x, seen, stepsByID[x.id], eventsByID[x.id])
		}
	}
	return finalizeProjectRows(accs)
}

func buildParentRootMap(all []sess, byID map[string]string, canon func(string) string) map[string]string {
	parentRoot := map[string]string{}
	for _, x := range all {
		r := x.root
		if byID[x.id] != "" {
			r = byID[x.id]
		}
		if r != "" {
			parentRoot[x.id] = canon(r)
		}
	}
	return parentRoot
}

func sessionProjectRoot(x sess, byID, parentRoot map[string]string) string {
	root := x.root
	if byID[x.id] != "" {
		root = byID[x.id]
	}
	if (root == "" || isAgyWorktreePath(root)) && x.parent != "" {
		return parentRoot[x.parent]
	}
	return root
}

func buildRootCanonicalizer(all []sess, vetsByRoot map[string]*projVetAcc) func(string) string {
	byID, wtMap, _ := resolveSessionRoots(all)
	return buildRootCanonicalizerWithRoots(all, vetsByRoot, byID, wtMap)
}

func buildRootCanonicalizerWithRoots(all []sess, vetsByRoot map[string]*projVetAcc, byID, wtMap map[string]string) func(string) string {
	set := map[string]bool{}
	for _, x := range all {
		r := strings.TrimSpace(x.root)
		if byID[x.id] != "" {
			r = byID[x.id]
		}
		if x.parent == "" && r != "" {
			clean := filepath.Clean(r)
			if !isNonCollapsibleRoot(clean) && !isAgyWorktreePath(clean) {
				set[clean] = true
			}
		}
	}
	for r := range vetsByRoot {
		if strings.TrimSpace(r) != "" {
			clean := filepath.Clean(r)
			if !isNonCollapsibleRoot(clean) && !isAgyWorktreePath(clean) {
				set[clean] = true
			}
		}
	}
	roots := sortedRootPrefixes(set)
	cache := map[string]string{}
	return func(raw string) string {
		if v, ok := cache[raw]; ok {
			return v
		}
		v := canonicalizeProjectRoot(raw, roots, wtMap, byID)
		cache[raw] = v
		return v
	}
}

func sortedRootPrefixes(set map[string]bool) []string {
	roots := make([]string, 0, len(set))
	for r := range set {
		roots = append(roots, r)
	}
	sort.Slice(roots, func(i, j int) bool { return len(roots[i]) < len(roots[j]) })
	return roots
}

func canonicalizeProjectRoot(raw string, roots []string, wtMap, byID map[string]string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	clean := filepath.Clean(raw)
	if mapped := wtMap[clean]; mapped != "" {
		clean = mapped
	} else if main, ok := resolveGitWorktreeOnDisk(clean); ok {
		clean = main
	} else if isAgyWorktreePath(clean) {
		if fb := resolveFallbackRoot("", clean, "", byID, nil, roots); fb != "" {
			clean = fb
		}
	}
	for _, r := range roots {
		if clean == r || strings.HasPrefix(clean, r+"/") {
			return r
		}
	}
	return clean
}

func accumulateProjectSession(p *projAcc, x sess, seen map[string]bool, steps []stp, events []sessEvent) {
	main := x.parent == ""
	if main {
		p.sessions++
		p.lastMS = max(p.lastMS, max(x.start, x.end))
		addProjectEventsFromSlice(p, events)
	}
	var last int64
	for _, st := range steps {
		if st.kind == "tool" || st.kind == "assistant" || st.kind == "generation" {
			key := x.vendor + "\x00" + x.label + "\x00" + st.ext
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		if st.ts > 0 {
			if main && last > 0 && st.ts-last < activeGap.Milliseconds() {
				p.activeMS += st.ts - last
			}
			last = st.ts
			p.lastMS = max(p.lastMS, st.ts)
		}
		p.tokens += st.in + st.out + st.cr + st.cw
		addProjectStepVerbs(p, st)
	}
}

func addProjectStepVerbs(p *projAcc, st stp) {
	if st.kind != "tool" || st.tool == "" {
		return
	}
	if st.tool == "Grep" {
		p.grepCalls++
	}
	for _, verb := range st.verbs {
		switch {
		case isSQLiteVerb(verb):
			p.sqliteVerbs++
		case isGrepVerb(verb):
			p.grepCalls++
		}
	}
}

func addProjectEventsFromSlice(p *projAcc, events []sessEvent) {
	for _, ev := range events {
		switch ev.kind {
		case "bug":
			p.escapedBugs++
		case "correction":
			p.corrections++
		}
	}
}

func finalizeProjectRows(accs map[string]*projAcc) []ProjectRow {
	if len(accs) == 0 {
		return nil
	}
	out := make([]ProjectRow, 0, len(accs))
	for _, p := range accs {
		row := ProjectRow{
			Project: p.root, Sessions: p.sessions, ActiveMin: round4(float64(p.activeMS) / 60000),
			Tokens: p.tokens, Corrections: p.corrections, EscapedBugs: p.escapedBugs,
		}
		if p.grepCalls > 0 {
			r := round4(float64(p.sqliteVerbs) / float64(p.grepCalls))
			row.SearchGrepRatio = &r
		}
		if p.vetTurns > 0 {
			m := round4(float64(p.vetScoreSum) / float64(p.vetTurns))
			row.MeanVetScore = &m
		}
		if p.lastMS > 0 {
			row.LastActive = time.UnixMilli(p.lastMS).UTC().Format("2006-01-02")
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		return out[i].Project < out[j].Project
	})
	return out
}
