package show

import (
	"fmt"
	"strings"

	r "github.com/kesokaj/fyr/internal/metrics/report"
)

// Add accumulates one session and its steps into the summary.
func (s *Summary) Add(x Sess, steps []Stp) {
	s.addSession(x)
	run := &stepRun{x: x, main: x.Parent == "", cursor: x.Start, a: s.acc(x.Vendor), tk: s.B.Tokens[x.Vendor]}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	for _, st := range steps {
		s.addStep(run, st)
	}
	if run.main && run.timed {
		if x.End > run.cursor && run.cursor > 0 {
			run.idleMS += x.End - run.cursor
		}
		s.B.ModelMin[x.Vendor] += float64(run.modelMS) / 60000
		s.B.ToolMin[x.Vendor] += float64(run.toolMS) / 60000
		s.B.WaitMin[x.Vendor] += float64(run.waitMS) / 60000
		s.B.IdleMin[x.Vendor] += float64(run.idleMS) / 60000
	}
	if run.main && run.peak > 0 {
		s.B.SessionPeaks[x.Vendor] = append(s.B.SessionPeaks[x.Vendor], r.SessionPeak{Session: x.ID, Date: sessionDay(x), Peak: run.peak})
	}
}

type stepRun struct {
	x                       Sess
	main, timed             bool
	a                       *acc
	tk                      *r.Tokens
	last, cursor, peak      int64
	modelMS, toolMS, waitMS int64
	idleMS                  int64
	lastHash                string
	reads                   int
	seenReads               map[string][][2]int
}

func (sr *stepRun) addTime(st Stp) {
	if !sr.main {
		return
	}
	var start, end int64
	switch st.Kind {
	case "user":
		if sr.cursor == 0 && st.TS > 0 {
			sr.cursor = st.TS
		}
		return
	case "assistant":
		start, end = st.TS, st.Ended
		if start == 0 {
			start = st.Started
		}
	case "tool":
		start, end = st.Started, st.Ended
	default:
		return
	}
	if end <= start || start <= 0 {
		return
	}
	sr.timed = true
	if sr.cursor > 0 && start > sr.cursor {
		sr.idleMS += start - sr.cursor
	}
	wait := min(max(st.Wait, 0), end-start)
	if st.Kind == "assistant" {
		sr.modelMS += (end - start) - wait
	} else {
		sr.toolMS += (end - start) - wait
	}
	sr.waitMS += wait
	sr.cursor = max(sr.cursor, end)
}

func agentType(x Sess) string {
	if x.Agent == "" {
		return "unknown"
	}
	return x.Agent
}

func (s *Summary) addSession(x Sess) {
	v := x.Vendor
	s.ensureVendorMaps(v)
	if x.Parent == "" {
		s.addMainSession(x)
	} else {
		s.Subagents[v][agentType(x)]++
	}
	s.addSessionCost(x)
	if s.B.Tokens[v] == nil {
		s.B.Tokens[v] = &r.Tokens{}
	}
}

func (s *Summary) ensureVendorMaps(v string) {
	for _, m := range []map[string]map[string]int{s.Subagents, s.B.ToolCallsByTool, s.C.Skills, s.D.SubagentToolCalls} {
		if m[v] == nil {
			m[v] = map[string]int{}
		}
	}
}

func (s *Summary) addMainSession(x Sess) {
	v := x.Vendor
	s.Sessions[v]++
	if x.Start > 0 {
		s.day(x.Start).Sessions[v]++
	}
	if x.End > x.Start && x.Start > 0 {
		s.B.WallMin[v] += float64(x.End-x.Start) / 60000
	}
}

func (s *Summary) addSessionCost(x Sess) {
	if x.Cost == nil {
		return
	}
	v, c := x.Vendor, *x.Cost
	if s.B.CostUSD[v] == nil {
		s.B.CostUSD[v] = new(float64)
	}
	*s.B.CostUSD[v] += c
	if x.Start > 0 {
		s.day(x.Start).CostUSD[v] += c
	}
}

