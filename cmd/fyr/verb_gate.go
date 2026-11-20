package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/kesokaj/fyr/internal/gate"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
)

// verify and land call vocabulary(), which reads verbs; register gate verbs in
// init() like check.go to avoid an initialization cycle.
func init() {
	verbs["verify"] = verb{flags: registerVerifyFlags, run: runVerify}
	verbs["gate"] = verb{flags: registerGateFlags, positional: true, run: runGate}
	verbs["land"] = verb{flags: registerLandFlags, positional: true, run: runLand}
	verbs["close"] = verb{flags: registerCloseFlags, run: runClose}
}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func registerVerifyFlags(c *ctx) {
	c.fs.BoolVar(&c.jsonOut, "json", false, "print report as JSON")
	c.fs.DurationVar(&c.gateTimeout, "timeout", 0, "per-command test timeout `D` (default 30m)")
}

func registerGateFlags(c *ctx) {
	c.fs.StringVar(&c.gateBase, "base", "", "base commit or branch `REF` (default HEAD)")
	c.fs.Var(&c.gateLease, "lease", "allowed path pattern `GLOB` (repeatable)")
	c.fs.StringVar(&c.gateLeaseFile, "lease-file", "", "read lease patterns from `FILE`")
	c.fs.IntVar(&c.gateMaxFiles, "max-files", 0, "maximum changed files `N` (default 50)")
	c.fs.IntVar(&c.gateMaxLines, "max-lines", 0, "maximum changed lines `N` (default 1500)")
	c.fs.StringVar(&c.gateJudge, "judge", "", "cross-family LLM judge acceptance criterion `TEXT`")
	c.fs.StringVar(&c.gateJudgeModel, "judge-model", "", "explicit judge model `ID` (with --judge)")
	c.fs.BoolVar(&c.jsonOut, "json", false, "print report as JSON")
}

func registerLandFlags(c *ctx) {
	c.fs.StringVar(&c.landMessage, "message", "", "squash commit subject/body `TEXT`")
	c.fs.Var(&c.gateLease, "lease", "allowed path pattern `GLOB` (repeatable)")
	c.fs.StringVar(&c.gateLeaseFile, "lease-file", "", "read lease patterns from `FILE`")
	c.fs.IntVar(&c.gateMaxFiles, "max-files", 0, "maximum changed files `N` (default 50)")
	c.fs.IntVar(&c.gateMaxLines, "max-lines", 0, "maximum changed lines `N` (default 1500)")
	c.fs.DurationVar(&c.gateTimeout, "timeout", 0, "per-command verify timeout `D` (default 30m)")
	c.fs.StringVar(&c.gateJudge, "judge", "", "cross-family LLM judge acceptance criterion `TEXT`")
	c.fs.StringVar(&c.gateJudgeModel, "judge-model", "", "explicit judge model `ID` (with --judge)")
	c.fs.BoolVar(&c.jsonOut, "json", false, "print report as JSON")
}

func registerCloseFlags(c *ctx) {
	c.fs.StringVar(&c.closeID, "id", "", "explicit handoff record `ID` (HANDOFF-<stamp>-<slug>)")
	c.fs.StringVar(&c.closeTitle, "title", "", "handoff record title `TEXT` (required)")
	c.fs.StringVar(&c.closeBodyFile, "body-file", "", "read handoff body from `FILE` instead of stdin")
	c.fs.BoolVar(&c.jsonOut, "json", false, "print report as JSON")
}

func runVerify(c *ctx) error {
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	rep, err := gate.Verify(context.Background(), gate.VerifyOptions{
		Dir:     wd,
		Home:    c.home,
		Vocab:   vocabulary(),
		Timeout: c.gateTimeout,
	})
	return emitGateOutcome(c, rep, len(rep.Steps) > 0 || err == nil, err, printVerifyText)
}

