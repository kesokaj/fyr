package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kesokaj/fyr/internal/auto"
	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/trail"
)

// autoOpts holds parsed flags for `fyr auto`.
type autoOpts struct {
	convIdle, level, projectIdle, userIdle string
	lines, limit                           int
	all, dryRun, force, jsonOut            bool
}

func autoOptsOf(c *runtimeCtx) *autoOpts {
	if o, ok := c.opts.(*autoOpts); ok {
		return o
	}
	o := &autoOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func autoVerb() verb {
	return verb{flags: registerAutoFlags, writesState: autoWritesState, positional: true, readsRepo: true, run: runAuto}
}

func autoWritesState(c *runtimeCtx) bool {
	if len(c.args) >= 1 && c.args[0] == "sim" {
		return len(c.args) >= 2 && (c.args[1] == "record" || c.args[1] == "merge")
	}
	// status and any --dry-run read; on, off and real run write auto.json or auto/.
	sub := c.fs.Arg(0)
	return !autoOptsOf(c).dryRun && (sub == "on" || sub == "off" || sub == "run")
}

func registerAutoFlags(c *runtimeCtx) {
	o := &autoOpts{}
	c.opts = o
	c.fs.StringVar(&o.convIdle, "conversation-idle", "", "on: how long a conversation sits untouched before rating, `D`, 5m to 7d (default 1h)")
	c.fs.StringVar(&o.level, "level", "", "on: deprecated no-op `L` (fyr auto is now binary on/off)")
	c.fs.StringVar(&o.projectIdle, "project-idle", "", "on: deprecated no-op `D` (use --conversation-idle)")
	c.fs.StringVar(&o.userIdle, "user-idle", "", "on: deprecated no-op `D` (use --conversation-idle)")
	c.fs.IntVar(&o.limit, "limit", 0, "log: alias for --lines, print the last `N` lines of auto.log")
	c.fs.IntVar(&o.lines, "lines", 0, "log: print the last `N` lines of auto.log (default 50)")
	c.fs.BoolVar(&o.all, "all", false, "log: print every line of auto.log")
	c.fs.BoolVar(&o.force, "force", false, "run: bypass the 4h model-run cooldown")
	c.fs.BoolVar(&o.dryRun, "dry-run", false, "print what would change, change nothing")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print the result as JSON")
}

// autoEnv is the machine a run reads; a test replaces it, and nil is the real
// one.
var autoEnv auto.Env

// autoSubverbs are `fyr auto`'s subcommands, each with the flags it reads:
// a flag another one owns is a usage error, never one parsed and dropped.
var autoSubverbs = []string{"on", "off", "status", "run", "log", "sim"}

var autoFlags = map[string]map[string]bool{
	"on":     {"conversation-idle": true, "level": true, "project-idle": true, "user-idle": true, "dry-run": true, "json": true},
	"off":    {"dry-run": true, "json": true},
	"status": {"json": true},
	"run":    {"dry-run": true, "force": true, "json": true},
	"log":    {"lines": true, "limit": true, "all": true, "json": true},
	"sim": {
		"signal": true, "path": true, "status": true, "limit": true, "json": true, "json-in": true,
		"problem": true, "root-cause": true, "mechanism": true, "what": true, "files": true,
		"proposal": true, "commit": true, "parent": true, "harness": true, "proposer-model": true,
		"policy-model": true, "source": true, "verdict": true, "field-status": true,
		"predicted-rows": true, "predicted-signals": true, "fixed-rows": true,
		"missed-rows": true, "regressed-rows": true, "provenance": true,
	},
}

// runAuto needs no harness tree: `off` and `on` work on any place, and
// harness skill edits apply only when a clean checkout is present.
func runAuto(c *runtimeCtx) error {
	if len(c.args) > 0 && c.args[0] == "sim" {
		return runAutoSIM(c, c.args[1:])
	}
	ao := autoOptsOf(c)
	sub, err := parseAutoSub(c, ao)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	out := c.out
	if ao.jsonOut {
		out = &buf
	}
	err = c.dispatchAutoSub(sub, auto.Options{Home: c.home, Out: out, DryRun: ao.dryRun, Force: ao.force})
	return finishAutoRun(c, ao, sub, &buf, err)
}

func parseAutoSub(c *runtimeCtx, ao *autoOpts) (string, error) {
	args := c.fs.Args()
	if len(args) == 0 && ao.dryRun {
		args = []string{"run"}
	}
	if len(args) != 1 {
		return "", usageErr(c.out, "auto", "")
	}
	sub := args[0]
	own, ok := autoFlags[sub]
	if !ok {
		return "", usageErr(c.out, "auto", fmt.Sprintf("fyr auto: unknown subcommand %q", sub))
	}
	var stray string
	c.fs.Visit(func(f *flag.Flag) {
		if !isGlobalFlag(f.Name) && !own[f.Name] {
			stray = f.Name
		}
	})
	if stray != "" {
		return "", usageErr(c.out, "auto", fmt.Sprintf("fyr auto %s takes no --%s", sub, stray))
	}
	return sub, nil
}

func finishAutoRun(c *runtimeCtx, ao *autoOpts, sub string, buf *bytes.Buffer, err error) error {
	var ee *auto.ExitError
	if errors.As(err, &ee) {
		if ao.jsonOut {
			if jerr := writeVerbExitErrorJSON(c.out, "auto", ee.Msg, buf.String()); jerr != nil {
				return jerr
			}
			return exitCode(ee.Code)
		}
		if ee.Msg != "" {
			fmt.Fprintln(c.out, ee.Msg)
		}
		return exitCode(ee.Code)
	}
	if err == nil && ao.jsonOut {
		return writeAutoJSON(c, sub, buf.String())
	}
	return err
}

func (c *runtimeCtx) dispatchAutoSub(sub string, o auto.Options) error {
	o.Ports = autoPorts(vocabulary())
	ao := autoOptsOf(c)
	switch sub {
	case "on":
		if reason := c.autoRequestError(); reason != "" {
			return usageErr(c.out, "auto", reason)
		}
		c.warnDeprecatedAutoFlags(o.Out)
		return auto.On(o, auto.Request{ConversationIdle: ao.convIdle})
	case "off":
		return auto.Off(o)
	case "status":
		o.Repo = c.resolveAutoRepo()
		return auto.Status(o)
	case "run":
		return auto.Run(c.autoRunOptions(o), ao.dryRun)
	case "log":
		if reason := c.autoLogError(); reason != "" {
			return usageErr(c.out, "auto", reason)
		}
		lines := ao.lines
		if ao.limit > 0 {
			lines = ao.limit
		}
		return auto.Log(o, lines, ao.all)
	}
	return nil
}

func (c *runtimeCtx) autoLogError() string {
	ao := autoOptsOf(c)
	var linesGiven, limitGiven bool
	c.fs.Visit(func(f *flag.Flag) {
		linesGiven = linesGiven || f.Name == "lines"
		limitGiven = limitGiven || f.Name == "limit"
	})
	if linesGiven && limitGiven {
		return "fyr auto log: --lines and --limit exclude each other"
	}
	if ao.lines < 0 || (linesGiven && ao.lines <= 0) {
		return "fyr auto log: --lines must be above 0"
	}
	if ao.limit < 0 || (limitGiven && ao.limit <= 0) {
		return "fyr auto log: --limit must be above 0"
	}
	if linesGiven && ao.all {
		return "fyr auto log: --lines and --all exclude each other"
	}
	if limitGiven && ao.all {
		return "fyr auto log: --limit and --all exclude each other"
	}
	return ""
}

func autoIngest(home string) (string, error) {
	r, err := ingestInto(metrics.Options{Home: home}, io.Discard)
	if err != nil {
		return "", err
	}
	if len(r.Failed) > 0 {
		err = fmt.Errorf("%d source(s) failed", len(r.Failed))
	}
	return r.Line(), err
}

func (c *runtimeCtx) autoRunOptions(o auto.Options) auto.Options {
	home := c.home
	o.Env, o.Ports = autoEnv, autoPorts(vocabulary())
	o.Repo = c.resolveAutoRepo()
	if self, err := os.Executable(); err == nil {
		o.Self = self
	}
	if real, err := os.UserHomeDir(); err == nil {
		o.RealHome = real
	}
	recordBg := func(row metrics.BackgroundRunRow) error {
		if o.DryRun {
			return nil
		}
		return metrics.RecordBackgroundRun(home, row)
	}
	o.RecordBackgroundRun = recordBg
	var ingested bool
	var ingestErr error
	o.Ingest = func() (string, error) {
		line, err := autoIngest(home)
		ingested, ingestErr = true, err
		return line, err
	}
	o.Dream = autoDreamCallback(home, o.Repo, recordBg)
	nowFn := o.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	o.Report = func() error { return autoWriteDailyReport(home, o.Repo, nowFn()) }
	o.Install = func() []auto.Finding { return autoInstallFindings(c.repoFlag, home) }
	o.Metrics = func() ([]auto.Finding, error) {
		refreshModelsBestEffort(context.Background(), home)
		if !ingested {
			return autoMetricsFindings(home)
		}
		fs, err := autoMetricsEvalFindings(home)
		return fs, errors.Join(ingestErr, err)
	}
	return o
}

func autoDreamCallback(home, repo string, recordBg func(metrics.BackgroundRunRow) error) func() error {
	return func() error {
		return dream.Run(dream.Options{
			Home:                home,
			Out:                 io.Discard,
			All:                 true,
			SkipIngest:          true,
			ConversationIdle:    auto.ConversationIdleDuration(home),
			HarnessRepo:         repo,
			Session:             func() (bool, error) { return true, nil },
			RecordBackgroundRun: recordBg,
		})
	}
}

func autoWriteDailyReport(home, harnessRepo string, now time.Time) error {
	dbPath := metrics.Path(home)
	if _, err := os.Lstat(dbPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	s, err := metrics.Open(dbPath)
	if err != nil {
		return err
	}
	defer s.Close()
	digest, err := refreshDailyDigest(s, home, harnessRepo, now)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(metrics.DailyReportPath(home, now)); err == nil {
		return nil
	}
	showOpts := metrics.ShowOptions{Home: home, Now: now}
	sum, err := s.Show(showOpts)
	if err != nil {
		return err
	}
	evals, err := metrics.ReportEvalsWithDevFallback(s, home, showOpts)
	if err != nil {
		return err
	}
	sum.DailyDigest = &digest
	return metrics.WriteDailyReport(sum, evals, home, now)
}

func refreshDailyDigest(s *metrics.Store, home, harnessRepo string, now time.Time) (metrics.DailyDigest, error) {
	digest, err := metrics.BuildDailyDigest(s, home, "", now)
	if err != nil {
		return metrics.DailyDigest{}, err
	}
	if err := metrics.SaveDailyDigest(s, digest); err != nil {
		return metrics.DailyDigest{}, err
	}
	if err := metrics.WriteLatestDailyDigest(home, digest); err != nil {
		return metrics.DailyDigest{}, err
	}
	_, _ = auto.SyncSIMFieldOutcomes(home, harnessRepo, &digest, s, now)
	return digest, nil
}

// autoRequestError refuses a flag of `on` given empty or out of range before
// anything is read or written.
func (c *runtimeCtx) autoRequestError() string {
	given := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if given["conversation-idle"] {
		if err := auto.ValidConversationIdle(autoOptsOf(c).convIdle); err != nil {
			return "fyr auto on: --conversation-idle: " + err.Error()
		}
	}
	return ""
}

func (c *runtimeCtx) warnDeprecatedAutoFlags(out io.Writer) {
	c.fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "level", "project-idle", "user-idle":
			fmt.Fprintf(out, "auto on: warning: --%s is deprecated and ignored (fyr auto is now binary on/off; use --conversation-idle)\n", f.Name)
		}
	})
}

