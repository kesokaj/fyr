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

// metricsOpts holds parsed flags for `fyr metrics`, embedding reportOpts so
// report-shared flags register once via registerReportFlags.
type metricsOpts struct {
	reportOpts
	source, outcome, harness string
	jsonIn                   bool
	limit                    int
}

func metricsOptsOf(c *runtimeCtx) *metricsOpts {
	if o, ok := c.opts.(*metricsOpts); ok {
		return o
	}
	return &metricsOpts{}
}

func metricsVerb() verb {
	return verb{
		flags:       registerMetricsFlags,
		afterParse:  validateMetricsProjectFlag,
		writesState: metricsWritesState,
		positional:  true,
		run:         runMetrics,
	}
}

func registerMetricsFlags(c *runtimeCtx) {
	o := &metricsOpts{}
	c.opts = o
	c.fs.StringVar(&o.source, "source", "", "ingest: the sandbox home `DIR` to read instead of this one")
	c.fs.StringVar(&o.outcome, "outcome", "", "ingest: the --source run's acceptance result, `pass|fail|error`")
	registerReportFlags(c.fs, &o.reportOpts, true)
	c.fs.BoolVar(&o.jsonOut, "json", false, "show, report, evals, trajectory, record-run: print JSON (report: stdout unless --out)")
	c.fs.BoolVar(&o.jsonIn, "json-in", false, "record-run: read JSON payload from stdin")
	c.fs.StringVar(&o.harness, "harness", "", "evals: only the trials of the harness key `KEY`, exactly")
	c.fs.IntVar(&o.limit, "limit", 0, "trajectory: print at most `N` main sessions (default 10)")
}

func metricsWritesState(c *runtimeCtx) bool {
	sub := c.fs.Arg(0)
	return sub == "ingest" || sub == "record-run" || (sub == "report" && reportWritesState(c))
}

