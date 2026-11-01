package metrics

import (
	"path/filepath"
	"strings"
	"time"
)

// ProjectVerbs reads each session's normalised command verbs under root,
// newest session first up to maxSessions sessions, for fyr auto's distill
// trigger.
func (s *Store) ProjectVerbs(root string, maxSessions int) (map[string][]string, []string, error) {
	p := filepath.Clean(root)
	rows, err := s.db.Query(`SELECT s.id, sv.verb FROM sessions s JOIN step_verbs sv ON sv.session_id = s.id
		WHERE (s.project_root = ? OR substr(s.project_root, 1, length(?)) = ?)
		ORDER BY COALESCE(s.started_at, s.ended_at, 0) DESC, s.id, sv.ext_id, sv.n`, p, p+"/", p+"/")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var order []string
	bySess := map[string][]string{}
	for rows.Next() {
		var sid, verb string
		if err := rows.Scan(&sid, &verb); err != nil {
			return nil, nil, err
		}
		if strings.TrimSpace(verb) == "" {
			continue
		}
		if _, ok := bySess[sid]; !ok {
			if maxSessions > 0 && len(order) >= maxSessions {
				continue
			}
			order = append(order, sid)
		}
		bySess[sid] = append(bySess[sid], verb)
	}
	return bySess, order, rows.Err()
}

// ProjectFrictions reads each main session's friction count (denials plus
// consecutive identical Bash command hashes) under root since since, ordered
// by session start time, for fyr auto's context-adequacy trigger.
func (s *Store) ProjectFrictions(root string, since time.Time) ([]int, error) {
	p := filepath.Clean(root)
	rows, err := s.db.Query(`SELECT s.id, COALESCE(st.tool, ''), COALESCE(st.cmd_hash, ''), COALESCE(st.denied, 0)
		FROM sessions s LEFT JOIN steps st ON st.session_id = s.id
		WHERE COALESCE(s.parent_id, '') = ''
		  AND (s.project_root = ? OR substr(s.project_root, 1, length(?)) = ?)
		  AND COALESCE(s.started_at, s.ended_at, 0) >= ?
		ORDER BY COALESCE(s.started_at, s.ended_at, 0), s.id, st.ts, st.ext_id`, p, p+"/", p+"/", since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var order []string
	friction := map[string]int{}
	lastHash := map[string]string{}
	for rows.Next() {
		var sid, tool, hash string
		var denied int
		if err := rows.Scan(&sid, &tool, &hash, &denied); err != nil {
			return nil, err
		}
		order = accumulateSessionFriction(order, friction, lastHash, sid, tool, hash, denied)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	vals := make([]int, 0, len(order))
	for _, sid := range order {
		vals = append(vals, friction[sid])
	}
	return vals, nil
}

func accumulateSessionFriction(order []string, friction map[string]int, lastHash map[string]string, sid, tool, hash string, denied int) []string {
	if _, ok := friction[sid]; !ok {
		order = append(order, sid)
		friction[sid] = 0
	}
	if denied != 0 || (hash != "" && hash == lastHash[sid]) {
		friction[sid]++
	}
	if tool == "Bash" || hash != "" {
		lastHash[sid] = hash
	}
	return order
}

// SuiteTrials reads every eval= row from trials in chronological order, for
// fyr auto's eval-rerun trigger.
func (s *Store) SuiteTrials() ([]Trial, error) {
	rows, err := s.db.Query(`SELECT run, label, harness, COALESCE(cli, ''), status, started_at
		FROM trials WHERE substr(label, 1, length(?)) = ? ORDER BY started_at ASC, run ASC`, evalPrefix, evalPrefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Trial
	for rows.Next() {
		var t Trial
		var started int64
		if err := rows.Scan(&t.Run, &t.Label, &t.Harness, &t.CLI, &t.Status, &started); err != nil {
			return nil, err
		}
		t.StartedAt = time.UnixMilli(started)
		out = append(out, t)
	}
	return out, rows.Err()
}
