package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// WriteJSON prints the summary as one indented object.
func (s *Summary) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// safeMap is a count map for the terminal: a key holding a character the
// BUG-152 filter replaces (a row an older build stored raw) prints as "?".
func safeMap(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		if printable(k) != k {
			k = "?"
		}
		out[k] += v
	}
	return out
}

// WriteText prints the summary as one short table per catalogue group, a
// column per Columns entry; "n/a" is a metric the CLI cannot produce.
func (s *Summary) WriteText(w io.Writer) {
	fmt.Fprintf(w, "metrics %s .. %s\n", s.Window["since"], s.Window["until"])
	cols := s.columns()
	width := 14
	for _, v := range cols {
		width = max(width, len(label(v)))
	}
	row := func(name string, f func(v string) string) {
		fmt.Fprintf(w, "  %-28s", name)
		for _, v := range cols {
			fmt.Fprintf(w, " %*s", width, f(v))
		}
		fmt.Fprintln(w)
	}
	head := func(title string) {
		fmt.Fprintf(w, "\n%s\n  %-28s", title, "")
		for _, v := range cols {
			fmt.Fprintf(w, " %*s", width, label(v))
		}
		fmt.Fprintln(w)
	}
	n := func(m map[string]int) func(string) string {
		return func(v string) string { return fmt.Sprint(m[v]) }
	}
	pf := func(m map[string]*float64, format string) func(string) string {
		return func(v string) string {
			if m[v] == nil {
				return "n/a"
			}
			return fmt.Sprintf(format, *m[v])
		}
	}
	pi := func(m map[string]*int) func(string) string {
		return func(v string) string {
			if m[v] == nil {
				return "n/a"
			}
			return fmt.Sprint(*m[v])
		}
	}
	head("sessions")
	row("main sessions", n(s.Sessions))
	row("subagents", func(v string) string {
		t := 0
		for _, c := range s.Subagents[v] {
			t += c
		}
		return fmt.Sprint(t)
	})
	head("A. outcome")
	row("DoD closed (ambiguous) / of", func(v string) string {
		c := s.A.DoDClosed[v]
		return fmt.Sprintf("%d (%d) / %d", c.Closed, c.Ambiguous, c.Of)
	})
	fmt.Fprintf(w, "  verifier %v; review findings %v; escaped bugs %d; corrections %d\n",
		safeMap(s.A.Verifier), safeMap(s.A.ReviewFindings), s.A.EscapedBugs, s.A.Corrections)
	for _, l := range sortedKeys(s.A.PassK) {
		p := s.A.PassK[l]
		// A label is any KEY=VALUE, and fyr eval's comes from the suite
		// file: printed through the filter, as every other key (SD55).
		fmt.Fprintf(w, "  pass^k %s: %d of %d passed, pass^%d = %d\n", printable(l), p.Passed, p.K, p.K, p.PassK)
	}
	head("B. efficiency")
	row("tool calls", n(s.B.ToolCalls))
	row("user turns", n(s.B.Turns))
	row("tokens in+out", func(v string) string {
		if t := s.B.Tokens[v]; t != nil {
			return fmt.Sprint(t.In + t.Out)
		}
		return "0"
	})
	row("cache read", func(v string) string {
		if t := s.B.Tokens[v]; t != nil {
			return fmt.Sprint(t.CacheRead)
		}
		return "0"
	})
	row("cost USD", pf(s.B.CostUSD, "%.2f"))
	row("wall / active min", func(v string) string { return fmt.Sprintf("%.0f / %.0f", s.B.WallMin[v], s.B.ActiveMin[v]) })
	row("peak context", func(v string) string { return fmt.Sprint(s.B.PeakContext[v]) })
	row(fmt.Sprintf("over context target %d", s.B.ContextTarget), s.overTarget)
	row("compactions", pi(s.B.Compactions))
	head("C. process adherence")
	row("fyr search|index", n(s.C.SQLiteVerbs))
	row("grep/rg/Grep", n(s.C.GrepCalls))
	row("search : grep", pf(s.C.SearchGrepRatio, "%.3f"))
	fmt.Fprintf(w, "  forbidden shapes %v\n  first reads %v\n", safeMap(s.C.Forbidden), safeMap(s.C.FirstReads))
	for _, v := range cols {
		if len(s.C.Skills[v]) > 0 {
			fmt.Fprintf(w, "  skills (%s) %v\n", label(v), safeMap(s.C.Skills[v]))
		}
	}
	head("D. reliability")
	row("tool error rate", pf(s.D.ToolErrorRate, "%.3f"))
	row("denials", pi(s.D.Denials))
	row("interrupts", pi(s.D.Interrupts))
	row("retry loops", n(s.D.RetryLoops))
	for _, v := range cols {
		if len(s.D.SubagentToolCalls[v]) > 0 {
			fmt.Fprintf(w, "  subagent tool calls (%s) %v\n", label(v), safeMap(s.D.SubagentToolCalls[v]))
		}
	}
	for _, note := range s.Notes {
		fmt.Fprintln(w, "note: "+note)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
