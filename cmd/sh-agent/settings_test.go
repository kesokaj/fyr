package main

import (
	"testing"

	"github.com/kesokaj/styrman-harness/internal/harness"
	"github.com/kesokaj/styrman-harness/internal/metrics"
)

// The backstop window install writes and the metric's context target are one
// number (plan session-sized-work D4, D27): the report's over-target count and
// the CLI's compaction point are the same line. internal/harness cannot import
// internal/metrics (the SQLite driver stays out of the installer, R4), so the
// two constants are pinned equal here, where both packages are already linked.
func TestAutoCompactWindowIsTheContextTarget(t *testing.T) {
	if harness.AutoCompactWindow != metrics.DefaultContextTarget {
		t.Fatalf("harness.AutoCompactWindow = %d, metrics.DefaultContextTarget = %d; one number (D27)", harness.AutoCompactWindow, metrics.DefaultContextTarget)
	}
}
