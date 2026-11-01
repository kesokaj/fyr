package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"

	"github.com/kesokaj/fyr/internal/dream"
)

// runDream needs no harness tree (R3's first exception).
func runDream(c *ctx) error {
	var err error
	// Every usage error goes through usageErr, as every other verb's does:
	// its reason, dream's synopsis and the hint, exit 2 on the caller's writer
	// (IN12, IN13; ledger item 86). All four are refused before anything is
	// locked, logged or read.
	switch {
	case c.installTimer && c.removeTimer:
		return usageErr(c.out, "dream", "fyr dream: --install-timer or --remove-timer, not both")
	case (c.installTimer || c.removeTimer) && (c.dreamAll || c.dreamProject != ""):
		// The timer always dreams --all over its home; a scope given with it
		// was silently ignored (SD33 of the 2026-10-27 audit, plan D18).
		return usageErr(c.out, "dream", "fyr dream: --install-timer and --remove-timer take no --all or --project")
	case c.installTimer || c.removeTimer:
		err = c.dreamTimer()
	case c.dreamAll && c.dreamProject != "":
		return usageErr(c.out, "dream", "fyr dream: --all or --project <dir>, not both")
	case !c.dreamAll && c.dreamProject == "":
		return usageErr(c.out, "dream", "")
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

// dreamTimer serves --install-timer and --remove-timer, each with or without
// --dry-run.
func (c *ctx) dreamTimer() error {
	var err error
	switch {
	case c.installTimer && c.dryRun:
		// The dry run makes every refusal the real run makes, then names what
		// would change and calls nothing (review-standards § 8; SD26).
		err = dream.CheckTimer(c.home, os.Getenv("PATH"), runtime.GOOS)
		if err == nil {
			fmt.Fprintln(c.out, "[dry-run] would install the hourly dream timer (fyr dream --all every hour, while an agy session runs); installed now: "+strconv.FormatBool(dream.TimerInstalledHere(c.home, runtime.GOOS)))
		}
	case c.removeTimer && c.dryRun:
		// A new side effect goes below the read-only mode (context § Hard
		// rules): the dry run names what would change and calls nothing.
		fmt.Fprintln(c.out, "[dry-run] would remove the dream timer; installed now: "+strconv.FormatBool(dream.TimerInstalledHere(c.home, runtime.GOOS)))
		otherSystemTimerNote(c)
	case c.installTimer:
		err = dream.InstallTimer(c.home, os.Getenv("PATH"), runtime.GOOS)
		if err == nil {
			fmt.Fprintln(c.out, "dream timer installed: fyr dream --all runs hourly while an agy session runs")
		}
	case c.removeTimer:
		var removed bool
		removed, err = dream.RemoveTimer(c.home, runtime.GOOS)
		if err == nil {
			if removed {
				fmt.Fprintln(c.out, "dream timer removed")
			} else {
				fmt.Fprintln(c.out, "no dream timer found for "+c.home+" on this system")
			}
			otherSystemTimerNote(c)
		}
	}
	return err
}
