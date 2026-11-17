package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kesokaj/fyr/internal/auto"
	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
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
var autoSubverbs = []string{"on", "off", "status", "run"}

var autoFlags = map[string]map[string]bool{
	"on":     {"conversation-idle": true, "level": true, "project-idle": true, "user-idle": true, "dry-run": true},
	"off":    {"dry-run": true},
	"status": {},
	"run":    {"dry-run": true},
}

// runAuto needs no harness tree: `off` and `on` work on any place, and
// harness skill edits apply only when a clean checkout is present.
func runAuto(c *ctx) error {
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
	o := auto.Options{Home: c.home, Out: c.out, DryRun: c.dryRun}
	var err error
	switch sub {
	case "on":
		if reason := c.autoRequestError(); reason != "" {
			return usageErr(c.out, "auto", reason)
		}
		c.warnDeprecatedAutoFlags()
		err = auto.On(o, auto.Request{ConversationIdle: c.autoConvIdle})
	case "off":
		err = auto.Off(o)
	case "status":
		err = auto.Status(o)
	case "run":
		err = auto.Run(c.autoRunOptions(o), c.dryRun)
	}
	var ee *auto.ExitError
	if errors.As(err, &ee) {
		if ee.Msg != "" {
			fmt.Fprintln(c.out, ee.Msg)
		}
		return exitCode(ee.Code)
	}
	return err
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
	o.Env, o.Vocabulary = autoEnv, vocabulary()
	if src, err := harness.ResolveSource(c.repoFlag, c.home, buildVersion()); err == nil {
		o.Repo = autoDreamHarnessRepo(c.repoFlag, c.home, src.Dir)
	}
	if self, err := os.Executable(); err == nil {
		o.Self = self
	}
	if real, err := os.UserHomeDir(); err == nil {
		o.RealHome = real
	}
	var ingested bool
	var ingestErr error
	o.Ingest = func() (string, error) {
		line, err := autoIngest(c.home)
		ingested, ingestErr = true, err
		return line, err
	}
	o.Dream = func() error {
		return dream.Run(dream.Options{
			Home:             c.home,
			Out:              io.Discard,
			All:              true,
			SkipIngest:       true,
			ConversationIdle: auto.ConversationIdleDuration(c.home),
			HarnessRepo:      o.Repo,
			Session:          func() (bool, error) { return true, nil },
		})
	}
	o.Install = func() []auto.Finding { return autoInstallFindings(c.repoFlag, c.home) }
	o.Metrics = func() ([]auto.Finding, error) {
		refreshModelsBestEffort(context.Background(), c.home)
		if !ingested {
			return autoMetricsFindings(c.home)
		}
		fs, err := autoMetricsEvalFindings(c.home)
		return fs, errors.Join(ingestErr, err)
	}
	return o
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

// autoDreamHarnessRepo restricts dream.Options.HarnessRepo to an explicit
// --repo / FYR_REPO or an installed checkout origin, preventing CWD's repo
// from leaking into isolated scratch --home runs or tests under cmd/fyr.
func autoDreamHarnessRepo(repoFlag, home, resolvedRepo string) string {
	if resolvedRepo == "" {
		return ""
	}
	if repoFlag != "" || os.Getenv("FYR_REPO") != "" {
		return resolvedRepo
	}
	if st, found, err := harness.LoadState(home); err == nil && found && st.Origin().Kind == harness.OriginPath {
		return resolvedRepo
	}
	return ""
}
