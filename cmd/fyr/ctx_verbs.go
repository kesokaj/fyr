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
	// src is the harness, resolved before run for the verbs that mark source
	src harness.Source
}

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

// jsonOpts holds --json for verbs whose only option is JSON output.
type jsonOpts struct {
	jsonOut bool
}

func jsonOptsOf(c *runtimeCtx) *jsonOpts {
	if o, ok := c.opts.(*jsonOpts); ok {
		return o
	}
	o := &jsonOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func registerJSONFlag(c *runtimeCtx) {
	o := &jsonOpts{}
	c.opts = o
	c.fs.BoolVar(&o.jsonOut, "json", false, "print the result as JSON")
}

// dryRunJSONOpts holds --dry-run and --json for install, sync and uninstall.
type dryRunJSONOpts struct {
	dryRun, jsonOut bool
}

func dryRunJSONOptsOf(c *runtimeCtx) *dryRunJSONOpts {
	if o, ok := c.opts.(*dryRunJSONOpts); ok {
		return o
	}
	o := &dryRunJSONOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func registerDryRunJSONFlags(c *runtimeCtx) {
	o := &dryRunJSONOpts{}
	c.opts = o
	c.fs.BoolVar(&o.dryRun, "dry-run", false, "print what would change, change nothing")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print the result as JSON")
}

func dryRunJSONWritesState(c *runtimeCtx) bool {
	return !dryRunJSONOptsOf(c).dryRun
}

// updateOpts holds parsed flags for `fyr update`.
type updateOpts struct {
	check, dryRun, jsonOut bool
	fromZip                string
}

func updateOptsOf(c *runtimeCtx) *updateOpts {
	if o, ok := c.opts.(*updateOpts); ok {
		return o
	}
	o := &updateOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func updateVerb() verb {
	return verb{
		flags: registerUpdateFlags,
		writesState: func(c *runtimeCtx) bool {
			o := updateOptsOf(c)
			return !o.dryRun && !o.check
		},
		source: true,
		run:    runUpdate,
	}
}

func registerUpdateFlags(c *runtimeCtx) {
	o := &updateOpts{}
	c.opts = o
	c.fs.BoolVar(&o.check, "check", false, "only report whether this place is behind; exit 2 when it is")
	c.fs.StringVar(&o.fromZip, "from-zip", "", "install the harness in the repository zip `FILE` with the running binary")
	c.fs.BoolVar(&o.dryRun, "dry-run", false, "print what would change, change nothing")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print the result as JSON")
}

// adoptOpts holds parsed flags for `fyr adopt`.
type adoptOpts struct {
	force, jsonOut bool
}

func adoptOptsOf(c *runtimeCtx) *adoptOpts {
	if o, ok := c.opts.(*adoptOpts); ok {
		return o
	}
	o := &adoptOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func adoptVerb() verb {
	return verb{
		flags:       registerAdoptFlags,
		writesState: func(*runtimeCtx) bool { return true },
		source:      true,
		positional:  true,
		run:         runAdopt,
	}
}

func registerAdoptFlags(c *runtimeCtx) {
	o := &adoptOpts{}
	c.opts = o
	c.fs.BoolVar(&o.force, "force", false, "replace a skill of the same name already in skills/")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print the result as JSON")
}

// wantsJSON reports whether a source-reading verb requested --json output.
func (c *runtimeCtx) wantsJSON() bool {
	switch o := c.opts.(type) {
	case *jsonOpts:
		return o.jsonOut
	case *dryRunJSONOpts:
		return o.jsonOut
	case *updateOpts:
		return o.jsonOut
	case *adoptOpts:
		return o.jsonOut
	}
	return false
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
		"version":   {flags: registerJSONFlag, run: runVersion},
		"uninstall": {flags: registerDryRunJSONFlags, writesState: dryRunJSONWritesState, run: runUninstall},
		"resume":    {flags: registerJSONFlag, run: runResume},
		"git-check": {flags: registerJSONFlag, run: runGitCheck},
		"check":     checkVerb(),
		"memory":    memoryVerb(),
		"dream":     dreamVerb(),
		"auto":      autoVerb(),
		"metrics":   metricsVerb(),
		"report":    reportVerb(),
		"record":    recordVerb(),
		"search":    searchVerb(),
		"ast":       astVerb(),
		"vet":       vetVerb(),
		"hook":      hookVerb(),
		"graph":     graphVerb(),
		"index":     indexVerb(),
		"eval":      evalVerb(),
		"models":    modelsVerb(),
		"state":     stateVerb(),
		"watch":     watchVerb(),
		"install":   {flags: registerDryRunJSONFlags, writesState: dryRunJSONWritesState, source: true, run: runInstall},
		"sync":      {flags: registerDryRunJSONFlags, writesState: dryRunJSONWritesState, source: true, run: runInstall},
		"status":    {flags: registerStatusFlags, source: true, run: runStatusVerb},
		"diff":      {flags: registerJSONFlag, source: true, run: runDiff},
		"update":    updateVerb(),
		"list":      {flags: registerJSONFlag, source: true, run: runList},
		"render":    {flags: registerJSONFlag, source: true, run: runRender},
		"adopt":     adoptVerb(),
	}
	registerGateVerbs(m)
	return m
}

func registerStatusFlags(c *runtimeCtx) {
	o := &jsonOpts{}
	c.opts = o
	c.fs.BoolVar(&o.jsonOut, "json", false, "print the status as JSON")
}

func runStatusVerb(c *runtimeCtx) error {
	if jsonOptsOf(c).jsonOut {
		return statusJSON(c.src, c.home, c.out)
	}
	return status(c.src, c.home, c.out)
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
