package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
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
	args []string
	out  io.Writer
	home string
	// global flags, accepted by every verb
	repoFlag, homeFlag, dirFlag string
	// per-verb flags; a verb's flags func registers only the ones it reads
	dryRun, check, force, openRecords, global, rebuild, dreamAll                                      bool
	jsonOut, jsonIn                                                                                   bool
	fromZip, kind, refs, memType, memName, memDesc, memBodyFile, dreamProject                         string
	memIncludeLow                                                                                     bool
	metricsSource, metricsLabel, metricsOutcome, metricsSince, metricsProject, metricsOut             string
	metricsUntil, metricsSinceTag, metricsHarness, metricsListen                                      string
	metricsContextTarget                                                                              int64
	metricsAllLabels, metricsJSON, metricsMD, metricsWeb, metricsDailyDigest, metricsIncludeSandboxes bool
	noIngest                                                                                          bool
	recTitle, recTitleFile, recBodyFile, recOut, recStatus, recID, recProject                         string
	recBody, recAll                                                                                   bool
	evalFixture                                                                                       string
	evalRunner                                                                                        string
	evalModel                                                                                         string
	evalList, evalACP                                                                                 bool
	evalRuns, evalParallel                                                                            int
	autoConvIdle, autoLevel, autoProjectIdle, autoUserIdle                                            string
	autoLines                                                                                         int
	autoAll                                                                                           bool
	modelsPick, modelsTier, modelsExplain                                                             string
	modelsRefresh                                                                                     bool
	limit, sliceHops, sliceBudget                                                                     int
	sliceQuery, sliceSession, contractsTarget, skeletonPath, pathFrom, pathTo, affectedTarget         string
	vetPrompt, hookEvent                                                                              string
	repoMap, register, searchIncludeDocs                                                              bool
	graphOut, graphListen                                                                             string
	graphDot, graphMD, graphJSON, graphWeb, graphIncludeTests, graphIncludeDocs                       bool
	graphIncludeVendored                                                                              bool
	gateBase, gateLeaseFile, landMessage, closeID, closeTitle, closeBodyFile                          string
	gateLease                                                                                         stringSliceFlag
	gateMaxFiles, gateMaxLines                                                                        int
	gateTimeout                                                                                       time.Duration
	gateJudge, gateJudgeModel, gateWorkerModel, gateWorkerSession                                     string
	watchConv                                                                                         string
	watchIdleSec                                                                                      int
	// src is the harness, resolved before run for the verbs that mark source
	src harness.Source
}

// repo is the tree on disk, "" for the files embedded in a release binary;
// no git helper is ever called with "" (D17).
func (c *ctx) repo() string { return c.src.Dir }

// projectDir is --dir / -C when given (made absolute), else the working directory.
func (c *ctx) projectDir() (string, error) {
	if c.dirFlag != "" {
		return filepath.Abs(c.dirFlag)
	}
	return os.Getwd()
}

func isGlobalFlag(name string) bool {
	return name == "home" || name == "repo" || name == "dir" || name == "C"
}

func (c *ctx) dryRunFlag() {
	c.fs.BoolVar(&c.dryRun, "dry-run", false, "print what would change, change nothing")
}

func (c *ctx) jsonFlag() {
	c.fs.BoolVar(&c.jsonOut, "json", false, "print the result as JSON")
}

