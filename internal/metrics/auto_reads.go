package metrics

import (
	"path/filepath"
	"strings"
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
