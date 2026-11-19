package metrics

import (
	"strings"
	"time"
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
