package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
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
	sum, err := s.Show(o)
	if err != nil {
		return err
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
