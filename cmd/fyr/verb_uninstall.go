package main

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/kesokaj/fyr/internal/auto"
	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/harness"
)

func runVersion(c *runtimeCtx) error {
	if jsonOptsOf(c).jsonOut {
		return writeVerbJSON(c.out, versionJSON{Version: buildVersion()})
	}
	fmt.Fprintln(c.out, "fyr", buildVersion())
	return nil
}

func runUninstall(c *runtimeCtx) error {
	o := dryRunJSONOptsOf(c)
	if !o.jsonOut {
		return runUninstallBody(c)
	}
	out, err := captureVerbOutput(c, func() error { return runUninstallBody(c) })
	if err != nil {
		return err
	}
	return writeVerbJSON(c.out, uninstallJSON{OK: true, DryRun: o.dryRun, Home: c.home, Output: out})
}

func runUninstallBody(c *runtimeCtx) error {
	dryRun := dryRunJSONOptsOf(c).dryRun
	// Any legacy dream timer is not in the manifest; uninstall removes it,
	// also when no manifest is left, and a dry run only says so.
	var timerErr error
	if dream.TimerInstalledHere(c.home, runtime.GOOS) {
		if dryRun {
			fmt.Fprintln(c.out, "[dry-run] would remove the dream timer")
		} else if removed, err := dream.RemoveTimer(c.home, runtime.GOOS); err != nil {
			timerErr = errors.Join(timerErr, err)
		} else if removed {
			// Said only when a unit was removed; the guard above already
			// counts this system's units alone.
			fmt.Fprintln(c.out, "removed the dream timer")
		}
	}
	// Auto's timer goes the same way; auto.json and auto/ stay, as metrics.db
	// does (R4).
	if auto.TimerInstalledHere(c.home, runtime.GOOS) {
		if dryRun {
			fmt.Fprintln(c.out, "[dry-run] would remove the auto timer")
		} else if removed, err := auto.RemoveTimer(c.home, runtime.GOOS); err != nil {
			// Its files may be gone with the manager's stop failed; the rest
			// of the uninstall still runs, and the exit status says so.
			timerErr = errors.Join(timerErr, err)
		} else if removed {
			fmt.Fprintln(c.out, "removed the auto timer")
		}
	}
	err := harness.Uninstall(c.home, dryRun, c.out)
	otherSystemTimerNote(c)
	return errors.Join(err, timerErr)
}

// otherSystemTimerNote names unit files of the operating system this binary
// is not running on: they are never removed here, and status counts them.
func otherSystemTimerNote(c *runtimeCtx) {
	if files := dream.OtherSystemTimerFiles(c.home, runtime.GOOS); len(files) > 0 {
		fmt.Fprintln(c.out, "a dream timer unit for another system is in this home and stays: "+strings.Join(files, ", "))
	}
	if files := auto.OtherSystemTimerFiles(c.home, runtime.GOOS); len(files) > 0 {
		fmt.Fprintln(c.out, "an auto timer unit for another system is in this home and stays: "+strings.Join(files, ", "))
	}
}
