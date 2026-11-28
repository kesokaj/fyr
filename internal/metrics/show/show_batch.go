package show

import (
	"database/sql"
	"strings"
)

// LoadShowData fetches batched steps, verbs, and events for all sessions.
func LoadShowData(db *sql.DB, all []Sess) (*ShowData, error) {
	ids := DistinctSessionIDs(all)
	data := &ShowData{
		Steps:  make(map[string][]Stp, len(ids)),
		Events: make(map[string][]SessEvent, len(ids)),
	}
	if len(ids) == 0 {
		return data, nil
	}
	stepIdx := make(map[string]map[string]int, len(ids))
	for off := 0; off < len(ids); off += ShowBatchChunk {
		chunk := ids[off:min(off+ShowBatchChunk, len(ids))]
		if err := loadBatchSteps(db, chunk, data.Steps, stepIdx); err != nil {
			return nil, err
		}
		if err := loadBatchStepVerbs(db, chunk, data.Steps, stepIdx); err != nil {
			return nil, err
		}
		if err := loadBatchEvents(db, chunk, data.Events, data.Steps, stepIdx); err != nil {
			return nil, err
		}
	}
	return data, nil
}

// DistinctSessionIDs returns unique non-empty session IDs in order.
func DistinctSessionIDs(all []Sess) []string {
	seen := make(map[string]bool, len(all))
	ids := make([]string, 0, len(all))
	for _, x := range all {
		if x.ID != "" && !seen[x.ID] {
			seen[x.ID] = true
			ids = append(ids, x.ID)
		}
	}
	return ids
}

// InPlaceholders returns SQL `?,?,...` placeholders and matching []any args.
func InPlaceholders(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if len(ids) == 1 {
		return "?", args
	}
	return "?" + strings.Repeat(",?", len(ids)-1), args
}

func loadBatchSteps(db *sql.DB, ids []string, steps map[string][]Stp, stepIdx map[string]map[string]int) error {
	ph, args := InPlaceholders(ids)
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
		var x Stp
		if err := rows.Scan(&sid, &x.Ext, &x.Kind, &x.Tool, &x.Skill, &x.KindOf, &x.Hash, &x.TS, &x.Started, &x.Ended, &x.Wait,
			&x.IsErr, &x.Denied, &x.In, &x.Out, &x.CR, &x.CW, &x.Ctx); err != nil {
			return err
		}
		idx := stepIdx[sid]
		if idx == nil {
			idx = map[string]int{}
			stepIdx[sid] = idx
		}
		idx[x.Ext] = len(steps[sid])
		steps[sid] = append(steps[sid], x)
	}
	return rows.Err()
}

func loadBatchStepVerbs(db *sql.DB, ids []string, steps map[string][]Stp, stepIdx map[string]map[string]int) error {
	ph, args := InPlaceholders(ids)
	rows, err := db.Query(`SELECT session_id, ext_id, n, verb FROM step_verbs WHERE session_id IN (`+ph+`) ORDER BY session_id, ext_id, n`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sid, ext, verb string
		var n int
		if err := rows.Scan(&sid, &ext, &n, &verb); err != nil {
			return err
		}
		if i, ok := stepIdx[sid][ext]; ok {
			steps[sid][i].Verbs = append(steps[sid][i].Verbs, verb)
			steps[sid][i].VerbNs = append(steps[sid][i].VerbNs, n)
		}
	}
	return rows.Err()
}

func loadBatchEvents(db *sql.DB, ids []string, events map[string][]SessEvent, steps map[string][]Stp, stepIdx map[string]map[string]int) error {
	ph, args := InPlaceholders(ids)
	rows, err := db.Query(`SELECT session_id, kind, COALESCE(ref,''), COALESCE(value,''), COALESCE(ts,0) FROM events WHERE session_id IN (`+ph+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		var ev SessEvent
		if err := rows.Scan(&sid, &ev.Kind, &ev.Ref, &ev.Value, &ev.TS); err != nil {
			return err
		}
		if ev.Kind == "hook_block" {
			if i, ok := stepIdx[sid][ev.Ref]; ok {
				steps[sid][i].DenialKind = ev.Value
			}
		}
		events[sid] = append(events[sid], ev)
	}
	return rows.Err()
}
