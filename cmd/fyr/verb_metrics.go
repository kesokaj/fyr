package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/metrics"
)

// metricsSummary is what show reads: the show options (a usage error exits
// 2 with its reason on the writer), the existing store, opened and closed
// here, and its summary.
func (c *ctx) metricsSummary(sub string) (*metrics.Summary, error) {
	o, err := c.metricsShowOptions()
	if err != nil {
		return nil, usageErr(c.out, "metrics", "fyr metrics "+sub+": "+err.Error())
	}
	s, err := c.existingMetricsStore()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.Show(o)
}

// runMetrics serves `metrics ingest`, `show`, `report`, `evals` and
// `export` (spec the record SPEC-fyr-metrics).
// metricsSubverbs is what runMetrics dispatches, in its order: its refusal
// texts are built from it and the check vocabulary reads it, and a test pins
// it against the switch (plan fyr-check D14).
var metricsSubverbs = []string{"ingest", "show", "report", "evals", "trajectory", "export", "record-run"}

func runMetrics(c *ctx) error {
	args := c.fs.Args()
	if len(args) == 0 {
		return usageErr(c.out, "metrics", "")
	}
	sub, args := args[0], args[1:]
	switch sub {
	case "ingest":
		return c.metricsIngest(args)
	case "show":
		return c.metricsShow(args)
	case "report":
		return c.metricsReport(args)
	case "evals":
		return c.metricsEvals(args)
	case "trajectory":
		return c.metricsTrajectory(args)
	case "export":
		return c.metricsExport(args)
	case "record-run":
		return c.metricsRecordRun(args)
	}
	return usageErr(c.out, "metrics", fmt.Sprintf("fyr metrics: unknown subcommand %q (%s)", sub, strings.Join(metricsSubverbs, ", ")))
}

// metricsIngest serves `metrics ingest`: a failed source is exit 1 after
// the summary line.
func (c *ctx) metricsIngest(args []string) error {
	if bad := c.metricsFlagsOutside("source", "label", "outcome"); bad != "" {
		return usageErr(c.out, "metrics", fmt.Sprintf("fyr metrics ingest: --%s belongs to show, report or evals", bad))
	}
	if len(args) > 0 {
		return usageErr(c.out, "metrics", fmt.Sprintf("metrics ingest takes no arguments: %q", args))
	}
	o := metrics.Options{Home: c.home, Source: c.metricsSource, Label: c.metricsLabel, Outcome: c.metricsOutcome}
	if err := o.Validate(); err != nil {
		return usageErr(c.out, "metrics", "fyr metrics ingest: "+err.Error())
	}
	r, err := ingestInto(o, c.out)
	if err != nil {
		return err
	}
	if len(r.Failed) > 0 {
		return exitCode(1)
	}
	return nil
}

// ingestInto is one ingest into the home's store and its summary line on w:
// `metrics ingest` and `fyr auto`'s metrics check both run it.
func ingestInto(o metrics.Options, w io.Writer) (metrics.Result, error) {
	s, err := metrics.Open(metrics.Path(o.Home))
	if err != nil {
		return metrics.Result{}, err
	}
	defer s.Close()
	r, err := metrics.Ingest(s, o, w)
	if err != nil {
		return r, err
	}
	fmt.Fprintln(w, r.Line())
	return r, nil
}

// ingestTo is the bare `fyr metrics ingest` (no --source, --label or
// --outcome); a failed source, exit 1 at the terminal, is returned as an error.
func ingestTo(home string, w io.Writer) error {
	r, err := ingestInto(metrics.Options{Home: home}, w)
	if err == nil && len(r.Failed) > 0 {
		err = fmt.Errorf("%d source(s) failed", len(r.Failed))
	}
	return err
}

