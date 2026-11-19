package main

import (
	"flag"
	"fmt"

	"github.com/kesokaj/fyr/internal/eval"
	fyrisolate "github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/search"
)

func init() {
	verbs["eval"] = verb{flags: func(c *ctx) {
		c.fs.StringVar(&c.evalFixture, "fixture", "", "the git repository `DIR` each row clones (default: this checkout)")
		c.fs.BoolVar(&c.evalList, "list", false, "print the suite's rows (id, agent or -, named or wide, version, kind) and run nothing")
		c.fs.IntVar(&c.evalParallel, "parallel", 1, "run up to `N` trials at once, 1 to 25 (default 1; benchmark trials stay at 1)")
		c.fs.IntVar(&c.evalRuns, "runs", 1, "run each named row `N` times, 1 to 25, each trial in its own sandbox")
		c.fs.String("suite-file", "", "read the suite from `PATH` outside every checkout; labels rows eval=holdout/<id>")
		c.fs.BoolVar(&c.evalACP, "acp", false, "drive trials via agy acp --stdio --process-per-session")
		c.fs.StringVar(&c.evalRunner, "runner", fyrisolate.DefaultRunner, "where each trial's session runs: `R` is isolate (default "+fyrisolate.DefaultRunner+")")
	}, positional: true, readsRepo: true, run: runEval}
}

func evalSuiteFile(c *ctx) (string, bool) {
	set := false
	c.fs.Visit(func(f *flag.Flag) { set = set || f.Name == "suite-file" })
	if fn := c.fs.Lookup("suite-file"); fn != nil {
		return fn.Value.String(), set
	}
	return "", set
}

func loadEvalSuite(c *ctx, root string, atHead bool) ([]eval.Row, error) {
	if path, set := evalSuiteFile(c); set {
		return eval.ReadSuiteFile(path)
	}
	return readSuite(c.home, root, atHead)
}

func labelHoldout(c *ctx, rows []eval.Row, results []eval.Result) {
	if _, set := evalSuiteFile(c); !set {
		return
	}
	for i := range rows {
		rows[i].ID = "holdout/" + rows[i].ID
	}
	for i := range results {
		results[i].Row = "holdout/" + results[i].Row
	}
}

// evalUsage is the usage errors, exit 2, before any other refusal.
func evalUsage(c *ctx, names []string) error {
	var runsSet, parallelSet, runnerSet bool
	c.fs.Visit(func(f *flag.Flag) {
		runsSet = runsSet || f.Name == "runs"
		parallelSet = parallelSet || f.Name == "parallel"
		runnerSet = runnerSet || f.Name == "runner"
	})
	switch {
	case c.evalList && len(names) > 0:
		fmt.Fprintln(c.out, "fyr eval: --list takes no row")
		return exitCode(2)
	case c.evalList && runsSet:
		fmt.Fprintln(c.out, "fyr eval: --list takes no --runs")
		return exitCode(2)
	case c.evalList && parallelSet:
		fmt.Fprintln(c.out, "fyr eval: --list takes no --parallel")
		return exitCode(2)
	case c.evalList && runnerSet:
		fmt.Fprintln(c.out, "fyr eval: --list takes no runner flags")
		return exitCode(2)
	case !c.evalList && len(names) == 0:
		return usageErr(c.out, "eval", "")
	case c.evalRuns < 1 || c.evalRuns > eval.MaxRuns:
		return usageErr(c.out, "eval", fmt.Sprintf("fyr eval: --runs %d: want 1 to %d", c.evalRuns, eval.MaxRuns))
	case c.evalParallel < 1 || c.evalParallel > eval.MaxParallel:
		return usageErr(c.out, "eval", fmt.Sprintf("fyr eval: --parallel %d: want 1 to %d", c.evalParallel, eval.MaxParallel))
	}
	if err := fyrisolate.CheckEvalRunner(c.evalRunner); err != nil {
		return usageErr(c.out, "eval", "fyr eval: "+err.Error())
	}
	return nil
}

// evalRefuse prints an eval refusal and returns exit 2.
func (c *ctx) evalRefuse(format string, a ...any) error {
	fmt.Fprintln(c.out, search.Printable("eval: "+fmt.Sprintf(format, a...)))
	return exitCode(2)
}
