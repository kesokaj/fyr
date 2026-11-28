package show

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	r "github.com/kesokaj/fyr/internal/metrics/report"
)

func (d Deps) populateSecondBrainAndProjects(sum *Summary, o Options, all []Sess, trials []Trial, data *ShowData, since, now time.Time) error {
	if o.Label == "" {
		home := ResolveMetricsHome(o, d.StorePath)
		where, args := WindowAndProjectWhere("created_at", o, since, now)
		acc, err := d.querySecondBrainFiltered(where, args, o, home)
		if err != nil {
			return err
		}
		data.SBAcc = acc
		sum.E = acc.SB
		if o.Project == "" {
			sum.Projects = d.buildProjectRowsWithOpts(all, acc.ByRoot, data.Steps, data.Events, o, home)
		}
	}
	waves, err := d.queryWaveRows(o, since, now)
	if err != nil {
		return err
	}
	data.Waves = waves
	sum.F = SummarizeWorkWaves(waves)
	if d.PopulateTriggerCoverage != nil {
		d.PopulateTriggerCoverage(sum, o, all, trials, data, since, now)
	}
	return nil
}

// WindowAndProjectWhere builds a timestamp-window and optional project-prefix WHERE clause.
func WindowAndProjectWhere(tsCol string, o Options, since, now time.Time) (string, []any) {
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

func beforeCutoffProjectWhere(tsCol string, o Options, cutoff time.Time) (string, []any) {
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

func (d Deps) querySecondBrainFiltered(where string, args []any, o Options, home string) (*SecondBrainAcc, error) {
	if o.Label == "" && !o.AllLabels {
		where += excludeLabeledTurnVetsSQL
	}
	filterSandboxes := o.Project == "" && !o.IncludeSandboxes
	return d.scanSecondBrainWhere(where, args, filterSandboxes, home)
}

func (d Deps) querySecondBrainWhere(where string, args []any) (*SecondBrainAcc, error) {
	return d.scanSecondBrainWhere(where, args, false, "")
}

func (d Deps) scanSecondBrainWhere(where string, args []any, filterSandboxes bool, home string) (*SecondBrainAcc, error) {
	rows, err := d.DB.Query(`SELECT project_root, conversation_id, gate, score, bypassed, pivots_count, ledger_saved, created_at,
		COALESCE(duration_ms, 0), COALESCE(timed_out, 0), COALESCE(injected_bytes, 0),
		COALESCE(pivots_used_count, 0), COALESCE(skills_loaded_count, 0)
		FROM turn_vets WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acc := &SecondBrainAcc{ByRoot: map[string]*ProjVetAcc{}, ByConv: map[string][2]int{}}
	for rows.Next() {
		var root, convID, gate string
		var score, bypassed, pivots, ledger, durMS, timedOut, injBytes, pivotsUsed, skillsLoaded int
		var createdAt int64
		if err := rows.Scan(&root, &convID, &gate, &score, &bypassed, &pivots, &ledger, &createdAt,
			&durMS, &timedOut, &injBytes, &pivotsUsed, &skillsLoaded); err != nil {
			return nil, err
		}
		if filterSandboxes && d.isEphemeralTurnVetRoot(root, home) {
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

func (d Deps) isEphemeralTurnVetRoot(root, home string) bool {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" || d.isSandboxRoot(trimmed, home) {
		return true
	}
	clean := filepath.Clean(trimmed)
	if d.Roots.IsTempRoot != nil && d.Roots.IsTempRoot(clean) {
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
			if isFyrEphemeralSubdir(segs[i+2]) {
				return true
			}
		case seg == ".gemini" && i+4 < len(segs) && segs[i+1] != "" && segs[i+2] == "brain" && segs[i+3] != "" && segs[i+4] == "scratch":
			return true
		}
	}
	return false
}

func isFyrEphemeralSubdir(seg string) bool {
	switch seg {
	case "auto", "eval", "sandbox", "dev":
		return true
	}
	return strings.HasPrefix(seg, "dream-agy-home-")
}

func (a *SecondBrainAcc) addRow(root, convID, gate string, score, bypassed, pivots, ledger, durMS, timedOut, injBytes, pivotsUsed, skillsLoaded int, createdAt int64) {
	a.SB.TurnVets++
	a.scoreSum += score
	if gate == "VET_FIRST" {
		a.SB.VetFirstTurns++
	}
	if bypassed != 0 {
		a.SB.BypassedTurns++
	}
	a.SB.PivotsSeeded += pivots
	a.SB.LedgerSaved += ledger
	a.SB.PivotsUsed += pivotsUsed
	a.SB.SkillsLoaded += skillsLoaded
	if timedOut != 0 {
		a.SB.TimedOutTurns++
	}
	if durMS > 0 {
		a.durations = append(a.durations, durMS)
	}
	if injBytes > 0 || durMS > 0 || timedOut != 0 {
		a.injSum += injBytes
		a.injCount++
	}
	if cid := strings.TrimSpace(convID); cid != "" {
		cur := a.ByConv[cid]
		cur[0]++
		if timedOut != 0 {
			cur[1]++
		}
		a.ByConv[cid] = cur
	}
	accumulateProjVet(a.ByRoot, root, score, NormalizeMilli(createdAt))
}

func (a *SecondBrainAcc) finish() {
	if a.SB.TurnVets > 0 {
		mean := r.Round4(float64(a.scoreSum) / float64(a.SB.TurnVets))
		a.SB.MeanVetScore = &mean
		rate := r.Round4(float64(a.SB.TimedOutTurns) * 100.0 / float64(a.SB.TurnVets))
		a.SB.TimeoutRatePct = &rate
	}
	if len(a.durations) > 0 {
		sort.Ints(a.durations)
		p50 := percentileInt(a.durations, 50)
		p95 := percentileInt(a.durations, 95)
		a.SB.P50DurationMS = &p50
		a.SB.P95DurationMS = &p95
	}
	if a.injCount > 0 {
		mi := r.Round4(float64(a.injSum) / float64(a.injCount))
		a.SB.MeanInjectedBytes = &mi
	}
}

func percentileInt(sorted []int, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(p)/100.0*float64(len(sorted)))) - 1
	idx = max(0, min(len(sorted)-1, idx))
	return r.Round4(float64(sorted[idx]))
}

func accumulateProjVet(byRoot map[string]*ProjVetAcc, root string, score int, tsMS int64) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return
	}
	clean := filepath.Clean(trimmed)
	pv := byRoot[clean]
	if pv == nil {
		pv = &ProjVetAcc{}
		byRoot[clean] = pv
	}
	pv.Turns++
	pv.ScoreSum += score
	pv.LastMS = max(pv.LastMS, tsMS)
}

// NormalizeMilli promotes Unix-second timestamps in [1e8, 1e11) to milliseconds.
func NormalizeMilli(ts int64) int64 {
	if ts >= 100_000_000 && ts < 100_000_000_000 {
		return ts * 1000
	}
	return ts
}