// metricsShow serves `metrics show`, as text or --json.
func (c *ctx) metricsShow(args []string) error {
	if bad := c.metricsFlagsOutside("since", "since-tag", "until", "project", "label", "all-labels", "context-target", "include-sandboxes", "json"); bad != "" {
		return usageErr(c.out, "metrics", fmt.Sprintf("fyr metrics show: --%s does not apply to show", bad))
	}
	if len(args) > 0 {
		return usageErr(c.out, "metrics", fmt.Sprintf("metrics show takes no arguments: %q", args))
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
}

// metricsReport serves `metrics report`: HTML, --md, --json, --daily-digest, or --web.
func (c *ctx) metricsReport(args []string) error {
	if bad := c.metricsFlagsOutside("since", "since-tag", "until", "project", "global", "md", "json", "daily-digest", "web", "listen", "include-sandboxes", "label", "all-labels", "context-target", "out", "no-ingest"); bad != "" {
		return usageErr(c.out, "metrics", fmt.Sprintf("fyr metrics report: --%s does not apply to report", bad))
	}
	if len(args) > 0 {
		return usageErr(c.out, "metrics", fmt.Sprintf("metrics report takes no arguments: %q", args))
	}
	return c.renderReport("fyr metrics report")
}

// metricsTrajectory serves `metrics trajectory`: per-session token trajectory,
// subagent hierarchy, and context/tool anomalies, as text or --json (D6).
func (c *ctx) metricsTrajectory(args []string) error {
	if bad := c.metricsFlagsOutside("project", "limit", "json"); bad != "" {
		return usageErr(c.out, "metrics", fmt.Sprintf("fyr metrics trajectory: --%s does not apply to trajectory", bad))
	}
	if len(args) > 1 {
		return usageErr(c.out, "metrics", fmt.Sprintf("metrics trajectory takes at most one session id: %q", args))
	}
	if c.limit < 0 {
		return usageErr(c.out, "metrics", "fyr metrics trajectory: --limit must be >= 0")
	}
	proj, err := c.trajectoryProject()
	if err != nil {
		return usageErr(c.out, "metrics", "fyr metrics trajectory: "+err.Error())
	}
	s, err := c.existingMetricsStore()
	if err != nil {
		return err
	}
	defer s.Close()
	var sid string
	if len(args) == 1 {
		sid = args[0]
	}
	list, err := s.Trajectories(metrics.TrajectoryOptions{
		Project: proj, SessionID: sid, Limit: c.limit,
	})
	if err != nil {
		fmt.Fprintln(c.out, "fyr metrics trajectory: "+err.Error())
		return exitCode(1)
	}
	if c.metricsJSON {
		return metrics.WriteTrajectoriesJSON(c.out, list)
	}
	metrics.WriteTrajectoriesText(c.out, list)
	return nil
}

func (c *ctx) trajectoryProject() (string, error) {
	if c.metricsProject != "" {
		return filepath.Abs(c.metricsProject)
	}
	if c.dirFlag != "" {
		return c.projectDir()
	}
	return "", nil
}

// metricsExport serves `metrics export FILE`.
func (c *ctx) metricsExport(args []string) error {
	if bad := c.metricsFlagsOutside(); bad != "" {
		return usageErr(c.out, "metrics", fmt.Sprintf("fyr metrics export: --%s does not apply to export", bad))
	}
	if len(args) != 1 {
		return usageErr(c.out, "metrics", "fyr metrics export requires FILE")
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

// metricsFlagsOutside names the first metrics flag set on the command line
// that is not in allowed, or "": a flag a subcommand does not read is a
// usage error, never ignored.
func (c *ctx) metricsFlagsOutside(allowed ...string) string {
	ok := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		ok[a] = true
	}
	bad := ""
	c.fs.Visit(func(f *flag.Flag) {
		if bad == "" && !isGlobalFlag(f.Name) && !ok[f.Name] {
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
	if c.metricsProject != "" && c.global {
		return o, errors.New("--project and --global exclude each other")
	}
	if err := validateShowLabelFlags(c.metricsLabel, c.metricsAllLabels); err != nil {
		return o, err
	}
	if err := c.validateReportAndShowFlags(); err != nil {
		return o, err
	}
	o.Home = c.home
	o.ContextTarget = c.metricsContextTarget
	o.Label, o.AllLabels = c.metricsLabel, c.metricsAllLabels
	o.IncludeSandboxes = c.metricsIncludeSandboxes
	if c.metricsProject != "" {
		proj, err := c.resolveMetricsProject()
		if err != nil {
			return o, err
		}
		o.Project = proj
	}
	if err := c.resolveMetricsWindow(&o); err != nil {
		return o, err
	}
	return o, nil
}

func (c *ctx) validateReportAndShowFlags() error {
	if c.metricsSource != "" || c.metricsOutcome != "" {
		return errors.New("--source and --outcome belong to ingest")
	}
	if c.metricsHarness != "" {
		return errors.New("--harness belongs to evals")
	}
	if c.limit != 0 {
		return errors.New("--limit belongs to trajectory")
	}
	var ctxGiven, listenGiven, outGiven bool
	c.fs.Visit(func(f *flag.Flag) {
		ctxGiven = ctxGiven || f.Name == "context-target"
		listenGiven = listenGiven || f.Name == "listen"
		outGiven = outGiven || f.Name == "out"
	})
	isReport := c.cmd == "report" || c.fs.Arg(0) == "report"
	if (c.metricsOut != "" || outGiven) && !isReport {
		return errors.New("--out belongs to report")
	}
	if (c.global || c.metricsMD || c.metricsWeb || c.metricsDailyDigest || c.noIngest || listenGiven || c.metricsListen != "") && !isReport {
		return errors.New("--global, --md, --web, --listen, --daily-digest and --no-ingest belong to report")
	}
	if ctxGiven && c.metricsContextTarget <= 0 {
		return errors.New("--context-target takes a token count above 0")
	}
	return c.validateReportModeExclusions(listenGiven, outGiven)
}

func (c *ctx) validateReportModeExclusions(listenGiven, outGiven bool) error {
	if c.metricsDailyDigest && (c.metricsWeb || c.metricsMD || outGiven || c.metricsOut != "" || listenGiven || c.metricsListen != "") {
		return errors.New("--daily-digest cannot be combined with --web, --md or --out")
	}
	if (listenGiven || c.metricsListen != "") && !c.metricsWeb {
		return errors.New("--listen requires --web")
	}
	if listenGiven && strings.TrimSpace(c.metricsListen) == "" {
		return errors.New("--listen takes an address HOST:PORT")
	}
	if c.metricsWeb && (outGiven || c.metricsOut != "" || c.metricsMD || c.metricsJSON) {
		return errors.New("--web cannot be combined with --out, --md or --json")
	}
	if c.metricsMD && c.metricsJSON {
		return errors.New("--md and --json exclude each other")
	}
	if outGiven && strings.TrimSpace(c.metricsOut) == "" {
		return errors.New("--out needs a file: --out <file>")
	}
	return nil
}

func validateShowLabelFlags(label string, allLabels bool) error {
	if label != "" && allLabels {
		return errors.New("--label and --all-labels exclude each other")
	}
	if label != "" {
		k, v, ok := strings.Cut(label, "=")
		if !ok || k == "" || v == "" {
			return fmt.Errorf("--label %q: want KEY=VALUE", label)
		}
	}
	return nil
}

// metricsWindow reads --since and --until: zero for either not given, and
// --until exclusive, so --since A --until B and --since B never overlap.
func (c *ctx) metricsWindow() (since, until time.Time, err error) {
	if c.metricsSince != "" {
		if since, err = time.Parse("2006-01-02", c.metricsSince); err != nil {
			return since, until, fmt.Errorf("--since %q: want YYYY-MM-DD", c.metricsSince)
		}
	}
	if c.metricsUntil != "" {
		if until, err = time.Parse("2006-01-02", c.metricsUntil); err != nil {
			return since, until, fmt.Errorf("--until %q: want YYYY-MM-DD", c.metricsUntil)
		}
		if !since.IsZero() && !until.After(since) {
			return since, until, errors.New("--until must be after --since")
		}
	}
	return since, until, nil
}
