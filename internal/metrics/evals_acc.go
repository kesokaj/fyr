package metrics

import "time"

// rowAcc gathers one line of the view: its trials' counts, then the steps
// of their main sessions.
type rowAcc struct {
	n, c, errors int
	last         int64
	runs         []string // the counted trials' runs
	unclaimed    []string // the sessions counted as trials of their own
	seen         map[string]bool

	sessions, durN               int
	turns, tools, errs, retries  int64
	in, out, cache, peaks, durMs int64
	activeMs                     int64
	seqs                         [][]string
}

func (a *rowAcc) count(t evalTrial) {
	switch t.status {
	case "pass":
		a.n++
		a.c++
	case "fail":
		a.n++
	case "error":
		a.errors++
	}
	a.last = max(a.last, t.start)
	if t.sessionID != "" {
		a.unclaimed = append(a.unclaimed, t.sessionID)
	} else {
		a.runs = append(a.runs, t.run)
	}
}

// addSessions walks the main sessions of the lines' trials: a session whose
// run a counted trial holds, or one counted as a trial itself.
func (s *Store) addSessions(lines map[rowKey]*rowAcc) error {
	byRun, byID := map[string]*rowAcc{}, map[string]*rowAcc{}
	for _, a := range lines {
		for _, r := range a.runs {
			byRun[r] = a
		}
		for _, id := range a.unclaimed {
			byID[id] = a
		}
	}
	rows, err := s.db.Query(`SELECT id, COALESCE(run, ''), COALESCE(started_at, 0), COALESCE(ended_at, 0) FROM sessions
		WHERE parent_id IS NULL AND substr(label, 1, length(?)) = ? ORDER BY started_at, id`, evalPrefix, evalPrefix)
	if err != nil {
		return err
	}
	type pick struct {
		id         string
		start, end int64
		a          *rowAcc
	}
	var picks []pick
	for rows.Next() {
		var p pick
		var run string
		if err := rows.Scan(&p.id, &run, &p.start, &p.end); err != nil {
			rows.Close()
			return err
		}
		if p.a = byID[p.id]; p.a == nil && run != "" {
			p.a = byRun[run]
		}
		if p.a != nil {
			picks = append(picks, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range picks {
		steps, err := s.stepsOf(p.id)
		if err != nil {
			return err
		}
		p.a.addSession(p.start, p.end, steps)
	}
	return nil
}

// addSession counts one main session as show counts it (show_add.go): a
// user step is a turn, a tool step with a tool a call, a Bash call whose
// command hash repeats the last one a retry, a gap under activeGap active
// time; a step replayed from another session of the row is counted once.
func (a *rowAcc) addSession(start, end int64, steps []stp) {
	a.sessions++
	if end > start && start > 0 {
		a.durMs += end - start
		a.durN++
	}
	var last, peak int64
	lastHash := ""
	var seq []string
	for _, st := range steps {
		if st.kind == "tool" || st.kind == "assistant" || st.kind == "generation" {
			if a.seen[st.ext] {
				continue
			}
			a.seen[st.ext] = true
		}
		if st.ts > 0 {
			if last > 0 && st.ts-last < activeGap.Milliseconds() {
				a.activeMs += st.ts - last
			}
			last = st.ts
		}
		a.in += st.in
		a.out += st.out
		a.cache += st.cr + st.cw
		peak = max(peak, st.ctx)
		switch {
		case st.kind == "user":
			a.turns++
		case st.kind == "tool" && st.tool != "":
			a.tools++
			seq = append(seq, st.tool)
			if st.isErr {
				a.errs++
			}
			if st.hash != "" && st.hash == lastHash {
				a.retries++
			}
			if st.tool == "Bash" {
				lastHash = st.hash
			}
		}
	}
	a.peaks += peak
	a.seqs = append(a.seqs, seq)
}

// row is the line's numbers: absent where the line has none to give.
func (a *rowAcc) row(k rowKey, flags []string) EvalRow {
	r := EvalRow{Row: rowName(k.label), Version: k.version, N: a.n, C: a.c, Errors: a.errors, Flags: flags,
		Last: time.UnixMilli(a.last).UTC().Format("2006-01-02")}
	if r.Flags == nil {
		r.Flags = []string{}
	}
	if v, ok := PassAtK(a.n, a.c, 1); ok {
		r.PassAt1 = ptr(round4(v))
	}
	if v, ok := PassPowK(a.n, a.c, 3); ok {
		r.PassPow3 = ptr(round4(v))
	}
	if a.n > 0 {
		lo, hi := Wilson(a.n, a.c, 1.96)
		r.Wilson95 = &[2]float64{round4(lo), round4(hi)}
	}
	if 0 < a.c && a.c < a.n {
		r.Flip = 1
	}
	if a.sessions > 0 {
		r.Means = a.means()
	}
	return r
}

func (a *rowAcc) means() *EvalMeans {
	n := float64(a.sessions)
	m := &EvalMeans{Sessions: a.sessions,
		Turns: round4(float64(a.turns) / n), ToolCalls: round4(float64(a.tools) / n),
		TokensIn: round4(float64(a.in) / n), TokensOut: round4(float64(a.out) / n), TokensCache: round4(float64(a.cache) / n),
		ActiveSeconds: round4(float64(a.activeMs) / 1000 / n), Retries: round4(float64(a.retries) / n),
		PeakContext: round4(float64(a.peaks) / n)}
	if a.durN > 0 {
		m.SessionSeconds = ptr(round4(float64(a.durMs) / 1000 / float64(a.durN)))
	}
	if a.tools > 0 {
		m.ToolErrorRate = ptr(round4(float64(a.errs) / float64(a.tools)))
	}
	return m
}

// suiteOf is the suite over the view's rows with a graded trial.
func suiteOf(rows []EvalRow, lines map[rowKey]*rowAcc, keys []rowKey) EvalSuite {
	var su EvalSuite
	var ps []float64
	simSum := 0.0
	for i, r := range rows {
		su.Errors += r.Errors
		if r.N > 0 {
			su.Rows++
			su.Trials += r.N
			ps = append(ps, float64(r.C)/float64(r.N))
		}
		if seqs := lines[keys[i]].seqs; len(seqs) >= 2 {
			simSum += pairwise(seqs)
			su.SimilarityOfRows++
		}
	}
	if len(ps) > 0 {
		mean := 0.0
		for _, p := range ps {
			mean += p
		}
		mean /= float64(len(ps))
		lo, hi := WilsonRate(len(ps), mean, 1.96)
		su.PassRate = ptr(round4(mean))
		su.Wilson95 = &[2]float64{round4(lo), round4(hi)}
		su.COut = ptr(round4(OutcomeConsistency(ps)))
	}
	if su.SimilarityOfRows > 0 {
		su.Similarity = ptr(round4(simSum / float64(su.SimilarityOfRows)))
	}
	return su
}

// pairwise is the mean SeqSimilarity over every pair of seqs (two or more).
func pairwise(seqs [][]string) float64 {
	sum, pairs := 0.0, 0
	for i := range seqs {
		for j := i + 1; j < len(seqs); j++ {
			sum += SeqSimilarity(seqs[i], seqs[j])
			pairs++
		}
	}
	return sum / float64(pairs)
}

func ptr[T any](v T) *T { return &v }