func (s *Summary) addStep(sr *stepRun, st Stp) {
	v := sr.x.Vendor
	if isReplayedStep(sr.x, st, s.seen) {
		return
	}
	if st.TS > 0 {
		if sr.main && sr.last > 0 && st.TS-sr.last < ActiveGap.Milliseconds() {
			s.B.ActiveMin[v] += float64(st.TS-sr.last) / 60000
		}
		sr.last = st.TS
	}
	sr.addTime(st)
	s.addTokens(sr, st)
	switch st.Kind {
	case "user":
		s.B.Turns[v]++
	case "interrupt":
		sr.a.interrupts++
	case "compaction":
		sr.a.compactions++
		sr.seenReads = nil
	case "tool":
		s.addTool(sr, st)
	}
}

func isReplayedStep(x Sess, st Stp, seen map[string]bool) bool {
	if st.Kind != "tool" && st.Kind != "assistant" && st.Kind != "generation" {
		return false
	}
	k := x.Vendor + "\x00" + x.Label + "\x00" + st.Ext
	if seen[k] {
		return true
	}
	seen[k] = true
	return false
}

func (s *Summary) addTokens(sr *stepRun, st Stp) {
	v := sr.x.Vendor
	sr.tk.In += st.In
	sr.tk.Out += st.Out
	sr.tk.CacheRead += st.CR
	sr.tk.CacheWrite += st.CW
	ctx := st.Ctx
	if v == Historical && st.Kind == "assistant" {
		ctx = st.In + st.CR + st.CW
	}
	s.B.PeakContext[v] = max(s.B.PeakContext[v], ctx)
	sr.peak = max(sr.peak, ctx)
	if st.TS > 0 && st.In+st.Out+st.CR+st.CW > 0 {
		s.day(st.TS).Tokens[v] += st.In + st.Out + st.CR + st.CW
	}
}

func (s *Summary) addTool(sr *stepRun, st Stp) {
	if st.Tool == "" {
		return
	}
	v := sr.x.Vendor
	sr.a.tools++
	s.B.ToolCalls[v]++
	s.B.ToolCallsByTool[v][st.Tool]++
	if !sr.main {
		s.D.SubagentToolCalls[v][agentType(sr.x)]++
	}
	if st.TS > 0 {
		s.day(st.TS).ToolCalls[v]++
	}
	s.recordToolOutcome(sr, v, st)
	s.recordToolUsage(sr, v, st)
	s.addBashRetry(sr, v, st)
	s.addFileRead(sr, v, st)
	s.verbs(v, st)
}

func (s *Summary) recordToolOutcome(sr *stepRun, v string, st Stp) {
	if st.IsErr && !st.Denied {
		sr.a.errs++
		if st.TS > 0 {
			s.day(st.TS).Errors[v]++
		}
	}
	if !st.Denied {
		return
	}
	sr.a.denials++
	if st.TS > 0 {
		day := s.day(st.TS)
		if day.Denials == nil {
			day.Denials = map[string]int{}
		}
		day.Denials[v]++
	}
}

func (s *Summary) recordToolUsage(sr *stepRun, v string, st Stp) {
	if st.Skill != "" {
		s.C.Skills[v][st.Skill]++
	}
	if st.Tool == "Grep" {
		sr.a.grep++
		if st.TS > 0 {
			s.day(st.TS).GrepCalls[v]++
		}
	}
	if sr.main && st.KindOf != "" && sr.reads < 3 && (st.Tool == "Read" || st.Tool == "Bash") {
		s.C.FirstReads[st.KindOf]++
		sr.reads++
	}
}

func (s *Summary) addBashRetry(sr *stepRun, v string, st Stp) {
	if st.Tool == "Bash" {
		if st.Hash != "" && st.Hash == sr.lastHash {
			s.D.RetryLoops[v]++
		}
		sr.lastHash = st.Hash
		return
	}
	sr.lastHash = ""
}

