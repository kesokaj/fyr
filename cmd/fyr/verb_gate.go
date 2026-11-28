package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/gate"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
)

// gateOpts holds parsed flags for `fyr verify`, `gate`, `land`, and `close`.
type gateOpts struct {
	base, leaseFile, landMessage, closeID, closeTitle, closeBodyFile string
	lease                                                            stringSliceFlag
	maxFiles, maxLines                                               int
	timeout                                                          time.Duration
	judge, judgeModel, workerModel, workerSession                    string
	jsonOut                                                          bool
}

func gateOptsOf(c *runtimeCtx) *gateOpts {
	if o, ok := c.opts.(*gateOpts); ok {
		return o
	}
	return &gateOpts{}
}

func registerGateVerbs(m map[string]verb) {
	m["verify"] = verb{flags: registerVerifyFlags, run: runVerify}
	m["gate"] = verb{flags: registerGateFlags, positional: true, run: runGate}
	m["land"] = verb{flags: registerLandFlags, positional: true, run: runLand}
	m["close"] = verb{flags: registerCloseFlags, run: runClose}
}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func registerVerifyFlags(c *runtimeCtx) {
	o := &gateOpts{}
	c.opts = o
	c.fs.BoolVar(&o.jsonOut, "json", false, "print report as JSON")
	c.fs.DurationVar(&o.timeout, "timeout", 0, "per-command test timeout `D` (default 30m)")
}

func registerGateFlags(c *runtimeCtx) {
	o := &gateOpts{}
	c.opts = o
	c.fs.StringVar(&o.base, "base", "", "base commit or branch `REF` (default HEAD)")
	c.fs.Var(&o.lease, "lease", "allowed path pattern `GLOB` (repeatable)")
	c.fs.StringVar(&o.leaseFile, "lease-file", "", "read lease patterns from `FILE`")
	c.fs.IntVar(&o.maxFiles, "max-files", 0, "maximum changed files `N` (default 50)")
	c.fs.IntVar(&o.maxLines, "max-lines", 0, "maximum changed lines `N` (default 1500)")
	c.fs.StringVar(&o.judge, "judge", "", "cross-family LLM judge acceptance criterion `TEXT`")
	c.fs.StringVar(&o.judgeModel, "judge-model", "", "explicit judge model `ID` (with --judge)")
	c.fs.StringVar(&o.workerModel, "worker-model", "", "worker model `ID` attributed on judge verdict")
	c.fs.StringVar(&o.workerSession, "worker-session", "", "worker session `ID` attributed on judge verdict")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print report as JSON")
}

func registerLandFlags(c *runtimeCtx) {
	o := &gateOpts{}
	c.opts = o
	c.fs.StringVar(&o.landMessage, "message", "", "squash commit subject/body `TEXT`")
	c.fs.Var(&o.lease, "lease", "allowed path pattern `GLOB` (repeatable)")
	c.fs.StringVar(&o.leaseFile, "lease-file", "", "read lease patterns from `FILE`")
	c.fs.IntVar(&o.maxFiles, "max-files", 0, "maximum changed files `N` (default 50)")
	c.fs.IntVar(&o.maxLines, "max-lines", 0, "maximum changed lines `N` (default 1500)")
	c.fs.DurationVar(&o.timeout, "timeout", 0, "per-command verify timeout `D` (default 30m)")
	c.fs.StringVar(&o.judge, "judge", "", "cross-family LLM judge acceptance criterion `TEXT`")
	c.fs.StringVar(&o.judgeModel, "judge-model", "", "explicit judge model `ID` (with --judge)")
	c.fs.StringVar(&o.workerModel, "worker-model", "", "worker model `ID` attributed on judge verdict")
	c.fs.StringVar(&o.workerSession, "worker-session", "", "worker session `ID` attributed on judge verdict")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print report as JSON")
}

