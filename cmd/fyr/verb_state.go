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

var stateFlags = map[string]map[string]bool{
	"export": {"out": true, "since": true, "include-memory": true, "json": true},
	"import": {"dry-run": true, "include-memory": true, "json": true},
}

func stateVerb() verb {
	return verb{flags: registerStateFlags, positional: true, readsRepo: true, run: runState}
}

func registerStateFlags(c *ctx) {
	c.dryRunFlag()
	c.fs.Bool("include-memory", false, "export, import: include memory, feedback, proposal and SIM ledger rows")
	c.fs.BoolVar(&c.jsonOut, "json", false, "print export or import summary as JSON")
	c.fs.StringVar(&c.metricsOut, "out", "", "export: write the zip bundle to `FILE` (0600, atomic)")
	c.fs.StringVar(&c.metricsSince, "since", "", "export: include rows on or after `YYYY-MM-DD`")
}

func runState(c *ctx) error {
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

func runStateExport(c *ctx, args []string, set map[string]bool) error {
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
	var since time.Time
	if set["since"] {
		if since, err = time.Parse("2006-01-02", c.metricsSince); err != nil {
			return usageErr(c.out, "state", fmt.Sprintf("fyr state export: --since %q: want YYYY-MM-DD", c.metricsSince))
		}
	}
	res, err := state.Export(state.ExportOptions{
		Home: c.home, Dir: wd, Repo: c.repoFlag, Since: since,
		IncludeMemory: stateBoolFlag(c.fs, "include-memory"), OutPath: outPath, FyrVersion: buildVersion(),
	})
	if err != nil {
		if brain.IsUsage(err) {
			return usageErr(c.out, "state", "fyr state export: "+err.Error())
		}
		return err
	}
	if c.jsonOut {
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

func runStateImport(c *ctx, args []string) error {
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
	res, err := state.Import(state.ImportOptions{
		Home: c.home, Dir: wd, BundlePath: bundlePath, Repo: c.repoFlag,
		IncludeMemory: stateBoolFlag(c.fs, "include-memory"), DryRun: c.dryRun,
	})
	if err != nil {
		if brain.IsUsage(err) {
			return usageErr(c.out, "state", "fyr state import: "+err.Error())
		}
		return err
	}
	if c.jsonOut {
		return writeJSONLine(c.out, res)
	}
	prefix := ""
	if res.DryRun {
		prefix = "[dry-run] "
	}
	simText := ""
	if stateBoolFlag(c.fs, "include-memory") || res.SIM.Total > 0 {
		simText = fmt.Sprintf(" sim=%d", res.SIM.Inserted+res.SIM.Updated)
	}
	fmt.Fprintf(c.out, "%sstate import: insert=%d updated=%d skip_duplicate=%d conflict=%d operator_preserved=%d (brain=%d metrics=%d%s)\n",
		prefix, res.Insert, res.Updated, res.SkipDuplicate, res.Conflict, res.OperatorPreserved, res.Brain.Inserted+res.Brain.Updated, res.Metrics.Inserted+res.Metrics.Updated, simText)
	return nil
}

func checkStateSubFlags(c *ctx, sub string, takes map[string]bool) (map[string]bool, error) {
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

func resolveStateOutPath(c *ctx, wd string, set map[string]bool) (string, error) {
	if !set["out"] {
		return "", nil
	}
	raw := strings.TrimSpace(c.metricsOut)
	if raw == "" || strings.HasPrefix(raw, "-") {
		return "", usageErr(c.out, "state", "fyr state export: --out needs a file: --out <file>")
	}
	if !filepath.IsAbs(raw) && c.dirFlag != "" {
		return filepath.Join(wd, raw), nil
	}
	return raw, nil
}

func stateBoolFlag(fs *flag.FlagSet, name string) bool {
	if f := fs.Lookup(name); f != nil {
		if g, ok := f.Value.(flag.Getter); ok {
			b, _ := g.Get().(bool)
			return b
		}
	}
	return false
}
