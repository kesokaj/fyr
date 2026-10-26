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
	// The unit under the name before the rename goes too (TR9).
	old := dream.OldTimerHere(c.home, runtime.GOOS)
	if dream.TimerInstalledHere(c.home, runtime.GOOS) || old != "" {
		if c.dryRun {
			fmt.Fprintln(c.out, "[dry-run] would remove the dream timer"+oldTimerNote(old))
		} else if removed, err := dream.RemoveTimer(c.home, runtime.GOOS); err != nil {
			return err
		} else if removed {
			// Said only when a unit was removed; the guard above already
			// counts this system's units alone.
			fmt.Fprintln(c.out, "removed the dream timer"+oldTimerNote(old))
		}
	}
	err := harness.Uninstall(c.home, c.dryRun, c.out)
	// A unit from v2.0.0 or earlier under the shared name is not this home's
	// to remove; the operator is told where it is and what to do, dry run or not.
	if notice := dream.LegacyTimerNotice(c.home, runtime.GOOS); notice != "" {
		fmt.Fprintln(c.out, notice)
	}
	otherSystemTimerNote(c)
	return err
}

// oldTimerNote names this home's unit under the name before the rename in a
// removal line, or says nothing when there is none.
func oldTimerNote(old string) string {
	if old == "" {
		return ""
	}
	return " (with " + old + ", its unit from before the rename to fyr)"
}

// otherSystemTimerNote names unit files of the operating system this binary
// is not running on: they are never removed here, and status counts them.
func otherSystemTimerNote(c *ctx) {
	if files := dream.OtherSystemTimerFiles(c.home, runtime.GOOS); len(files) > 0 {
		fmt.Fprintln(c.out, "a dream timer unit for another system is in this home and stays: "+strings.Join(files, ", "))
	}
}
