package main

import (
	"flag"
	"fmt"

	"github.com/kesokaj/fyr/internal/eval"
	fyrisolate "github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/search"
)

// evalOpts holds parsed flags for `fyr eval`.
type evalOpts struct {
	fixture, runner, model string
	list, acp, jsonOut     bool
	runs, parallel         int
}

func evalOptsOf(c *runtimeCtx) *evalOpts {
	if o, ok := c.opts.(*evalOpts); ok {
		return o
	}
	o := &evalOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func evalVerb() verb {
	return verb{flags: registerEvalFlags, writesState: evalWritesState, positional: true, readsRepo: true, run: runEval}
}

func evalWritesState(c *runtimeCtx) bool {
	// Sandboxes, the metrics store and the testing record; --list reads,
	// and a refused --home is refused before anything moves (D37).
	return !evalOptsOf(c).list && evalHomeRefused(c.homeFlag, c.home) == ""
}

func registerEvalFlags(c *runtimeCtx) {
	o := &evalOpts{}
	c.opts = o
	c.fs.StringVar(&o.fixture, "fixture", "", "the git repository `DIR` each row clones (default: this checkout)")
	c.fs.BoolVar(&o.list, "list", false, "print the suite's rows (id, agent or -, named or wide, version, kind) and run nothing")
	c.fs.StringVar(&o.model, "model", "", "pass --model `ID` to agy for each trial (default: agy default)")
	c.fs.IntVar(&o.parallel, "parallel", 1, "run up to `N` trials at once, 1 to 25 (default 1; benchmark trials stay at 1)")
	c.fs.IntVar(&o.runs, "runs", 1, "run each named row `N` times, 1 to 25, each trial in its own sandbox")
	c.fs.String("suite-file", "", "read the suite from `PATH` outside every checkout; labels rows eval=holdout/<id>")
	c.fs.BoolVar(&o.acp, "acp", false, "drive trials via agy acp --stdio --process-per-session")
	c.fs.StringVar(&o.runner, "runner", fyrisolate.DefaultRunner, "where each trial's session runs: `R` is isolate (default "+fyrisolate.DefaultRunner+")")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print the result as JSON")
}

func evalSuiteFile(c *runtimeCtx) (string, bool) {
	set := false
	c.fs.Visit(func(f *flag.Flag) { set = set || f.Name == "suite-file" })
	if fn := c.fs.Lookup("suite-file"); fn != nil {
		return fn.Value.String(), set
	}
	return "", set
}

func loadEvalSuite(c *runtimeCtx, root string, atHead bool) ([]eval.Row, error) {
	if path, set := evalSuiteFile(c); set {
		return eval.ReadSuiteFile(path)
	}
	return readSuite(c.home, root, atHead)
}

func labelHoldout(c *runtimeCtx, rows []eval.Row, results []eval.Result) {
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

func evalListUsage(c *runtimeCtx, names []string, runsSet, parallelSet, runnerSet, modelSet bool) error {
	switch {
	case len(names) > 0:
		fmt.Fprintln(c.out, "fyr eval: --list takes no row")
		return exitCode(2)
	case runsSet:
		fmt.Fprintln(c.out, "fyr eval: --list takes no --runs")
		return exitCode(2)
	case parallelSet:
		fmt.Fprintln(c.out, "fyr eval: --list takes no --parallel")
		return exitCode(2)
	case runnerSet:
		fmt.Fprintln(c.out, "fyr eval: --list takes no runner flags")
		return exitCode(2)
	case modelSet:
		fmt.Fprintln(c.out, "fyr eval: --list takes no --model")
		return exitCode(2)
	}
	return nil
}

func evalModelUsage(c *runtimeCtx, modelSet bool, model string) error {
	if !modelSet && model == "" {
		return nil
	}
	if !models.ModelIDRe.MatchString(model) {
		return usageErr(c.out, "eval", fmt.Sprintf("fyr eval: invalid --model %q", model))
	}
	if models.IsInteractiveOnlyID(model) {
		return usageErr(c.out, "eval", fmt.Sprintf("fyr eval: --model %q is interactive-only and cannot run headless", model))
	}
	return nil
}

// evalUsage is the usage errors, exit 2, before any other refusal.
func evalUsage(c *runtimeCtx, names []string) error {
	o := evalOptsOf(c)
	var runsSet, parallelSet, runnerSet, modelSet bool
	c.fs.Visit(func(f *flag.Flag) {
		runsSet = runsSet || f.Name == "runs"
		parallelSet = parallelSet || f.Name == "parallel"
		runnerSet = runnerSet || f.Name == "runner"
		modelSet = modelSet || f.Name == "model"
	})
	if o.list {
		if err := evalListUsage(c, names, runsSet, parallelSet, runnerSet, modelSet); err != nil {
			return err
		}
	}
	switch {
	case !o.list && len(names) == 0:
		return usageErr(c.out, "eval", "")
	case o.runs < 1 || o.runs > eval.MaxRuns:
		return usageErr(c.out, "eval", fmt.Sprintf("fyr eval: --runs %d: want 1 to %d", o.runs, eval.MaxRuns))
	case o.parallel < 1 || o.parallel > eval.MaxParallel:
		return usageErr(c.out, "eval", fmt.Sprintf("fyr eval: --parallel %d: want 1 to %d", o.parallel, eval.MaxParallel))
	}
	if err := evalModelUsage(c, modelSet, o.model); err != nil {
		return err
	}
	if err := fyrisolate.CheckEvalRunner(o.runner); err != nil {
		return usageErr(c.out, "eval", "fyr eval: "+err.Error())
	}
	return nil
}

// evalRefuse prints an eval refusal and returns exit 2.
func (c *runtimeCtx) evalRefuse(format string, a ...any) error {
	fmt.Fprintln(c.out, search.Printable("eval: "+fmt.Sprintf(format, a...)))
	return exitCode(2)
}
