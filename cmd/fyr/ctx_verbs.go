package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/kesokaj/fyr/internal/harness"
)

// exitCode carries a non-error exit status such as "drift found".
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

// ctx is what one verb runs with: its FlagSet (the global flags plus the
// verb's own, and nothing else), the parsed values, the home, the writer, and
// for the verbs that read the harness, the resolved source.
type ctx struct {
	cmd  string
	fs   *flag.FlagSet
	out  io.Writer
	home string
	// global flags, accepted by every verb
	repoFlag, homeFlag string
	// per-verb flags; a verb's flags func registers only the ones it reads
	dryRun, check, force, openRecords, global, rebuild, dreamAll, installTimer, removeTimer bool
	fromZip, kind, refs, memType, memName, memDesc, dreamProject                            string
	metricsSource, metricsLabel, metricsOutcome, metricsSince, metricsProject, metricsOut   string
	metricsUntil                                                                            string
	metricsContextTarget                                                                    int64
	metricsAllLabels, metricsJSON                                                           bool
	recTitle, recStatus, recID, recProject                                                  string
	recBody, recAll                                                                         bool
	evalFixture                                                                             string
	evalList                                                                                bool
	limit                                                                                   int
	// src is the harness, resolved before run for the verbs that mark source
	src harness.Source
}

// repo is the tree on disk, "" for the files embedded in a release binary;
// no git helper is ever called with "" (D17).
func (c *ctx) repo() string { return c.src.Dir }

func (c *ctx) dryRunFlag() {
	c.fs.BoolVar(&c.dryRun, "dry-run", false, "print what would change, change nothing")
}

// verb is one command: the flags it declares, whether it reads the harness
// tree, and what it does.
type verb struct {
	flags      func(c *ctx) // registers the verb's own flags; nil for none
	source     bool         // resolve the harness source before run
	positional bool         // takes positional arguments (search words, adopt's name, memory's subverb); every other verb refuses a stray one
	run        func(c *ctx) error
}

