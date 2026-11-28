package show

import (
	"fmt"
	"sort"
	"strconv"

	r "github.com/kesokaj/fyr/internal/metrics/report"
)

// Finish turns accumulated totals into rates, columns, context peaks, and notes.
func (s *Summary) Finish(all []Sess, trials []Trial, claimed map[string]bool, est Estimators) {
	s.PassK(all, trials, claimed, est)
	s.C.RedundantReads = map[string]int{"agy": 0}
	s.C.RangedReadPct = map[string]*float64{"agy": nil}
	s.Columns = []string{"agy"}
	if s.Sessions[Historical] > 0 || len(s.Subagents[Historical]) > 0 {
		s.Columns = append(s.Columns, Historical)
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

// PassK computes per-label pass@k / pass^k / Wilson 95% intervals into s.A.PassK.
func (s *Summary) PassK(all []Sess, trials []Trial, claimed map[string]bool, est Estimators) {
	labels := map[string]*r.PassK{}
	count := func(lbl, status string) {
		p := labels[lbl]
		if p == nil {
			p = &r.PassK{}
			labels[lbl] = p
		}
		switch status {
		case "pass":
			p.N++
			p.C++
		case "fail":
			p.N++
		case "error":
			p.Errors++
		}
	}
	for _, t := range trials {
		count(t.Label, t.Status)
	}
	for _, x := range all {
		if x.Parent == "" && x.Label != "" && x.Outcome != "" && !claimed[x.Run] {
			count(x.Label, x.Outcome)
		}
	}
	for l, p := range labels {
		estimatePassK(p, est)
		s.A.PassK[l] = *p
	}
}

func estimatePassK(p *r.PassK, est Estimators) {
	p.K, p.Passed = p.N, p.C
	if p.N > 0 && p.C == p.N {
		p.PassK = 1
	}
	for _, k := range []int{1, 3, 5} {
		recordPassKMetric(&p.PassAt, est.AtK, p.N, p.C, k)
		recordPassKMetric(&p.PassPow, est.PowK, p.N, p.C, k)
	}
	if est.Wilson != nil {
		lo, hi := est.Wilson(p.N, p.C, 1.96)
		p.Wilson95 = [2]float64{r.Round4(lo), r.Round4(hi)}
	}
	if 0 < p.C && p.C < p.N {
		p.Flip = 1
	}
}

func recordPassKMetric(dst *map[string]float64, fn func(n, c, k int) (float64, bool), n, c, k int) {
	if fn == nil {
		return
	}
	v, ok := fn(n, c, k)
	if !ok {
		return
	}
	if *dst == nil {
		*dst = map[string]float64{}
	}
	(*dst)[strconv.Itoa(k)] = r.Round4(v)
}

func (s *Summary) finishColumn(v string) {
	a := s.acc(v)
	if s.Sessions[v] == 0 && len(s.Subagents[v]) == 0 {
		return
	}
	if a.tools > 0 {
		rate := float64(a.errs) / float64(a.tools)
		s.D.ToolErrorRate[v] = &rate
	}
	if s.D.ToolErrors == nil {
		s.D.ToolErrors = map[string]int{}
	}
	s.D.ToolErrors[v] = a.errs
	if v != Historical {
		if s.D.HookDenials == nil {
			s.D.HookDenials = map[string]int{}
		}
		s.D.HookDenials[v] = a.denials
	}
	s.finishColumnReadsAndSearch(v, a)
	s.finishColumnNotes(v, a)
}

func (s *Summary) finishColumnReadsAndSearch(v string, a *acc) {
	s.C.SQLiteVerbs[v] = a.sqlite
	s.C.GrepCalls[v] = a.grep
	if a.grep > 0 {
		ratio := float64(a.sqlite) / float64(a.grep)
		s.C.SearchGrepRatio[v] = &ratio
	}
	s.C.RedundantReads[v] = s.redundantReads[v]
	if total := s.readCalls[v]; total > 0 {
		pct := r.Round4(float64(s.rangedReads[v]) * 100.0 / float64(total))
		s.C.RangedReadPct[v] = &pct
	}
}

func (s *Summary) finishColumnNotes(v string, a *acc) {
	if v == Historical {
		d, i, c := a.denials, a.interrupts, a.compactions
		s.D.Denials[v], s.D.Interrupts[v], s.B.Compactions[v] = &d, &i, &c
		if s.B.CostUSD[v] == nil {
			s.Notes = append(s.Notes, colLabel(v)+": no session in the window has ended with a cost line")
		}
		return
	}
	s.D.Denials[v], s.D.Interrupts[v], s.B.Compactions[v] = nil, nil, nil
	s.Notes = append(s.Notes, "agy: interrupts are not recorded by the CLI; hook denials and compactions appear in health signals")
	if s.B.CostUSD[v] == nil {
		s.Notes = append(s.Notes, "agy: cost is not exposed by the CLI")
	}
}

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

func (s *Summary) overTarget(v string) string {
	p := s.B.SessionsOverContextTarget[v]
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d of %d", *p, len(s.B.SessionPeaks[v]))
}
