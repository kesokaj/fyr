package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
	"github.com/kesokaj/fyr/internal/work"
)

// workSelf is the fyr each sandbox installs; a test replaces it, since the
// test binary is not a fyr.
var workSelf = os.Executable

func init() {
	verbs["work"] = verb{flags: func(c *ctx) {
		c.fs.DurationVar(&c.workTimeout, "timeout", work.DefaultTimeout, "kill the worker's agy after `D`, whole minutes, at least 10m")
		c.fs.StringVar(&c.workModel, "model", "", "the agy model `M` the worker runs on (default: agy's own)")
		c.fs.BoolVar(&c.workKeep, "keep", false, "keep the sandbox of a fetched worker too")
		c.fs.IntVar(&c.workMax, "max-workers", work.DefaultMaxWorkers, "run at most `N` workers at once across the machine, 1 to 8")
		c.fs.StringVar(&c.workKill, "kill", "", "end the running fyr work whose stamp line printed `STAMP`")
	}, positional: true, readsRepo: true, run: runWork}
}

// runWork serves `fyr work` for one or more Kickoff files, or --kill
// (SPEC-fyr-work, plan PLAN-2026-10-29-workers Phases 3-4). Every refusal
// exits 2 before anything is made (D34): the usage errors, a --timeout or a
// --max-workers of 0 or less, not a checkout, the install source as eval
// resolves it (D40), then internal/work's own refusals. No --home refusal:
// the verb writes no store, so the real home is its default (D35).
func runWork(c *ctx) error {
	files := c.fs.Args()
	if c.workKill != "" {
		return c.workKillRun(files)
	}
	switch {
	case len(files) == 0:
		return usageErr(c.out, "work", "")
	case c.workMax < 1:
		// internal/work reads 0 as its default and refuses over 8.
		return c.workRefuse("--max-workers %d: give 1 to %d", c.workMax, work.MaxMaxWorkers)
	case c.workTimeout <= 0:
		// internal/work reads 0 as its default; a typed 0 is a mistake.
		return c.workRefuse("--timeout %s: give whole minutes, at least 10m", c.workTimeout)
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	_, root, err := trail.Roots(wd)
	if err != nil {
		return c.workRefuse("%s is not a git checkout", wd)
	}
	src, err := harness.ResolveSource(c.repoFlag, c.home, buildVersion())
	if err != nil {
		return c.workRefuse("%v", err)
	}
	realHome, err := os.UserHomeDir()
	if err != nil {
		return c.workRefuse("no home directory: %v", err)
	}
	self, err := workSelf()
	if err != nil {
		return c.workRefuse("the running fyr: %v", err)
	}
	return c.workRun(work.Options{Root: root, Home: c.home, RealHome: realHome, Self: self, Repo: src.Dir,
		Timeout: c.workTimeout, Model: c.workModel, Keep: c.workKeep, MaxWorkers: c.workMax, Out: c.out}, files)
}

// workRun runs the files under the verb's signals and maps the results to
// the exit status: 0 every worker passed, 1 any failed, 2 a refusal.
func (c *ctx) workRun(o work.Options, files []string) error {
	// A hangup too, as eval, and a quit: either default would kill fyr
	// before the token link is removed, and agy, in its own process group,
	// would run on with the wide grant (the Phase 3 security review).
	sig, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer stop()
	res, err := work.Run(sig, o, files...)
	if err != nil {
		return c.workRefuse("%v", err)
	}
	for _, r := range res {
		if r.Verdict != "PASS" {
			return exitCode(1)
		}
	}
	return nil
}

// workKillRun is --kill: SIGTERM to the run the stamp names, which then
// prints its own result contract. It takes no Kickoff file.
func (c *ctx) workKillRun(files []string) error {
	if len(files) > 0 {
		return c.workRefuse("--kill takes a stamp and no Kickoff file")
	}
	pid, err := work.Kill(c.home, c.workKill)
	if err != nil {
		return c.workRefuse("%v", err)
	}
	fmt.Fprintln(c.out, search.Printable(fmt.Sprintf("work: SIGTERM sent to %d, the run %s; it prints its result contract", pid, c.workKill)))
	return nil
}

// workRefuse prints a work refusal and returns exit 2.
func (c *ctx) workRefuse(format string, a ...any) error {
	fmt.Fprintln(c.out, search.Printable("work: "+fmt.Sprintf(format, a...)))
	return exitCode(2)
}
