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

// runReport serves the top-level `fyr report` verb (ADR-516): inside a git
// checkout with neither --project nor --global, it defaults to --project at
// the checkout root; outside a git checkout, it defaults to --global.
func runReport(c *ctx) error {
	if c.metricsProject == "" && !c.global {
		if wd, err := c.projectDir(); err == nil {
			if root, _, err := trail.Roots(wd); err == nil {
				c.metricsProject = root
			} else {
				c.global = true
			}
		} else {
			c.global = true
		}
	}
	return c.renderReport("fyr report")
}

// renderReport executes the shared report pipeline for `fyr report` and
// `fyr metrics report`.
func (c *ctx) renderReport(prefix string) error {
	o, err := c.metricsShowOptions()
	if err != nil {
		return usageErr(c.out, c.cmd, prefix+": "+err.Error())
	}
	s, err := c.existingMetricsStore()
	if err != nil {
		return err
	}
	defer s.Close()
	if c.metricsDailyDigest {
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
	if c.metricsWeb {
		return c.serveReportWeb(sum, evals, now)
	}
	return c.writeOrPrintReport(sum, evals, now)
}

func (c *ctx) renderDailyDigestReport(s *metrics.Store, o metrics.ShowOptions) error {
	d, err := metrics.BuildDailyDigest(s, c.home, o.Project, time.Now())
	if err != nil {
		return err
	}
	if strings.TrimSpace(o.Project) == "" {
		if err := metrics.SaveDailyDigest(s, d); err != nil {
			return err
		}
		if err := metrics.WriteLatestDailyDigest(c.home, d); err != nil {
			return err
		}
	}
	if c.metricsJSON {
		return metrics.RenderDailyDigestJSON(c.out, d)
	}
	_, err = io.WriteString(c.out, metrics.RenderDailyDigestText(d))
	return err
}

func (c *ctx) writeOrPrintReport(sum *metrics.Summary, evals *metrics.EvalRows, now time.Time) error {
	if c.metricsJSON {
		if c.metricsOut != "" {
			if err := metrics.WriteJSONReport(sum, evals, c.metricsOut, now); err != nil {
				return err
			}
			fmt.Fprintln(c.out, "metrics: report written to "+c.metricsOut)
			return nil
		}
		return metrics.RenderJSONReport(c.out, sum, evals, now)
	}
	if c.metricsMD {
		if c.metricsOut != "" {
			if err := metrics.WriteMarkdownReport(sum, evals, c.metricsOut, now); err != nil {
				return err
			}
			fmt.Fprintln(c.out, "metrics: report written to "+c.metricsOut)
			return nil
		}
		return metrics.RenderMarkdownReport(c.out, sum, evals, now)
	}
	file := c.metricsOut
	if file == "" {
		file = metrics.ReportPath(c.home, now)
	}
	if err := metrics.WriteReport(sum, evals, file, now); err != nil {
		return err
	}
	fmt.Fprintln(c.out, "metrics: report written to "+file)
	return nil
}

func (c *ctx) serveReportWeb(sum *metrics.Summary, evals *metrics.EvalRows, now time.Time) error {
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
		Listen:     c.metricsListen,
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
func (c *ctx) resolveMetricsProject() (string, error) {
	if c.metricsProject == bareProjectSentinel {
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
	p := c.metricsProject
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

func (c *ctx) resolveMetricsWindow(o *metrics.ShowOptions) error {
	var sinceGiven, sinceTagGiven bool
	c.fs.Visit(func(f *flag.Flag) {
		sinceGiven = sinceGiven || f.Name == "since"
		sinceTagGiven = sinceTagGiven || f.Name == "since-tag"
	})
	if (sinceGiven || c.metricsSince != "") && (sinceTagGiven || c.metricsSinceTag != "") {
		return errors.New("--since and --since-tag exclude each other")
	}
	since, until, err := c.metricsWindow()
	if err != nil {
		return err
	}
	if sinceTagGiven || c.metricsSinceTag != "" {
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

func (c *ctx) resolveSinceTag(project string) (string, time.Time, error) {
	tag, err := validateSinceTag(c.metricsSinceTag)
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

func (c *ctx) resolveTagRepoRoot(project string) (string, error) {
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
