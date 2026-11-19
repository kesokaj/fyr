package metrics

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// ExpectMatch is a suite row's expect entries against what its trials used,
// pooled over the trials: M entries found of N asked (ADR-085). It never
// gates.
type ExpectMatch struct {
	M int `json:"m"`
	N int `json:"n"`
}

// String is "m of n", or "-" when nothing was asked.
func (x ExpectMatch) String() string {
	if x.N == 0 {
		return "-"
	}
	return fmt.Sprintf("%d of %d", x.M, x.N)
}

// add pools y into x.
func (x *ExpectMatch) add(y ExpectMatch) {
	x.M += y.M
	x.N += y.N
}

// expectFound reports whether a trial used entry: a tool the store names, or
// a verb the entry is, or leads word by word (`fyr record` is found in
// `fyr record add --title`, never in `fyr recorder`).
func expectFound(entry string, tools, verbs map[string]bool) bool {
	if tools[entry] || verbs[entry] {
		return true
	}
	for v := range verbs {
		if strings.HasPrefix(v, entry+" ") {
			return true
		}
	}
	return false
}

// matchExpect is expect, a stored comma-joined list, against one trial's
// tools and verbs, in any order.
func matchExpect(expect string, tools, verbs map[string]bool) ExpectMatch {
	var x ExpectMatch
	for _, e := range strings.Split(expect, ",") {
		x.N++
		if expectFound(e, tools, verbs) {
			x.M++
		}
	}
	return x
}

// ExpectMatches is, per run among runs, its expect list matched at read time
// against the tools and verbs of its main sessions together (a prompt2
// trial has two). A trial with no expect, an ERROR, or no main session in
// the store has no entry: nothing says what such a trial used.
func (s *Store) ExpectMatches(runs []string) (map[string]ExpectMatch, error) {
	uniqueRuns := distinctRuns(runs)
	if len(uniqueRuns) == 0 {
		return map[string]ExpectMatch{}, nil
	}
	expects, err := s.loadExpectsForRuns(uniqueRuns)
	if err != nil || len(expects) == 0 {
		return map[string]ExpectMatch{}, err
	}
	matchedRuns := sortedExpectRuns(expects)
	sessToRun, hasSession, sessionIDs, err := s.loadRunMainSessions(matchedRuns)
	if err != nil || len(sessionIDs) == 0 {
		return map[string]ExpectMatch{}, err
	}
	toolsByRun, verbsByRun, err := s.loadRunToolsAndVerbs(sessToRun, sessionIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ExpectMatch, len(expects))
	for run, expect := range expects {
		if hasSession[run] {
			out[run] = matchExpect(expect, toolsByRun[run], verbsByRun[run])
		}
	}
	return out, nil
}

func distinctRuns(runs []string) []string {
	seen := make(map[string]bool, len(runs))
	out := make([]string, 0, len(runs))
	for _, r := range runs {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

func sortedExpectRuns(expects map[string]string) []string {
	runs := make([]string, 0, len(expects))
	for r := range expects {
		runs = append(runs, r)
	}
	sort.Strings(runs)
	return runs
}

func (s *Store) loadExpectsForRuns(runs []string) (map[string]string, error) {
	expects := map[string]string{}
	for off := 0; off < len(runs); off += showBatchChunk {
		chunk := runs[off:min(off+showBatchChunk, len(runs))]
		ph, args := inPlaceholders(chunk)
		q := `SELECT run, expect FROM trials WHERE run IN (` + ph + `) AND expect IS NOT NULL AND expect != '' AND status IN ('pass', 'fail')`
		rows, err := s.db.Query(q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var run, expect string
			if err := rows.Scan(&run, &expect); err != nil {
				rows.Close()
				return nil, err
			}
			expects[run] = expect
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return expects, nil
}

func (s *Store) loadRunMainSessions(runs []string) (map[string]string, map[string]bool, []string, error) {
	sessToRun := map[string]string{}
	hasSession := map[string]bool{}
	var sessionIDs []string
	for off := 0; off < len(runs); off += showBatchChunk {
		chunk := runs[off:min(off+showBatchChunk, len(runs))]
		ph, args := inPlaceholders(chunk)
		rows, err := s.db.Query(`SELECT id, run FROM sessions WHERE run IN (`+ph+`) AND parent_id IS NULL`, args...)
		if err != nil {
			return nil, nil, nil, err
		}
		for rows.Next() {
			var id, run string
			if err := rows.Scan(&id, &run); err != nil {
				rows.Close()
				return nil, nil, nil, err
			}
			hasSession[run] = true
			if sessToRun[id] == "" {
				sessToRun[id] = run
				sessionIDs = append(sessionIDs, id)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		rows.Close()
	}
	return sessToRun, hasSession, sessionIDs, nil
}

func (s *Store) loadRunToolsAndVerbs(sessToRun map[string]string, sessionIDs []string) (map[string]map[string]bool, map[string]map[string]bool, error) {
	toolsByRun := map[string]map[string]bool{}
	verbsByRun := map[string]map[string]bool{}
	for off := 0; off < len(sessionIDs); off += showBatchChunk {
		chunk := sessionIDs[off:min(off+showBatchChunk, len(sessionIDs))]
		if err := loadBatchRunTools(s.db, chunk, sessToRun, toolsByRun); err != nil {
			return nil, nil, err
		}
		if err := loadBatchRunToolVerbs(s.db, chunk, sessToRun, verbsByRun); err != nil {
			return nil, nil, err
		}
	}
	return toolsByRun, verbsByRun, nil
}

func loadBatchRunTools(db *sql.DB, chunk []string, sessToRun map[string]string, out map[string]map[string]bool) error {
	ph, args := inPlaceholders(chunk)
	rows, err := db.Query(`SELECT session_id, COALESCE(tool, '') FROM steps WHERE session_id IN (`+ph+`) AND kind = 'tool' AND COALESCE(tool, '') != ''`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sid, tool string
		if err := rows.Scan(&sid, &tool); err != nil {
			return err
		}
		ensureRunSet(out, sessToRun[sid])[tool] = true
	}
	return rows.Err()
}

func loadBatchRunToolVerbs(db *sql.DB, chunk []string, sessToRun map[string]string, out map[string]map[string]bool) error {
	ph, args := inPlaceholders(chunk)
	q := `SELECT sv.session_id, sv.verb FROM step_verbs sv JOIN steps st ON st.session_id = sv.session_id AND st.ext_id = sv.ext_id WHERE sv.session_id IN (` + ph + `) AND st.kind = 'tool'`
	rows, err := db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sid, verb string
		if err := rows.Scan(&sid, &verb); err != nil {
			return err
		}
		ensureRunSet(out, sessToRun[sid])[verb] = true
	}
	return rows.Err()
}

func ensureRunSet(m map[string]map[string]bool, run string) map[string]bool {
	set := m[run]
	if set == nil {
		set = map[string]bool{}
		m[run] = set
	}
	return set
}

// addExpect pools each line's counted trials' matches into the line.
func (s *Store) addExpect(lines map[rowKey]*rowAcc) error {
	var runs []string
	for _, a := range lines {
		runs = append(runs, a.runs...)
	}
	matches, err := s.ExpectMatches(runs)
	if err != nil {
		return err
	}
	for _, a := range lines {
		for _, r := range a.runs {
			a.expect.add(matches[r])
		}
	}
	return nil
}
