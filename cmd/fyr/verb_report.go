package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/trail"
	"github.com/kesokaj/fyr/internal/webpreview"
)

const reportWebHTMLCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src data:"

// serveWebPreview is webpreview.Serve; tests replace it to inspect assets or
// drive a bounded context.
var serveWebPreview = webpreview.Serve

// reportOpts holds parsed flags for `fyr report` and the report-shared subset
// of `fyr metrics`.
type reportOpts struct {
	since, sinceTag, until, project, out, label, listen string
	contextTarget                                       int64
	global, md, jsonOut, dailyDigest, web, noIngest     bool
	includeSandboxes, allLabels                         bool
}

func reportOptsOf(c *runtimeCtx) *reportOpts {
	switch o := c.opts.(type) {
	case *reportOpts:
		return o
	case *metricsOpts:
		return &o.reportOpts
	}
	o := &reportOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func reportVerb() verb {
	return verb{
		flags:       registerReportVerbFlags,
		afterParse:  validateMetricsProjectFlag,
		writesState: reportWritesState,
		run:         runReport,
	}
}

func registerReportVerbFlags(c *runtimeCtx) {
	o := &reportOpts{}
	c.opts = o
	registerReportFlags(c.fs, o, false)
}

func registerReportFlags(fs *flag.FlagSet, o *reportOpts, withSubverbs bool) {
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
		fs.BoolVar(&o.jsonOut, "json", false, "write JSON instead of HTML (stdout unless --out)")
	}
	fs.StringVar(&o.since, "since", "", p("show, report, evals", "the window's start, `YYYY-MM-DD` (default 30 days ago)"))
	fs.StringVar(&o.sinceTag, "since-tag", "", p("show, report", "window start at git tag `TAG`"))
	fs.StringVar(&o.until, "until", "", p("show, report, evals", "the window's end, `YYYY-MM-DD`, exclusive (default now)"))
	fs.StringVar(&o.project, "project", "", project)
	fs.BoolVar(&o.global, "global", false, p("report", "all projects across the local store"))
	fs.BoolVar(&o.md, "md", false, p("report", "write Markdown instead of HTML (stdout unless --out)"))
	fs.BoolVar(&o.dailyDigest, "daily-digest", false, p("report", "compute, store and print today's daily health digest"))
	fs.BoolVar(&o.web, "web", false, p("report", "serve the rendered report in memory on 127.0.0.1:0 in the foreground"))
	fs.StringVar(&o.listen, "listen", "", p("report", "with --web, bind `ADDR` instead of 127.0.0.1:0"))
	fs.BoolVar(&o.noIngest, "no-ingest", false, p("report", "skip incremental metrics ingest before rendering"))
	fs.BoolVar(&o.includeSandboxes, "include-sandboxes", false, p("show, report", "include ephemeral eval, smoke, probe and scratch roots"))
	fs.BoolVar(&o.allLabels, "all-labels", false, p("show, report", "include labelled (sandbox) sessions"))
	fs.Int64Var(&o.contextTarget, "context-target", 0, p("show, report", "the peak context, `N` tokens, a main session should stay under (default 300000)"))
	fs.StringVar(&o.out, "out", "", out)
	fs.StringVar(&o.label, "label", "", label)
}

func reportWritesState(c *runtimeCtx) bool {
	o := reportOptsOf(c)
	return !o.noIngest || o.dailyDigest
}

func validateMetricsProjectFlag(c *runtimeCtx) (bool, error) {
	projectVisited := false
	c.fs.Visit(func(f *flag.Flag) { projectVisited = projectVisited || f.Name == "project" })
	proj := reportOptsOf(c).project
	if projectVisited && (proj == "" || proj == missingProjectSentinel || strings.HasPrefix(proj, "-")) {
		fmt.Fprintf(c.out, "fyr %s: --project needs a directory: --project <dir>\n", c.cmd)
		return true, exitCode(2)
	}
	return false, nil
}

// runReport serves the top-level `fyr report` verb (ADR-516): inside a git
// checkout with neither --project nor --global, it defaults to --project at
// the checkout root; outside a git checkout, it defaults to --global.
func runReport(c *runtimeCtx) error {
	ro := reportOptsOf(c)
	if ro.project == "" && !ro.global {
		if wd, err := c.projectDir(); err == nil {
			if root, _, err := trail.Roots(wd); err == nil {
				ro.project = root
			} else {
				ro.global = true
			}
		} else {
			ro.global = true
		}
	}
	return c.renderReport("fyr report")
}

