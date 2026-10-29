package main

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/harness"
)

func runVersion(c *ctx) error {
	fmt.Fprintln(c.out, "fyr", buildVersion())
	return nil
}

func runUninstall(c *ctx) error {
	// The dream timer is not in the manifest (the next install would delete
	// it as stale); uninstall removes it by R4's dream clause, also when no
	// manifest is left, and a dry run only says so.
	if dream.TimerInstalledHere(c.home, runtime.GOOS) {
		if c.dryRun {
			fmt.Fprintln(c.out, "[dry-run] would remove the dream timer")
		} else if removed, err := dream.RemoveTimer(c.home, runtime.GOOS); err != nil {
			return err
		} else if removed {
			// Said only when a unit was removed; the guard above already
			// counts this system's units alone.
			fmt.Fprintln(c.out, "removed the dream timer")
		}
	}
	err := harness.Uninstall(c.home, c.dryRun, c.out)
	otherSystemTimerNote(c)
	return err
}

// otherSystemTimerNote names unit files of the operating system this binary
// is not running on: they are never removed here, and status counts them.
func otherSystemTimerNote(c *ctx) {
	if files := dream.OtherSystemTimerFiles(c.home, runtime.GOOS); len(files) > 0 {
		fmt.Fprintln(c.out, "a dream timer unit for another system is in this home and stays: "+strings.Join(files, ", "))
	}
}
