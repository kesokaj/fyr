package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kesokaj/fyr/internal/relay"
	"github.com/kesokaj/fyr/internal/search"
)

// Test hooks for CLI tests; nil in production.
var (
	relaySessionRunner relay.SessionRunner
	relayQuotaCheck    relay.QuotaCheckFunc
	relayVerifyFn      relay.VerifyFunc
	relayJudgeFn       relay.GateFunc
)

func init() {
	verbs["relay"] = verb{flags: registerRelayFlags, run: runRelay}
}

func registerRelayFlags(c *ctx) {
	c.fs.StringVar(&c.relayPlan, "plan", "", "plan record `ID` to advance (required)")
	c.fs.IntVar(&c.relayMaxPhases, "max-phases", relay.DefaultMaxPhases, "maximum ready phases `N` to run, 1 to 20 (default 1)")
	c.fs.StringVar(&c.relayJudgeModel, "judge-model", "", "explicit cross-family judge model `ID`")
	c.dryRunFlag()
	c.fs.BoolVar(&c.jsonOut, "json", false, "print report as JSON")
}

func runRelay(c *ctx) error {
	if strings.TrimSpace(c.relayPlan) == "" {
		return usageErr(c.out, "relay", "fyr relay requires --plan <PLAN-ID>")
	}
	if c.relayMaxPhases < 1 || c.relayMaxPhases > relay.MaxPhasesLimit {
		return usageErr(c.out, "relay", fmt.Sprintf("fyr relay: --max-phases must be 1 to %d (got %d)", relay.MaxPhasesLimit, c.relayMaxPhases))
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	rep, runErr := relay.Run(context.Background(), relay.Options{
		Dir: wd, Home: c.home, PlanID: strings.TrimSpace(c.relayPlan),
		MaxPhases: c.relayMaxPhases, JudgeModel: strings.TrimSpace(c.relayJudgeModel),
		DryRun: c.dryRun, Vocab: vocabulary(), SessionRunner: relaySessionRunner,
		QuotaCheck: relayQuotaCheck, VerifyFn: relayVerifyFn, JudgeFn: relayJudgeFn,
		RecordVerdict: recordGateJudgeVerdict(c.home),
	})
	return emitRelayOutcome(c, rep, runErr)
}

func emitRelayOutcome(c *ctx, rep relay.Report, err error) error {
	if err == nil || (rep.Status != "" && errors.Is(err, relay.ErrHalted)) {
		if c.jsonOut {
			if jerr := writeJSONLine(c.out, rep); jerr != nil {
				return jerr
			}
		} else {
			printRelayText(c.out, rep)
		}
		if err != nil {
			return exitCode(1)
		}
		return nil
	}
	fmt.Fprintln(stderr, search.Printable("fyr relay: "+err.Error()))
	if errors.Is(err, relay.ErrRefused) {
		return exitCode(2)
	}
	return exitCode(1)
}

func printRelayText(w io.Writer, rep relay.Report) {
	switch rep.Status {
	case relay.StatusComplete:
		fmt.Fprintf(w, "relay: COMPLETE (%s — all phases checked)\n", search.Printable(rep.PlanID))
	case relay.StatusDryRun:
		fmt.Fprintf(w, "relay: DRY_RUN (%s)\n  git_clean: %v\n  quota_ok:  %v (%s)\n",
			search.Printable(rep.PlanID), rep.GitClean, rep.QuotaOK, search.Printable(rep.QuotaNote))
		for i, ph := range rep.PlannedPhases {
			fmt.Fprintf(w, "  next[%d]:   %s (%d open task(s))\n", i+1, search.Printable(ph.Title), len(ph.OpenTasks))
		}
		if rep.DryRunKickoff != "" {
			fmt.Fprintf(w, "\n--- Kickoff Preview (%d bytes) ---\n%s\n", len(rep.DryRunKickoff), rep.DryRunKickoff)
		}
	case relay.StatusOK:
		printRelayPhases(w, rep)
		suffix := ""
		if rep.PlanComplete {
			suffix = "; plan COMPLETE"
		}
		fmt.Fprintf(w, "relay: OK (%s, %d phase(s) passed%s)\n", search.Printable(rep.PlanID), len(rep.Phases), suffix)
	default:
		printRelayPhases(w, rep)
		fmt.Fprintf(w, "relay: HALTED [%s] %s\n", search.Printable(rep.HaltReason), search.Printable(rep.HaltDetail))
	}
}

func printRelayPhases(w io.Writer, rep relay.Report) {
	for _, ph := range rep.Phases {
		line := fmt.Sprintf("  %s: %s", search.Printable(ph.PhaseID), search.Printable(ph.Status))
		if ph.HeadBefore != "" && ph.HeadAfter != "" {
			line += fmt.Sprintf(" (%s..%s)", shortHash(ph.HeadBefore, 8), shortHash(ph.HeadAfter, 8))
		}
		if ph.HaltReason != "" {
			line += " [" + search.Printable(ph.HaltReason) + "]"
		}
		if ph.HandoffID != "" {
			line += " handoff=" + search.Printable(ph.HandoffID)
		}
		fmt.Fprintln(w, line)
	}
}
