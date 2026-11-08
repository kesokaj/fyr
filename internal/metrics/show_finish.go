package metrics

import (
	"fmt"
	"math"
	"sort"
	"strconv"
)

func (s *Summary) finish(all []sess, trials []trial, claimed map[string]bool) {
	s.passK(all, trials, claimed)
	s.C.RedundantReads = map[string]int{"agy": 0}
	s.C.RangedReadPct = map[string]*float64{"agy": nil}
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

// passK is each label's A6 over its trials: the trials rows in the window,
// plus one trial per main session with an outcome that no trial in the
// store claims (a NULL run: stored before migration 4, or by a kit's
// ingest; or a run whose trial was never written), under which a prompt2
// row still counts two (SPEC-fyr-eval § Known limits). A session a trial
// claims is counted by the trial alone, in the trial's own window.
func (s *Summary) passK(all []sess, trials []trial, claimed map[string]bool) {
	labels := map[string]*PassK{}
	count := func(label, status string) {
		p := labels[label]
		if p == nil {
			p = &PassK{}
			labels[label] = p
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
		count(t.label, t.status)
	}
	for _, x := range all {
		if x.parent == "" && x.label != "" && x.outcome != "" && !claimed[x.run] {
			count(x.label, x.outcome)
		}
	}
	for l, p := range labels {
		p.estimate()
		s.A.PassK[l] = *p
	}
}

// estimate fills a label's rates from n and c: the first consumers' keys
// (all n passed), pass@k and pass^k for k in 1, 3, 5 that n reaches, the
// Wilson 95 % interval and flip. A k above n is omitted, never a number.
func (p *PassK) estimate() {
	p.K, p.Passed = p.N, p.C
	if p.N > 0 && p.C == p.N {
		p.PassK = 1
	}
	for _, k := range []int{1, 3, 5} {
		if v, ok := PassAtK(p.N, p.C, k); ok {
			if p.PassAt == nil {
				p.PassAt = map[string]float64{}
			}
			p.PassAt[strconv.Itoa(k)] = round4(v)
		}
		if v, ok := PassPowK(p.N, p.C, k); ok {
			if p.PassPow == nil {
				p.PassPow = map[string]float64{}
			}
			p.PassPow[strconv.Itoa(k)] = round4(v)
		}
	}
	lo, hi := Wilson(p.N, p.C, 1.96)
	p.Wilson95 = [2]float64{round4(lo), round4(hi)}
	if 0 < p.C && p.C < p.N {
		p.Flip = 1
	}
}

// round4 is v at four decimals, the precision a rate is printed with.
func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

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
	s.C.RedundantReads[v] = s.redundantReads[v]
	if total := s.readCalls[v]; total > 0 {
		pct := round4(float64(s.rangedReads[v]) * 100.0 / float64(total))
		s.C.RangedReadPct[v] = &pct
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