// renderReport executes the shared report pipeline for `fyr report` and
// `fyr metrics report`.
func (c *runtimeCtx) renderReport(prefix string) error {
	ro := reportOptsOf(c)
	o, err := c.metricsShowOptions()
	if err != nil {
		return usageErr(c.out, c.cmd, prefix+": "+err.Error())
	}
	if !ro.noIngest {
		r, ierr := ingestInto(metrics.Options{Home: c.home}, io.Discard)
		if ierr != nil {
			fmt.Fprintf(stderr, "%s: warning: auto-ingest failed: %v\n", prefix, ierr)
		} else if len(r.Failed) > 0 {
			fmt.Fprintf(stderr, "%s: warning: auto-ingest had %d failed source(s)\n", prefix, len(r.Failed))
		}
	}
	s, err := c.existingMetricsStore()
	if err != nil {
		return err
	}
	defer s.Close()
	if ro.dailyDigest {
		return c.renderDailyDigestReport(s, o)
	}
	sum, err := s.Show(o)
	if err != nil {
		return err
	}
	if strings.TrimSpace(o.Project) == "" {
		if dd, derr := metrics.LatestDailyDigest(s); derr == nil && dd != nil {
			sum.DailyDigest = dd
		}
	}
	evals, err := metrics.ReportEvalsWithDevFallback(s, c.home, o)
	if err != nil {
		return err
	}
	now := time.Now()
	if ro.web {
		return c.serveReportWeb(sum, evals, now)
	}
	return c.writeOrPrintReport(sum, evals, now)
}

func (c *runtimeCtx) renderDailyDigestReport(s *metrics.Store, o metrics.ShowOptions) error {
	now := time.Now()
	var (
		d   metrics.DailyDigest
		err error
	)
	if strings.TrimSpace(o.Project) == "" {
		d, err = refreshDailyDigest(s, c.home, c.resolveAutoRepo(), now)
	} else {
		d, err = metrics.BuildDailyDigest(s, c.home, o.Project, now)
	}
	if err != nil {
		return err
	}
	if reportOptsOf(c).jsonOut {
		return metrics.RenderDailyDigestJSON(c.out, d)
	}
	_, err = io.WriteString(c.out, metrics.RenderDailyDigestText(d))
	return err
}

func (c *runtimeCtx) writeOrPrintReport(sum *metrics.Summary, evals *metrics.EvalRows, now time.Time) error {
	ro := reportOptsOf(c)
	if ro.jsonOut {
		if ro.out != "" {
			if err := metrics.WriteJSONReport(sum, evals, ro.out, now); err != nil {
				return err
			}
			fmt.Fprintln(c.out, "metrics: report written to "+ro.out)
			return nil
		}
		return metrics.RenderJSONReport(c.out, sum, evals, now)
	}
	if ro.md {
		if ro.out != "" {
			if err := metrics.WriteMarkdownReport(sum, evals, ro.out, now); err != nil {
				return err
			}
			fmt.Fprintln(c.out, "metrics: report written to "+ro.out)
			return nil
		}
		return metrics.RenderMarkdownReport(c.out, sum, evals, now)
	}
	file := ro.out
	if file == "" {
		file = metrics.ReportPath(c.home, now)
	}
	if err := metrics.WriteReport(sum, evals, file, now); err != nil {
		return err
	}
	fmt.Fprintln(c.out, "metrics: report written to "+file)
	return nil
}

func (c *runtimeCtx) serveReportWeb(sum *metrics.Summary, evals *metrics.EvalRows, now time.Time) error {
	var htmlBuf, mdBuf, jsonBuf bytes.Buffer
	if err := metrics.RenderReport(&htmlBuf, sum, evals, now); err != nil {
		return err
	}
	if err := metrics.RenderMarkdownReport(&mdBuf, sum, evals, now); err != nil {
		return err
	}
	if err := metrics.RenderJSONReport(&jsonBuf, sum, evals, now); err != nil {
		return err
	}
	htmlBytes := htmlBuf.Bytes()
	assets := map[string]webpreview.Asset{
		"/":            {ContentType: "text/html; charset=utf-8", Body: htmlBytes, CSP: reportWebHTMLCSP},
		"/report.html": {ContentType: "text/html; charset=utf-8", Body: htmlBytes, CSP: reportWebHTMLCSP},
		"/report.md":   {ContentType: "text/markdown; charset=utf-8", Body: mdBuf.Bytes(), CSP: "default-src 'none'"},
		"/report.json": {ContentType: "application/json; charset=utf-8", Body: jsonBuf.Bytes(), CSP: "default-src 'none'"},
	}
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveWebPreview(sigCtx, assets, webpreview.Options{
		Listen:     reportOptsOf(c).listen,
		DefaultCSP: reportWebHTMLCSP,
		Out:        c.out,
		Err:        stderr,
	})
}