func (s *Summary) addFileRead(sr *stepRun, v string, st Stp) {
	if st.IsErr || st.Denied {
		return
	}
	ranged, base, start, end := ParseFileHashRange(st.Hash)
	switch st.Tool {
	case "Read":
		s.recordReadStep(sr, v, ranged, base, start, end)
	case "Write", "Edit":
		if base != "" && sr.seenReads != nil {
			delete(sr.seenReads, base)
		}
	}
}

func (s *Summary) recordReadStep(sr *stepRun, v string, ranged bool, base string, start, end int) {
	if s.readCalls == nil {
		s.readCalls = map[string]int{}
		s.rangedReads = map[string]int{}
		s.redundantReads = map[string]int{}
	}
	s.readCalls[v]++
	if ranged {
		s.rangedReads[v]++
	}
	if base == "" {
		return
	}
	if sr.seenReads == nil {
		sr.seenReads = map[string][][2]int{}
	}
	if RecordReadInterval(sr.seenReads, base, start, end) {
		s.redundantReads[v]++
	}
}

func (s *Summary) verbs(v string, st Stp) {
	a := s.acc(v)
	commit, test := false, false
	firstVerb := st.FirstNonCDVerbIdx()
	for i, verb := range st.Verbs {
		s.recordSearchVerb(a, v, st.TS, verb, i == firstVerb)
		if s.recordGitVerb(strings.Fields(verb)) {
			commit = true
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

func (s *Summary) recordSearchVerb(a *acc, v string, ts int64, verb string, isFirst bool) {
	switch {
	case IsSQLiteVerb(verb):
		a.sqlite++
		if ts > 0 {
			s.day(ts).SQLiteVerbs[v]++
		}
	case isFirst && IsGrepVerb(verb):
		a.grep++
		if ts > 0 {
			s.day(ts).GrepCalls[v]++
		}
	}
}

func (s *Summary) recordGitVerb(w []string) bool {
	if len(w) < 2 || w[0] != "git" {
		return false
	}
	flags := " " + strings.Join(w[2:], " ") + " "
	switch w[1] {
	case "add":
		s.recordForbiddenGitAdd(flags)
	case "push":
		if strings.Contains(flags, " -f ") || strings.Contains(flags, " --force ") {
			s.C.Forbidden["git push --force"]++
		}
	case "commit":
		return true
	}
	return false
}

func (s *Summary) recordForbiddenGitAdd(flags string) {
	if strings.Contains(flags, " -A ") || strings.Contains(flags, " --all ") {
		s.C.Forbidden["git add -A"]++
	}
	if strings.Contains(flags, " . ") {
		s.C.Forbidden["git add ."]++
	}
}

// AddSessionEvents incorporates joined trail/feedback events for a main session.
func (s *Summary) AddSessionEvents(x Sess, events []SessEvent) {
	if x.Parent != "" {
		return
	}
	commits, ambiguous, handoff := 0, 0, false
	for _, ev := range events {
		switch ev.Kind {
		case "commit":
			if ev.Value == "ambiguous" {
				ambiguous++
			} else {
				commits++
			}
		case "handoff":
			handoff = true
		case "verifier":
			s.A.Verifier[ev.Value]++
		case "review":
			s.addReviewEvent(ev.Value)
		case "bug":
			s.A.EscapedBugs++
		case "correction":
			s.A.Corrections++
		}
	}
	s.recordDoDOutcome(x.Vendor, commits, ambiguous, handoff)
}

func (s *Summary) addReviewEvent(val string) {
	for _, kv := range strings.Split(val, ",") {
		k, n, _ := strings.Cut(kv, "=")
		var c int
		fmt.Sscan(n, &c)
		s.A.ReviewFindings[k] += c
	}
}

func (s *Summary) recordDoDOutcome(vendor string, commits, ambiguous int, handoff bool) {
	c := s.A.DoDClosed[vendor]
	c.Of++
	switch {
	case commits > 0 && handoff:
		c.Closed++
	case ambiguous > 0 && handoff:
		c.Ambiguous++
	}
	s.A.DoDClosed[vendor] = c
}