func (c *ctx) dryRunAndJSONFlags() {
	c.dryRunFlag()
	c.jsonFlag()
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

// allVerbs lazily constructs the command table once on first use without any
// init() functions, avoiding initialization cycles with runCheck/vocabulary. A
// verb's FlagSet holds the global flags and the ones its flags func registers,
// so `list --check` is a usage error instead of a flag parsed and silently
// ignored (IN5). Adding a verb here is a map change under the project's README
// map rule (D9: verbs unchanged).
var (
	verbsOnce   sync.Once
	cachedVerbs map[string]verb
)

func allVerbs() map[string]verb {
	verbsOnce.Do(func() {
		cachedVerbs = buildVerbs()
	})
	return cachedVerbs
}

func buildVerbs() map[string]verb {
	m := map[string]verb{
		"version":   {flags: (*ctx).jsonFlag, run: runVersion},
		"uninstall": {flags: (*ctx).dryRunAndJSONFlags, run: runUninstall},
		"resume":    {flags: (*ctx).jsonFlag, run: runResume},
		"git-check": {flags: (*ctx).jsonFlag, run: runGitCheck},
		"check":     checkVerb(),
		"memory":    memoryVerb(),
		"dream":     {flags: registerDreamFlags, run: runDream},
		"auto":      autoVerb(),
		"metrics":   {flags: registerMetricsFlags, positional: true, run: runMetrics},
		"report":    {flags: func(c *ctx) { c.registerReportFlags(false) }, run: runReport},
		"record":    {flags: registerRecordFlags, positional: true, run: runRecord},
		"search":    {flags: registerSearchFlags, positional: true, run: runSearch},
		"graph":     {flags: registerGraphFlags, run: runGraph},
		"index":     {flags: registerIndexFlags, run: runSearch},
		"eval":      evalVerb(),
		"models":    modelsVerb(),
		"state":     stateVerb(),
		"watch":     watchVerb(),
		"install":   {flags: (*ctx).dryRunAndJSONFlags, source: true, run: runInstall},
		"sync":      {flags: (*ctx).dryRunAndJSONFlags, source: true, run: runInstall},
		"status":    {flags: registerStatusFlags, source: true, run: runStatusVerb},
		"diff":      {flags: (*ctx).jsonFlag, source: true, run: runDiff},
		"update":    {flags: registerUpdateFlags, source: true, run: runUpdate},
		"list":      {flags: (*ctx).jsonFlag, source: true, run: runList},
		"render":    {flags: (*ctx).jsonFlag, source: true, run: runRender},
		"adopt":     {flags: registerAdoptFlags, source: true, positional: true, run: runAdopt},
	}
	registerGateVerbs(m)
	return m
}

func registerDreamFlags(c *ctx) {
	c.fs.BoolVar(&c.dreamAll, "all", false, "every workspace discovered from conversations, brain.db and search.db")
	c.fs.StringVar(&c.dreamProject, "project", "", "one workspace, `DIR`, or __global__")
	c.dryRunAndJSONFlags()
}

func registerMetricsFlags(c *ctx) {
	c.fs.StringVar(&c.metricsSource, "source", "", "ingest: the sandbox home `DIR` to read instead of this one")
	c.fs.StringVar(&c.metricsOutcome, "outcome", "", "ingest: the --source run's acceptance result, `pass|fail|error`")
	c.registerReportFlags(true)
	c.fs.BoolVar(&c.metricsJSON, "json", false, "show, report, evals, trajectory, record-run: print JSON (report: stdout unless --out)")
	c.fs.BoolVar(&c.jsonIn, "json-in", false, "record-run: read JSON payload from stdin")
	c.fs.StringVar(&c.metricsHarness, "harness", "", "evals: only the trials of the harness key `KEY`, exactly")
	c.fs.IntVar(&c.limit, "limit", 0, "trajectory: print at most `N` main sessions (default 10)")
}

func registerRecordFlags(c *ctx) {
	c.fs.StringVar(&c.recTitle, "title", "", "add, edit: the record's one-line title, `T`")
	c.fs.StringVar(&c.recTitleFile, "title-file", "", "add: read the title from `FILE`, one line, instead of --title")
	c.fs.StringVar(&c.recBodyFile, "body-file", "", "add, edit: read the body from `FILE` instead of stdin")
	c.fs.StringVar(&c.recOut, "out", "", "get: write the record's body to `FILE` under .agents/scratch/")
	c.fs.StringVar(&c.recStatus, "status", "", "add, edit: the status `S`; each kind takes its own, and a status it does not take is refused with the kind's list")
	c.fs.StringVar(&c.recID, "id", "", "add: the `ID` to use instead of the next one")
	c.fs.StringVar(&c.recProject, "project", "", "add, list with --global: the project `ROOT` a global record is about")
	c.fs.BoolVar(&c.global, "global", false, "the global scope instead of this project's")
	c.fs.BoolVar(&c.recBody, "body", false, "edit: read the new body on stdin")
	c.fs.BoolVar(&c.jsonIn, "json-in", false, "add, edit: read JSON payload from stdin")
	c.fs.BoolVar(&c.jsonOut, "json", false, "get, list: print records as JSON")
	c.fs.StringVar(&c.kind, "kind", "", "list: only records of kind `K`")
	c.fs.IntVar(&c.limit, "limit", 0, "list: print at most `N` records")
	c.fs.BoolVar(&c.openRecords, "open", false, "list: only open records (open plans and bugs, current specs, deploy docs and guides, queued proposals)")
	c.fs.BoolVar(&c.recAll, "all", false, "list: every scope, each line ending in its scope")
	c.dryRunFlag()
}

func registerSearchFlags(c *ctx) {
	c.fs.IntVar(&c.limit, "limit", 0, "print at most `N` records and as many files, 1-50 (default 10)")
	c.fs.StringVar(&c.kind, "kind", "", "only hits of kind `K`: bug adr plan change handoff review testing retro spec deploy trail code doc feedback proposal learning guide memory")
	c.fs.StringVar(&c.refs, "refs", "", "every record naming `REF`: an ID, a file, or a directory (trailing /)")
	c.fs.BoolVar(&c.openRecords, "open", false, "open bugs, plans with unticked boxes, and the other open records")
	c.fs.BoolVar(&c.global, "global", false, "also the global records and every project registered with index --register, from the brain store")
	c.fs.BoolVar(&c.jsonOut, "json", false, "print hits or query results as JSON")
	c.fs.StringVar(&c.sliceQuery, "slice", "", "0/1/2-hop context capsule for `QUERY` ranked by PageRank")
	c.fs.StringVar(&c.contractsTarget, "contracts", "", "deterministic AST signatures, linked SPEC-* contracts and ADR guardrails for `TARGET`")
	c.fs.IntVar(&c.sliceHops, "hops", 0, "slice: maximum graph hops `N`, 0-6 (default 2)")
	c.fs.IntVar(&c.sliceBudget, "budget", 0, "slice, contracts: token budget `N` estimated as chars/4 (default 2000; 1500 for --contracts)")
	c.fs.StringVar(&c.sliceSession, "session", "", "slice: conversation `ID` whose read files get the 50x boost")
	c.fs.StringVar(&c.skeletonPath, "skeleton", "", "declaration-only start-end signature lines for `FILE`")
	c.fs.StringVar(&c.pathFrom, "path", "", "shortest call/type/interface path from `SYM` (<= 6 hops)")
	c.fs.StringVar(&c.pathTo, "to", "", "path: target symbol `SYM` (or give as positional argument)")
	c.fs.StringVar(&c.affectedTarget, "affected", "", "reverse-BFS blast radius (<= 2 hops) for `TARGET` path or symbol")
	c.fs.BoolVar(&c.repoMap, "map", false, "top-10 God Nodes by PageRank, package deps and cycle check")
	c.fs.BoolVar(&c.searchIncludeDocs, "include-docs", false, "include Markdown docs and headings in AST maps and slices")
	c.fs.StringVar(&c.vetPrompt, "vet", "", "9-mode intent, 6-anchor vetter and skill/record match for `PROMPT`")
	c.fs.StringVar(&c.hookEvent, "hook", "", "Antigravity lifecycle hook `EVENT`: session-start, pre-invocation, pre-tool-use, post-tool-use or stop")
}

func registerGraphFlags(c *ctx) {
	c.fs.BoolVar(&c.graphDot, "dot", false, "write Graphviz DOT instead of HTML (stdout unless --out)")
	c.fs.BoolVar(&c.force, "force", false, "overwrite an existing output file")
	c.fs.BoolVar(&c.graphIncludeDocs, "include-docs", false, "include Markdown files and doc headings in the graph")
	c.fs.BoolVar(&c.graphIncludeTests, "include-tests", false, "include test files, test packages and test edges")
	c.fs.BoolVar(&c.graphIncludeVendored, "include-vendored", false, "include vendored skill references and assets in the graph")
	c.fs.BoolVar(&c.graphJSON, "json", false, "write the graph payload as JSON (stdout unless --out)")
	c.fs.StringVar(&c.graphListen, "listen", "", "with --web, bind `ADDR` instead of 127.0.0.1:0")
	c.fs.BoolVar(&c.graphMD, "md", false, "write the Markdown architecture summary (stdout unless --out)")
	c.fs.StringVar(&c.graphOut, "out", "", "write output to `FILE` (default .agents/scratch/graph.html for HTML)")
	c.fs.BoolVar(&c.graphWeb, "web", false, "serve the rendered graph in memory on 127.0.0.1:0 in the foreground")
}

func registerIndexFlags(c *ctx) {
	c.fs.BoolVar(&c.global, "global", false, "the global search index, ~/.config/fyr/search.db, instead of this checkout's")
	c.fs.BoolVar(&c.rebuild, "rebuild", false, "delete the index and build it again")
	c.fs.BoolVar(&c.register, "register", false, "register this checkout (the operator's consent, ADR-068) so cross-project readers take it (fyr search --global, fyr record import --global, and the metrics attribution join) once it has records in brain.db or .agents/trail.jsonl, then index it")
	c.jsonFlag()
}

func registerStatusFlags(c *ctx) {
	c.fs.BoolVar(&c.jsonOut, "json", false, "print the status as JSON")
}

func runStatusVerb(c *ctx) error {
	if c.jsonOut {
		return statusJSON(c.src, c.home, c.out)
	}
	return status(c.src, c.home, c.out)
}

func registerUpdateFlags(c *ctx) {
	c.fs.BoolVar(&c.check, "check", false, "only report whether this place is behind; exit 2 when it is")
	c.fs.StringVar(&c.fromZip, "from-zip", "", "install the harness in the repository zip `FILE` with the running binary")
	c.dryRunAndJSONFlags()
}

func registerAdoptFlags(c *ctx) {
	c.fs.BoolVar(&c.force, "force", false, "replace a skill of the same name already in skills/")
	c.jsonFlag()
}

func (c *ctx) registerReportFlags(withSubverbs bool) {
	p := func(sub, text string) string {
		if withSubverbs {
			return sub + ": " + text
		}
		return text
	}
	project := "only sessions in the project `DIR` (bare --project uses this checkout)"
	out := "the output file `FILE` (default ~/.config/fyr/reports/<date-time>-metrics.html)"
	label := "only sessions with `KEY=VALUE`"
	if withSubverbs {
		project = "show, trajectory: only sessions in `DIR` (required); report: DIR (bare uses checkout)"
		out = "report: the HTML, Markdown or JSON file `FILE` to write (default ~/.config/fyr/reports/<date-time>-metrics.html)"
		label = "ingest: `KEY=VALUE` stamped on every --source session; show, report: only that label"
	} else {
		c.fs.BoolVar(&c.metricsJSON, "json", false, "write JSON instead of HTML (stdout unless --out)")
	}
	c.fs.StringVar(&c.metricsSince, "since", "", p("show, report, evals", "the window's start, `YYYY-MM-DD` (default 30 days ago)"))
	c.fs.StringVar(&c.metricsSinceTag, "since-tag", "", p("show, report", "window start at git tag `TAG`"))
	c.fs.StringVar(&c.metricsUntil, "until", "", p("show, report, evals", "the window's end, `YYYY-MM-DD`, exclusive (default now)"))
	c.fs.StringVar(&c.metricsProject, "project", "", project)
	c.fs.BoolVar(&c.global, "global", false, p("report", "all projects across the local store"))
	c.fs.BoolVar(&c.metricsMD, "md", false, p("report", "write Markdown instead of HTML (stdout unless --out)"))
	c.fs.BoolVar(&c.metricsDailyDigest, "daily-digest", false, p("report", "compute, store and print today's daily health digest"))
	c.fs.BoolVar(&c.metricsWeb, "web", false, p("report", "serve the rendered report in memory on 127.0.0.1:0 in the foreground"))
	c.fs.StringVar(&c.metricsListen, "listen", "", p("report", "with --web, bind `ADDR` instead of 127.0.0.1:0"))
	c.fs.BoolVar(&c.noIngest, "no-ingest", false, p("report", "skip incremental metrics ingest before rendering"))
	c.fs.BoolVar(&c.metricsIncludeSandboxes, "include-sandboxes", false, p("show, report", "include ephemeral eval, smoke, probe and scratch roots"))
	c.fs.BoolVar(&c.metricsAllLabels, "all-labels", false, p("show, report", "include labelled (sandbox) sessions"))
	c.fs.Int64Var(&c.metricsContextTarget, "context-target", 0, p("show, report", "the peak context, `N` tokens, a main session should stay under (default 300000)"))
	c.fs.StringVar(&c.metricsOut, "out", "", out)
	c.fs.StringVar(&c.metricsLabel, "label", "", label)
}

func registerGlobalFlags(fs *flag.FlagSet, c *ctx) {
	if c == nil {
		c = &ctx{}
	}
	fs.StringVar(&c.repoFlag, "repo", "", "the harness checkout `DIR`")
	fs.StringVar(&c.homeFlag, "home", "", "install under `DIR` instead of $HOME")
	fs.StringVar(&c.dirFlag, "dir", "", "the project checkout `DIR` (default working directory)")
	fs.StringVar(&c.dirFlag, "C", "", "shorthand for --dir `DIR`")
}

// newCtx is a verb's ctx with its FlagSet: the global flags and the
// verb's own. run parses it; the check vocabulary reads it (plan fyr-check
// D14), so the vocabulary is the flags the verb really takes.
func newCtx(cmd string, v verb, out io.Writer) *ctx {
	c := &ctx{cmd: cmd, out: out, fs: flag.NewFlagSet(cmd, flag.ContinueOnError)}
	c.fs.SetOutput(out)
	// The verb's page is help's to print (parse); the flag package prints
	// only its one reason line for a flag error (plan ledger D21 3).
	c.fs.Usage = func() {}
	registerGlobalFlags(c.fs, c)
	if v.flags != nil {
		v.flags(c)
	}
	return c
}
