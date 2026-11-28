package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/state"
)

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
