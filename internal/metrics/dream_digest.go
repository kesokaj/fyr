package metrics

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RetryPattern summarizes error/retry counts for one tool name.
type RetryPattern struct {
	Tool  string `json:"tool"`
	Count int    `json:"count"`
}

// VerbLoop summarizes a 2-3 verb sequence seen across multiple sessions.
type VerbLoop struct {
	Verbs    string `json:"verbs"`
	Sessions int    `json:"sessions"`
}

// DreamTrajectoryDigest is a shape-only summary of recent project sessions
// (strictly tool/verb names and counts; zero raw arguments, prose, or paths).
type DreamTrajectoryDigest struct {
	Sessions              int            `json:"sessions"`
	Retries               []RetryPattern `json:"retries,omitempty"`
	RepeatedVerbs         []VerbLoop     `json:"repeatedVerbs,omitempty"`
	RedundantReadHotspots int            `json:"redundantReadHotspots,omitempty"`
}

type dreamEventCounts struct {
	retryTools     map[string]int
	redundantReads int
}

// DreamTrajectoryDigest aggregates shape-only retries, repeated 2-3 verb loops,
// and redundant-read counts across label-free sessions since since.
func (s *Store) DreamTrajectoryDigest(projectRoot string, since time.Time) (DreamTrajectoryDigest, error) {
	if s == nil || s.db == nil {
		return DreamTrajectoryDigest{}, nil
	}
	where, args := dreamSessionFilter(projectRoot, since)
	sessions, err := s.countDreamSessions(where, args)
	if err != nil || sessions == 0 {
		return DreamTrajectoryDigest{}, err
	}
	ev, err := s.readDreamEventCounts(where, args)
	if err != nil {
		return DreamTrajectoryDigest{}, err
	}
	retries, err := s.readDreamRetries(where, args, ev.retryTools)
	if err != nil {
		return DreamTrajectoryDigest{}, err
	}
	verbs, err := s.readDreamRepeatedVerbs(where, args)
	if err != nil {
		return DreamTrajectoryDigest{}, err
	}
	stepReads, err := s.readStepRedundantReads(where, args)
	if err != nil {
		return DreamTrajectoryDigest{}, err
	}
	return DreamTrajectoryDigest{
		Sessions:              sessions,
		Retries:               retries,
		RepeatedVerbs:         verbs,
		RedundantReadHotspots: ev.redundantReads + stepReads,
	}, nil
}

func dreamSessionFilter(projectRoot string, since time.Time) (string, []any) {
	where := `s.label IS NULL`
	var args []any
	if projectRoot != "" {
		p := filepath.Clean(projectRoot)
		where += ` AND (s.project_root = ? OR substr(s.project_root, 1, length(?)) = ?)`
		args = append(args, p, p+"/", p+"/")
	}
	if !since.IsZero() {
		where += ` AND COALESCE(s.started_at, s.ended_at, 0) >= ?`
		args = append(args, since.UnixMilli())
	}
	return where, args
}

