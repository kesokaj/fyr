package main

import (
	"errors"
	"fmt"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/metrics"
)

// runDream needs no harness tree (R3's first exception).
func runDream(c *ctx) error {
	var err error
	project := c.dreamProject
	if project == "" && !c.dreamAll && c.dirFlag != "" {
		project, err = c.projectDir()
		if err != nil {
			return err
		}
	}
	// Every usage error goes through usageErr, as every other verb's does:
	// its reason, dream's synopsis and the hint, exit 2 on the caller's writer
	// (IN12, IN13; ledger item 86). All are refused before anything is locked,
	// logged or read.
	switch {
	case c.dreamAll && c.dreamProject != "":
		return usageErr(c.out, "dream", "fyr dream: --all or --project <dir>, not both")
	case !c.dreamAll && project == "":
		return usageErr(c.out, "dream", "")
	default:
		var recordBg func(metrics.BackgroundRunRow) error
		if !c.dryRun {
			home := c.home
			recordBg = func(row metrics.BackgroundRunRow) error {
				return metrics.RecordBackgroundRun(home, row)
			}
		}
		err = dream.Run(dream.Options{
			Home:                c.home,
			All:                 c.dreamAll,
			Project:             project,
			DryRun:              c.dryRun,
			Out:                 c.out,
			RecordBackgroundRun: recordBg,
		})
	}
	var ee *dream.ExitError
	if errors.As(err, &ee) {
		fmt.Fprintln(c.out, ee.Msg)
		return exitCode(ee.Code)
	}
	return err
}
