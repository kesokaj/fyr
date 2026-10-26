package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"

	"github.com/kesokaj/fyr/internal/dream"
)

// runDream needs no harness tree (R3's exception).
func runDream(c *ctx) error {
	var err error
	switch {
	case c.installTimer && c.removeTimer:
		// A usage error exits 2 like every other dream usage error, and says
		// why on the caller's writer (IN12, IN13).
		fmt.Fprintln(c.out, "fyr dream: --install-timer or --remove-timer, not both")
		return exitCode(2)
	case (c.installTimer || c.removeTimer) && c.dryRun:
		// A new side effect goes below the read-only mode (context § Hard
		// rules): the dry run names what would change and calls nothing.
		what := "install the nightly dream timer (fyr dream --all at 03:00)"
		if c.removeTimer {
			what = "remove the nightly dream timer" + oldTimerNote(dream.OldTimerHere(c.home, runtime.GOOS))
		}
		fmt.Fprintln(c.out, "[dry-run] would "+what+"; installed now: "+strconv.FormatBool(dream.TimerInstalledHere(c.home, runtime.GOOS)))
		if notice := dream.LegacyTimerNotice(c.home, runtime.GOOS); c.removeTimer && notice != "" {
			fmt.Fprintln(c.out, notice)
		}
		if c.removeTimer {
			otherSystemTimerNote(c)
		}
	case c.installTimer:
		err = dream.InstallTimer(c.home, os.Getenv("PATH"), runtime.GOOS)
		if err == nil {
			fmt.Fprintln(c.out, "dream timer installed: fyr dream --all runs nightly at 03:00")
		}
	case c.removeTimer:
		var removed bool
		old := dream.OldTimerHere(c.home, runtime.GOOS)
		removed, err = dream.RemoveTimer(c.home, runtime.GOOS)
		if err == nil {
			if removed {
				fmt.Fprintln(c.out, "dream timer removed"+oldTimerNote(old))
			} else {
				fmt.Fprintln(c.out, "no dream timer found for "+c.home+" on this system")
			}
			if notice := dream.LegacyTimerNotice(c.home, runtime.GOOS); notice != "" {
				fmt.Fprintln(c.out, notice)
			}
			otherSystemTimerNote(c)
		}
	default:
		err = dream.Run(dream.Options{Home: c.home, All: c.dreamAll, Project: c.dreamProject, DryRun: c.dryRun, Out: c.out})
	}
	var ee *dream.ExitError
	if errors.As(err, &ee) {
		fmt.Fprintln(c.out, ee.Msg)
		return exitCode(ee.Code)
	}
	return err
}
