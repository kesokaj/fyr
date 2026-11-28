package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/state"
	"github.com/kesokaj/fyr/internal/watch"
)

// dreamOpts holds parsed flags for `fyr dream`.
type dreamOpts struct {
	all, dryRun, jsonOut bool
	project              string
}

func dreamOptsOf(c *runtimeCtx) *dreamOpts {
	if o, ok := c.opts.(*dreamOpts); ok {
		return o
	}
	o := &dreamOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func dreamVerb() verb {
	return verb{flags: registerDreamFlags, writesState: dreamWritesState, run: runDream}
}

func registerDreamFlags(c *runtimeCtx) {
	o := &dreamOpts{}
	c.opts = o
	c.fs.BoolVar(&o.all, "all", false, "every workspace discovered from conversations, brain.db and search.db")
	c.fs.StringVar(&o.project, "project", "", "one workspace, `DIR`, or __global__")
	c.fs.BoolVar(&o.dryRun, "dry-run", false, "print what would change, change nothing")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print the result as JSON")
}

func dreamWritesState(c *runtimeCtx) bool {
	return !dreamOptsOf(c).dryRun
}

// runDream needs no harness tree (R3's first exception).
func runDream(c *runtimeCtx) error {
	o := dreamOptsOf(c)
	project, err := resolveDreamProject(c, o)
	if err != nil {
		return err
	}
	var recordBg func(metrics.BackgroundRunRow) error
	if !o.dryRun {
		home := c.home
		recordBg = func(row metrics.BackgroundRunRow) error { return metrics.RecordBackgroundRun(home, row) }
	}
	var buf bytes.Buffer
	out := c.out
	if o.jsonOut {
		out = &buf
	}
	err = dream.Run(dream.Options{
		Home: c.home, All: o.all, Project: project, DryRun: o.dryRun, Out: out, RecordBackgroundRun: recordBg,
	})
	return finishDreamRun(c, o, project, &buf, err)
}

func resolveDreamProject(c *runtimeCtx, o *dreamOpts) (string, error) {
	project := o.project
	if project == "" && !o.all && c.dirFlag != "" {
		var err error
		if project, err = c.projectDir(); err != nil {
			return "", err
		}
	}
	// Every usage error goes through usageErr, as every other verb's does:
	// its reason, dream's synopsis and the hint, exit 2 on the caller's writer
	// (IN12, IN13; ledger item 86). All are refused before anything is locked,
	// logged or read.
	switch {
	case o.all && o.project != "":
		return "", usageErr(c.out, "dream", "fyr dream: --all or --project <dir>, not both")
	case !o.all && project == "":
		return "", usageErr(c.out, "dream", "")
	}
	return project, nil
}

func finishDreamRun(c *runtimeCtx, o *dreamOpts, project string, buf *bytes.Buffer, err error) error {
	var ee *dream.ExitError
	if errors.As(err, &ee) {
		if o.jsonOut {
			if jerr := writeVerbExitErrorJSON(c.out, "dream", ee.Msg, buf.String()); jerr != nil {
				return jerr
			}
			return exitCode(ee.Code)
		}
		fmt.Fprintln(c.out, ee.Msg)
		return exitCode(ee.Code)
	}
	if err == nil && o.jsonOut {
		return writeDreamJSON(c, project, buf.String())
	}
	return err
}

var watchProcLister func() ([]watch.ProcInfo, error)

// watchOpts holds parsed flags for `fyr watch`.
type watchOpts struct {
	conv           string
	idleSec        int
	check, jsonOut bool
}

func watchOptsOf(c *runtimeCtx) *watchOpts {
	if o, ok := c.opts.(*watchOpts); ok {
		return o
	}
	o := &watchOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func watchVerb() verb {
	return verb{flags: registerWatchFlags, run: runWatch}
}

func registerWatchFlags(c *runtimeCtx) {
	o := &watchOpts{}
	c.opts = o
	c.fs.BoolVar(&o.check, "check", false, "run one out-of-band hang, interactive-wait and budget check")
	c.fs.StringVar(&o.conv, "conv", "", "inspect conversation `ID` and its child subagents")
	c.fs.IntVar(&o.idleSec, "idle-sec", 0, "stall threshold `N` in seconds (default 90)")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print report as JSON")
}

func runWatch(c *runtimeCtx) error {
	o := watchOptsOf(c)
	if !o.check {
		return usageErr(c.out, "watch", "fyr watch requires --check")
	}
	if o.idleSec < 0 {
		return usageErr(c.out, "watch", "fyr watch: --idle-sec must be >= 0")
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	rep, err := watch.Check(context.Background(), watch.Options{
		Home:       c.home,
		Dir:        wd,
		ConvID:     o.conv,
		IdleSec:    o.idleSec,
		ProcLister: watchProcLister,
	})
	if err != nil {
		fmt.Fprintln(stderr, search.Printable("fyr watch: "+err.Error()))
		return exitCode(2)
	}
	if o.jsonOut {
		if err := writeJSONLine(c.out, rep); err != nil {
			return err
		}
	} else {
		printWatchText(c.out, rep)
	}
	if !rep.OK {
		return exitCode(1)
	}
	return nil
}

func printWatchText(w io.Writer, rep watch.Report) {
	if rep.OK {
		fmt.Fprintf(w, "watch: ok (%d checked)\n", rep.Checked)
		return
	}
	for _, f := range rep.Findings {
		fmt.Fprintln(w, formatWatchFinding(f))
	}
}

func formatWatchFinding(f watch.Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s", search.Printable(f.Kind), search.Printable(f.Target))
	if f.PID > 0 {
		fmt.Fprintf(&b, " pid=%d", f.PID)
	}
	if f.IdleSec > 0 {
		fmt.Fprintf(&b, " idle=%ds", f.IdleSec)
	}
	if f.ArtifactStatus != "" {
		fmt.Fprintf(&b, " status=%s", search.Printable(f.ArtifactStatus))
	}
	if f.PromptHint != "" {
		fmt.Fprintf(&b, " hint=%q", search.Printable(f.PromptHint))
	}
	if f.Action != "" {
		fmt.Fprintf(&b, " -- %s", search.Printable(f.Action))
	}
	return b.String()
}

// stateOpts holds parsed flags for `fyr state`.
type stateOpts struct {
	out, since                     string
	dryRun, jsonOut, includeMemory bool
}

func stateOptsOf(c *runtimeCtx) *stateOpts {
	if o, ok := c.opts.(*stateOpts); ok {
		return o
	}
	return &stateOpts{}
}

var stateFlags = map[string]map[string]bool{
	"export": {"out": true, "since": true, "include-memory": true, "json": true},
	"import": {"dry-run": true, "include-memory": true, "json": true},
}

func stateVerb() verb {
	return verb{flags: registerStateFlags, positional: true, readsRepo: true, run: runState}
}

func registerStateFlags(c *runtimeCtx) {
	o := &stateOpts{}
	c.opts = o
	c.fs.BoolVar(&o.dryRun, "dry-run", false, "print what would change, change nothing")
	c.fs.BoolVar(&o.includeMemory, "include-memory", false, "export, import: include memory, feedback, proposal and SIM ledger rows")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print export or import summary as JSON")
	c.fs.StringVar(&o.out, "out", "", "export: write the zip bundle to `FILE` (0600, atomic)")
	c.fs.StringVar(&o.since, "since", "", "export: include rows on or after `YYYY-MM-DD`")
}

func runState(c *runtimeCtx) error {
	args := c.fs.Args()
	if len(args) == 0 {
		return usageErr(c.out, "state", "")
	}
	sub, rest := args[0], args[1:]
	takes, ok := stateFlags[sub]
	if !ok {
		return usageErr(c.out, "state", fmt.Sprintf("fyr state: unknown subcommand %q (export, import)", sub))
	}
	set, err := checkStateSubFlags(c, sub, takes)
	if err != nil {
		return err
	}
	if set["repo"] && (strings.TrimSpace(c.repoFlag) == "" || strings.HasPrefix(c.repoFlag, "-")) {
		return usageErr(c.out, "state", "fyr state "+sub+": --repo needs an ID or current")
	}
	if sub == "export" {
		return runStateExport(c, rest, set)
	}
	return runStateImport(c, rest)
}

func runStateExport(c *runtimeCtx, args []string, set map[string]bool) error {
	if len(args) > 0 {
		return usageErr(c.out, "state", fmt.Sprintf("state export takes no arguments: %q", args))
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	outPath, err := resolveStateOutPath(c, wd, set)
	if err != nil {
		return err
	}
	o := stateOptsOf(c)
	var since time.Time
	if set["since"] {
		if since, err = time.Parse("2006-01-02", o.since); err != nil {
			return usageErr(c.out, "state", fmt.Sprintf("fyr state export: --since %q: want YYYY-MM-DD", o.since))
		}
	}
	res, err := state.Export(state.ExportOptions{
		Home: c.home, Dir: wd, Repo: c.repoFlag, Since: since,
		IncludeMemory: o.includeMemory, OutPath: outPath, FyrVersion: buildVersion(),
	})
	if err != nil {
		if brain.IsUsage(err) {
			return usageErr(c.out, "state", "fyr state export: "+err.Error())
		}
		return err
	}
	if o.jsonOut {
		return writeJSONLine(c.out, res)
	}
	simText := ""
	if res.Manifest.IncludeMemory || res.Manifest.Counts.SIMProblems > 0 || res.Manifest.Counts.SIMAttempts > 0 || res.Manifest.Counts.SIMAliases > 0 {
		simText = fmt.Sprintf(" sim_problems=%d sim_attempts=%d sim_aliases=%d",
			res.Manifest.Counts.SIMProblems, res.Manifest.Counts.SIMAttempts, res.Manifest.Counts.SIMAliases)
	}
	fmt.Fprintf(c.out, "state export: %s (brain_records=%d metrics_rows=%d%s include_memory=%t)\n",
		res.OutPath, res.Manifest.Counts.BrainRecords, res.Manifest.Counts.MetricsRows, simText, res.Manifest.IncludeMemory)
	return nil
}

func runStateImport(c *runtimeCtx, args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return usageErr(c.out, "state", "fyr state import requires <bundle.zip>")
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	bundlePath := args[0]
	if !filepath.IsAbs(bundlePath) && c.dirFlag != "" {
		bundlePath = filepath.Join(wd, bundlePath)
	}
	o := stateOptsOf(c)
	res, err := state.Import(state.ImportOptions{
		Home: c.home, Dir: wd, BundlePath: bundlePath, Repo: c.repoFlag,
		IncludeMemory: o.includeMemory, DryRun: o.dryRun,
	})
	if err != nil {
		if brain.IsUsage(err) {
			return usageErr(c.out, "state", "fyr state import: "+err.Error())
		}
		return err
	}
	if o.jsonOut {
		return writeJSONLine(c.out, res)
	}
	prefix := ""
	if res.DryRun {
		prefix = "[dry-run] "
	}
	simText := ""
	if o.includeMemory || res.SIM.Total > 0 {
		simText = fmt.Sprintf(" sim=%d", res.SIM.Inserted+res.SIM.Updated)
	}
	fmt.Fprintf(c.out, "%sstate import: insert=%d updated=%d skip_duplicate=%d conflict=%d operator_preserved=%d (brain=%d metrics=%d%s)\n",
		prefix, res.Insert, res.Updated, res.SkipDuplicate, res.Conflict, res.OperatorPreserved, res.Brain.Inserted+res.Brain.Updated, res.Metrics.Inserted+res.Metrics.Updated, simText)
	return nil
}

func checkStateSubFlags(c *runtimeCtx, sub string, takes map[string]bool) (map[string]bool, error) {
	set := map[string]bool{}
	var stray []string
	c.fs.Visit(func(f *flag.Flag) {
		set[f.Name] = true
		if !isGlobalFlag(f.Name) && !takes[f.Name] {
			stray = append(stray, "--"+f.Name)
		}
	})
	if len(stray) > 0 {
		sort.Strings(stray)
		return nil, usageErr(c.out, "state", fmt.Sprintf("fyr state %s does not take %s", sub, strings.Join(stray, ", ")))
	}
	return set, nil
}

func resolveStateOutPath(c *runtimeCtx, wd string, set map[string]bool) (string, error) {
	if !set["out"] {
		return "", nil
	}
	raw := strings.TrimSpace(stateOptsOf(c).out)
	if raw == "" || strings.HasPrefix(raw, "-") {
		return "", usageErr(c.out, "state", "fyr state export: --out needs a file: --out <file>")
	}
	if !filepath.IsAbs(raw) && c.dirFlag != "" {
		return filepath.Join(wd, raw), nil
	}
	return raw, nil
}
