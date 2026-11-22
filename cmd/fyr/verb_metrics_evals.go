package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/kesokaj/fyr/internal/metrics"
)

// metricsEvals serves `metrics evals`: the per-row view of the suite's
// trials in the store, as text or --json (SPEC-fyr-metrics § Interfaces).
// A trial belongs to no project, so --project is refused with the other
// flags evals does not read (PLAN.md D16).
func (c *ctx) metricsEvals(args []string) error {
	if bad := c.metricsFlagsOutside("since", "until", "harness", "json"); bad != "" {
		return usageErr(c.out, "metrics", fmt.Sprintf("fyr metrics evals: --%s does not apply to evals", bad))
	}
	if len(args) > 0 {
		return usageErr(c.out, "metrics", fmt.Sprintf("metrics evals takes no arguments: %q", args))
	}
	since, until, err := c.metricsWindow()
	if err != nil {
		return usageErr(c.out, "metrics", "fyr metrics evals: "+err.Error())
	}
	opts := metrics.EvalOptions{Since: since, Now: until, Harness: c.metricsHarness}
	e, err := c.loadMetricsEvalsWithFallback(opts)
	if err != nil {
		return err
	}
	if c.metricsJSON {
		return e.WriteJSON(c.out)
	}
	e.WriteText(c.out)
	return nil
}

func (c *ctx) loadMetricsEvalsWithFallback(opts metrics.EvalOptions) (*metrics.EvalRows, error) {
	if _, err := os.Lstat(metrics.Path(c.home)); errors.Is(err, os.ErrNotExist) {
		if devStore, cleanup, ok := metrics.OpenDevEvalFallbackRO(c.home); ok {
			defer cleanup()
			return devStore.Evals(opts)
		}
	}
	s, err := c.existingMetricsStore()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	e, err := s.Evals(opts)
	if err != nil || len(e.Rows) > 0 {
		return e, err
	}
	if devStore, cleanup, ok := metrics.OpenDevEvalFallbackRO(c.home); ok {
		defer cleanup()
		if devE, derr := devStore.Evals(opts); derr == nil && len(devE.Rows) > 0 {
			return devE, nil
		}
	}
	return e, nil
}
