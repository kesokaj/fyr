package metrics

import (
	"sort"
	"strings"
	"time"
)

// totals per vendor that become rates in finish.
type acc struct{ tools, errs, sqlite, grep, denials, interrupts, compactions int }

func (s *Summary) acc(v string) *acc {
	if s.accs == nil {
		s.accs = map[string]*acc{}
	}
	if s.accs[v] == nil {
		s.accs[v] = &acc{}
	}
	return s.accs[v]
}

func (s *Summary) day(ts int64) *Day {
	d := time.UnixMilli(ts).UTC().Format("2006-01-02")
	for i := range s.Daily {
		if s.Daily[i].Date == d {
			return &s.Daily[i]
		}
	}
	s.Daily = append(s.Daily, Day{Date: d, Sessions: map[string]int{}, ToolCalls: map[string]int{}, Tokens: map[string]int64{},
		CostUSD: map[string]float64{}, SQLiteVerbs: map[string]int{}, GrepCalls: map[string]int{}, Errors: map[string]int{}})
	return &s.Daily[len(s.Daily)-1]
}

// isSQLiteVerb counts the binary's search verbs under its name and, for the
// sessions from before the rename, under sh-agent (fyr D7, D20).
func isSQLiteVerb(v string) bool {
	for _, p := range []string{"fyr search", "fyr index", "sh-agent search", "sh-agent index"} {
		if strings.HasPrefix(v, p) {
			return true
		}
	}
	return false
}

func isGrepVerb(v string) bool {
	p, _, _ := strings.Cut(v, " ")
	return p == "grep" || p == "rg"
}

// columns are s.Columns, or the agy column alone for a summary not yet
// finished.
func (s *Summary) columns() []string {
	if len(s.Columns) == 0 {
		return []string{"agy"}
	}
	return s.Columns
}

// label is a column's name in a table.
func label(v string) string {
	if v == historical {
		return historical + " (historical)"
	}
	return v
}

// sessionDay is the UTC day a session started, or ended when no start is
// recorded; "" when neither is.
func sessionDay(x sess) string {
	ts := x.start
	if ts == 0 {
		ts = x.end
	}
	if ts == 0 {
		return ""
	}
	return time.UnixMilli(ts).UTC().Format("2006-01-02")
}

func isUnrangedBashReadVerb(v string) bool {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "cat", "nl":
		return true
	case "bat", "batcat":
		return !isBatRangedFlags(fields[1:])
	default:
		return false
	}
}

func isRangedBashReadVerb(v string) bool {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "head", "tail":
		return true
	case "sed":
		return isSedReadFlags(fields[1:])
	case "bat", "batcat":
		return isBatRangedFlags(fields[1:])
	default:
		return false
	}
}

func isBatRangedFlags(flags []string) bool {
	for _, f := range flags {
		if f == "-r" || f == "--line-range" || strings.HasPrefix(f, "--line-range=") || (strings.HasPrefix(f, "-r") && len(f) > 2) {
			return true
		}
	}
	return false
}

func isSedReadFlags(flags []string) bool {
	hasN, hasI := false, false
	for _, f := range flags {
		if strings.HasPrefix(f, "-") && !strings.HasPrefix(f, "--") {
			hasN = hasN || strings.ContainsRune(f[1:], 'n')
			hasI = hasI || strings.ContainsRune(f[1:], 'i')
		} else if f == "--quiet" || f == "--silent" {
			hasN = true
		} else if f == "--in-place" {
			hasI = true
		}
	}
	return hasN && !hasI
}

func isCDBashVerb(v string) bool {
	return v == "cd" || strings.HasPrefix(v, "cd ")
}

func firstNonCDBashVerb(verbs []string) string {
	for _, v := range verbs {
		if isCDBashVerb(v) {
			continue
		}
		return v
	}
	return ""
}

// bashReadTracker identifies true unguarded Bash file-read bypasses within one
// session (F-022): unranged full-file reads (cat/nl/unranged bat) always count,
// while allowed first-time ranged/ledger-tracked reads (head/tail/sed -n/bat -r)
// are excluded unless repeated with the same cmd_hash without an edit/compaction.
type bashReadTracker struct {
	seenRanged map[string]bool
}

func (b *bashReadTracker) isUnguardedSkip(st stp) bool {
	if st.kind == "compaction" || (!st.isErr && !st.denied && (st.tool == "Write" || st.tool == "Edit")) {
		b.seenRanged = nil
		return false
	}
	if st.kind != "tool" || st.tool != "Bash" || st.denied {
		return false
	}
	v := firstNonCDBashVerb(st.verbs)
	if isUnrangedBashReadVerb(v) {
		return true
	}
	if !isRangedBashReadVerb(v) {
		return false
	}
	if st.hash == "" {
		return false
	}
	if b.seenRanged == nil {
		b.seenRanged = map[string]bool{}
	}
	if b.seenRanged[st.hash] {
		return true
	}
	b.seenRanged[st.hash] = true
	return false
}

type adherenceVerbAcc struct {
	seen       map[string]bool
	verbCounts map[string]int
	skips      map[string]int
}

func (a *adherenceVerbAcc) addSessionSteps(x sess, steps []stp) {
	var tracker bashReadTracker
	for _, st := range steps {
		a.addStep(x, st, &tracker)
	}
}

func (a *adherenceVerbAcc) addStep(x sess, st stp, tracker *bashReadTracker) {
	if st.kind == "compaction" {
		tracker.isUnguardedSkip(st)
		return
	}
	if st.kind != "tool" && st.kind != "assistant" && st.kind != "generation" {
		return
	}
	key := x.vendor + "\x00" + x.label + "\x00" + st.ext
	if a.seen[key] {
		return
	}
	a.seen[key] = true
	if tracker.isUnguardedSkip(st) {
		a.skips[x.vendor]++
	}
	if x.vendor != historical && st.kind == "tool" && st.tool == "Bash" {
		a.countBashVerbs(st.verbs)
	}
}

func (a *adherenceVerbAcc) countBashVerbs(verbs []string) {
	for _, v := range verbs {
		if pv := printable(strings.TrimSpace(v)); pv != "" {
			a.verbCounts[pv]++
		}
	}
}

func (a *adherenceVerbAcc) bashReadSkips(agySessions int) map[string]int {
	if agySessions <= 0 && len(a.skips) == 0 {
		return nil
	}
	out := map[string]int{}
	if agySessions > 0 || a.skips["agy"] > 0 {
		out["agy"] = a.skips["agy"]
	}
	for v, c := range a.skips {
		if v != "agy" && c > 0 {
			out[v] = c
		}
	}
	return out
}

func populateAdherenceVerbs(sum *Summary, all []sess, stepsByID map[string][]stp) {
	acc := adherenceVerbAcc{
		seen:       map[string]bool{},
		verbCounts: map[string]int{},
		skips:      map[string]int{},
	}
	for _, x := range all {
		acc.addSessionSteps(x, stepsByID[x.id])
	}
	if skips := acc.bashReadSkips(sum.Sessions["agy"]); skips != nil {
		sum.C.BashReadSkips = skips
	}
	sum.C.TopBashVerbs = topVerbCounts(acc.verbCounts, 10)
}

func topVerbCounts(counts map[string]int, limit int) []VerbCount {
	if len(counts) == 0 || limit <= 0 {
		return nil
	}
	out := make([]VerbCount, 0, len(counts))
	for v, c := range counts {
		out = append(out, VerbCount{Verb: v, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Verb < out[j].Verb
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
