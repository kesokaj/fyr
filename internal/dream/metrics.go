package dream

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kesokaj/fyr/internal/metrics"
)

// ingestMetrics reads every session's shape into the metrics store before
// the dream (R3, ADR-055): store-wide, whatever --project names, so nothing
// expires unread. It returns ingest's summary line; a source that failed is
// an error carrying that line. A variable so a test can make it fail.
var ingestMetrics = func(home string) (string, error) {
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		return "", err
	}
	defer s.Close()
	r, err := metrics.Ingest(s, metrics.Options{Home: home}, io.Discard)
	if err != nil {
		return "", err
	}
	if len(r.Failed) > 0 {
		return "", fmt.Errorf("%s", r.Line())
	}
	return r.Line(), nil
}

// ingestFirst runs ingestMetrics and logs its outcome. It never fails the
// dream: the metrics are a side record, and a dream that stopped for them
// would also stop the proposals (D16).
func (o Options) ingestFirst(logf func(string)) {
	line, err := ingestMetrics(o.Home)
	if err != nil {
		logf("metrics ingest failed: " + scrub(err.Error(), o.Home))
		return
	}
	logf(line)
}

// metricsSummary is the numbers-only summary of root's agent metrics for the
// window [since, now] and the window of the same length before it, read from
// a copy of the metrics store (R3's addendum, ADR-055a). No store, or no
// session of root in either window: "" with no error. A variable so a test
// can replace it.
var metricsSummary = func(home, root string, since, now time.Time) (string, error) {
	digest, _ := metrics.ReadLatestDailyDigest(home)
	path := metrics.Path(home)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if digest == nil {
			return "", nil
		}
		b, err := json.MarshalIndent(map[string]any{"daily_digest": digest}, "", " ")
		return scrub(string(b), home), err
	}
	return buildMetricsStoreSummary(home, root, path, since, now, digest)
}

func buildMetricsStoreSummary(home, root, path string, since, now time.Time, digest *metrics.DailyDigest) (string, error) {
	tmp, err := os.MkdirTemp(filepath.Dir(path), "dream-metrics-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	s, err := metrics.Snapshot(path, tmp)
	if err != nil {
		return "", err
	}
	defer s.Close()
	cur, err := s.Show(metrics.ShowOptions{Home: home, Since: since, Now: now, Project: root})
	if err != nil {
		return "", err
	}
	prev, err := s.Show(metrics.ShowOptions{Home: home, Since: since.Add(-now.Sub(since)), Now: since, Project: root})
	if err != nil {
		return "", err
	}
	diag := metrics.BuildDiagnosticReport(*cur, nil, "project", root)
	if countWindowSessions(cur, prev) == 0 && len(diag.Signals) == 0 && digest == nil {
		return "", nil
	}
	return encodeMetricsSummaryJSON(home, since, now, cur, prev, diag, digest)
}

// Every column counts, the historical one too: its sessions are the
// project's record as much as the agy ones (plan fyr D7).
func countWindowSessions(cur, prev *metrics.Summary) int {
	n := 0
	for _, m := range []map[string]int{cur.Sessions, prev.Sessions} {
		for _, c := range m {
			n += c
		}
	}
	return n
}

func encodeMetricsSummaryJSON(home string, since, now time.Time, cur, prev *metrics.Summary, diag metrics.DiagnosticReport, digest *metrics.DailyDigest) (string, error) {
	payload := map[string]any{
		"window":      map[string]string{"since": since.UTC().Format(time.RFC3339), "until": now.UTC().Format(time.RFC3339)},
		"current":     cur.Numbers(),
		"previous":    prev.Numbers(),
		"diagnostics": diag,
	}
	if digest != nil {
		payload["daily_digest"] = digest
	}
	b, err := json.MarshalIndent(payload, "", " ")
	return scrub(string(b), home), err
}
