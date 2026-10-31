package metrics

// evalsSection is the per-row view as the report shows it, under the
// pass^k chart: the table of `fyr metrics evals` and its suite lines.
type evalsSection struct {
	Table table
	Lines []string
}

// evalsSectionOf is e's section, or nil when e holds no row: a report with
// no suite trial in its window has no such section.
func evalsSectionOf(e *EvalRows) *evalsSection {
	if e == nil || len(e.Rows) == 0 {
		return nil
	}
	return &evalsSection{Table: table{Head: evalHead, Rows: e.Table()}, Lines: e.Suite.lines()}
}
