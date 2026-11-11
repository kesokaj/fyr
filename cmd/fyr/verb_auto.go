package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/kesokaj/fyr/internal/auto"
	"github.com/kesokaj/fyr/internal/harness"
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
	"on":     {"level": true, "project-idle": true, "user-idle": true},
	"off":    {},
	"status": {},
	"run":    {"dry-run": true},
}

// runAuto needs no harness tree: `off` has to work on any place, and only
// `on --level harness` looks for a checkout.
func runAuto(c *ctx) error {
	args := c.fs.Args()
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
	o := auto.Options{Home: c.home, Out: c.out}
	var err error
	switch sub {
	case "on":
		if reason := c.autoRequestError(); reason != "" {
			return usageErr(c.out, "auto", reason)
		}
		err = auto.On(o, auto.Request{Level: c.autoLevel, ProjectIdle: c.autoProjectIdle, UserIdle: c.autoUserIdle, CheckHarness: c.harnessCheckout})
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

func (c *ctx) autoRunOptions(o auto.Options) auto.Options {
	o.Env, o.Vocabulary = autoEnv, vocabulary()
	o.Version = buildVersion()
	if src, err := harness.ResolveSource(c.repoFlag, c.home, o.Version); err == nil {
		o.Repo = src.Dir
	}
	if self, err := os.Executable(); err == nil {
		o.Self = self
	}
	if real, err := os.UserHomeDir(); err == nil {
		o.RealHome = real
	}
	o.Install = func() []auto.Finding { return autoInstallFindings(c.repoFlag, c.home) }
	o.Metrics = func() ([]auto.Finding, error) {
		refreshModelsBestEffort(context.Background(), c.home)
		return autoMetricsFindings(c.home)
	}
	return o
}

// autoRequestError refuses a flag of `on` given empty or out of range: every
// check here comes before anything is read or written.
func (c *ctx) autoRequestError() string {
	checks := []struct {
		name, value string
		valid       func(string) error
	}{
		{"level", c.autoLevel, auto.ValidLevel},
		{"project-idle", c.autoProjectIdle, auto.ValidProjectIdle},
		{"user-idle", c.autoUserIdle, auto.ValidUserIdle},
	}
	given := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	for _, k := range checks {
		if !given[k.name] {
			continue
		}
		if err := k.valid(k.value); err != nil {
			return "fyr auto on: --" + k.name + ": " + err.Error()
		}
	}
	return ""
}

// harnessCheckout answers whether this place's harness is a checkout: level
// harness commits to one, and a release binary or a zip place has nothing to
// commit to.
func (c *ctx) harnessCheckout() error {
	src, err := harness.ResolveSource(c.repoFlag, c.home, buildVersion())
	if err != nil {
		return err
	}
	if src.Dir == "" {
		return errors.New("this place's harness is built into the binary")
	}
	if harness.Inspect(src.Dir).Commit == "" {
		return errors.New(src.Dir + " is a plain copy, not its own git checkout")
	}
	return nil
}
