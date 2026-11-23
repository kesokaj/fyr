package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kesokaj/fyr/internal/metrics"
)

const maxMetricsRunJSONIn = 64 * 1024

// metricsRecordRun serves `fyr metrics record-run --json-in [--json]` (F-032),
// validating and persisting one shape-only OrchestrationRunRow into metrics.db.
func (c *ctx) metricsRecordRun(args []string) error {
	if bad := c.metricsFlagsOutside("json-in", "json"); bad != "" {
		return usageErr(c.out, "metrics", fmt.Sprintf("fyr metrics record-run: --%s does not apply to record-run", bad))
	}
	if len(args) > 0 {
		return usageErr(c.out, "metrics", fmt.Sprintf("metrics record-run takes no arguments: %q", args))
	}
	if !c.jsonIn {
		return usageErr(c.out, "metrics", "fyr metrics record-run requires --json-in")
	}
	row, err := decodeOrchestrationRunJSON(stdin)
	if err != nil {
		return usageErr(c.out, "metrics", "fyr metrics record-run: "+err.Error())
	}
	if err := metrics.RecordOrchestrationRun(c.home, row); err != nil {
		if errors.Is(err, metrics.ErrInvalidOrchestrationRun) {
			return usageErr(c.out, "metrics", "fyr metrics record-run: "+err.Error())
		}
		return err
	}
	return c.writeRecordedRunAck(row)
}

func decodeOrchestrationRunJSON(r io.Reader) (metrics.OrchestrationRunRow, error) {
	if r == nil {
		return metrics.OrchestrationRunRow{}, errors.New("invalid --json-in payload: empty input")
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxMetricsRunJSONIn+1))
	if err != nil {
		return metrics.OrchestrationRunRow{}, err
	}
	if len(raw) > maxMetricsRunJSONIn {
		return metrics.OrchestrationRunRow{}, fmt.Errorf("invalid --json-in payload: exceeds %d bytes", maxMetricsRunJSONIn)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return metrics.OrchestrationRunRow{}, errors.New("invalid --json-in payload: expected a JSON object")
	}
	var row metrics.OrchestrationRunRow
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&row); err != nil {
		return metrics.OrchestrationRunRow{}, fmt.Errorf("invalid --json-in payload: %w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return metrics.OrchestrationRunRow{}, errors.New("invalid --json-in payload: trailing data after JSON object")
	}
	return row, nil
}

func (c *ctx) writeRecordedRunAck(row metrics.OrchestrationRunRow) error {
	id := strings.TrimSpace(row.ID)
	kind := strings.ToLower(strings.TrimSpace(row.Kind))
	status := strings.TrimSpace(row.Status)
	if c.metricsJSON {
		enc := json.NewEncoder(c.out)
		return enc.Encode(map[string]any{
			"id":     id,
			"kind":   kind,
			"status": status,
		})
	}
	fmt.Fprintf(c.out, "metrics: recorded orchestration run %s (%s %s)\n", id, kind, status)
	return nil
}
