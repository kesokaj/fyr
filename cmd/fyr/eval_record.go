package main

import (
	"fmt"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
)

// recordEval stores every trial and ingests its sandbox, then writes the
// testing record and the summary line; any trial not PASS, failed trial
// write or ingest, or unrun trial is exit 1.
func recordEval(c *ctx, root string, o eval.Options, src harness.Source, key string, rows []eval.Row, results []eval.Result) error {
	failed := false
	cli := agyVersion()
	byID := map[string]eval.Row{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	// After the verdict, never before: the store stamps a label's outcome
	// first-write-wins (D6), and an interrupted invocation still ingests the
	// trials it started (D22). The trial first, so a trial whose sessions
	// fail to ingest, or that has none, is still counted (the eval-metrics
	// plan D13).
	for _, r := range results {
		if r.Status != eval.StatusPass {
			failed = true
		}
		if err := recordTrial(c.home, trialOf(r, byID[r.Row], key, cli)); err != nil {
			fmt.Fprintln(c.out, search.Printable(fmt.Sprintf("eval: %s#%d trial: %v", r.Row, r.Trial, err)))
			failed = true
		}
		if err := ingestRow(c.home, r); err != nil {
			fmt.Fprintln(c.out, search.Printable(fmt.Sprintf("eval: %s#%d ingest: %v", r.Row, r.Trial, err)))
			failed = true
		}
	}
	// Asked again here: a row's agent ran for minutes with command(*) and
	// could have linked --home's state to the real one (D37's security review).
	if why := evalHomeRefused(c.homeFlag, c.home); why != "" {
		fmt.Fprintln(c.out, search.Printable("eval: record: not written: "+why))
		return exitCode(1)
	}
	tl := tallyOf(results)
	runs := max(o.Runs, 1)
	rec := evalRecord{root: root, o: o, src: src, cli: cli, key: key, model: modelName(trialModels(c.home, results)), runs: runs, results: results, tally: tl}
	id, err := brain.Add(brain.Options{Home: c.home, Dir: root}, brain.AddInput{
		Kind: "testing", Status: "closed",
		Title: fmt.Sprintf("Eval — %d of %d PASS at %s%s", tl.pass, tl.graded, gitShort(root), titleSuffix(runs, tl.errors)),
		Body:  strings.NewReader(rec.body()),
	})
	if err != nil {
		fmt.Fprintln(c.out, search.Printable("eval: record: "+err.Error()))
		return exitCode(1)
	}
	fmt.Fprintln(c.out, search.Printable(fmt.Sprintf("eval: %d of %d PASS, %s; record %s", tl.pass, tl.graded, errorsText(tl.errors), id)))
	if failed || len(results) < len(rows)*runs {
		return exitCode(1)
	}
	return nil
}

// tally is a run's counts: graded trials (PASS and FAIL) and their passes,
// the ERROR trials apart, and the same per row, in the order first run.
type tally struct {
	pass, graded, errors int
	rows                 []rowTally
}

// rowTally is one row's n graded trials, c passed, and its errors.
type rowTally struct {
	row          string
	n, c, errors int
}

func tallyOf(results []eval.Result) tally {
	var tl tally
	at := map[string]int{}
	for _, r := range results {
		i, ok := at[r.Row]
		if !ok {
			i = len(tl.rows)
			at[r.Row] = i
			tl.rows = append(tl.rows, rowTally{row: r.Row})
		}
		rt := &tl.rows[i]
		switch r.Status {
		case eval.StatusError:
			tl.errors++
			rt.errors++
		case eval.StatusPass:
			tl.pass++
			rt.c++
			fallthrough
		default:
			tl.graded++
			rt.n++
		}
	}
	return tl
}

// interval is the row's Wilson 95 % interval over its graded trials, or
// n/a when it has none.
func (rt rowTally) interval() string {
	if rt.n == 0 {
		return "n/a"
	}
	return bracket(metrics.Wilson(rt.n, rt.c, 1.96))
}

// suiteInterval is the Wilson 95 % interval over the rows with a graded
// trial, each scored c/n (the eval-metrics plan § Interfaces), or n/a.
func (tl tally) suiteInterval() string {
	n, sum := 0, 0.0
	for _, rt := range tl.rows {
		if rt.n > 0 {
			n++
			sum += float64(rt.c) / float64(rt.n)
		}
	}
	if n == 0 {
		return "n/a"
	}
	return bracket(metrics.WilsonRate(n, sum/float64(n), 1.96))
}

func bracket(lo, hi float64) string { return fmt.Sprintf("[%.2f, %.2f]", lo, hi) }

// titleSuffix is the title's " (k=<N>, <e> errors)", shown past one run per
// row or when a trial was an ERROR.
func titleSuffix(runs, errors int) string {
	if runs <= 1 && errors == 0 {
		return ""
	}
	return fmt.Sprintf(" (k=%d, %s)", runs, errorsText(errors))
}

func errorsText(n int) string {
	if n == 1 {
		return "1 error"
	}
	return fmt.Sprintf("%d errors", n)
}

// trialModels is the models the run's sessions name in home's store; a
// store that cannot be read names none, and the record says unknown.
func trialModels(home string, results []eval.Result) []string {
	runs := make([]string, 0, len(results))
	for _, r := range results {
		runs = append(runs, r.Run)
	}
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		return nil
	}
	defer s.Close()
	models, _ := s.RunModels(runs)
	return models
}

// modelName is the header's model: the one model the sessions name,
// "mixed (<each>)" for several, "unknown" for none.
func modelName(models []string) string {
	switch len(models) {
	case 0:
		return "unknown"
	case 1:
		return models[0]
	}
	return "mixed (" + strings.Join(models, ", ") + ")"
}
