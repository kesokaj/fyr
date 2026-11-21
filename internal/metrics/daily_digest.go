package metrics

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/search"
)

const (
	VerdictImproving = "improving"
	VerdictSteady    = "steady"
	VerdictRegress   = "regressing"

	StatusPendingData     = "pending_data"
	StatusRecovered       = "recovered"
	StatusStillRegressing = "still_regressing"

	minRecoverySessions = 2
)

// DailyDigest holds one strictly shape-only daily quality digest snapshot.
type DailyDigest struct {
	Day               string                   `json:"day"`
	Verdict           string                   `json:"verdict"`
	CreatedAt         int64                    `json:"created_at"`
	Rolling7d         RollingKPIDeltas         `json:"rolling_7d"`
	ASTSnapshot       search.StructuralSignals `json:"ast_snapshot"`
	ASTDiff           ASTStructuralDiff        `json:"ast_diff"`
	PreInvocation     PreInvocationSnapshot    `json:"pre_invocation"`
	PreInvocationDiff PreInvocationDiff        `json:"pre_invocation_diff"`
	Actions           []DigestAppliedAction    `json:"actions"`
	Signals           []DigestAction           `json:"signals"`
	NewRegressions    []string                 `json:"new_regressions,omitempty"`
	ResolvedSignals   []string                 `json:"resolved_signals,omitempty"`
	PersistentHigh    []string                 `json:"persistent_high,omitempty"`
}

// RollingKPIDeltas holds 7-day vs prior-7-day behavioral KPI deltas.
type RollingKPIDeltas struct {
	Sessions             MetricDelta  `json:"sessions"`
	SearchGrepRatio      *MetricDelta `json:"search_grep_ratio,omitempty"`
	BashReadSkips        MetricDelta  `json:"bash_read_skips"`
	RedundantReadRatePct *MetricDelta `json:"redundant_read_rate_pct,omitempty"`
	ToolErrorRatePct     *MetricDelta `json:"tool_error_rate_pct,omitempty"`
	PreInvTimeoutRatePct *MetricDelta `json:"pre_invocation_timeout_rate_pct,omitempty"`
	PreInvP50MS          *MetricDelta `json:"pre_invocation_p50_ms,omitempty"`
	PreInvP95MS          *MetricDelta `json:"pre_invocation_p95_ms,omitempty"`
}

// ASTStructuralDiff compares today's AST snapshot against the prior digest day.
type ASTStructuralDiff struct {
	HasPrevious             bool    `json:"has_previous"`
	HiddenCouplingEditPairs int     `json:"hidden_coupling_edit_pairs"`
	HiddenCouplingReadPairs int     `json:"hidden_coupling_read_pairs"`
	ProdPkgCycles           int     `json:"prod_pkg_cycles"`
	LargeFilesOver350       int     `json:"large_files_over_350"`
	HubConcentration        float64 `json:"hub_concentration"`
}

// PreInvocationSnapshot summarizes PreInvocation hook latency and timeout rates.
type PreInvocationSnapshot struct {
	TurnVets          int      `json:"turn_vets"`
	TimedOutTurns     int      `json:"timed_out_turns"`
	TimeoutRatePct    *float64 `json:"timeout_rate_pct,omitempty"`
	P50DurationMS     *float64 `json:"p50_duration_ms,omitempty"`
	P95DurationMS     *float64 `json:"p95_duration_ms,omitempty"`
	MeanInjectedBytes *float64 `json:"mean_injected_bytes,omitempty"`
}

// PreInvocationDiff compares today's PreInvocation snapshot against the prior day.
type PreInvocationDiff struct {
	HasPrevious            bool     `json:"has_previous"`
	TurnVetsDelta          int      `json:"turn_vets_delta"`
	TimedOutTurnsDelta     int      `json:"timed_out_turns_delta"`
	TimeoutRatePctDelta    *float64 `json:"timeout_rate_pct_delta,omitempty"`
	P50DurationMSDelta     *float64 `json:"p50_duration_ms_delta,omitempty"`
	P95DurationMSDelta     *float64 `json:"p95_duration_ms_delta,omitempty"`
	MeanInjectedBytesDelta *float64 `json:"mean_injected_bytes_delta,omitempty"`
}

