package main

import (
	"flag"
	"fmt"
	"io"
	"time"

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
	workTimeout                                                                             time.Duration
	workModel                                                                               string
	workKeep                                                                                bool
	workMax                                                                                 int
	workKill                                                                                string
	limit, port                                                                             int
	register                                                                                bool
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
	readsRepo  bool         // reads --repo itself without source (eval resolves its install source after its own refusals)
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
		c.fs.StringVar(&c.memType, "type", "", "add: the memory's type `T`: user, feedback, project or reference")
		c.fs.StringVar(&c.memName, "name", "", "add: the memory's slug, `SLUG`")
		c.fs.StringVar(&c.memDesc, "description", "", "add: the memory in one line, `TEXT`")
	}, positional: true, run: runMemory},
	"dream": {flags: func(c *ctx) {
		c.fs.BoolVar(&c.dreamAll, "all", false, "every project registered with index --register")
		c.fs.StringVar(&c.dreamProject, "project", "", "one checkout, `DIR`, registered or not")
		c.fs.BoolVar(&c.installTimer, "install-timer", false, "install the hourly user timer")
		c.fs.BoolVar(&c.removeTimer, "remove-timer", false, "remove the dream's user timer")
		c.dryRunFlag()
	}, run: runDream},
	"metrics": {flags: func(c *ctx) {
		c.fs.StringVar(&c.metricsSource, "source", "", "ingest: the sandbox home `DIR` to read instead of this one")
		c.fs.StringVar(&c.metricsLabel, "label", "", "ingest: `KEY=VALUE` stamped on every --source session; show, report: only that label")
		c.fs.StringVar(&c.metricsOutcome, "outcome", "", "ingest: the --source run's acceptance result, `pass|fail`")
		c.fs.StringVar(&c.metricsSince, "since", "", "show, report: the window's start, `YYYY-MM-DD` (default 30 days ago)")
		c.fs.StringVar(&c.metricsUntil, "until", "", "show, report: the window's end, `YYYY-MM-DD`, exclusive (default now)")
		c.fs.StringVar(&c.metricsProject, "project", "", "show, report: only sessions in the project root `DIR`")
		c.fs.BoolVar(&c.metricsAllLabels, "all-labels", false, "show, report: include labelled (sandbox) sessions")
		c.fs.BoolVar(&c.metricsJSON, "json", false, "show: print the summary as JSON")
		c.fs.Int64Var(&c.metricsContextTarget, "context-target", 0, "show, report: the peak context, `N` tokens, a main session should stay under (default 300000)")
		c.fs.StringVar(&c.metricsOut, "out", "", "report: the HTML file `FILE` to write (default ~/.config/fyr/reports/<date-time>-metrics.html)")
	}, positional: true, run: runMetrics},
	"record": {flags: func(c *ctx) {
		c.fs.StringVar(&c.recTitle, "title", "", "add, edit: the record's one-line title, `T`")
		c.fs.StringVar(&c.recStatus, "status", "", "add, edit: the status `S`; each kind takes its own, and a status it does not take is refused with the kind's list")
		c.fs.StringVar(&c.recID, "id", "", "add: the `ID` to use instead of the next one")
		c.fs.StringVar(&c.recProject, "project", "", "add, list with --global: the project `ROOT` a global record is about")
		c.fs.BoolVar(&c.global, "global", false, "the global scope instead of this project's")
		c.fs.BoolVar(&c.recBody, "body", false, "edit: read the new body on stdin")
		c.fs.StringVar(&c.kind, "kind", "", "list: only records of kind `K`")
		c.fs.BoolVar(&c.openRecords, "open", false, "list: only open records (open plans and bugs, current specs, deploy docs and guides, queued proposals)")
		c.fs.BoolVar(&c.recAll, "all", false, "list: every scope, each line ending in its scope")
		c.dryRunFlag()
	}, positional: true, run: runRecord},
	"search": {flags: func(c *ctx) {
		c.fs.IntVar(&c.limit, "limit", 0, "print at most `N` records and as many files, 1-50 (default 10)")
		c.fs.StringVar(&c.kind, "kind", "", "only hits of kind `K`: bug adr plan change handoff review testing retro spec deploy trail code doc feedback proposal learning guide memory")
		c.fs.StringVar(&c.refs, "refs", "", "every record naming `REF`: an ID, a file, or a directory (trailing /)")
		c.fs.BoolVar(&c.openRecords, "open", false, "open bugs, plans with unticked boxes, and the other open records")
		c.fs.BoolVar(&c.global, "global", false, "also every project registered with index --register, and the global search index, ~/.config/fyr/search.db")
	}, positional: true, run: runSearch},
	"index": {flags: func(c *ctx) {
		c.fs.BoolVar(&c.global, "global", false, "the global search index, ~/.config/fyr/search.db, instead of this checkout's")
		c.fs.BoolVar(&c.rebuild, "rebuild", false, "delete the index and build it again")
		c.fs.BoolVar(&c.register, "register", false, "register this checkout (the operator's consent, ADR-068) so the cross-project reads take it (search --global, dream --all), then index it")
	}, run: runSearch},
	"install": {flags: (*ctx).dryRunFlag, source: true, run: runInstall},
	"sync":    {flags: (*ctx).dryRunFlag, source: true, run: runInstall},
	"status":  {source: true, run: func(c *ctx) error { return status(c.src, c.home, c.out) }},
	"diff":    {source: true, run: runDiff},
	"update": {flags: func(c *ctx) {
		c.fs.BoolVar(&c.check, "check", false, "only report whether this place is behind; exit 2 when it is")
		c.fs.StringVar(&c.fromZip, "from-zip", "", "install the harness in the repository zip `FILE` with the running binary")
		c.dryRunFlag()
	}, source: true, run: runUpdate},
	"list":   {source: true, run: runList},
	"render": {source: true, run: runRender},
	"adopt": {flags: func(c *ctx) {
		c.fs.BoolVar(&c.force, "force", false, "replace a skill of the same name already in skills/")
	}, source: true, positional: true, run: runAdopt},
}

// newCtx is a verb's ctx with its FlagSet: the two global flags and the
// verb's own. run parses it; the check vocabulary reads it (plan fyr-check
// D14), so the vocabulary is the flags the verb really takes.
func newCtx(cmd string, v verb, out io.Writer) *ctx {
	c := &ctx{cmd: cmd, out: out, fs: flag.NewFlagSet(cmd, flag.ContinueOnError)}
	c.fs.SetOutput(out)
	// The verb's page is help's to print (parse); the flag package prints
	// only its one reason line for a flag error (plan ledger D21 3).
	c.fs.Usage = func() {}
	c.fs.StringVar(&c.repoFlag, "repo", "", "the harness checkout `DIR`")
	c.fs.StringVar(&c.homeFlag, "home", "", "install under `DIR` instead of $HOME")
	if v.flags != nil {
		v.flags(c)
	}
	return c
}