// verbs is the command table. A verb's FlagSet holds the two global flags
// and the ones its flags func registers, so `list --check` is a usage error
// instead of a flag parsed and silently ignored (IN5). Adding a verb here is
// a map change under the project's README map rule (D9: verbs unchanged).
var verbs = map[string]verb{
	"version":   {run: runVersion},
	"uninstall": {flags: (*ctx).dryRunFlag, run: runUninstall},
	"memory": {flags: func(c *ctx) {
		c.fs.StringVar(&c.memType, "type", "", "memory add: user, feedback, project or reference")
		c.fs.StringVar(&c.memName, "name", "", "memory add: the memory's slug")
		c.fs.StringVar(&c.memDesc, "description", "", "memory add: one line")
	}, positional: true, run: runMemory},
	"dream": {flags: func(c *ctx) {
		c.fs.BoolVar(&c.dreamAll, "all", false, "dream: every project in the global search index")
		c.fs.StringVar(&c.dreamProject, "project", "", "dream: one checkout")
		c.fs.BoolVar(&c.installTimer, "install-timer", false, "dream: install the nightly user timer")
		c.fs.BoolVar(&c.removeTimer, "remove-timer", false, "dream: remove the nightly user timer")
		c.dryRunFlag()
	}, run: runDream},
	"metrics": {flags: func(c *ctx) {
		c.fs.StringVar(&c.metricsSource, "source", "", "metrics ingest: a sandbox home to read instead of this one")
		c.fs.StringVar(&c.metricsLabel, "label", "", "metrics ingest: KEY=VALUE stamped on every --source session; show: only that label")
		c.fs.StringVar(&c.metricsOutcome, "outcome", "", "metrics ingest: pass or fail, the --source run's acceptance result")
		c.fs.StringVar(&c.metricsSince, "since", "", "metrics show: start of the window, YYYY-MM-DD (default 30 days ago)")
		c.fs.StringVar(&c.metricsUntil, "until", "", "metrics show: end of the window, YYYY-MM-DD, exclusive (default now)")
		c.fs.StringVar(&c.metricsProject, "project", "", "metrics show: only sessions in this project root")
		c.fs.BoolVar(&c.metricsAllLabels, "all-labels", false, "metrics show: include labelled (sandbox) sessions")
		c.fs.BoolVar(&c.metricsJSON, "json", false, "metrics show: print the summary as JSON")
		c.fs.Int64Var(&c.metricsContextTarget, "context-target", 0, "metrics show, report: the peak context, in tokens, a main session should stay under (default 300000)")
		c.fs.StringVar(&c.metricsOut, "out", "", "metrics report: the HTML file to write (default ~/.config/fyr/reports/<date-time>-metrics.html)")
	}, positional: true, run: runMetrics},
	"record": {flags: func(c *ctx) {
		c.fs.StringVar(&c.recTitle, "title", "", "record add, edit: the record's one-line title")
		c.fs.StringVar(&c.recStatus, "status", "", "record add, edit: the status (per kind; see fyr help)")
		c.fs.StringVar(&c.recID, "id", "", "record add: the ID to use instead of the next one")
		c.fs.StringVar(&c.recProject, "project", "", "record add, list with --global: the project a global record is about")
		c.fs.BoolVar(&c.global, "global", false, "record: the global scope")
		c.fs.BoolVar(&c.recBody, "body", false, "record edit: read the new body on stdin")
		c.fs.StringVar(&c.kind, "kind", "", "record list: only records of this kind")
		c.fs.BoolVar(&c.openRecords, "open", false, "record list: only open records")
		c.fs.BoolVar(&c.recAll, "all", false, "record list: every scope")
		c.dryRunFlag()
	}, positional: true, run: runRecord},
	"search": {flags: func(c *ctx) {
		c.fs.IntVar(&c.limit, "limit", 0, "search: hits to print, 1-50 (default 10)")
		c.fs.StringVar(&c.kind, "kind", "", "search: only hits of this kind")
		c.fs.StringVar(&c.refs, "refs", "", "search: records naming this ID or path")
		c.fs.BoolVar(&c.openRecords, "open", false, "search: open bugs and plans with unticked boxes")
		c.fs.BoolVar(&c.global, "global", false, "search, index: the global search index")
	}, positional: true, run: runSearch},
	"index": {flags: func(c *ctx) {
		c.fs.BoolVar(&c.global, "global", false, "search, index: the global search index")
		c.fs.BoolVar(&c.rebuild, "rebuild", false, "index: delete and rebuild the index")
	}, run: runSearch},
	"install": {flags: (*ctx).dryRunFlag, source: true, run: runInstall},
	"sync":    {flags: (*ctx).dryRunFlag, source: true, run: runInstall},
	"status":  {source: true, run: func(c *ctx) error { return status(c.src, c.home, c.out) }},
	"diff":    {source: true, run: runDiff},
	"update": {flags: func(c *ctx) {
		c.fs.BoolVar(&c.check, "check", false, "update: only report whether the repo is behind")
		c.fs.StringVar(&c.fromZip, "from-zip", "", "update: install the harness in this repository zip with the running binary")
		c.dryRunFlag()
	}, source: true, run: runUpdate},
	"list":   {source: true, run: runList},
	"render": {source: true, run: runRender},
	"adopt": {flags: func(c *ctx) {
		c.fs.BoolVar(&c.force, "force", false, "adopt: replace an existing skill")
	}, source: true, positional: true, run: runAdopt},
}

// newCtx is a verb's ctx with its FlagSet: the two global flags and the
// verb's own. run parses it; the check vocabulary reads it (plan fyr-check
// D14), so the vocabulary is the flags the verb really takes.
func newCtx(cmd string, v verb, out io.Writer) *ctx {
	c := &ctx{cmd: cmd, out: out, fs: flag.NewFlagSet(cmd, flag.ContinueOnError)}
	c.fs.SetOutput(out)
	c.fs.StringVar(&c.repoFlag, "repo", "", "harness checkout")
	c.fs.StringVar(&c.homeFlag, "home", "", "install under this directory instead of $HOME")
	if v.flags != nil {
		v.flags(c)
	}
	return c
}