// DigestAppliedAction tracks an auto commit/proposal and its recovery status.
type DigestAppliedAction struct {
	SignalID       string   `json:"signal_id"`
	ReportDate     string   `json:"report_date"`
	ActionScope    string   `json:"action_scope"`
	Status         string   `json:"status"`
	InstallPending bool     `json:"install_pending,omitempty"`
	RevertCmd      string   `json:"revert_cmd,omitempty"`
	Summary        string   `json:"summary,omitempty"`
	AppliedSHA     string   `json:"applied_sha,omitempty"`
	Metric         string   `json:"metric,omitempty"`
	BaselineValue  *float64 `json:"baseline_value,omitempty"`
	AppliedAt      int64    `json:"applied_at,omitempty"`
}

// DigestAction is one shape-only prioritized action item from diagnostics.
type DigestAction struct {
	SignalID          string `json:"signal_id"`
	Severity          string `json:"severity"`
	Rule              string `json:"rule"`
	ActionScope       string `json:"action_scope"`
	RecommendedAction string `json:"recommended_action"`
	Summary           string `json:"summary"`
}

// BuildDailyDigest computes today's shape-only DailyDigest from store, brain.db, and prior snapshots.
func BuildDailyDigest(store *Store, home, repoRoot string, now time.Time) (DailyDigest, error) {
	if store == nil || store.db == nil {
		return DailyDigest{}, errors.New("metrics: nil store")
	}
	if now.IsZero() {
		now = time.Now()
	}
	nowUTC := now.UTC()
	day := nowUTC.Format("2006-01-02")
	since7 := nowUTC.Add(-7 * 24 * time.Hour)
	curSum, err := store.Show(ShowOptions{Home: home, Project: repoRoot, Since: since7, Now: nowUTC})
	if err != nil {
		return DailyDigest{}, err
	}
	prevSum, err := store.Show(ShowOptions{Home: home, Project: repoRoot, Since: since7.Add(-7 * 24 * time.Hour), Now: since7, skipDeltas: true})
	if err != nil {
		return DailyDigest{}, err
	}
	var prevDigest, todayDigest *DailyDigest
	if strings.TrimSpace(repoRoot) == "" {
		prevDigest, err = PreviousDailyDigest(store, day)
		if err != nil {
			return DailyDigest{}, err
		}
		todayDigest, _ = dailyDigestByDay(store, day)
	}
	return assembleDailyDigest(day, nowUTC, home, repoRoot, curSum, prevSum, prevDigest, todayDigest), nil
}

func assembleDailyDigest(day string, nowUTC time.Time, home, repoRoot string, curSum, prevSum *Summary, prevDigest, todayDigest *DailyDigest) DailyDigest {
	astSnap, preSnap := curSum.Health.Structural, preInvFromSummary(curSum)
	astDiff, preDiff := buildASTDiff(astSnap, prevDigest), buildPreInvDiff(preSnap, prevDigest)
	signals := collectDigestSignals(curSum, astSnap, astDiff, repoRoot)
	actions := collectDigestActions(home, repoRoot, day, curSum, prevDigest, todayDigest, signals)
	newReg, resolved, persistHigh := computeSignalTransitions(signals, prevDigest)
	d := DailyDigest{
		Day: day, CreatedAt: nowUTC.UnixMilli(),
		Rolling7d:   buildRolling7d(curSum, prevSum),
		ASTSnapshot: astSnap, ASTDiff: astDiff,
		PreInvocation: preSnap, PreInvocationDiff: preDiff,
		Actions: actions, Signals: signals,
		NewRegressions: newReg, ResolvedSignals: resolved, PersistentHigh: persistHigh,
	}
	d.Verdict = computeDigestVerdict(d)
	return d
}

func computeSignalTransitions(signals []DigestAction, prev *DailyDigest) (newReg, resolved, persistHigh []string) {
	if prev == nil {
		return nil, nil, nil
	}
	prevByID := make(map[string]DigestAction, len(prev.Signals))
	for _, s := range prev.Signals {
		prevByID[s.SignalID] = s
	}
	curByID := make(map[string]bool, len(signals))
	for _, s := range signals {
		curByID[s.SignalID] = true
		p, existed := prevByID[s.SignalID]
		if !existed {
			newReg = append(newReg, s.SignalID)
		} else if s.Severity == "high" && p.Severity == "high" {
			persistHigh = append(persistHigh, s.SignalID)
		}
	}
	for _, s := range prev.Signals {
		if !curByID[s.SignalID] {
			resolved = append(resolved, s.SignalID)
		}
	}
	sort.Strings(newReg)
	sort.Strings(resolved)
	sort.Strings(persistHigh)
	return newReg, resolved, persistHigh
}