func (s *Store) countDreamSessions(where string, args []any) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions s WHERE `+where, args...).Scan(&n)
	return n, err
}

func (s *Store) readDreamEventCounts(where string, args []any) (dreamEventCounts, error) {
	out := dreamEventCounts{retryTools: map[string]int{}}
	q := `SELECT LOWER(e.kind), COALESCE(e.ref, '') FROM events e JOIN sessions s ON s.id = e.session_id WHERE ` +
		where + ` AND LOWER(e.kind) IN ('retry_loop', 'redundant_read')`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, ref string
		if err := rows.Scan(&kind, &ref); err != nil {
			return out, err
		}
		switch kind {
		case "retry_loop":
			tool := cleanShapeTool(ref)
			if tool == "" {
				tool = "Bash"
			}
			out.retryTools[tool]++
		case "redundant_read":
			out.redundantReads++
		}
	}
	return out, rows.Err()
}

func (s *Store) readDreamRetries(where string, args []any, extra map[string]int) ([]RetryPattern, error) {
	counts := map[string]int{}
	for k, v := range extra {
		counts[k] += v
	}
	q := `SELECT COALESCE(st.tool, ''), COUNT(*) FROM steps st JOIN sessions s ON s.id = st.session_id WHERE ` +
		where + ` AND st.is_error = 1 AND COALESCE(st.denied, 0) = 0 AND COALESCE(st.tool, '') != '' GROUP BY st.tool`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tool string
		var n int
		if err := rows.Scan(&tool, &n); err != nil {
			return nil, err
		}
		if t := cleanShapeTool(tool); t != "" {
			counts[t] += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return topRetryPatterns(counts, 5), nil
}

func topRetryPatterns(counts map[string]int, limit int) []RetryPattern {
	var out []RetryPattern
	for tool, n := range counts {
		if n > 0 {
			out = append(out, RetryPattern{Tool: tool, Count: n})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Tool < out[j].Tool
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Store) readDreamRepeatedVerbs(where string, args []any) ([]VerbLoop, error) {
	q := `SELECT sv.session_id, sv.verb FROM step_verbs sv JOIN sessions s ON s.id = sv.session_id WHERE ` +
		where + ` ORDER BY sv.session_id, sv.ext_id, sv.n`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bySess := map[string][]string{}
	for rows.Next() {
		var sid, verb string
		if err := rows.Scan(&sid, &verb); err != nil {
			return nil, err
		}
		if v := cleanShapeVerb(verb); v != "" {
			bySess[sid] = append(bySess[sid], v)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return topVerbLoops(bySess, 5), nil
}

func topVerbLoops(bySess map[string][]string, limit int) []VerbLoop {
	patSess := map[string]int{}
	for _, verbs := range bySess {
		recordSessionVerbPatterns(verbs, patSess)
	}
	var out []VerbLoop
	for pat, n := range patSess {
		if n >= 2 {
			out = append(out, VerbLoop{Verbs: pat, Sessions: n})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return out[i].Verbs < out[j].Verbs
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func recordSessionVerbPatterns(verbs []string, patSess map[string]int) {
	seen := map[string]bool{}
	for size := 2; size <= 3; size++ {
		for i := 0; i+size <= len(verbs); i++ {
			pat := strings.Join(verbs[i:i+size], " -> ")
			if !seen[pat] {
				seen[pat] = true
				patSess[pat]++
			}
		}
	}
}

func (s *Store) readStepRedundantReads(where string, args []any) (int, error) {
	q := `SELECT st.session_id, COALESCE(st.tool, ''), COALESCE(st.cmd_hash, '') FROM steps st JOIN sessions s ON s.id = st.session_id WHERE ` +
		where + ` AND st.kind = 'tool' AND st.tool IN ('Read', 'Write', 'Edit') AND COALESCE(st.is_error, 0) = 0 AND COALESCE(st.denied, 0) = 0 ORDER BY st.session_id, st.ts, st.ext_id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	total, curSid := 0, ""
	seen := map[string][][2]int{}
	for rows.Next() {
		var sid, tool, hash string
		if err := rows.Scan(&sid, &tool, &hash); err != nil {
			return 0, err
		}
		if sid != curSid {
			curSid = sid
			seen = map[string][][2]int{}
		}
		_, base, start, end := parseFileHashRange(hash)
		if base == "" {
			continue
		}
		if tool == "Read" {
			if recordReadInterval(seen, base, start, end) {
				total++
			}
		} else {
			delete(seen, base)
		}
	}
	return total, rows.Err()
}

func cleanShapeTool(s string) string {
	t := strings.TrimSpace(s)
	if t == "" || len(t) > 64 || strings.ContainsAny(t, "/\\\r\n\t ") {
		return ""
	}
	return t
}

func cleanShapeVerb(s string) string {
	v := strings.TrimSpace(s)
	if v == "" || len(v) > 64 || strings.ContainsAny(v, "/\\\r\n\t") {
		return ""
	}
	return v
}
