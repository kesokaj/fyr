package metrics

import (
	"fmt"
	"sort"
)

func (s *Summary) finish(all []sess) {
	s.passK(all)
	s.Columns = []string{"agy"}
	if s.Sessions[historical] > 0 || len(s.Subagents[historical]) > 0 {
		s.Columns = append(s.Columns, historical)
	}
	for _, v := range s.Columns {
		s.finishContext(v)
		s.finishColumn(v)
	}
	sort.Slice(s.Daily, func(i, j int) bool { return s.Daily[i].Date < s.Daily[j].Date })
	for _, c := range s.A.DoDClosed {
		if c.Ambiguous > 0 {
			s.Notes = append(s.Notes, "some sessions overlap another in the same project; their commits are counted as ambiguous, not closed")
			break
		}
	}
}

// passK is each label's pass^k: of its main sessions with an outcome, how
// many passed, and 1 when every one did.
func (s *Summary) passK(all []sess) {
	labels := map[string]*PassK{}
	for _, x := range all {
		if x.parent != "" || x.label == "" || x.outcome == "" {
			continue
		}
		p := labels[x.label]
		if p == nil {
			p = &PassK{}
			labels[x.label] = p
		}
		p.K++
		if x.outcome == "pass" {
			p.Passed++
		}
	}
	for l, p := range labels {
		if p.Passed == p.K {
			p.PassK = 1
		}
		s.A.PassK[l] = *p
	}
}

// finishColumn turns column v's totals into its rates, and notes what the
// column's CLI does not record; a column with no session has none.
func (s *Summary) finishColumn(v string) {
	a := s.acc(v)
	if s.Sessions[v] == 0 && len(s.Subagents[v]) == 0 {
		return
	}
	if a.tools > 0 {
		r := float64(a.errs) / float64(a.tools)
		s.D.ToolErrorRate[v] = &r
	}
	s.C.SQLiteVerbs[v] = a.sqlite
	s.C.GrepCalls[v] = a.grep
	if a.grep > 0 {
		r := float64(a.sqlite) / float64(a.grep)
		s.C.SearchGrepRatio[v] = &r
	}
	if v == historical {
		d, i, c := a.denials, a.interrupts, a.compactions
		s.D.Denials[v], s.D.Interrupts[v], s.B.Compactions[v] = &d, &i, &c
		if s.B.CostUSD[v] == nil {
			s.Notes = append(s.Notes, label(v)+": no session in the window has ended with a cost line")
		}
	} else {
		s.D.Denials[v], s.D.Interrupts[v], s.B.Compactions[v] = nil, nil, nil
		s.Notes = append(s.Notes, "agy: denials, interrupts and compactions are not recorded by the CLI")
		if s.B.CostUSD[v] == nil {
			s.Notes = append(s.Notes, "agy: cost is not exposed by the CLI")
		}
	}
}

// finishContext orders v's session peaks, largest first, and counts those
// past the target; with no peak the count is nil and a note says so.
func (s *Summary) finishContext(v string) {
	peaks := s.B.SessionPeaks[v]
	if len(peaks) == 0 {
		delete(s.B.SessionPeaks, v)
		s.B.SessionsOverContextTarget[v] = nil
		s.Notes = append(s.Notes, v+": no main session in the window carries a context value; sessions over context target is n/a")
		return
	}
	sort.Slice(peaks, func(i, j int) bool {
		if peaks[i].Peak != peaks[j].Peak {
			return peaks[i].Peak > peaks[j].Peak
		}
		return peaks[i].Session < peaks[j].Session
	})
	over := 0
	for _, p := range peaks {
		if p.Peak > s.B.ContextTarget {
			over++
		}
	}
	s.B.SessionsOverContextTarget[v] = &over
}

// overTarget renders the over-target count as "k of n", n the main sessions
// with a peak, or "n/a".
func (s *Summary) overTarget(v string) string {
	p := s.B.SessionsOverContextTarget[v]
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d of %d", *p, len(s.B.SessionPeaks[v]))
}