func buildRolling7d(cur, prev *Summary) RollingKPIDeltas {
	r := RollingKPIDeltas{
		Sessions:             newMetricDelta(float64(cur.Sessions["agy"]), float64(prev.Sessions["agy"])),
		SearchGrepRatio:      ptrOptionalDelta(cur.C.SearchGrepRatio["agy"], prev.C.SearchGrepRatio["agy"], 1),
		BashReadSkips:        newMetricDelta(float64(cur.C.BashReadSkips["agy"]), float64(prev.C.BashReadSkips["agy"])),
		ToolErrorRatePct:     ptrOptionalDelta(cur.D.ToolErrorRate["agy"], prev.D.ToolErrorRate["agy"], 100),
		PreInvTimeoutRatePct: ptrOptionalDelta(cur.E.TimeoutRatePct, prev.E.TimeoutRatePct, 1),
		PreInvP50MS:          ptrOptionalDelta(cur.E.P50DurationMS, prev.E.P50DurationMS, 1),
		PreInvP95MS:          ptrOptionalDelta(cur.E.P95DurationMS, prev.E.P95DurationMS, 1),
	}
	curRR, _, _, ok1 := repeatReadsCounts(cur)
	prevRR, _, _, ok2 := repeatReadsCounts(prev)
	if ok1 && ok2 {
		r.RedundantReadRatePct = ptrMetricDelta(curRR, prevRR)
	}
	return r
}

func preInvFromSummary(sum *Summary) PreInvocationSnapshot {
	return PreInvocationSnapshot{
		TurnVets: sum.E.TurnVets, TimedOutTurns: sum.E.TimedOutTurns,
		TimeoutRatePct: sum.E.TimeoutRatePct, P50DurationMS: sum.E.P50DurationMS,
		P95DurationMS: sum.E.P95DurationMS, MeanInjectedBytes: sum.E.MeanInjectedBytes,
	}
}

func buildASTDiff(cur search.StructuralSignals, prev *DailyDigest) ASTStructuralDiff {
	if prev == nil {
		return ASTStructuralDiff{}
	}
	p := prev.ASTSnapshot
	return ASTStructuralDiff{
		HasPrevious: true, HiddenCouplingEditPairs: cur.HiddenCouplingEditPairs - p.HiddenCouplingEditPairs,
		HiddenCouplingReadPairs: cur.HiddenCouplingReadPairs - p.HiddenCouplingReadPairs,
		ProdPkgCycles:           cur.ProdPkgCycles - p.ProdPkgCycles,
		LargeFilesOver350:       cur.LargeFilesOver350 - p.LargeFilesOver350,
		HubConcentration:        round4(cur.HubConcentration - p.HubConcentration),
	}
}

func buildPreInvDiff(cur PreInvocationSnapshot, prev *DailyDigest) PreInvocationDiff {
	if prev == nil {
		return PreInvocationDiff{}
	}
	p := prev.PreInvocation
	return PreInvocationDiff{
		HasPrevious: true, TurnVetsDelta: cur.TurnVets - p.TurnVets, TimedOutTurnsDelta: cur.TimedOutTurns - p.TimedOutTurns,
		TimeoutRatePctDelta:    optFloatDiff(cur.TimeoutRatePct, p.TimeoutRatePct),
		P50DurationMSDelta:     optFloatDiff(cur.P50DurationMS, p.P50DurationMS),
		P95DurationMSDelta:     optFloatDiff(cur.P95DurationMS, p.P95DurationMS),
		MeanInjectedBytesDelta: optFloatDiff(cur.MeanInjectedBytes, p.MeanInjectedBytes),
	}
}

func optFloatDiff(cur, prev *float64) *float64 {
	if cur == nil || prev == nil {
		return nil
	}
	v := round4(*cur - *prev)
	return &v
}

