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
	}, positional: true, readsRepo: true, run: runWork}
}

// runWork serves `fyr work` for one Kickoff file (SPEC-fyr-work, plan
// PLAN-2026-10-29-workers Phase 3). Every refusal exits 2 before anything is
// made (D34): the usage errors and a --timeout of 0 or less, not a
// checkout, the install source as eval resolves it (D40), then
// internal/work's own refusals. No --home refusal: the verb writes no store,
// so the real home is its default (D35).
func runWork(c *ctx) error {
	files := c.fs.Args()
	switch {
	case len(files) == 0:
		return usageErr(c.out, "work", "")
	case len(files) > 1:
		return c.workRefuse("one Kickoff file at a time; got %d", len(files))
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
	o := work.Options{Root: root, Home: c.home, RealHome: realHome, Self: self, Repo: src.Dir,
		Timeout: c.workTimeout, Model: c.workModel, Keep: c.workKeep, Out: c.out}

	// A hangup too, as eval, and a quit: either default would kill fyr
	// before the token link is removed, and agy, in its own process group,
	// would run on with the wide grant (the Phase 3 security review).
	sig, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer stop()
	res, err := work.Run(sig, o, files[0])
	if err != nil {
		return c.workRefuse("%v", err)
	}
	if res.Verdict != "PASS" {
		return exitCode(1)
	}
	return nil
}

// workRefuse prints a work refusal and returns exit 2.
func (c *ctx) workRefuse(format string, a ...any) error {
	fmt.Fprintln(c.out, search.Printable("work: "+fmt.Sprintf(format, a...)))
	return exitCode(2)
}
