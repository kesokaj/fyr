package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/relay"
	"github.com/kesokaj/fyr/internal/search"
)

type relayErrorJSON struct {
	OK     bool   `json:"ok"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

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
		return emitRelayUsageErr(c, "fyr relay requires --plan <PLAN-ID>")
	}
	if c.relayMaxPhases < 1 || c.relayMaxPhases > relay.MaxPhasesLimit {
		return emitRelayUsageErr(c, fmt.Sprintf("fyr relay: --max-phases must be 1 to %d (got %d)", relay.MaxPhasesLimit, c.relayMaxPhases))
	}
	wd, err := c.projectDir()
	if err != nil {
		return emitRelayOutcome(c, relay.Report{}, err)
	}
	sig, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	start := time.Now()
	rep, runErr := relay.Run(sig, relay.Options{
		Dir: wd, Home: c.home, RealHome: realUserHome(), PlanID: strings.TrimSpace(c.relayPlan),
		MaxPhases: c.relayMaxPhases, JudgeModel: strings.TrimSpace(c.relayJudgeModel),
		DryRun: c.dryRun, Vocab: vocabulary(), SessionRunner: relaySessionRunner,
		QuotaCheck: relayQuotaCheck, VerifyFn: relayVerifyFn, JudgeFn: relayJudgeFn,
		RecordVerdict: recordGateJudgeVerdict(c.home),
	})
	if !c.dryRun && rep.Status != "" {
		recordRelayOrchestrationRun(c.home, wd, rep, start, time.Since(start))
	}
	return emitRelayOutcome(c, rep, runErr)
}

func recordRelayOrchestrationRun(home, wd string, rep relay.Report, start time.Time, elapsed time.Duration) {
	if strings.TrimSpace(home) == "" {
		return
	}
	var okN, failN int
	var workerModel, judgeModel, verdict string
	for _, ph := range rep.Phases {
		if ph.Status == "PASS" {
			okN++
		} else {
			failN++
		}
		if m := strings.TrimSpace(ph.WorkerModel); m != "" {
			workerModel = m
		}
		if jm := strings.TrimSpace(ph.JudgeModel); jm != "" {
			judgeModel = jm
		}
		if jv := strings.TrimSpace(ph.JudgeVerdict); jv != "" {
			verdict = jv
		}
	}
	if verdict == "" {
		if rep.OK {
			verdict = "PASS"
		} else {
			verdict = "FAIL"
		}
	}
	root := strings.TrimSpace(rep.RepoRoot)
	if root == "" {
		root = wd
	}
	row := metrics.OrchestrationRunRow{
		ID:          relayRunID(rep.PlanID, start),
		TS:          time.Now().UTC().Format(time.RFC3339),
		Kind:        "relay",
		ProjectRoot: root,
		Status:      rep.Status,
		HaltCode:    rep.HaltReason,
		Units:       len(rep.Phases),
		Failed:      failN,
		DurationMS:  max(int64(0), elapsed.Milliseconds()),
		WorkerModel: workerModel,
		JudgeModel:  judgeModel,
		Verdict:     verdict,
	}
	row.Passed = okN
	_ = metrics.RecordOrchestrationRun(home, row)
}

func relayRunID(planID string, start time.Time) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(planID) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
		if b.Len() >= 48 {
			break
		}
	}
	slug := b.String()
	if slug == "" {
		slug = "run"
	}
	return fmt.Sprintf("relay-%s-%d", slug, start.UTC().UnixNano())
}

func emitRelayUsageErr(c *ctx, msg string) error {
	if c.jsonOut {
		if jerr := writeJSONLine(c.out, relayErrorJSON{OK: false, Status: "REFUSED", Error: search.Printable(msg)}); jerr != nil {
			return jerr
		}
		return exitCode(2)
	}
	return usageErr(c.out, "relay", msg)
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
	status, code := "ERROR", exitCode(1)
	if errors.Is(err, relay.ErrRefused) {
		status, code = "REFUSED", exitCode(2)
	}
	cleanErr := search.Printable(redact.Redact(err.Error()))
	if c.jsonOut {
		if jerr := writeJSONLine(c.out, relayErrorJSON{OK: false, Status: status, Error: cleanErr}); jerr != nil {
			return jerr
		}
		return code
	}
	fmt.Fprintln(stderr, "fyr relay: "+cleanErr)
	return code
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
			cleanKickoff := sanitizeRelayMultiline(rep.DryRunKickoff)
			fmt.Fprintf(w, "\n--- Kickoff Preview (%d bytes) ---\n%s\n", len(rep.DryRunKickoff), cleanKickoff)
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

func sanitizeRelayMultiline(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = search.Printable(line)
	}
	return strings.Join(lines, "\n")
}
