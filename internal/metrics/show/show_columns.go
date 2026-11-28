package show

import (
	"sort"
	"strings"
	"time"

	r "github.com/kesokaj/fyr/internal/metrics/report"
	"github.com/kesokaj/fyr/internal/search"
)

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

func (s *Summary) day(ts int64) *r.Day {
	d := time.UnixMilli(ts).UTC().Format("2006-01-02")
	for i := range s.Daily {
		if s.Daily[i].Date == d {
			return &s.Daily[i]
		}
	}
	dt := map[string]int64{}
	s.Daily = append(s.Daily, r.Day{Date: d, Sessions: map[string]int{}, ToolCalls: map[string]int{}, Tokens: dt,
		CostUSD: map[string]float64{}, SQLiteVerbs: map[string]int{}, GrepCalls: map[string]int{}, Errors: map[string]int{}})
	return &s.Daily[len(s.Daily)-1]
}

// IsSQLiteVerb reports whether v is a fyr/sh-agent search or index verb.
func IsSQLiteVerb(v string) bool {
	for _, p := range []string{"fyr search", "fyr index", "sh-agent search", "sh-agent index"} {
		if strings.HasPrefix(v, p) {
			return true
		}
	}
	return false
}

// IsGrepVerb reports whether v opens with grep or rg.
func IsGrepVerb(v string) bool {
	p, _, _ := strings.Cut(v, " ")
	return p == "grep" || p == "rg"
}

func (s *Summary) columns() []string {
	if len(s.Columns) == 0 {
		return []string{"agy"}
	}
	return s.Columns
}

func colLabel(v string) string {
	if v == Historical {
		return Historical + " (historical)"
	}
	return v
}

func sessionDay(x Sess) string {
	ts := x.Start
	if ts == 0 {
		ts = x.End
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

// IsCDBashVerb reports whether v is a `cd` command verb.
func IsCDBashVerb(v string) bool {
	return v == "cd" || strings.HasPrefix(v, "cd ")
}

func firstNonCDBashVerb(verbs []string) string {
	for _, v := range verbs {
		if IsCDBashVerb(v) {
			continue
		}
		return v
	}
	return ""
}

// BashReadTracker identifies unguarded Bash file-read bypasses within one session.
type BashReadTracker struct {
	seenRanged map[string]bool
}

// IsUnguardedSkip reports whether st is an unguarded Bash read bypass.
func (b *BashReadTracker) IsUnguardedSkip(st Stp) bool {
	if st.Kind == "compaction" || (!st.IsErr && !st.Denied && (st.Tool == "Write" || st.Tool == "Edit")) {
		b.seenRanged = nil
		return false
	}
	if st.Kind != "tool" || st.Tool != "Bash" || st.Denied {
		return false
	}
	v := firstNonCDBashVerb(st.Verbs)
	if isUnrangedBashReadVerb(v) {
		return true
	}
	if !isRangedBashReadVerb(v) || st.Hash == "" {
		return false
	}
	if b.seenRanged == nil {
		b.seenRanged = map[string]bool{}
	}
	if b.seenRanged[st.Hash] {
		return true
	}
	b.seenRanged[st.Hash] = true
	return false
}

type adherenceVerbAcc struct {
	seen       map[string]bool
	verbCounts map[string]int
	skips      map[string]int
}

func (a *adherenceVerbAcc) addSessionSteps(x Sess, steps []Stp) {
	var tracker BashReadTracker
	for _, st := range steps {
		a.addStep(x, st, &tracker)
	}
}

func (a *adherenceVerbAcc) addStep(x Sess, st Stp, tracker *BashReadTracker) {
	if st.Kind == "compaction" {
		tracker.IsUnguardedSkip(st)
		return
	}
	if st.Kind != "tool" && st.Kind != "assistant" && st.Kind != "generation" {
		return
	}
	if isReplayedStep(x, st, a.seen) {
		return
	}
	if tracker.IsUnguardedSkip(st) {
		a.skips[x.Vendor]++
	}
	if x.Vendor != Historical && st.Kind == "tool" && st.Tool == "Bash" {
		a.countBashVerbs(st.Verbs)
	}
}

func (a *adherenceVerbAcc) countBashVerbs(verbs []string) {
	for _, v := range verbs {
		if pv := search.Printable(strings.TrimSpace(v)); pv != "" {
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

// PopulateAdherenceVerbs computes Bash read bypasses and top Bash verbs for sum.
func PopulateAdherenceVerbs(sum *Summary, all []Sess, stepsByID map[string][]Stp) {
	acc := adherenceVerbAcc{
		seen:       map[string]bool{},
		verbCounts: map[string]int{},
		skips:      map[string]int{},
	}
	for _, x := range all {
		acc.addSessionSteps(x, stepsByID[x.ID])
	}
	if skips := acc.bashReadSkips(sum.Sessions["agy"]); skips != nil {
		sum.C.BashReadSkips = skips
	}
	sum.C.TopBashVerbs = topVerbCounts(acc.verbCounts, 10)
}

func topVerbCounts(counts map[string]int, limit int) []r.VerbCount {
	if len(counts) == 0 || limit <= 0 {
		return nil
	}
	out := make([]r.VerbCount, 0, len(counts))
	for v, c := range counts {
		out = append(out, r.VerbCount{Verb: v, Count: c})
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
