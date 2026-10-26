package metrics

import (
	"fmt"
	"strings"
)

func (s *Summary) add(x sess, steps []stp) {
	v := x.vendor
	// A column's count maps exist before anything counts into them: newSummary
	// makes agy's, and the historical column's are made on its first row.
	for _, m := range []map[string]map[string]int{s.Subagents, s.B.ToolCallsByTool, s.C.Skills, s.D.SubagentToolCalls} {
		if m[v] == nil {
			m[v] = map[string]int{}
		}
	}
	a := s.acc(v)
	main := x.parent == ""
	if main {
		s.Sessions[v]++
		if x.start > 0 {
			s.day(x.start).Sessions[v]++
		}
		if x.end > x.start && x.start > 0 {
			s.B.WallMin[v] += float64(x.end-x.start) / 60000
		}
	} else {
		t := x.agent
		if t == "" {
			t = "unknown"
		}
		s.Subagents[v][t]++
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
	tk := s.B.Tokens[v]
	var last, peak int64
	var lastHash string
	reads := 0
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	for _, st := range steps {
		if st.kind == "tool" || st.kind == "assistant" || st.kind == "generation" {
			key := v + "\x00" + x.label + "\x00" + st.ext // within one label: a sandbox never hides real steps
			if s.seen[key] {
				continue // replayed history, counted under the session that first had it
			}
			s.seen[key] = true
		}
		if st.ts > 0 {
			// main sessions only, as wall time: a subagent runs inside its parent
			if main && last > 0 && st.ts-last < activeGap.Milliseconds() {
				s.B.ActiveMin[v] += float64(st.ts-last) / 60000
			}
			last = st.ts
		}
		tk.In += st.in
		tk.Out += st.out
		tk.CacheRead += st.cr
		tk.CacheWrite += st.cw
		ctx := st.ctx
		if v == historical && st.kind == "assistant" {
			ctx = st.in + st.cr + st.cw
		}
		s.B.PeakContext[v] = max(s.B.PeakContext[v], ctx)
		peak = max(peak, ctx)
		if st.ts > 0 && st.in+st.out+st.cr+st.cw > 0 {
			s.day(st.ts).Tokens[v] += st.in + st.out + st.cr + st.cw
		}
		switch st.kind {
		case "user":
			s.B.Turns[v]++
		case "interrupt":
			a.interrupts++
		case "compaction":
			a.compactions++
		case "tool":
			if st.tool == "" {
				continue // a result whose call was never seen
			}
			a.tools++
			s.B.ToolCalls[v]++
			s.B.ToolCallsByTool[v][st.tool]++
			if !main {
				t := x.agent
				if t == "" {
					t = "unknown"
				}
				s.D.SubagentToolCalls[v][t]++
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
			if main && st.kindOf != "" && reads < 3 && (st.tool == "Read" || st.tool == "Bash") {
				s.C.FirstReads[st.kindOf]++
				reads++
			}
			if st.hash != "" && st.hash == lastHash {
				s.D.RetryLoops[v]++
			}
			if st.tool == "Bash" {
				lastHash = st.hash
			}
			s.verbs(v, st)
		}
	}
	// A main session with at least one model call has a peak; a subagent runs
	// inside its parent's window and is not a session of its own here.
	if main && peak > 0 {
		s.B.SessionPeaks[v] = append(s.B.SessionPeaks[v], SessionPeak{Session: x.id, Date: sessionDay(x), Peak: peak})
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
