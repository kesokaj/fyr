package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"text/tabwriter"
)

// evalHead is the view's columns, in the order of the eval-metrics plan
// § Interfaces; the report and `fyr serve` show the same table.
var evalHead = []string{"row", "v", "n", "c", "errors", "pass@1", "pass^3", "wilson95", "flip", "turns", "tool calls",
	"tokens in/out/cache", "session s", "active s", "tool err %", "retries", "peak ctx", "last", "flags"}

// WriteJSON prints the view as indented JSON: snake_case keys, the numbers
// of the text, and no key for a rate the row cannot have.
func (e *EvalRows) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(e)
}

// WriteText prints the window line, one aligned line per row, and the
// suite lines.
func (e *EvalRows) WriteText(w io.Writer) {
	line := fmt.Sprintf("evals %s to %s", e.Window["since"], e.Window["until"])
	if e.Harness != "" {
		line += ", harness " + e.Harness
	}
	fmt.Fprintln(w, line)
	if len(e.Rows) == 0 {
		fmt.Fprintln(w, "no eval trial in the window: fyr eval writes them")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(evalHead, "\t"))
	for _, cells := range e.Table() {
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	tw.Flush()
	for _, l := range e.Suite.lines() {
		fmt.Fprintln(w, l)
	}
}

// Table is the view's rows as cells under evalHead.
func (e *EvalRows) Table() [][]string {
	out := make([][]string, 0, len(e.Rows))
	for _, r := range e.Rows {
		cells := []string{r.Row, fmt.Sprint(r.Version), fmt.Sprint(r.N), fmt.Sprint(r.C), fmt.Sprint(r.Errors),
			optRate(r.PassAt1), optRate(r.PassPow3), optInterval(r.Wilson95), fmt.Sprint(r.Flip)}
		cells = append(cells, r.Means.cells()...)
		flags := "-"
		if len(r.Flags) > 0 {
			flags = strings.Join(r.Flags, ",")
		}
		out = append(out, append(cells, r.Last, flags))
	}
	return out
}

// cells are the efficiency columns, "-" each when no session ran.
func (m *EvalMeans) cells() []string {
	if m == nil {
		return []string{"-", "-", "-", "-", "-", "-", "-", "-"}
	}
	secs := "-"
	if m.SessionSeconds != nil {
		secs = fmt.Sprintf("%.1f", *m.SessionSeconds)
	}
	errRate := "-"
	if m.ToolErrorRate != nil {
		errRate = fmt.Sprintf("%.1f", *m.ToolErrorRate*100)
	}
	// A mean of tokens is shown in whole tokens: short prints a fraction
	// below 1000.
	tok := func(v float64) string { return short(math.Round(v)) }
	return []string{fmt.Sprintf("%.1f", m.Turns), fmt.Sprintf("%.1f", m.ToolCalls),
		tok(m.TokensIn) + "/" + tok(m.TokensOut) + "/" + tok(m.TokensCache), secs,
		fmt.Sprintf("%.1f", m.ActiveSeconds), errRate, fmt.Sprintf("%.1f", m.Retries), tok(m.PeakContext)}
}

// lines are the suite's summary under the table.
func (su EvalSuite) lines() []string {
	rate := "pass rate n/a: no graded trial"
	if su.PassRate != nil {
		rate = fmt.Sprintf("pass rate %.2f %s over rows; C_out %.2f", *su.PassRate, optInterval(su.Wilson95), *su.COut)
	}
	sim := "trajectory similarity n/a: no row has two sessions"
	if su.Similarity != nil {
		sim = fmt.Sprintf("trajectory similarity %.2f over %d rows with two or more sessions", *su.Similarity, su.SimilarityOfRows)
	}
	return []string{
		fmt.Sprintf("suite: %s graded, %s, %s; %s", plural(su.Rows, "row"), plural(su.Trials, "graded trial"), plural(su.Errors, "error"), rate),
		"suite: " + sim,
	}
}

func optRate(p *float64) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", *p)
}

func optInterval(p *[2]float64) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("[%.2f, %.2f]", p[0], p[1])
}

// plural is n and word, with an s unless n is 1.
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