func registerCloseFlags(c *runtimeCtx) {
	o := &gateOpts{}
	c.opts = o
	c.fs.StringVar(&o.closeID, "id", "", "explicit handoff record `ID` (HANDOFF-<stamp>-<slug>)")
	c.fs.StringVar(&o.closeTitle, "title", "", "handoff record title `TEXT` (required)")
	c.fs.StringVar(&o.closeBodyFile, "body-file", "", "read handoff body from `FILE` instead of stdin")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print report as JSON")
}

func runVerify(c *runtimeCtx) error {
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	rep, err := gate.Verify(context.Background(), gate.VerifyOptions{
		Dir:     wd,
		Home:    c.home,
		Vocab:   vocabulary(),
		Timeout: gateOptsOf(c).timeout,
	})
	return emitGateOutcome(c, rep, len(rep.Steps) > 0 || err == nil, err, printVerifyText)
}

func runGate(c *runtimeCtx) error {
	if c.fs.NArg() != 1 {
		return usageErr(c.out, "gate", "fyr gate requires <worktree> or <branch>")
	}
	o := gateOptsOf(c)
	if strings.TrimSpace(o.judgeModel) != "" && strings.TrimSpace(o.judge) == "" {
		return usageErr(c.out, "gate", "fyr gate: --judge-model requires --judge")
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	rep, err := gate.Gate(context.Background(), gate.GateOptions{
		Dir:             wd,
		Target:          c.fs.Arg(0),
		Base:            o.base,
		Home:            c.home,
		RealHome:        realUserHome(),
		Lease:           o.lease,
		LeaseFile:       o.leaseFile,
		MaxFiles:        o.maxFiles,
		MaxLines:        o.maxLines,
		Judge:           o.judge,
		JudgeModel:      o.judgeModel,
		WorkerModel:     strings.TrimSpace(o.workerModel),
		WorkerSessionID: strings.TrimSpace(o.workerSession),
		RecordVerdict:   recordGateJudgeVerdict(c.home),
	})
	return emitGateOutcome(c, rep, rep.MainRoot != "", err, printGateText)
}

func runLand(c *runtimeCtx) error {
	if c.fs.NArg() != 1 {
		return usageErr(c.out, "land", "fyr land requires <branch>")
	}
	o := gateOptsOf(c)
	if strings.TrimSpace(o.judgeModel) != "" && strings.TrimSpace(o.judge) == "" {
		return usageErr(c.out, "land", "fyr land: --judge-model requires --judge")
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	rep, err := gate.Land(context.Background(), gate.LandOptions{
		Dir:             wd,
		Branch:          c.fs.Arg(0),
		Message:         o.landMessage,
		Home:            c.home,
		RealHome:        realUserHome(),
		Vocab:           vocabulary(),
		Timeout:         o.timeout,
		Lease:           o.lease,
		LeaseFile:       o.leaseFile,
		MaxFiles:        o.maxFiles,
		MaxLines:        o.maxLines,
		Judge:           o.judge,
		JudgeModel:      o.judgeModel,
		WorkerModel:     strings.TrimSpace(o.workerModel),
		WorkerSessionID: strings.TrimSpace(o.workerSession),
		RecordVerdict:   recordGateJudgeVerdict(c.home),
	})
	return emitGateOutcome(c, rep, rep.MainRoot != "", err, printLandText)
}

func realUserHome() string {
	h, _ := os.UserHomeDir()
	return strings.TrimSpace(h)
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

func runClose(c *runtimeCtx) error {
	o := gateOptsOf(c)
	if strings.TrimSpace(o.closeTitle) == "" {
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
		ID:    o.closeID,
		Title: o.closeTitle,
		Body:  bodyText,
	})
	if err != nil {
		return gateErrCode(c.cmd, err)
	}
	warnRedactable(o.closeID, o.closeTitle, bodyText)
	if o.jsonOut {
		return writeJSONLine(c.out, rep)
	}
	fmt.Fprintln(c.out, rep.ID)
	return nil
}

func readCloseBody(c *runtimeCtx) (string, error) {
	bodySet := false
	c.fs.Visit(func(f *flag.Flag) { bodySet = bodySet || f.Name == "body-file" })
	var (
		r       io.Reader
		cleanup = func() {}
		err     error
	)
	if !bodySet {
		var ok bool
		r, ok = peekCloseStdin(stdin)
		if !ok {
			return "", fmt.Errorf("%w: no body on stdin; pass --body-file <file> or pipe a handoff body on stdin", gate.ErrRefused)
		}
	} else {
		r, cleanup, err = openBodyReader(gateOptsOf(c).closeBodyFile, true)
		if err != nil {
			if brain.IsRefusal(err) || brain.IsUsage(err) {
				return "", fmt.Errorf("%w: %v", gate.ErrRefused, err)
			}
			return "", err
		}
	}
	defer cleanup()
	raw, err := io.ReadAll(io.LimitReader(r, brain.MaxBodyBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) > brain.MaxBodyBytes {
		return "", fmt.Errorf("%w: handoff body exceeds %d bytes", gate.ErrRefused, brain.MaxBodyBytes)
	}
	return string(raw), nil
}

// peekCloseStdin checks whether stdin carries immediate data without blocking
// on a TTY or idle open pipe and without discarding the first read byte.
func peekCloseStdin(r io.Reader) (io.Reader, bool) {
	if r == nil {
		return nil, false
	}
	if f, ok := r.(*os.File); ok {
		fi, err := f.Stat()
		if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
			return nil, false
		}
		if fi.Mode().IsRegular() {
			return peekOneByte(f)
		}
		return peekNonBlockPipe(f)
	}
	return peekOneByte(r)
}

func peekOneByte(r io.Reader) (io.Reader, bool) {
	var b [1]byte
	n, _ := r.Read(b[:])
	if n == 0 {
		return nil, false
	}
	return io.MultiReader(bytes.NewReader(b[:n]), r), true
}

const closePipeReadyWindow = 150 * time.Millisecond

func peekNonBlockPipe(f *os.File) (io.Reader, bool) {
	if err := f.SetReadDeadline(time.Now().Add(closePipeReadyWindow)); err == nil {
		defer f.SetReadDeadline(time.Time{})
		return peekOneByte(f)
	}
	return peekPipeSyscallConn(f)
}

func peekPipeSyscallConn(f *os.File) (io.Reader, bool) {
	raw, err := f.SyscallConn()
	if err != nil {
		return nil, false
	}
	var b [1]byte
	var got int
	var readErr error
	deadline := time.Now().Add(closePipeReadyWindow)
	cerr := raw.Control(func(fd uintptr) {
		for {
			rem := time.Until(deadline)
			if rem < 0 {
				rem = 0
			}
			if !waitPipeReady(fd, rem) {
				return
			}
			n, rerr := syscall.Read(int(fd), b[:])
			if errors.Is(rerr, syscall.EINTR) && time.Now().Before(deadline) {
				continue
			}
			if (errors.Is(rerr, syscall.EAGAIN) || errors.Is(rerr, syscall.EWOULDBLOCK)) && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			got, readErr = n, rerr
			return
		}
	})
	if cerr != nil || readErr != nil || got <= 0 {
		return nil, false
	}
	return io.MultiReader(bytes.NewReader(b[:got]), f), true
}

func emitGateOutcome[T any](c *runtimeCtx, rep T, hasReport bool, err error, printText func(io.Writer, T)) error {
	if err == nil || (hasReport && errors.Is(err, gate.ErrFailed)) {
		if gateOptsOf(c).jsonOut {
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
	printed := false
	if rep.Gate != nil && len(rep.Gate.Findings) > 0 {
		printGateText(w, *rep.Gate)
		printed = true
	}
	if rep.Verify != nil && len(rep.Verify.Steps) > 0 {
		printVerifyText(w, *rep.Verify)
		printed = true
	}
	if !printed && rep.Detail != "" {
		fmt.Fprintf(w, "land: FAIL %s\n", search.Printable(rep.Detail))
	}
}
