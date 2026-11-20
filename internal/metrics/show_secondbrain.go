package metrics

import (
	"path/filepath"
	"strings"
	"time"
)

type projVetAcc struct {
	turns, scoreSum int
	lastMS          int64
}

func (s *Store) populateSecondBrainAndProjects(sum *Summary, o ShowOptions, all []sess, trials []trial, data *showData, since, now time.Time) error {
	if o.Label == "" {
		sb, vetsByRoot, err := s.querySecondBrain(o, since, now)
		if err != nil {
			return err
		}
		sum.E = sb
		if o.Project == "" {
			home := resolveMetricsHome(o, s.path)
			sum.Projects = buildProjectRowsWithOpts(all, vetsByRoot, data.steps, data.events, o, home)
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