const (
	bareProjectSentinel    = "\x00cwd"
	missingProjectSentinel = "\x00missing-project"
)

// resolveMetricsProject resolves --project for show and report: bare --project
// on report (bareProjectSentinel) requires a git checkout at the working
// directory (or -C), while an explicit path is made absolute and canonicalized
// to its checkout root when inside one.
func (c *runtimeCtx) resolveMetricsProject() (string, error) {
	p := reportOptsOf(c).project
	if p == bareProjectSentinel {
		wd, err := c.projectDir()
		if err != nil {
			return "", err
		}
		root, _, err := trail.Roots(wd)
		if err != nil {
			return "", errors.New("not in a git checkout; pass --project DIR or --global")
		}
		return root, nil
	}
	if !filepath.IsAbs(p) && c.dirFlag != "" {
		wd, err := c.projectDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(wd, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if root, _, err := trail.Roots(abs); err == nil {
		return root, nil
	}
	return abs, nil
}

func (c *runtimeCtx) resolveMetricsWindow(o *metrics.ShowOptions) error {
	ro := reportOptsOf(c)
	var sinceGiven, sinceTagGiven bool
	c.fs.Visit(func(f *flag.Flag) {
		sinceGiven = sinceGiven || f.Name == "since"
		sinceTagGiven = sinceTagGiven || f.Name == "since-tag"
	})
	if (sinceGiven || ro.since != "") && (sinceTagGiven || ro.sinceTag != "") {
		return errors.New("--since and --since-tag exclude each other")
	}
	since, until, err := c.metricsWindow()
	if err != nil {
		return err
	}
	if sinceTagGiven || ro.sinceTag != "" {
		tag, tagTime, err := c.resolveSinceTag(o.Project)
		if err != nil {
			return err
		}
		if !until.IsZero() && !until.After(tagTime) {
			return errors.New("--until must be after --since-tag")
		}
		since, o.SinceTag = tagTime, tag
	}
	o.Since, o.Now = since, until
	return nil
}

func (c *runtimeCtx) resolveSinceTag(project string) (string, time.Time, error) {
	tag, err := validateSinceTag(reportOptsOf(c).sinceTag)
	if err != nil {
		return "", time.Time{}, err
	}
	repoRoot, err := c.resolveTagRepoRoot(project)
	if err != nil {
		return "", time.Time{}, err
	}
	out, err := trail.Command(repoRoot, "log", "-1", "--format=%ct", "refs/tags/"+tag, "--").Output()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("--since-tag %q: tag not found", tag)
	}
	rawTS := strings.TrimSpace(string(out))
	sec, err := strconv.ParseInt(rawTS, 10, 64)
	if err != nil || sec < 0 || rawTS == "" {
		return "", time.Time{}, fmt.Errorf("--since-tag %q: unresolvable tag commit time", tag)
	}
	return tag, time.Unix(sec, 0).UTC(), nil
}

func (c *runtimeCtx) resolveTagRepoRoot(project string) (string, error) {
	if project != "" {
		root, _, err := trail.Roots(project)
		if err != nil {
			return "", fmt.Errorf("--since-tag: %s is not a git checkout", project)
		}
		return root, nil
	}
	wd, err := c.projectDir()
	if err != nil {
		return "", err
	}
	root, _, err := trail.Roots(wd)
	if err != nil {
		return "", errors.New("--since-tag: not in a git checkout")
	}
	return root, nil
}

func validateSinceTag(raw string) (string, error) {
	tag := strings.TrimSpace(raw)
	if tag == "" {
		return "", errors.New("--since-tag takes a tag name")
	}
	bad := strings.HasPrefix(tag, "-") || strings.ContainsAny(tag, " \t\r\n^~:?*[\\") ||
		strings.Contains(tag, "..") || strings.Contains(tag, "@{") ||
		strings.HasPrefix(tag, "/") || strings.HasSuffix(tag, "/") || strings.HasSuffix(tag, ".lock")
	if bad {
		return "", fmt.Errorf("--since-tag %q: invalid tag name", tag)
	}
	return tag, nil
}
