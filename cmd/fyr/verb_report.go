package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/trail"
)

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
		fmt.Fprintln(c.out, prefix+": "+err.Error())
		return exitCode(2)
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
	evals, err := reportEvals(s, o)
	if err != nil {
		return err
	}
	now := time.Now()
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
