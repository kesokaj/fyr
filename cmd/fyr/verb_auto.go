package main

import (
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

// auto's run is registered here, not in the verbs literal: a run's check reads
// the vocabulary, which reads verbs, and the literal would refer to itself.
func init() {
	v := verbs["auto"]
	v.run = runAuto
	verbs["auto"] = v
}

// autoEnv is the machine a run reads; a test replaces it, and nil is the real
// one.
var autoEnv auto.Env

// autoSubverbs are `fyr auto`'s subcommands, each with the flags it reads:
// a flag another one owns is a usage error, never one parsed and dropped.
var autoSubverbs = []string{"on", "off", "status", "run", "log", "sim"}

var autoFlags = map[string]map[string]bool{
	"on":     {"conversation-idle": true, "level": true, "project-idle": true, "user-idle": true, "dry-run": true},
	"off":    {"dry-run": true},
	"status": {},
	"run":    {"dry-run": true, "force": true},
	"log":    {"lines": true, "all": true},
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
func runAuto(c *ctx) error {
	if len(c.args) > 0 && c.args[0] == "sim" {
		return runAutoSIM(c, c.args[1:])
	}
	args := c.fs.Args()
	if len(args) == 0 && c.dryRun {
		args = []string{"run"}
	}
	if len(args) != 1 {
		return usageErr(c.out, "auto", "")
	}
	sub := args[0]
	own, ok := autoFlags[sub]
	if !ok {
		return usageErr(c.out, "auto", fmt.Sprintf("fyr auto: unknown subcommand %q", sub))
	}
	var stray string
	c.fs.Visit(func(f *flag.Flag) {
		if !isGlobalFlag(f.Name) && !own[f.Name] {
			stray = f.Name
		}
	})
	if stray != "" {
		return usageErr(c.out, "auto", fmt.Sprintf("fyr auto %s takes no --%s", sub, stray))
	}
	err := c.dispatchAutoSub(sub, auto.Options{Home: c.home, Out: c.out, DryRun: c.dryRun, Force: c.force})
	var ee *auto.ExitError
	if errors.As(err, &ee) {
		if ee.Msg != "" {
			fmt.Fprintln(c.out, ee.Msg)
		}
		return exitCode(ee.Code)
	}
	return err
}

func (c *ctx) dispatchAutoSub(sub string, o auto.Options) error {
	switch sub {
	case "on":
		if reason := c.autoRequestError(); reason != "" {
			return usageErr(c.out, "auto", reason)
		}
		c.warnDeprecatedAutoFlags()
		return auto.On(o, auto.Request{ConversationIdle: c.autoConvIdle})
	case "off":
		return auto.Off(o)
	case "status":
		return auto.Status(o)
	case "run":
		return auto.Run(c.autoRunOptions(o), c.dryRun)
	case "log":
		if reason := c.autoLogError(); reason != "" {
			return usageErr(c.out, "auto", reason)
		}
		return auto.Log(o, c.autoLines, c.autoAll)
	}
	return nil
}

func (c *ctx) autoLogError() string {
	linesGiven := false
	c.fs.Visit(func(f *flag.Flag) { linesGiven = linesGiven || f.Name == "lines" })
	if c.autoLines < 0 || (linesGiven && c.autoLines <= 0) {
		return "fyr auto log: --lines must be above 0"
	}
	if linesGiven && c.autoAll {
		return "fyr auto log: --lines and --all exclude each other"
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

func (c *ctx) autoRunOptions(o auto.Options) auto.Options {
	home := c.home
	o.Env, o.Vocabulary = autoEnv, vocabulary()
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
func (c *ctx) autoRequestError() string {
	given := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if given["conversation-idle"] {
		if err := auto.ValidConversationIdle(c.autoConvIdle); err != nil {
			return "fyr auto on: --conversation-idle: " + err.Error()
		}
	}
	return ""
}

func (c *ctx) warnDeprecatedAutoFlags() {
	c.fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "level", "project-idle", "user-idle":
			fmt.Fprintf(c.out, "auto on: warning: --%s is deprecated and ignored (fyr auto is now binary on/off; use --conversation-idle)\n", f.Name)
		}
	})
}

func (c *ctx) resolveAutoRepo() string {
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
