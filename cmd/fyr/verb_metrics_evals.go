package main

import (
	"fmt"

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
	s, err := c.existingMetricsStore()
	if err != nil {
		return err
	}
	defer s.Close()
	e, err := s.Evals(metrics.EvalOptions{Since: since, Now: until, Harness: c.metricsHarness})
	if err != nil {
		return err
	}
	if c.metricsJSON {
		return e.WriteJSON(c.out)
	}
	e.WriteText(c.out)
	return nil
}

// reportEvals is the per-row view a report shows under its pass^k chart,
// over the report's window; none under --project, since a trial belongs to
// no project (PLAN.md D16).
func reportEvals(s *metrics.Store, o metrics.ShowOptions) (*metrics.EvalRows, error) {
	if o.Project != "" {
		return nil, nil
	}
	return s.Evals(metrics.EvalOptions{Since: o.Since, Now: o.Now})
}
