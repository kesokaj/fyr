package main

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/metrics"
)

// dreamOpts holds parsed flags for `fyr dream`.
type dreamOpts struct {
	all, dryRun, jsonOut bool
	project              string
}

func dreamOptsOf(c *runtimeCtx) *dreamOpts {
	if o, ok := c.opts.(*dreamOpts); ok {
		return o
	}
	o := &dreamOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func dreamVerb() verb {
	return verb{flags: registerDreamFlags, writesState: dreamWritesState, run: runDream}
}

func registerDreamFlags(c *runtimeCtx) {
	o := &dreamOpts{}
	c.opts = o
	c.fs.BoolVar(&o.all, "all", false, "every workspace discovered from conversations, brain.db and search.db")
	c.fs.StringVar(&o.project, "project", "", "one workspace, `DIR`, or __global__")
	c.fs.BoolVar(&o.dryRun, "dry-run", false, "print what would change, change nothing")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print the result as JSON")
}

func dreamWritesState(c *runtimeCtx) bool {
	return !dreamOptsOf(c).dryRun
}

// runDream needs no harness tree (R3's first exception).
func runDream(c *runtimeCtx) error {
	o := dreamOptsOf(c)
	project, err := resolveDreamProject(c, o)
	if err != nil {
		return err
	}
	var recordBg func(metrics.BackgroundRunRow) error
	if !o.dryRun {
		home := c.home
		recordBg = func(row metrics.BackgroundRunRow) error { return metrics.RecordBackgroundRun(home, row) }
	}
	var buf bytes.Buffer
	out := c.out
	if o.jsonOut {
		out = &buf
	}
	err = dream.Run(dream.Options{
		Home: c.home, All: o.all, Project: project, DryRun: o.dryRun, Out: out, RecordBackgroundRun: recordBg,
	})
	return finishDreamRun(c, o, project, &buf, err)
}

func resolveDreamProject(c *runtimeCtx, o *dreamOpts) (string, error) {
	project := o.project
	if project == "" && !o.all && c.dirFlag != "" {
		var err error
		if project, err = c.projectDir(); err != nil {
			return "", err
		}
	}
	// Every usage error goes through usageErr, as every other verb's does:
	// its reason, dream's synopsis and the hint, exit 2 on the caller's writer
	// (IN12, IN13; ledger item 86). All are refused before anything is locked,
	// logged or read.
	switch {
	case o.all && o.project != "":
		return "", usageErr(c.out, "dream", "fyr dream: --all or --project <dir>, not both")
	case !o.all && project == "":
		return "", usageErr(c.out, "dream", "")
	}
	return project, nil
}

func finishDreamRun(c *runtimeCtx, o *dreamOpts, project string, buf *bytes.Buffer, err error) error {
	var ee *dream.ExitError
	if errors.As(err, &ee) {
		if o.jsonOut {
			if jerr := writeVerbExitErrorJSON(c.out, "dream", ee.Msg, buf.String()); jerr != nil {
				return jerr
			}
			return exitCode(ee.Code)
		}
		fmt.Fprintln(c.out, ee.Msg)
		return exitCode(ee.Code)
	}
	if err == nil && o.jsonOut {
		return writeDreamJSON(c, project, buf.String())
	}
	return err
}
