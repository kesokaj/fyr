package metrics

import (
	"fmt"
	"strings"
)

func (s *Summary) add(x sess, steps []stp) {
	s.addSession(x)
	r := &stepRun{x: x, main: x.parent == "", cursor: x.start, a: s.acc(x.vendor), tk: s.B.Tokens[x.vendor]}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	for _, st := range steps {
		s.addStep(r, st)
	}
	if r.main && r.timed {
		if x.end > r.cursor && r.cursor > 0 {
			r.idleMS += x.end - r.cursor
		}
		s.B.ModelMin[x.vendor] += float64(r.modelMS) / 60000
		s.B.ToolMin[x.vendor] += float64(r.toolMS) / 60000
		s.B.WaitMin[x.vendor] += float64(r.waitMS) / 60000
		s.B.IdleMin[x.vendor] += float64(r.idleMS) / 60000
	}
	// A main session with at least one model call has a peak; a subagent runs
	// inside its parent's window and is not a session of its own here.
	if r.main && r.peak > 0 {
		s.B.SessionPeaks[x.vendor] = append(s.B.SessionPeaks[x.vendor], SessionPeak{Session: x.id, Date: sessionDay(x), Peak: r.peak})
	}
}

// stepRun is one session's state while add walks its steps.
type stepRun struct {
	x                       sess
	main, timed             bool
	a                       *acc
	tk                      *Tokens
	last, cursor, peak      int64
	modelMS, toolMS, waitMS int64
	idleMS                  int64
	lastHash                string
	reads                   int
	seenReads               map[string]bool
}

func (r *stepRun) addTime(st stp) {
	if !r.main {
		return
	}
	var start, end int64
	switch st.kind {
	case "user":
		if r.cursor == 0 && st.ts > 0 {
			r.cursor = st.ts
		}
		return
	case "assistant":
		start, end = st.ts, st.ended
		if start == 0 {
			start = st.started
		}
	case "tool":
		start, end = st.started, st.ended
	default:
		return
	}
	if end <= start || start <= 0 {
		return
	}
	r.timed = true
	if r.cursor > 0 && start > r.cursor {
		r.idleMS += start - r.cursor
	}
	wait := min(max(st.wait, 0), end-start)
	if st.kind == "assistant" {
		r.modelMS += (end - start) - wait
	} else {
		r.toolMS += (end - start) - wait
	}
	r.waitMS += wait
	r.cursor = max(r.cursor, end)
}

// agentType is a subagent's type, "unknown" when it has none.
func agentType(x sess) string {
	if x.agent == "" {
		return "unknown"
	}
	return x.agent
}

// addSession counts the session itself: a main session's count, day and
// wall time or a subagent's type, its cost, and its column's maps.
func (s *Summary) addSession(x sess) {
	v := x.vendor
	// A column's count maps exist before anything counts into them: newSummary
	// makes agy's, and the historical column's are made on its first row.
	for _, m := range []map[string]map[string]int{s.Subagents, s.B.ToolCallsByTool, s.C.Skills, s.D.SubagentToolCalls} {
		if m[v] == nil {
			m[v] = map[string]int{}
		}
	}
	if x.parent == "" {
		s.Sessions[v]++
		if x.start > 0 {
			s.day(x.start).Sessions[v]++
		}
		if x.end > x.start && x.start > 0 {
			s.B.WallMin[v] += float64(x.end-x.start) / 60000
		}
	} else {
		s.Subagents[v][agentType(x)]++
	}
	if x.cost != nil {
		c := *x.cost
		if s.B.CostUSD[v] == nil {
			s.B.CostUSD[v] = new(float64)
		}
		*s.B.CostUSD[v] += c
		if x.start > 0 {
			s.day(x.start).CostUSD[v] += c
		}
	}
	if s.B.Tokens[v] == nil {
		s.B.Tokens[v] = &Tokens{}
	}
}

func (s *Summary) addStep(r *stepRun, st stp) {
	v := r.x.vendor
	if st.kind == "tool" || st.kind == "assistant" || st.kind == "generation" {
		key := v + "\x00" + r.x.label + "\x00" + st.ext // within one label: a sandbox never hides real steps
		if s.seen[key] {
			return // replayed history, counted under the session that first had it
		}
		s.seen[key] = true
	}
	if st.ts > 0 {
		// main sessions only, as wall time: a subagent runs inside its parent
		if r.main && r.last > 0 && st.ts-r.last < activeGap.Milliseconds() {
			s.B.ActiveMin[v] += float64(st.ts-r.last) / 60000
		}
		r.last = st.ts
	}
	r.addTime(st)
	s.addTokens(r, st)
	switch st.kind {
	case "user":
		s.B.Turns[v]++
	case "interrupt":
		r.a.interrupts++
	case "compaction":
		r.a.compactions++
		r.seenReads = nil
	case "tool":
		s.addTool(r, st)
	}
}

func (s *Summary) addTokens(r *stepRun, st stp) {
	v := r.x.vendor
	r.tk.In += st.in
	r.tk.Out += st.out
	r.tk.CacheRead += st.cr
	r.tk.CacheWrite += st.cw
	ctx := st.ctx
	if v == historical && st.kind == "assistant" {
		ctx = st.in + st.cr + st.cw
	}
	s.B.PeakContext[v] = max(s.B.PeakContext[v], ctx)
	r.peak = max(r.peak, ctx)
	if st.ts > 0 && st.in+st.out+st.cr+st.cw > 0 {
		s.day(st.ts).Tokens[v] += st.in + st.out + st.cr + st.cw
	}
}

