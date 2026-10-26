package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/metrics"
)

// metricsSummary is what show and report share: the show options (a usage
// error exits 2 with its reason on the writer), the existing store, opened
// and closed here, and its summary.
func (c *ctx) metricsSummary(sub string) (*metrics.Summary, error) {
	o, err := c.metricsShowOptions()
	if err != nil {
		fmt.Fprintln(c.out, "fyr metrics "+sub+": "+err.Error())
		return nil, exitCode(2)
	}
	s, err := c.existingMetricsStore()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.Show(o)
}

// runMetrics serves `metrics ingest`, `show`, `report` and `export` (spec
// the record SPEC-fyr-metrics).
// metricsSubverbs is what runMetrics dispatches, in its order: its refusal
// texts are built from it and the check vocabulary reads it, and a test pins
// it against the switch (plan fyr-check D14). export comes last, since the
// bare-verb usage line ends in its FILE.
var metricsSubverbs = []string{"ingest", "show", "report", "export"}

func runMetrics(c *ctx) error {
	args := c.fs.Args()
	if len(args) == 0 {
		fmt.Fprintln(c.out, "fyr metrics: "+strings.Join(metricsSubverbs, " | ")+" FILE")
		return exitCode(2)
	}
	sub, args := args[0], args[1:]
	switch sub {
	case "ingest":
		if bad := c.metricsFlagsOutside("source", "label", "outcome"); bad != "" {
			fmt.Fprintf(c.out, "fyr metrics ingest: --%s belongs to show or report\n", bad)
			return exitCode(2)
		}
		if len(args) > 0 {
			fmt.Fprintf(c.out, "metrics ingest takes no arguments: %q\n", args)
			return exitCode(2)
		}
		o := metrics.Options{Home: c.home, Source: c.metricsSource, Label: c.metricsLabel, Outcome: c.metricsOutcome}
		if err := o.Validate(); err != nil {
			fmt.Fprintln(c.out, "fyr metrics ingest: "+err.Error())
			return exitCode(2)
		}
		s, err := metrics.Open(metrics.Path(c.home))
		if err != nil {
			return err
		}
		defer s.Close()
		r, err := metrics.Ingest(s, o, c.out)
		if err != nil {
			return err
		}
		fmt.Fprintln(c.out, r.Line())
		if len(r.Failed) > 0 {
			return exitCode(1)
		}
		return nil
	case "show":
		if len(args) > 0 {
			fmt.Fprintf(c.out, "metrics show takes no arguments: %q\n", args)
			return exitCode(2)
		}
		sum, err := c.metricsSummary("show")
		if err != nil {
			return err
		}
		if c.metricsJSON {
			return sum.WriteJSON(c.out)
		}
		sum.WriteText(c.out)
		return nil
	case "report":
		if len(args) > 0 || c.metricsJSON {
			fmt.Fprintln(c.out, "fyr metrics report [--since YYYY-MM-DD] [--until YYYY-MM-DD] [--project DIR] [--label KEY=VALUE | --all-labels] [--context-target N] [--out FILE]")
			return exitCode(2)
		}
		sum, err := c.metricsSummary("report")
		if err != nil {
			return err
		}
		now := time.Now()
		file := c.metricsOut
		if file == "" {
			file = metrics.ReportPath(c.home, now)
		}
		if err := metrics.WriteReport(sum, file, now); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "metrics: report written to "+file)
		return nil
	case "export":
		if bad := c.metricsFlagsOutside(); bad != "" {
			fmt.Fprintf(c.out, "fyr metrics export: --%s does not apply to export\n", bad)
			return exitCode(2)
		}
		if len(args) != 1 {
			fmt.Fprintln(c.out, "fyr metrics export FILE")
			return exitCode(2)
		}
		s, err := c.existingMetricsStore()
		if err != nil {
			return err
		}
		defer s.Close()
		if err := s.Export(args[0]); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "metrics: exported to "+args[0])
		return nil
	}
	fmt.Fprintf(c.out, "fyr metrics: unknown subcommand %q (%s)\n", sub, strings.Join(metricsSubverbs, ", "))
	return exitCode(2)
}

// metricsFlagsOutside names the first metrics flag set on the command line
// that is not in allowed, or "": a flag a subcommand does not read is a
// usage error, never ignored.
func (c *ctx) metricsFlagsOutside(allowed ...string) string {
	own := map[string]bool{"source": true, "label": true, "outcome": true, "since": true, "until": true, "project": true, "all-labels": true, "json": true, "out": true, "context-target": true}
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	bad := ""
	c.fs.Visit(func(f *flag.Flag) {
		if bad == "" && own[f.Name] && !ok[f.Name] {
			bad = f.Name
		}
	})
	return bad
}

// existingMetricsStore opens the store for show, report and export, which
// read it: a home that never ingested has none, and creating an empty one
// would print zeros that read as "no sessions" (exit 1 instead).
func (c *ctx) existingMetricsStore() (*metrics.Store, error) {
	path := metrics.Path(c.home)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(c.out, "fyr metrics: no metrics store at %s; run `fyr metrics ingest` first\n", path)
		return nil, exitCode(1)
	}
	return metrics.Open(path)
}

// metricsShowOptions reads show's filters; --label and --all-labels exclude
// each other, and --project is made absolute as the store records roots.
func (c *ctx) metricsShowOptions() (metrics.ShowOptions, error) {
	var o metrics.ShowOptions
	if c.metricsSince != "" {
		t, err := time.Parse("2006-01-02", c.metricsSince)
		if err != nil {
			return o, fmt.Errorf("--since %q: want YYYY-MM-DD", c.metricsSince)
		}
		o.Since = t
	}
	if c.metricsUntil != "" {
		t, err := time.Parse("2006-01-02", c.metricsUntil)
		if err != nil {
			return o, fmt.Errorf("--until %q: want YYYY-MM-DD", c.metricsUntil)
		}
		if !o.Since.IsZero() && !t.After(o.Since) {
			return o, errors.New("--until must be after --since")
		}
		o.Now = t // exclusive: --since A --until B and --since B never overlap
	}
	if c.metricsLabel != "" && c.metricsAllLabels {
		return o, errors.New("--label and --all-labels exclude each other")
	}
	if c.metricsSource != "" || c.metricsOutcome != "" {
		return o, errors.New("--source and --outcome belong to ingest")
	}
	if c.metricsOut != "" && c.fs.Arg(0) != "report" {
		return o, errors.New("--out belongs to report")
	}
	// Given means set: 0 left to the package is the default target, 0 typed
	// is a mistake.
	given := false
	c.fs.Visit(func(f *flag.Flag) { given = given || f.Name == "context-target" })
	if given && c.metricsContextTarget <= 0 {
		return o, errors.New("--context-target takes a token count above 0")
	}
	o.ContextTarget = c.metricsContextTarget
	o.Label, o.AllLabels = c.metricsLabel, c.metricsAllLabels
	if c.metricsProject != "" {
		abs, err := filepath.Abs(c.metricsProject)
		if err != nil {
			return o, err
		}
		o.Project = abs
	}
	return o, nil
}
