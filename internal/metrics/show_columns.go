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

func isBashReadVerb(v string) bool {
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "cat", "head", "tail", "nl", "bat", "batcat":
		return true
	case "sed":
		return isSedReadFlags(fields[1:])
	default:
		return false
	}
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

func isBashReadStep(st stp) bool {
	if st.kind != "tool" || st.tool != "Bash" || st.denied {
		return false
	}
	for _, v := range st.verbs {
		if v == "cd" || strings.HasPrefix(v, "cd ") {
			continue
		}
		return isBashReadVerb(v)
	}
	return false
}

func populateAdherenceVerbs(sum *Summary, all []sess, stepsByID map[string][]stp) {
	seen := map[string]bool{}
	verbCounts := map[string]int{}
	bypasses := map[string]int{}
	for _, x := range all {
		for _, st := range stepsByID[x.id] {
			if st.kind != "tool" && st.kind != "assistant" && st.kind != "generation" {
				continue
			}
			key := x.vendor + "\x00" + x.label + "\x00" + st.ext
			if seen[key] {
				continue
			}
			seen[key] = true
			if isBashReadStep(st) {
				bypasses[x.vendor]++
			}
			if x.vendor != historical && st.kind == "tool" && st.tool == "Bash" {
				for _, v := range st.verbs {
					if pv := printable(strings.TrimSpace(v)); pv != "" {
						verbCounts[pv]++
					}
				}
			}
		}
	}
	if sum.Sessions["agy"] > 0 || len(bypasses) > 0 {
		outBypasses := map[string]int{}
		if sum.Sessions["agy"] > 0 || bypasses["agy"] > 0 {
			outBypasses["agy"] = bypasses["agy"]
		}
		for v, c := range bypasses {
			if v != "agy" && c > 0 {
				outBypasses[v] = c
			}
		}
		sum.C.BashReadSkips = outBypasses
	}
	sum.C.TopBashVerbs = topVerbCounts(verbCounts, 10)
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
