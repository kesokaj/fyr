package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/kesokaj/fyr/internal/harness"
)

// exitCode carries a non-error exit status such as "drift found".
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

// runtimeCtx is what one verb runs with: its FlagSet (the global flags plus
// the verb's own, and nothing else), the parsed per-verb option struct in opts,
// the home, the writer, and for the verbs that read the harness, the resolved
// source.
type runtimeCtx struct {
	cmd           string
	fs            *flag.FlagSet
	args          []string
	out           io.Writer
	home          string
	opts          any
	writesStateFn func(*runtimeCtx) bool
	// global flags, accepted by every verb
	repoFlag, homeFlag, dirFlag string
	// per-verb flags for unmigrated verbs (Part 2)
	dryRun, check, force, openRecords, global, rebuild, dreamAll                              bool
	jsonOut, jsonIn                                                                           bool
	fromZip, kind, refs, memType, memName, memDesc, memBodyFile, dreamProject                 string
	memIncludeLow                                                                             bool
	evalFixture                                                                               string
	evalRunner                                                                                string
	evalModel                                                                                 string
	evalList, evalACP                                                                         bool
	evalRuns, evalParallel                                                                    int
	autoConvIdle, autoLevel, autoProjectIdle, autoUserIdle                                    string
	autoLines                                                                                 int
	autoAll                                                                                   bool
	modelsPick, modelsTier, modelsExplain                                                     string
	modelsRefresh                                                                             bool
	limit, sliceHops, sliceBudget                                                             int
	sliceQuery, sliceSession, contractsTarget, skeletonPath, pathFrom, pathTo, affectedTarget string
	vetPrompt, hookEvent                                                                      string
	repoMap, register, searchIncludeDocs                                                      bool
	graphOut, graphListen                                                                     string
	graphDot, graphMD, graphJSON, graphWeb, graphIncludeTests, graphIncludeDocs               bool
	graphIncludeVendored                                                                      bool
	watchConv                                                                                 string
	watchIdleSec                                                                              int
	// src is the harness, resolved before run for the verbs that mark source
	src harness.Source
}

// ctx is a temporary alias for unmigrated verbs (removed in Part 2).
type ctx = runtimeCtx

// repo is the tree on disk, "" for the files embedded in a release binary;
// no git helper is ever called with "" (D17).
func (c *runtimeCtx) repo() string { return c.src.Dir }

// projectDir is --dir / -C when given (made absolute), else the working directory.
func (c *runtimeCtx) projectDir() (string, error) {
	if c.dirFlag != "" {
		return filepath.Abs(c.dirFlag)
	}
	return os.Getwd()
}

func isGlobalFlag(name string) bool {
	return name == "home" || name == "repo" || name == "dir" || name == "C"
}

func (c *runtimeCtx) dryRunFlag() {
	c.fs.BoolVar(&c.dryRun, "dry-run", false, "print what would change, change nothing")
}

func (c *runtimeCtx) jsonFlag() {
	c.fs.BoolVar(&c.jsonOut, "json", false, "print the result as JSON")
}

func (c *runtimeCtx) dryRunAndJSONFlags() {
	c.dryRunFlag()
	c.jsonFlag()
}