// metricsSummary is what show reads: the show options (a usage error exits
// 2 with its reason on the writer), the existing store, opened and closed
// here, and its summary.
func (c *runtimeCtx) metricsSummary(sub string) (*metrics.Summary, error) {
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

func runMetrics(c *runtimeCtx) error {
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
func (c *runtimeCtx) metricsIngest(args []string) error {
	if bad := c.metricsFlagsOutside("source", "label", "outcome"); bad != "" {
		return usageErr(c.out, "metrics", fmt.Sprintf("fyr metrics ingest: --%s belongs to show, report or evals", bad))
	}
	if len(args) > 0 {
		return usageErr(c.out, "metrics", fmt.Sprintf("metrics ingest takes no arguments: %q", args))
	}
	mo := metricsOptsOf(c)
	o := metrics.Options{Home: c.home, Source: mo.source, Label: mo.label, Outcome: mo.outcome}
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
func (c *runtimeCtx) metricsShow(args []string) error {
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
	if metricsOptsOf(c).jsonOut {
		return sum.WriteJSON(c.out)
	}
	sum.WriteText(c.out)
	return nil
}

// metricsReport serves `metrics report`: HTML, --md, --json, --daily-digest, or --web.
func (c *runtimeCtx) metricsReport(args []string) error {
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
func (c *runtimeCtx) metricsTrajectory(args []string) error {
	if bad := c.metricsFlagsOutside("project", "limit", "json"); bad != "" {
		return usageErr(c.out, "metrics", fmt.Sprintf("fyr metrics trajectory: --%s does not apply to trajectory", bad))
	}
	if len(args) > 1 {
		return usageErr(c.out, "metrics", fmt.Sprintf("metrics trajectory takes at most one session id: %q", args))
	}
	mo := metricsOptsOf(c)
	if mo.limit < 0 {
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
		Project: proj, SessionID: sid, Limit: mo.limit,
	})
	if err != nil {
		fmt.Fprintln(c.out, "fyr metrics trajectory: "+err.Error())
		return exitCode(1)
	}
	if mo.jsonOut {
		return metrics.WriteTrajectoriesJSON(c.out, list)
	}
	metrics.WriteTrajectoriesText(c.out, list)
	return nil
}

func (c *runtimeCtx) trajectoryProject() (string, error) {
	if proj := reportOptsOf(c).project; proj != "" {
		return filepath.Abs(proj)
	}
	if c.dirFlag != "" {
		return c.projectDir()
	}
	return "", nil
}

// metricsExport serves `metrics export FILE`.
func (c *runtimeCtx) metricsExport(args []string) error {
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
func (c *runtimeCtx) metricsFlagsOutside(allowed ...string) string {
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
func (c *runtimeCtx) existingMetricsStore() (*metrics.Store, error) {
	path := metrics.Path(c.home)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(c.out, "fyr metrics: no metrics store at %s; run `fyr metrics ingest` first\n", path)
		return nil, exitCode(1)
	}
	return metrics.Open(path)
}

// metricsShowOptions reads show's filters; --label and --all-labels exclude
// each other, and --project is made absolute as the store records roots.
func (c *runtimeCtx) metricsShowOptions() (metrics.ShowOptions, error) {
	ro := reportOptsOf(c)
	var o metrics.ShowOptions
	if ro.project != "" && ro.global {
		return o, errors.New("--project and --global exclude each other")
	}
	if err := validateShowLabelFlags(ro.label, ro.allLabels); err != nil {
		return o, err
	}
	if err := c.validateReportAndShowFlags(); err != nil {
		return o, err
	}
	o.Home = c.home
	o.ContextTarget = ro.contextTarget
	o.Label, o.AllLabels = ro.label, ro.allLabels
	o.IncludeSandboxes = ro.includeSandboxes
	if ro.project != "" {
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

func (c *runtimeCtx) validateReportAndShowFlags() error {
	if err := c.validateMetricsOnlySubverbFlags(); err != nil {
		return err
	}
	ro := reportOptsOf(c)
	ctxGiven, listenGiven, outGiven := c.visitedReportFlags()
	isReport := c.cmd == "report" || c.fs.Arg(0) == "report"
	if (ro.out != "" || outGiven) && !isReport {
		return errors.New("--out belongs to report")
	}
	if ro.hasReportOnlyFlag(listenGiven) && !isReport {
		return errors.New("--global, --md, --web, --listen, --daily-digest and --no-ingest belong to report")
	}
	if ctxGiven && ro.contextTarget <= 0 {
		return errors.New("--context-target takes a token count above 0")
	}
	return c.validateReportModeExclusions(listenGiven, outGiven)
}

func (c *runtimeCtx) validateMetricsOnlySubverbFlags() error {
	mo, ok := c.opts.(*metricsOpts)
	if !ok {
		return nil
	}
	if mo.source != "" || mo.outcome != "" {
		return errors.New("--source and --outcome belong to ingest")
	}
	if mo.harness != "" {
		return errors.New("--harness belongs to evals")
	}
	if mo.limit != 0 {
		return errors.New("--limit belongs to trajectory")
	}
	return nil
}

func (c *runtimeCtx) visitedReportFlags() (ctxGiven, listenGiven, outGiven bool) {
	c.fs.Visit(func(f *flag.Flag) {
		ctxGiven = ctxGiven || f.Name == "context-target"
		listenGiven = listenGiven || f.Name == "listen"
		outGiven = outGiven || f.Name == "out"
	})
	return ctxGiven, listenGiven, outGiven
}

func (ro *reportOpts) hasReportOnlyFlag(listenGiven bool) bool {
	return ro.global || ro.md || ro.web || ro.dailyDigest || ro.noIngest || listenGiven || ro.listen != ""
}

func (c *runtimeCtx) validateReportModeExclusions(listenGiven, outGiven bool) error {
	ro := reportOptsOf(c)
	if ro.dailyDigest && (ro.web || ro.md || outGiven || ro.out != "" || listenGiven || ro.listen != "") {
		return errors.New("--daily-digest cannot be combined with --web, --md or --out")
	}
	if (listenGiven || ro.listen != "") && !ro.web {
		return errors.New("--listen requires --web")
	}
	if listenGiven && strings.TrimSpace(ro.listen) == "" {
		return errors.New("--listen takes an address HOST:PORT")
	}
	if ro.web && (outGiven || ro.out != "" || ro.md || ro.jsonOut) {
		return errors.New("--web cannot be combined with --out, --md or --json")
	}
	if ro.md && ro.jsonOut {
		return errors.New("--md and --json exclude each other")
	}
	if outGiven && strings.TrimSpace(ro.out) == "" {
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
func (c *runtimeCtx) metricsWindow() (since, until time.Time, err error) {
	ro := reportOptsOf(c)
	if ro.since != "" {
		if since, err = time.Parse("2006-01-02", ro.since); err != nil {
			return since, until, fmt.Errorf("--since %q: want YYYY-MM-DD", ro.since)
		}
	}
	if ro.until != "" {
		if until, err = time.Parse("2006-01-02", ro.until); err != nil {
			return since, until, fmt.Errorf("--until %q: want YYYY-MM-DD", ro.until)
		}
		if !since.IsZero() && !until.After(since) {
			return since, until, errors.New("--until must be after --since")
		}
	}
	return since, until, nil
}
