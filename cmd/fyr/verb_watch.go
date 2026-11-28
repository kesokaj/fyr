package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/watch"
)

var watchProcLister func() ([]watch.ProcInfo, error)

func watchVerb() verb {
	return verb{flags: registerWatchFlags, run: runWatch}
}

func registerWatchFlags(c *ctx) {
	c.fs.BoolVar(&c.check, "check", false, "run one out-of-band hang, interactive-wait and budget check")
	c.fs.StringVar(&c.watchConv, "conv", "", "inspect conversation `ID` and its child subagents")
	c.fs.IntVar(&c.watchIdleSec, "idle-sec", 0, "stall threshold `N` in seconds (default 90)")
	c.fs.BoolVar(&c.jsonOut, "json", false, "print report as JSON")
}

func runWatch(c *ctx) error {
	if !c.check {
		return usageErr(c.out, "watch", "fyr watch requires --check")
	}
	if c.watchIdleSec < 0 {
		return usageErr(c.out, "watch", "fyr watch: --idle-sec must be >= 0")
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	rep, err := watch.Check(context.Background(), watch.Options{
		Home:       c.home,
		Dir:        wd,
		ConvID:     c.watchConv,
		IdleSec:    c.watchIdleSec,
		ProcLister: watchProcLister,
	})
	if err != nil {
		fmt.Fprintln(stderr, search.Printable("fyr watch: "+err.Error()))
		return exitCode(2)
	}
	if c.jsonOut {
		if err := writeJSONLine(c.out, rep); err != nil {
			return err
		}
	} else {
		printWatchText(c.out, rep)
	}
	if !rep.OK {
		return exitCode(1)
	}
	return nil
}

func printWatchText(w io.Writer, rep watch.Report) {
	if rep.OK {
		fmt.Fprintf(w, "watch: ok (%d checked)\n", rep.Checked)
		return
	}
	for _, f := range rep.Findings {
		fmt.Fprintln(w, formatWatchFinding(f))
	}
}

func formatWatchFinding(f watch.Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s", search.Printable(f.Kind), search.Printable(f.Target))
	if f.PID > 0 {
		fmt.Fprintf(&b, " pid=%d", f.PID)
	}
	if f.IdleSec > 0 {
		fmt.Fprintf(&b, " idle=%ds", f.IdleSec)
	}
	if f.ArtifactStatus != "" {
		fmt.Fprintf(&b, " status=%s", search.Printable(f.ArtifactStatus))
	}
	if f.PromptHint != "" {
		fmt.Fprintf(&b, " hint=%q", search.Printable(f.PromptHint))
	}
	if f.Action != "" {
		fmt.Fprintf(&b, " -- %s", search.Printable(f.Action))
	}
	return b.String()
}