// verb is one command: the flags it declares, whether it reads the harness
// tree, and what it does.
type verb struct {
	flags       func(c *runtimeCtx)               // registers the verb's own flags; nil for none
	afterParse  func(c *runtimeCtx) (bool, error) // optional post-parse validation; stop=true ends run
	writesState func(c *runtimeCtx) bool          // reports whether this invocation may write under the state dir
	source      bool                              // resolve the harness source before run
	positional  bool                              // takes positional arguments (search words, adopt's name, memory's subverb); every other verb refuses a stray one
	readsRepo   bool                              // reads --repo itself without source (eval resolves its install source after its own refusals)
	run         func(c *runtimeCtx) error
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
		"version":   {flags: (*runtimeCtx).jsonFlag, run: runVersion},
		"uninstall": {flags: (*runtimeCtx).dryRunAndJSONFlags, run: runUninstall},
		"resume":    {flags: (*runtimeCtx).jsonFlag, run: runResume},
		"git-check": {flags: (*runtimeCtx).jsonFlag, run: runGitCheck},
		"check":     checkVerb(),
		"memory":    memoryVerb(),
		"dream":     {flags: registerDreamFlags, run: runDream},
		"auto":      autoVerb(),
		"metrics":   metricsVerb(),
		"report":    reportVerb(),
		"record":    recordVerb(),
		"search":    {flags: registerSearchFlags, positional: true, run: runSearch},
		"graph":     {flags: registerGraphFlags, run: runGraph},
		"index":     {flags: registerIndexFlags, run: runSearch},
		"eval":      evalVerb(),
		"models":    modelsVerb(),
		"state":     stateVerb(),
		"watch":     watchVerb(),
		"install":   {flags: (*runtimeCtx).dryRunAndJSONFlags, source: true, run: runInstall},
		"sync":      {flags: (*runtimeCtx).dryRunAndJSONFlags, source: true, run: runInstall},
		"status":    {flags: registerStatusFlags, source: true, run: runStatusVerb},
		"diff":      {flags: (*runtimeCtx).jsonFlag, source: true, run: runDiff},
		"update":    {flags: registerUpdateFlags, source: true, run: runUpdate},
		"list":      {flags: (*runtimeCtx).jsonFlag, source: true, run: runList},
		"render":    {flags: (*runtimeCtx).jsonFlag, source: true, run: runRender},
		"adopt":     {flags: registerAdoptFlags, source: true, positional: true, run: runAdopt},
	}
	registerGateVerbs(m)
	return m
}

func registerDreamFlags(c *runtimeCtx) {
	c.fs.BoolVar(&c.dreamAll, "all", false, "every workspace discovered from conversations, brain.db and search.db")
	c.fs.StringVar(&c.dreamProject, "project", "", "one workspace, `DIR`, or __global__")
	c.dryRunAndJSONFlags()
}

func registerSearchFlags(c *runtimeCtx) {
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

func registerGraphFlags(c *runtimeCtx) {
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

func registerIndexFlags(c *runtimeCtx) {
	c.fs.BoolVar(&c.global, "global", false, "the global search index, ~/.config/fyr/search.db, instead of this checkout's")
	c.fs.BoolVar(&c.rebuild, "rebuild", false, "delete the index and build it again")
	c.fs.BoolVar(&c.register, "register", false, "register this checkout (the operator's consent, ADR-068) so cross-project readers take it (fyr search --global, fyr record import --global, and the metrics attribution join) once it has records in brain.db or .agents/trail.jsonl, then index it")
	c.jsonFlag()
}

func registerStatusFlags(c *runtimeCtx) {
	c.fs.BoolVar(&c.jsonOut, "json", false, "print the status as JSON")
}

func runStatusVerb(c *runtimeCtx) error {
	if c.jsonOut {
		return statusJSON(c.src, c.home, c.out)
	}
	return status(c.src, c.home, c.out)
}

func registerUpdateFlags(c *runtimeCtx) {
	c.fs.BoolVar(&c.check, "check", false, "only report whether this place is behind; exit 2 when it is")
	c.fs.StringVar(&c.fromZip, "from-zip", "", "install the harness in the repository zip `FILE` with the running binary")
	c.dryRunAndJSONFlags()
}

func registerAdoptFlags(c *runtimeCtx) {
	c.fs.BoolVar(&c.force, "force", false, "replace a skill of the same name already in skills/")
	c.jsonFlag()
}

func registerGlobalFlags(fs *flag.FlagSet, c *runtimeCtx) {
	if c == nil {
		c = &runtimeCtx{}
	}
	fs.StringVar(&c.repoFlag, "repo", "", "the harness checkout `DIR`")
	fs.StringVar(&c.homeFlag, "home", "", "install under `DIR` instead of $HOME")
	fs.StringVar(&c.dirFlag, "dir", "", "the project checkout `DIR` (default working directory)")
	fs.StringVar(&c.dirFlag, "C", "", "shorthand for --dir `DIR`")
}

// newCtx is a verb's runtimeCtx with its FlagSet: the global flags and the
// verb's own. run parses it; the check vocabulary reads it (plan fyr-check
// D14), so the vocabulary is the flags the verb really takes.
func newCtx(cmd string, v verb, out io.Writer) *runtimeCtx {
	c := &runtimeCtx{
		cmd:           cmd,
		out:           out,
		fs:            flag.NewFlagSet(cmd, flag.ContinueOnError),
		writesStateFn: v.writesState,
	}
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
