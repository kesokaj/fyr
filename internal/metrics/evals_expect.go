package metrics

import (
	"fmt"
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
	wanted := map[string]bool{}
	for _, r := range runs {
		wanted[r] = true
	}
	rows, err := s.db.Query(`SELECT run, expect FROM trials WHERE expect IS NOT NULL AND expect != '' AND status IN ('pass', 'fail')`)
	if err != nil {
		return nil, err
	}
	expects := map[string]string{}
	for rows.Next() {
		var run, expect string
		if err := rows.Scan(&run, &expect); err != nil {
			rows.Close()
			return nil, err
		}
		if wanted[run] {
			expects[run] = expect
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[string]ExpectMatch{}
	for run, expect := range expects {
		tools, verbs, sessions, err := s.runUses(run)
		if err != nil {
			return nil, err
		}
		if sessions > 0 {
			out[run] = matchExpect(expect, tools, verbs)
		}
	}
	return out, nil
}

// runUses is the tools and verbs of a run's main sessions, and how many
// sessions those are.
func (s *Store) runUses(run string) (tools, verbs map[string]bool, sessions int, err error) {
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE run = ? AND parent_id IS NULL`, run)
	if err != nil {
		return nil, nil, 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, 0, err
	}
	tools, verbs = map[string]bool{}, map[string]bool{}
	for _, id := range ids {
		steps, err := s.stepsOf(id)
		if err != nil {
			return nil, nil, 0, err
		}
		for _, st := range steps {
			if st.kind != "tool" {
				continue
			}
			if st.tool != "" {
				tools[st.tool] = true
			}
			for _, v := range st.verbs {
				verbs[v] = true
			}
		}
	}
	return tools, verbs, len(ids), nil
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
