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

func (s *Store) populateSecondBrainAndProjects(sum *Summary, o ShowOptions, all []sess, trials []trial, since, now time.Time) error {
	if o.Label == "" {
		sb, vetsByRoot, err := s.querySecondBrain(o, since, now)
		if err != nil {
			return err
		}
		sum.E = sb
		if o.Project == "" {
			projects, err := s.buildProjectRows(all, vetsByRoot)
			if err != nil {
				return err
			}
			sum.Projects = projects
		}
	}
	ws, err := s.queryWorkWaves(o, since, now)
	if err != nil {
		return err
	}
	sum.F = ws
	return s.populateTriggerCoverage(sum, o, all, trials, since, now)
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

func (s *Store) queryWorkWaves(o ShowOptions, since, now time.Time) (WorkSummary, error) {
	where, args := windowAndProjectWhere("updated_at", o, since, now)
	rows, err := s.db.Query(`SELECT stamp, state, base_check, judge_model, judge_verdict FROM work_waves WHERE `+where, args...)
	if err != nil {
		return WorkSummary{}, err
	}
	defer rows.Close()
	var ws WorkSummary
	stamps := map[string]bool{}
	for rows.Next() {
		var stamp, state, baseCheck, judgeModel, judgeVerdict string
		if err := rows.Scan(&stamp, &state, &baseCheck, &judgeModel, &judgeVerdict); err != nil {
			return WorkSummary{}, err
		}
		if !waveMatchesLabel(stamp, o) {
			continue
		}
		if stamp != "" {
			stamps[stamp] = true
		}
		ws.Workers++
		addWaveWorkerCounts(&ws, state, baseCheck, judgeModel, judgeVerdict)
	}
	ws.Waves = len(stamps)
	return ws, rows.Err()
}

func addWaveWorkerCounts(ws *WorkSummary, state, baseCheck, judgeModel, judgeVerdict string) {
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
	if strings.Contains(strings.ToLower(judgeModel), "gemini") {
		ws.JudgeFallback++
	}
}

func (s *Store) buildProjectRows(all []sess, vetsByRoot map[string]*projVetAcc) ([]ProjectRow, error) {
	canon := buildRootCanonicalizer(all, vetsByRoot)
	accs := map[string]*projAcc{}
	getAcc := func(raw string) *projAcc {
		r := canon(raw)
		if r == "" {
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
	parentRoot := map[string]string{}
	for _, x := range all {
		if x.root != "" {
			parentRoot[x.id] = canon(x.root)
		}
	}
	seen := map[string]bool{}
	for _, x := range all {
		root := x.root
		if root == "" && x.parent != "" {
			root = parentRoot[x.parent]
		}
		p := getAcc(root)
		if p == nil {
			continue
		}
		if err := s.accumulateProjectSession(p, x, seen); err != nil {
			return nil, err
		}
	}
	return finalizeProjectRows(accs), nil
}

func buildRootCanonicalizer(all []sess, vetsByRoot map[string]*projVetAcc) func(string) string {
	set := map[string]bool{}
	for _, x := range all {
		if x.parent == "" && strings.TrimSpace(x.root) != "" {
			set[filepath.Clean(x.root)] = true
		}
	}
	for r := range vetsByRoot {
		if strings.TrimSpace(r) != "" {
			set[filepath.Clean(r)] = true
		}
	}
	roots := make([]string, 0, len(set))
	for r := range set {
		roots = append(roots, r)
	}
	sort.Slice(roots, func(i, j int) bool { return len(roots[i]) < len(roots[j]) })
	return func(raw string) string {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return ""
		}
		clean := filepath.Clean(raw)
		for _, r := range roots {
			if clean == r || strings.HasPrefix(clean, r+"/") {
				return r
			}
		}
		return clean
	}
}

func (s *Store) accumulateProjectSession(p *projAcc, x sess, seen map[string]bool) error {
	main := x.parent == ""
	if main {
		p.sessions++
		p.lastMS = max(p.lastMS, max(x.start, x.end))
		if err := s.addProjectEvents(p, x.id); err != nil {
			return err
		}
	}
	steps, err := s.stepsOf(x.id)
	if err != nil {
		return err
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
	return nil
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

func (s *Store) addProjectEvents(p *projAcc, sessionID string) error {
	rows, err := s.db.Query(`SELECT kind FROM events WHERE session_id = ? AND kind IN ('bug', 'correction')`, sessionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return err
		}
		switch kind {
		case "bug":
			p.escapedBugs++
		case "correction":
			p.corrections++
		}
	}
	return rows.Err()
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
