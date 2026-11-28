package show

import (
	"encoding/json"
	"fmt"
	"io"

	r "github.com/kesokaj/fyr/internal/metrics/report"
	"github.com/kesokaj/fyr/internal/search"
)

// WriteJSON prints the summary as one indented object.
func (s *Summary) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

func safeMap(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		if search.Printable(k) != k {
			k = "?"
		}
		out[k] += v
	}
	return out
}

// WriteText prints the summary as one short table per catalogue group.
func (s *Summary) WriteText(w io.Writer) {
	fmt.Fprintf(w, "metrics %s .. %s\n", s.Window["since"], s.Window["until"])
	t := textWriter{w: w, cols: s.columns(), width: 14}
	for _, v := range t.cols {
		t.width = max(t.width, len(colLabel(v)))
	}
	s.textOutcome(t)
	s.textEfficiency(t)
	s.textAdherence(t)
	s.textReliability(t)
	for _, note := range s.Notes {
		fmt.Fprintln(w, "note: "+note)
	}
}

type textWriter struct {
	w     io.Writer
	cols  []string
	width int
}

func (t textWriter) row(name string, f func(v string) string) {
	fmt.Fprintf(t.w, "  %-28s", name)
	for _, v := range t.cols {
		fmt.Fprintf(t.w, " %*s", t.width, f(v))
	}
	fmt.Fprintln(t.w)
}

func (t textWriter) head(title string) {
	fmt.Fprintf(t.w, "\n%s\n  %-28s", title, "")
	for _, v := range t.cols {
		fmt.Fprintf(t.w, " %*s", t.width, colLabel(v))
	}
	fmt.Fprintln(t.w)
}

func countCol(m map[string]int) func(string) string {
	return func(v string) string { return fmt.Sprint(m[v]) }
}

func floatCol(m map[string]*float64, format string) func(string) string {
	return func(v string) string {
		if m[v] == nil {
			return "n/a"
		}
		return fmt.Sprintf(format, *m[v])
	}
}

func intCol(m map[string]*int) func(string) string {
	return func(v string) string {
		if m[v] == nil {
			return "n/a"
		}
		return fmt.Sprint(*m[v])
	}
}

func (s *Summary) textOutcome(t textWriter) {
	t.head("sessions")
	t.row("main sessions", countCol(s.Sessions))
	t.row("subagents", func(v string) string {
		n := 0
		for _, c := range s.Subagents[v] {
			n += c
		}
		return fmt.Sprint(n)
	})
	t.head("A. outcome")
	t.row("DoD closed (ambiguous) / of", func(v string) string {
		c := s.A.DoDClosed[v]
		return fmt.Sprintf("%d (%d) / %d", c.Closed, c.Ambiguous, c.Of)
	})
	fmt.Fprintf(t.w, "  verifier %v; review findings %v; escaped bugs %d; corrections %d\n",
		safeMap(s.A.Verifier), safeMap(s.A.ReviewFindings), s.A.EscapedBugs, s.A.Corrections)
	for _, l := range r.SortedKeys(s.A.PassK) {
		p := s.A.PassK[l]
		fmt.Fprintf(t.w, "  pass^k %s: %d of %d passed, pass^%d = %d; n %d, c %d, errors %d, wilson95 %s, flip %d\n",
			search.Printable(l), p.Passed, p.K, p.K, p.PassK, p.N, p.C, p.Errors, r.WilsonText(p.Wilson95), p.Flip)
	}
}

func (s *Summary) textEfficiency(t textWriter) {
	t.head("B. efficiency")
	t.row("tool calls", countCol(s.B.ToolCalls))
	t.row("user turns", countCol(s.B.Turns))
	t.row("tokens in+out", func(v string) string {
		if tk := s.B.Tokens[v]; tk != nil {
			return fmt.Sprint(tk.In + tk.Out)
		}
		return "0"
	})
	t.row("cache read", func(v string) string {
		if tk := s.B.Tokens[v]; tk != nil {
			return fmt.Sprint(tk.CacheRead)
		}
		return "0"
	})
	t.row("cost USD", floatCol(s.B.CostUSD, "%.2f"))
	t.row("wall / active min", func(v string) string { return fmt.Sprintf("%.0f / %.0f", s.B.WallMin[v], s.B.ActiveMin[v]) })
	t.row("time model/tool/wait/idle m", func(v string) string {
		return fmt.Sprintf("%.1f / %.1f / %.1f / %.1f", s.B.ModelMin[v], s.B.ToolMin[v], s.B.WaitMin[v], s.B.IdleMin[v])
	})
	t.row("peak context", func(v string) string { return fmt.Sprint(s.B.PeakContext[v]) })
	t.row(fmt.Sprintf("over context target %d", s.B.ContextTarget), s.overTarget)
	t.row("compactions", intCol(s.B.Compactions))
}

func (s *Summary) textAdherence(t textWriter) {
	t.head("C. process adherence")
	t.row("fyr search|index", countCol(s.C.SQLiteVerbs))
	t.row("grep/rg/Grep", countCol(s.C.GrepCalls))
	t.row("search : grep", floatCol(s.C.SearchGrepRatio, "%.3f"))
	t.row("redundant reads", countCol(s.C.RedundantReads))
	t.row("ranged read %", floatCol(s.C.RangedReadPct, "%.1f%%"))
	fmt.Fprintf(t.w, "  forbidden shapes %v\n  first reads %v\n", safeMap(s.C.Forbidden), safeMap(s.C.FirstReads))
	for _, v := range t.cols {
		if len(s.C.Skills[v]) > 0 {
			fmt.Fprintf(t.w, "  skills (%s) %v\n", colLabel(v), safeMap(s.C.Skills[v]))
		}
	}
}

func (s *Summary) textReliability(t textWriter) {
	t.head("D. reliability")
	t.row("tool error rate", floatCol(s.D.ToolErrorRate, "%.3f"))
	t.row("denials", intCol(s.D.Denials))
	if s.Health.HookDenials > 0 || s.D.HookDenials["agy"] > 0 {
		t.row("hook denials (excl. errors)", func(v string) string {
			if v == "agy" {
				return fmt.Sprint(max(s.Health.HookDenials, s.D.HookDenials["agy"]))
			}
			return "n/a"
		})
	}
	t.row("interrupts", intCol(s.D.Interrupts))
	t.row("retry loops", countCol(s.D.RetryLoops))
	for _, v := range t.cols {
		if len(s.D.SubagentToolCalls[v]) > 0 {
			fmt.Fprintf(t.w, "  subagent tool calls (%s) %v\n", colLabel(v), safeMap(s.D.SubagentToolCalls[v]))
		}
	}
}