func runGate(c *ctx) error {
	if c.fs.NArg() != 1 {
		return usageErr(c.out, "gate", "fyr gate requires <worktree> or <branch>")
	}
	if strings.TrimSpace(c.gateJudgeModel) != "" && strings.TrimSpace(c.gateJudge) == "" {
		return usageErr(c.out, "gate", "fyr gate: --judge-model requires --judge")
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	rep, err := gate.Gate(context.Background(), gate.GateOptions{
		Dir:           wd,
		Target:        c.fs.Arg(0),
		Base:          c.gateBase,
		Home:          c.home,
		Lease:         c.gateLease,
		LeaseFile:     c.gateLeaseFile,
		MaxFiles:      c.gateMaxFiles,
		MaxLines:      c.gateMaxLines,
		Judge:         c.gateJudge,
		JudgeModel:    c.gateJudgeModel,
		RecordVerdict: recordGateJudgeVerdict(c.home),
	})
	return emitGateOutcome(c, rep, rep.MainRoot != "", err, printGateText)
}

func runLand(c *ctx) error {
	if c.fs.NArg() != 1 {
		return usageErr(c.out, "land", "fyr land requires <branch>")
	}
	if strings.TrimSpace(c.gateJudgeModel) != "" && strings.TrimSpace(c.gateJudge) == "" {
		return usageErr(c.out, "land", "fyr land: --judge-model requires --judge")
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	rep, err := gate.Land(context.Background(), gate.LandOptions{
		Dir:           wd,
		Branch:        c.fs.Arg(0),
		Message:       c.landMessage,
		Home:          c.home,
		Vocab:         vocabulary(),
		Timeout:       c.gateTimeout,
		Lease:         c.gateLease,
		LeaseFile:     c.gateLeaseFile,
		MaxFiles:      c.gateMaxFiles,
		MaxLines:      c.gateMaxLines,
		Judge:         c.gateJudge,
		JudgeModel:    c.gateJudgeModel,
		RecordVerdict: recordGateJudgeVerdict(c.home),
	})
	return emitGateOutcome(c, rep, rep.MainRoot != "", err, printLandText)
}

func recordGateJudgeVerdict(home string) func(gate.JudgeVerdictRecord) error {
	return func(rec gate.JudgeVerdictRecord) error {
		workerModel, workerSession := rec.WorkerModel, rec.WorkerSessionID
		if workerModel == "" || workerSession == "" {
			infModel, infSess := metrics.InferWorkerAttribution(home, rec.ProjectRoot, rec.Target)
			if workerModel == "" {
				workerModel = infModel
			}
			if workerSession == "" {
				workerSession = infSess
			}
		}
		repoID := rec.RepoID
		if repoID == "" && rec.ProjectRoot != "" {
			repoID = metrics.ResolveRepoID(rec.ProjectRoot)
		}
		return metrics.RecordJudgeVerdict(home, metrics.JudgeVerdictRow{
			TS:              rec.TS,
			ProjectRoot:     rec.ProjectRoot,
			RepoID:          repoID,
			Target:          rec.Target,
			Model:           rec.Model,
			WorkerModel:     workerModel,
			WorkerSessionID: workerSession,
			FallbackReason:  rec.FallbackReason,
			CriterionHash:   rec.CriterionHash,
			Verdict:         rec.Verdict,
		})
	}
}

func runClose(c *ctx) error {
	if strings.TrimSpace(c.closeTitle) == "" {
		return usageErr(c.out, "close", "fyr close requires --title")
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	bodyText, err := readCloseBody(c)
	if err != nil {
		return gateErrCode(c.cmd, err)
	}
	rep, err := gate.Close(context.Background(), gate.CloseOptions{
		Dir:   wd,
		Home:  c.home,
		ID:    c.closeID,
		Title: c.closeTitle,
		Body:  bodyText,
	})
	if err != nil {
		return gateErrCode(c.cmd, err)
	}
	warnRedactable(c.closeID, c.closeTitle, bodyText)
	if c.jsonOut {
		return writeJSONLine(c.out, rep)
	}
	fmt.Fprintln(c.out, rep.ID)
	return nil
}

func readCloseBody(c *ctx) (string, error) {
	bodySet := false
	c.fs.Visit(func(f *flag.Flag) { bodySet = bodySet || f.Name == "body-file" })
	r, cleanup, err := openBodyReader(c.closeBodyFile, bodySet)
	if err != nil {
		return "", err
	}
	defer cleanup()
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func emitGateOutcome[T any](c *ctx, rep T, hasReport bool, err error, printText func(io.Writer, T)) error {
	if err == nil || (hasReport && errors.Is(err, gate.ErrFailed)) {
		if c.jsonOut {
			if jerr := writeJSONLine(c.out, rep); jerr != nil {
				return jerr
			}
		} else {
			printText(c.out, rep)
		}
		if err != nil {
			return exitCode(1)
		}
		return nil
	}
	return gateErrCode(c.cmd, err)
}

func gateErrCode(verb string, err error) error {
	fmt.Fprintln(stderr, search.Printable("fyr "+verb+": "+err.Error()))
	if errors.Is(err, gate.ErrRefused) {
		return exitCode(2)
	}
	return exitCode(1)
}

func writeJSONLine(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func shortHash(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func printVerifyText(w io.Writer, rep gate.VerifyReport) {
	for _, s := range rep.Steps {
		state := "FAIL"
		if s.OK {
			state = "PASS"
		}
		fmt.Fprintf(w, "verify %-8s %s (%dms)\n", s.Name+":", state, s.DurationMs)
		if !s.OK && s.Detail != "" {
			fmt.Fprintln(w, search.Printable(s.Detail))
		}
	}
	if rep.OK {
		fmt.Fprintf(w, "verify: PASS (%s)\n", shortHash(rep.TreeSHA256, 12))
	}
}

func printGateText(w io.Writer, rep gate.GateReport) {
	if rep.OK {
		if rep.JudgeModel != "" {
			fmt.Fprintf(w, "gate: PASS (%d file(s), +%d/-%d; judge %s=%s)\n", rep.FilesChanged, rep.LinesAdded, rep.LinesDeleted, rep.JudgeModel, rep.JudgeVerdict)
			return
		}
		fmt.Fprintf(w, "gate: PASS (%d file(s), +%d/-%d)\n", rep.FilesChanged, rep.LinesAdded, rep.LinesDeleted)
		return
	}
	for _, f := range rep.Findings {
		loc := f.Path
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
		}
		if loc != "" {
			fmt.Fprintf(w, "[%s] %s: %s\n", f.Check, loc, search.Printable(f.Detail))
		} else {
			fmt.Fprintf(w, "[%s] %s\n", f.Check, search.Printable(f.Detail))
		}
	}
}

func printLandText(w io.Writer, rep gate.LandReport) {
	if rep.OK {
		fmt.Fprintf(w, "land: PASS %s -> %s (%s)\n", rep.Branch, rep.Target, shortHash(rep.LandCommit, 12))
		return
	}
	if rep.Gate != nil && len(rep.Gate.Findings) > 0 {
		printGateText(w, *rep.Gate)
	}
	if rep.Verify != nil && len(rep.Verify.Steps) > 0 {
		printVerifyText(w, *rep.Verify)
	}
}
