package metrics

import (
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
