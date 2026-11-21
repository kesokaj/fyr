package metrics

import (
	"database/sql"
	"strings"
)

const showBatchChunk = 200

type waveRow struct {
	stamp, worker, projectRoot, state, model, baseCheck, judgeModel, judgeVerdict string
	updatedAt                                                                     int64
}

type showData struct {
	steps  map[string][]stp
	events map[string][]sessEvent
	waves  []waveRow
	sbAcc  *secondBrainAcc
}

func (s *Store) loadShowData(all []sess) (*showData, error) {
	ids := distinctSessionIDs(all)
	data := &showData{
		steps:  make(map[string][]stp, len(ids)),
		events: make(map[string][]sessEvent, len(ids)),
	}
	if len(ids) == 0 {
		return data, nil
	}
	stepIdx := make(map[string]map[string]int, len(ids))
	for off := 0; off < len(ids); off += showBatchChunk {
		chunk := ids[off:min(off+showBatchChunk, len(ids))]
		if err := loadBatchSteps(s.db, chunk, data.steps, stepIdx); err != nil {
			return nil, err
		}
		if err := loadBatchStepVerbs(s.db, chunk, data.steps, stepIdx); err != nil {
			return nil, err
		}
		if err := loadBatchEvents(s.db, chunk, data.events, data.steps, stepIdx); err != nil {
			return nil, err
		}
	}
	return data, nil
}

func distinctSessionIDs(all []sess) []string {
	seen := make(map[string]bool, len(all))
	ids := make([]string, 0, len(all))
	for _, x := range all {
		if x.id != "" && !seen[x.id] {
			seen[x.id] = true
			ids = append(ids, x.id)
		}
	}
	return ids
}

func inPlaceholders(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if len(ids) == 1 {
		return "?", args
	}
	return "?" + strings.Repeat(",?", len(ids)-1), args
}

func loadBatchSteps(db *sql.DB, ids []string, steps map[string][]stp, stepIdx map[string]map[string]int) error {
	ph, args := inPlaceholders(ids)
	rows, err := db.Query(`SELECT session_id, ext_id, COALESCE(kind,''), COALESCE(tool,''), COALESCE(skill,''), COALESCE(target_kind,''),
		COALESCE(cmd_hash,''), COALESCE(ts,0), COALESCE(started_ms,0), COALESCE(ended_ms,0), COALESCE(wait_ms,0),
		COALESCE(is_error,0), COALESCE(denied,0), COALESCE(tok_in,0), COALESCE(tok_out,0),
		COALESCE(cache_read,0), COALESCE(cache_write,0), COALESCE(context,0)
		FROM steps WHERE session_id IN (`+ph+`) ORDER BY session_id, ts, ext_id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		var x stp
		if err := rows.Scan(&sid, &x.ext, &x.kind, &x.tool, &x.skill, &x.kindOf, &x.hash, &x.ts, &x.started, &x.ended, &x.wait,
			&x.isErr, &x.denied, &x.in, &x.out, &x.cr, &x.cw, &x.ctx); err != nil {
			return err
		}
		idx := stepIdx[sid]
		if idx == nil {
			idx = map[string]int{}
			stepIdx[sid] = idx
		}
		idx[x.ext] = len(steps[sid])
		steps[sid] = append(steps[sid], x)
	}
	return rows.Err()
}

func loadBatchStepVerbs(db *sql.DB, ids []string, steps map[string][]stp, stepIdx map[string]map[string]int) error {
	ph, args := inPlaceholders(ids)
	rows, err := db.Query(`SELECT session_id, ext_id, verb FROM step_verbs WHERE session_id IN (`+ph+`) ORDER BY session_id, ext_id, n`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sid, ext, verb string
		if err := rows.Scan(&sid, &ext, &verb); err != nil {
			return err
		}
		if i, ok := stepIdx[sid][ext]; ok {
			steps[sid][i].verbs = append(steps[sid][i].verbs, verb)
		}
	}
	return rows.Err()
}

func loadBatchEvents(db *sql.DB, ids []string, events map[string][]sessEvent, steps map[string][]stp, stepIdx map[string]map[string]int) error {
	ph, args := inPlaceholders(ids)
	rows, err := db.Query(`SELECT session_id, kind, COALESCE(ref,''), COALESCE(value,''), COALESCE(ts,0) FROM events WHERE session_id IN (`+ph+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		var ev sessEvent
		if err := rows.Scan(&sid, &ev.kind, &ev.ref, &ev.value, &ev.ts); err != nil {
			return err
		}
		if ev.kind == "hook_block" {
			if i, ok := stepIdx[sid][ev.ref]; ok {
				steps[sid][i].denialKind = ev.value
			}
		}
		events[sid] = append(events[sid], ev)
	}
	return rows.Err()
}