func (s *Summary) addTool(r *stepRun, st stp) {
	if st.tool == "" {
		return // a result whose call was never seen
	}
	v, a := r.x.vendor, r.a
	a.tools++
	s.B.ToolCalls[v]++
	s.B.ToolCallsByTool[v][st.tool]++
	if !r.main {
		s.D.SubagentToolCalls[v][agentType(r.x)]++
	}
	if st.ts > 0 {
		s.day(st.ts).ToolCalls[v]++
	}
	if st.isErr {
		a.errs++
		if st.ts > 0 {
			s.day(st.ts).Errors[v]++
		}
	}
	if st.denied {
		a.denials++
	}
	if st.skill != "" {
		s.C.Skills[v][st.skill]++
	}
	if st.tool == "Grep" {
		a.grep++
		if st.ts > 0 {
			s.day(st.ts).GrepCalls[v]++
		}
	}
	// A Bash step carries a kind only when it is a record verb
	// (recordRead): a migrated session reads its trail that way.
	if r.main && st.kindOf != "" && r.reads < 3 && (st.tool == "Read" || st.tool == "Bash") {
		s.C.FirstReads[st.kindOf]++
		r.reads++
	}
	if st.tool == "Bash" {
		if st.hash != "" && st.hash == r.lastHash {
			s.D.RetryLoops[v]++
		}
		r.lastHash = st.hash
	}
	s.addFileRead(r, v, st)
	s.verbs(v, st)
}

func (s *Summary) addFileRead(r *stepRun, v string, st stp) {
	ranged, base := parseFileHash(st.hash)
	switch st.tool {
	case "Read":
		if st.isErr || st.denied {
			return
		}
		if s.readCalls == nil {
			s.readCalls = map[string]int{}
			s.rangedReads = map[string]int{}
			s.redundantReads = map[string]int{}
		}
		s.readCalls[v]++
		if ranged {
			s.rangedReads[v]++
		}
		if base != "" {
			if r.seenReads == nil {
				r.seenReads = map[string]bool{}
			}
			if r.seenReads[base] {
				s.redundantReads[v]++
			} else {
				r.seenReads[base] = true
			}
		}
	case "Write", "Edit":
		if !st.isErr && !st.denied && base != "" && r.seenReads != nil {
			delete(r.seenReads, base)
		}
	}
}

func parseFileHash(h string) (bool, string) {
	switch {
	case strings.HasPrefix(h, "r:"):
		return true, strings.TrimPrefix(h, "r:")
	case strings.HasPrefix(h, "f:"):
		return false, strings.TrimPrefix(h, "f:")
	default:
		return false, ""
	}
}

func (s *Summary) verbs(v string, st stp) {
	a := s.acc(v)
	commit, test := false, false
	for _, verb := range st.verbs {
		w := strings.Fields(verb)
		switch {
		case isSQLiteVerb(verb):
			a.sqlite++
			if st.ts > 0 {
				s.day(st.ts).SQLiteVerbs[v]++
			}
		case isGrepVerb(verb):
			a.grep++
			if st.ts > 0 {
				s.day(st.ts).GrepCalls[v]++
			}
		}
		if len(w) >= 2 && w[0] == "git" {
			flags := " " + strings.Join(w[2:], " ") + " "
			switch w[1] {
			case "add":
				if strings.Contains(flags, " -A ") || strings.Contains(flags, " --all ") {
					s.C.Forbidden["git add -A"]++
				}
				if strings.Contains(flags, " . ") {
					s.C.Forbidden["git add ."]++
				}
			case "push":
				if strings.Contains(flags, " -f ") || strings.Contains(flags, " --force ") {
					s.C.Forbidden["git push --force"]++
				}
			case "commit":
				commit = true
			}
		}
		if strings.Contains(" "+verb+" ", " --no-verify ") {
			s.C.Forbidden["--no-verify"]++
		}
		if strings.HasPrefix(verb, "go test") {
			test = true
		}
	}
	if commit && test {
		s.C.Forbidden["commit chained to test"]++
	}
}

func (st *Store) addEvents(s *Summary, x sess) error {
	if x.parent != "" {
		return nil
	}
	rows, err := st.db.Query(`SELECT kind, value FROM events WHERE session_id = ?`, x.id)
	if err != nil {
		return err
	}
	defer rows.Close()
	commits, ambiguous, handoff := 0, 0, false
	for rows.Next() {
		var kind, value string
		if err := rows.Scan(&kind, &value); err != nil {
			return err
		}
		switch kind {
		case "commit":
			if value == "ambiguous" {
				ambiguous++
			} else {
				commits++
			}
		case "handoff":
			handoff = true
		case "verifier":
			s.A.Verifier[value]++
		case "review":
			for _, kv := range strings.Split(value, ",") {
				k, n, _ := strings.Cut(kv, "=")
				var c int
				fmt.Sscan(n, &c)
				s.A.ReviewFindings[k] += c
			}
		case "bug":
			s.A.EscapedBugs++
		case "correction":
			s.A.Corrections++
		}
	}
	c := s.A.DoDClosed[x.vendor]
	c.Of++
	switch {
	case commits > 0 && handoff:
		c.Closed++
	case ambiguous > 0 && handoff:
		c.Ambiguous++
	}
	s.A.DoDClosed[x.vendor] = c
	return rows.Err()
}
