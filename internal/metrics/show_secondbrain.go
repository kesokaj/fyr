package metrics

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type projVetAcc struct {
	turns, scoreSum int
	lastMS          int64
}

type secondBrainAcc struct {
	sb               SecondBrain
	scoreSum         int
	durations        []int
	injSum, injCount int
	byRoot           map[string]*projVetAcc
	byConv           map[string][2]int
}

func (s *Store) populateSecondBrainAndProjects(sum *Summary, o ShowOptions, all []sess, trials []trial, data *showData, since, now time.Time) error {
	if o.Label == "" {
		home := resolveMetricsHome(o, s.path)
		where, args := windowAndProjectWhere("created_at", o, since, now)
		acc, err := s.querySecondBrainFiltered(where, args, o, home)
		if err != nil {
			return err
		}
		data.sbAcc = acc
		sum.E = acc.sb
		if o.Project == "" {
			sum.Projects = buildProjectRowsWithOpts(all, acc.byRoot, data.steps, data.events, o, home)
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

func beforeCutoffProjectWhere(tsCol string, o ShowOptions, cutoff time.Time) (string, []any) {
	expr := `(CASE WHEN ` + tsCol + ` >= 100000000 AND ` + tsCol + ` < 100000000000 THEN ` + tsCol + ` * 1000 ELSE ` + tsCol + ` END)`
	w := expr + ` > 0 AND ` + expr + ` < ?`
	args := []any{cutoff.UnixMilli()}
	if o.Project != "" {
		p := filepath.Clean(o.Project)
		w += ` AND (project_root = ? OR substr(project_root, 1, length(?)) = ?)`
		args = append(args, p, p+"/", p+"/")
	}
	if o.Label == "" && !o.AllLabels {
		w += excludeLabeledTurnVetsSQL
	}
	return w, args
}

const excludeLabeledTurnVetsSQL = ` AND NOT EXISTS (SELECT 1 FROM sessions s WHERE s.id = turn_vets.conversation_id AND s.label IS NOT NULL AND s.label != '')`

func (s *Store) querySecondBrain(o ShowOptions, since, now time.Time) (SecondBrain, map[string]*projVetAcc, error) {
	home := resolveMetricsHome(o, s.path)
	where, args := windowAndProjectWhere("created_at", o, since, now)
	acc, err := s.querySecondBrainFiltered(where, args, o, home)
	if err != nil {
		return SecondBrain{}, nil, err
	}
	return acc.sb, acc.byRoot, nil
}

func (s *Store) querySecondBrainFiltered(where string, args []any, o ShowOptions, home string) (*secondBrainAcc, error) {
	if o.Label == "" && !o.AllLabels {
		where += excludeLabeledTurnVetsSQL
	}
	filterSandboxes := o.Project == "" && !o.IncludeSandboxes
	return s.scanSecondBrainWhere(where, args, filterSandboxes, home)
}

func (s *Store) querySecondBrainWhere(where string, args []any) (*secondBrainAcc, error) {
	return s.scanSecondBrainWhere(where, args, false, "")
}

func (s *Store) scanSecondBrainWhere(where string, args []any, filterSandboxes bool, home string) (*secondBrainAcc, error) {
	rows, err := s.db.Query(`SELECT project_root, conversation_id, gate, score, bypassed, pivots_count, ledger_saved, created_at,
		COALESCE(duration_ms, 0), COALESCE(timed_out, 0), COALESCE(injected_bytes, 0),
		COALESCE(pivots_used_count, 0), COALESCE(skills_loaded_count, 0)
		FROM turn_vets WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acc := &secondBrainAcc{byRoot: map[string]*projVetAcc{}, byConv: map[string][2]int{}}
	for rows.Next() {
		var root, convID, gate string
		var score, bypassed, pivots, ledger, durMS, timedOut, injBytes, pivotsUsed, skillsLoaded int
		var createdAt int64
		if err := rows.Scan(&root, &convID, &gate, &score, &bypassed, &pivots, &ledger, &createdAt,
			&durMS, &timedOut, &injBytes, &pivotsUsed, &skillsLoaded); err != nil {
			return nil, err
		}
		if filterSandboxes && isEphemeralTurnVetRoot(root, home) {
			continue
		}
		acc.addRow(root, convID, gate, score, bypassed, pivots, ledger, durMS, timedOut, injBytes, pivotsUsed, skillsLoaded, createdAt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	acc.finish()
	return acc, nil
}

func isEphemeralTurnVetRoot(root, home string) bool {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" || isSandboxProjectRoot(trimmed, home) {
		return true
	}
	clean := filepath.Clean(trimmed)
	if isTempRoot(clean) {
		return true
	}
	return hasExtraEphemeralTurnVetSegment(clean)
}

func hasExtraEphemeralTurnVetSegment(clean string) bool {
	segs := strings.Split(filepath.ToSlash(clean), "/")
	for i, seg := range segs {
		switch {
		case strings.HasPrefix(seg, "fyr-smoke-"):
			return true
		case seg == ".config" && i+2 < len(segs) && segs[i+1] == "fyr":
			switch segs[i+2] {
			case "auto", "eval", "sandbox", "dev":
				return true
			}
			if strings.HasPrefix(segs[i+2], "dream-agy-home-") {
				return true
			}
		case seg == ".gemini" && i+4 < len(segs) && segs[i+1] != "" && segs[i+2] == "brain" && segs[i+3] != "" && segs[i+4] == "scratch":
			return true
		}
	}
	return false
}

func (a *secondBrainAcc) addRow(root, convID, gate string, score, bypassed, pivots, ledger, durMS, timedOut, injBytes, pivotsUsed, skillsLoaded int, createdAt int64) {
	a.sb.TurnVets++
	a.scoreSum += score
	if gate == "VET_FIRST" {
		a.sb.VetFirstTurns++
	}
	if bypassed != 0 {
		a.sb.BypassedTurns++
	}
	a.sb.PivotsSeeded += pivots
	a.sb.LedgerSaved += ledger
	a.sb.PivotsUsed += pivotsUsed
	a.sb.SkillsLoaded += skillsLoaded
	if timedOut != 0 {
		a.sb.TimedOutTurns++
	}
	if durMS > 0 {
		a.durations = append(a.durations, durMS)
	}
	if injBytes > 0 || durMS > 0 || timedOut != 0 {
		a.injSum += injBytes
		a.injCount++
	}
	if cid := strings.TrimSpace(convID); cid != "" {
		cur := a.byConv[cid]
		cur[0]++
		if timedOut != 0 {
			cur[1]++
		}
		a.byConv[cid] = cur
	}
	accumulateProjVet(a.byRoot, root, score, normalizeMilli(createdAt))
}

func (a *secondBrainAcc) finish() {
	if a.sb.TurnVets > 0 {
		mean := round4(float64(a.scoreSum) / float64(a.sb.TurnVets))
		a.sb.MeanVetScore = &mean
		rate := round4(float64(a.sb.TimedOutTurns) * 100.0 / float64(a.sb.TurnVets))
		a.sb.TimeoutRatePct = &rate
	}
	if len(a.durations) > 0 {
		sort.Ints(a.durations)
		p50 := percentileInt(a.durations, 50)
		p95 := percentileInt(a.durations, 95)
		a.sb.P50DurationMS = &p50
		a.sb.P95DurationMS = &p95
	}
	if a.injCount > 0 {
		mi := round4(float64(a.injSum) / float64(a.injCount))
		a.sb.MeanInjectedBytes = &mi
	}
}

func percentileInt(sorted []int, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(p)/100.0*float64(len(sorted)))) - 1
	idx = max(0, min(len(sorted)-1, idx))
	return round4(float64(sorted[idx]))
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