func (c *runtimeCtx) resolveAutoRepo() string {
	explicit := c.repoFlag
	if explicit == "" && os.Getenv("FYR_REPO") == "" && !harness.IsReleaseVersion(buildVersion()) {
		st, found, err := harness.LoadState(c.home)
		if err != nil || !found || st.Origin().Kind != harness.OriginPath || st.Repo == "" {
			return ""
		}
		explicit = st.Repo
	}
	src, err := harness.ResolveSource(explicit, c.home, buildVersion())
	if err != nil {
		return ""
	}
	return autoDreamHarnessRepo(c.repoFlag, c.home, src.Dir)
}

// autoDreamHarnessRepo restricts dream.Options.HarnessRepo to an explicit
// --repo / FYR_REPO or a resolved path matching the installed OriginPath
// checkout in state.json, preventing CWD's repo from leaking into scratch
// runs or untrusted CWD clones.
func autoDreamHarnessRepo(repoFlag, home, resolvedRepo string) string {
	if resolvedRepo == "" {
		return ""
	}
	if repoFlag != "" || os.Getenv("FYR_REPO") != "" {
		return resolvedRepo
	}
	if st, found, err := harness.LoadState(home); err == nil && found && st.Origin().Kind == harness.OriginPath && st.Repo != "" && trail.SameDir(resolvedRepo, st.Repo) {
		return resolvedRepo
	}
	return ""
}