func collectDigestSignals(curSum *Summary, astSnap search.StructuralSignals, astDiff ASTStructuralDiff, repoRoot string) []DigestAction {
	var raw []DiagnosticSignal
	if curSum.Diagnostics != nil {
		raw = append(raw, curSum.Diagnostics.Signals...)
	}
	if astDiff.HasPrevious && (astDiff.ProdPkgCycles > 0 || astDiff.LargeFilesOver350 > 0 || astDiff.HiddenCouplingEditPairs > 0) {
		scope := pickSev(strings.TrimSpace(repoRoot) != "", "project", "global")
		_, pfx := resolveDiagnosticScope(scope, repoRoot)
		sev := pickSev(astDiff.ProdPkgCycles > 0 || astDiff.HiddenCouplingEditPairs > 0, "high", "medium")
		msg := fmt.Sprintf("day-over-day structural regression (cycles %+d, co_edit %+d, large_files %+d)",
			astDiff.ProdPkgCycles, astDiff.HiddenCouplingEditPairs, astDiff.LargeFilesOver350)
		raw = append(raw, makeSignal(pfx, "structural_regression", sev, "H.structural.regression",
			float64(astSnap.ProdPkgCycles+astSnap.HiddenCouplingEditPairs+astSnap.LargeFilesOver350), 0, max(1, astSnap.TotalCodeFiles), msg))
	}
	dedup := dedupAndSortSignals(raw)
	assignDiagnosticActions(dedup)
	out := make([]DigestAction, 0, len(dedup))
	for _, s := range dedup {
		out = append(out, DigestAction{
			SignalID: printable(s.SignalID), Severity: printable(s.Severity), Rule: printable(s.Rule),
			ActionScope: printable(s.ActionScope), RecommendedAction: printable(s.RecommendedAction), Summary: printable(s.Summary),
		})
	}
	return out
}

func computeDigestVerdict(d DailyDigest) string {
	if hasDigestRegression(d) {
		return VerdictRegress
	}
	if hasDigestImprovement(d) {
		return VerdictImproving
	}
	return VerdictSteady
}

func hasDigestRegression(d DailyDigest) bool {
	if d.PreInvocationDiff.TimeoutRatePctDelta != nil && *d.PreInvocationDiff.TimeoutRatePctDelta > 0 {
		return true
	}
	if d.Rolling7d.PreInvTimeoutRatePct != nil && d.Rolling7d.PreInvTimeoutRatePct.Delta > 0 && d.PreInvocation.TimeoutRatePct != nil && *d.PreInvocation.TimeoutRatePct >= minTimeoutRatePct {
		return true
	}
	if d.PreInvocationDiff.P95DurationMSDelta != nil && *d.PreInvocationDiff.P95DurationMSDelta >= 20 && d.PreInvocation.P95DurationMS != nil && *d.PreInvocation.P95DurationMS >= 100 {
		return true
	}
	if d.Rolling7d.BashReadSkips.Delta > 0 && d.Rolling7d.BashReadSkips.Current >= float64(minDirectShellReads) {
		return true
	}
	if d.ASTDiff.HasPrevious && (d.ASTDiff.HiddenCouplingEditPairs > 0 || d.ASTDiff.ProdPkgCycles > 0) {
		return true
	}
	for _, s := range d.Signals {
		if s.Severity == "high" {
			return true
		}
	}
	return false
}

func hasDigestImprovement(d DailyDigest) bool {
	if (d.Rolling7d.SearchGrepRatio != nil && d.Rolling7d.SearchGrepRatio.Delta > 0) ||
		(d.Rolling7d.RedundantReadRatePct != nil && d.Rolling7d.RedundantReadRatePct.Delta < 0) ||
		(d.Rolling7d.ToolErrorRatePct != nil && d.Rolling7d.ToolErrorRatePct.Delta < 0) ||
		(d.PreInvocationDiff.TimeoutRatePctDelta != nil && *d.PreInvocationDiff.TimeoutRatePctDelta < 0) {
		return true
	}
	if d.ASTDiff.HasPrevious && (d.ASTDiff.ProdPkgCycles < 0 || d.ASTDiff.HiddenCouplingEditPairs < 0) {
		return true
	}
	for _, a := range d.Actions {
		if a.Status == StatusRecovered {
			return true
		}
	}
	return false
}
