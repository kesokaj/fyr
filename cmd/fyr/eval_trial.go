package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// trialTextMax is the longest harness or cli value metrics stores on a
// trial (its trialText).
const trialTextMax = 120

// harnessKey names the harness a run's trials install (the eval-metrics
// plan D15): the install source's short HEAD with " (dirty)" when what an
// install reads (harness.ManagedDirs) has changes, so the trail a run's own
// testing record writes leaves the key alone (PLAN.md item 53), " (dirty:
// unknown)" when that cannot be read, "unknown" for
// a source that is not a git checkout, or "embedded <version>" for the
// harness built into a release binary ("embedded" when it has none). Two
// dirty trees at one HEAD share a key, and so do two binaries built from
// one HEAD with different cmd/ or internal/ edits: stated limits
// (SPEC-fyr-eval § Known limits).
func harnessKey(src harness.Source) string {
	if src.Dir == "" {
		return trialText(strings.TrimSpace("embedded " + src.Version))
	}
	key := gitShort(src.Dir)
	if key == "unknown" {
		return key
	}
	if dirty, err := trail.DirtyUnder(src.Dir, harness.ManagedDirs); err != nil {
		key += " (dirty: unknown)"
	} else if dirty {
		key += " (dirty)"
	}
	return trialText(key)
}

// trialText is s as a trial stores it: printable, at most trialTextMax
// bytes, cut on a rune boundary.
func trialText(s string) string {
	s = search.Printable(s)
	if len(s) <= trialTextMax {
		return s
	}
	n := trialTextMax
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// trialOf is the trials row of one result of suite row row, under the run's
// harness key and the CLI's version line: the row's version, kind and
// cluster as parsed, defaults applied, and its expect list (ADR-085).
func trialOf(r eval.Result, row eval.Row, key, cli string) metrics.Trial {
	t := metrics.Trial{
		Run: r.Run, Label: "eval=" + r.Row, Harness: key, CLI: trialText(cli),
		Status: map[string]string{eval.StatusPass: "pass", eval.StatusFail: "fail", eval.StatusError: "error"}[r.Status],
		Cause:  r.Cause, StartedAt: r.Started, Seconds: int64(r.Seconds),
		RowVersion: row.Version, Kind: row.Kind, Cluster: row.Cluster, Expect: row.Expect,
	}
	if r.SetupSeconds >= 0 {
		s := int64(r.SetupSeconds)
		t.SetupSeconds = &s
	}
	if r.SessionSeconds >= 0 {
		s := int64(r.SessionSeconds)
		t.SessionSeconds = &s
	}
	if r.CheckSeconds >= 0 {
		s := int64(r.CheckSeconds)
		t.CheckSeconds = &s
	}
	return t
}

// recordTrial stores t in home's metrics store, once (INSERT OR IGNORE).
// Asked first, as ingestRow asks: the trial's agent may have linked
// --home's state to the real one since the start check.
func recordTrial(home string, t metrics.Trial) error {
	if real, shared := sharesRealState(home); shared {
		return fmt.Errorf("not recorded: --home now shares the real state of %s", real)
	}
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		return err
	}
	defer s.Close()
	return s.RecordTrial(t)
}
