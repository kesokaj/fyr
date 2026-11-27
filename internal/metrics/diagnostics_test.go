package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

func TestDiagnosticReportShapeOnly(t *testing.T) {
	home := t.TempDir()
	s := openT(t, Path(home))
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	since := now.Add(-24 * time.Hour)
	sensitiveRoot := "/Users/alice/private-corp/payments-api"
	seedDiagnosticStoreFixture(t, s, sensitiveRoot, now)

	sumGlobal, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatalf("Show global: %v", err)
	}
	sumProj, err := s.Show(ShowOptions{Home: home, Since: since, Now: now, Project: sensitiveRoot})
	if err != nil {
		t.Fatalf("Show project: %v", err)
	}
	assertDiagnosticReportNoLeak(t, sumGlobal.Diagnostics, "sig:global:", sensitiveRoot, home)
	assertDiagnosticReportNoLeak(t, sumProj.Diagnostics, "sig:proj-", sensitiveRoot, home)
}

func seedDiagnosticStoreFixture(t *testing.T, s *Store, root string, now time.Time) {
	t.Helper()
	ts := now.Add(-2 * time.Hour).UnixMilli()
	for i := 0; i < 6; i++ {
		sid := fmt.Sprintf("conv-canary-%d", i)
		if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES (?, 'agy', ?, ?, ?)`,
			sid, root, ts+int64(i)*1000, ts+int64(i)*1000+500); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, is_error, context, cmd_hash) VALUES (?, 'm1', ?, 'model', '', 0, 210000, ''), (?, 't1', ?, 'tool', 'Grep', 1, 0, '/Users/alice/raw-arg'), (?, 't2', ?, 'tool', 'Bash', 1, 0, 'canary-cmd')`,
			sid, ts+10, sid, ts+20, sid, ts+30); err != nil {
			t.Fatal(err)
		}
	}
	var ratings []SessionRating
	for i := 0; i < 5; i++ {
		ratings = append(ratings, SessionRating{
			SessionID: fmt.Sprintf("conv-canary-%d", i), ProjectRoot: root, EvaluatedAt: ts,
			OutcomeScore: 2, TokenEfficiency: 2, PlanAlignment: "missed", SkillAction: "rewrite-skill",
			TargetSkill: "skills/golang/SKILL.md", Summary: "leaked-prose about /Users/alice/private-corp",
		})
	}
	if err := s.UpsertSessionRatings(ratings); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.InsertBackgroundRun(BackgroundRunRow{TS: ts + int64(i), Kind: "dream", Outcome: "fail", DurationMS: 100}); err != nil {
			t.Fatal(err)
		}
	}
}

func assertDiagnosticReportNoLeak(t *testing.T, rep *DiagnosticReport, wantPrefix, sensitiveRoot, home string) {
	t.Helper()
	if rep == nil || len(rep.Signals) == 0 {
		t.Fatalf("expected non-empty DiagnosticReport for %s, got %+v", wantPrefix, rep)
	}
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{
		sensitiveRoot, "Users/alice", "private-corp", "payments-api",
		"conv-canary", "leaked-prose", "raw-arg", "canary-cmd", "skills/golang", home,
	} {
		if forbidden != "" && strings.Contains(text, forbidden) {
			t.Fatalf("DiagnosticReport JSON leaked %q:\n%s", forbidden, text)
		}
	}
	for i, sig := range rep.Signals {
		if !strings.HasPrefix(sig.SignalID, wantPrefix) || strings.Contains(sig.SignalID, "/") {
			t.Fatalf("invalid SignalID %q (want prefix %q)", sig.SignalID, wantPrefix)
		}
		if i > 0 && severityRank(rep.Signals[i-1].Severity) < severityRank(sig.Severity) {
			t.Fatalf("signals out of severity order: %+v", rep.Signals)
		}
	}
}

func TestDiagnosticReportDedupAndMinSamples(t *testing.T) {
	er, sr, lowScore := 0.50, 0.10, 2.0
	under := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
	under.Sessions["agy"] = 4
	under.Health.MainSessions = 4
	under.Health.SessionsOverKnee = 4
	under.B.ToolCalls["agy"] = 20
	under.D.ToolErrorRate["agy"] = &er
	under.C.GrepCalls["agy"] = 10
	under.C.SearchGrepRatio["agy"] = &sr
	under.A.RatingsCount = 4
	under.A.MeanOutcomeScore = &lowScore
	under.F = WorkSummary{Workers: 2, Failed: 2}
	under.Health.DreamRuns = BackgroundKindStats{Runs: 2, Failed: 2}
	under.Health.Structural = search.StructuralSignals{HiddenCouplingPairs: 2}

	rep := BuildDiagnosticReport(*under, []EvalRow{{Kind: "capability", N: 2, C: 0}}, "global", "")
	if len(rep.Signals) != 0 {
		t.Fatalf("below-min-sample inputs triggered signals: %+v", rep.Signals)
	}
	assertDiagnosticDedupAndCycleMin(t, under)
}

func assertDiagnosticDedupAndCycleMin(t *testing.T, base *Summary) {
	t.Helper()
	base.Health.Structural.ProdPkgCycles = 1
	base.Health.Structural.TotalProdPkgs = 4
	rep := BuildDiagnosticReport(*base, nil, "project", "/work/svc")
	if len(rep.Signals) != 1 || rep.Signals[0].Rule != "prod_pkg_cycles" || rep.Signals[0].Severity != "high" {
		t.Fatalf("want single high prod_pkg_cycles signal, got %+v", rep.Signals)
	}

	dups := dedupAndSortSignals([]DiagnosticSignal{
		{SignalID: "sig:global:b_rule", Severity: "low", Rule: "b_rule"},
		{SignalID: "sig:global:a_rule", Severity: "medium", Rule: "a_rule"},
		{SignalID: "sig:global:b_rule", Severity: "high", Rule: "b_rule"},
		{SignalID: "sig:global:c_rule", Severity: "medium", Rule: "c_rule"},
	})
	if len(dups) != 3 || dups[0].SignalID != "sig:global:b_rule" || dups[0].Severity != "high" ||
		dups[1].SignalID != "sig:global:a_rule" || dups[2].SignalID != "sig:global:c_rule" {
		t.Fatalf("dedupAndSortSignals = %+v", dups)
	}
}

func TestHiddenCouplingEditVsReadDiagnostic(t *testing.T) {
	readOnly := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
	readOnly.Health.Structural = search.StructuralSignals{
		HiddenCouplingPairs:     5,
		HiddenCouplingEditPairs: 0,
		HiddenCouplingReadPairs: 5,
		HiddenCouplingRate:      1.0,
	}
	if rep := BuildDiagnosticReport(*readOnly, nil, "project", "/work/svc"); len(rep.Signals) != 0 {
		t.Fatalf("co_read-only pairs falsely triggered hidden_coupling: %+v", rep.Signals)
	}

	belowEditMin := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
	belowEditMin.Health.Structural = search.StructuralSignals{
		HiddenCouplingPairs:     6,
		HiddenCouplingEditPairs: 2,
		HiddenCouplingReadPairs: 4,
		HiddenCouplingRate:      0.8,
	}
	if rep := BuildDiagnosticReport(*belowEditMin, nil, "project", "/work/svc"); len(rep.Signals) != 0 {
		t.Fatalf("below-threshold co_edit with high co_read triggered signal: %+v", rep.Signals)
	}

	editTriggered := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
	editTriggered.Health.Structural = search.StructuralSignals{
		HiddenCouplingPairs:     5,
		HiddenCouplingEditPairs: 3,
		HiddenCouplingReadPairs: 2,
		HiddenCouplingRate:      0.6,
	}
	rep := BuildDiagnosticReport(*editTriggered, nil, "project", "/work/svc")
	if len(rep.Signals) != 1 || rep.Signals[0].Rule != "hidden_coupling" ||
		rep.Signals[0].Metric != "H.structural.hidden_coupling_edit_pairs" || rep.Signals[0].Current != 3 {
		t.Fatalf("co_edit diagnostic mismatch: %+v", rep.Signals)
	}
}

func TestDiagnosticsHealthRegressionRulesAndActionScopes(t *testing.T) {
	t.Run("pre_invocation_timeouts thresholds", func(t *testing.T) {
		pLow := 4.9
		underRuns := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
		underRuns.E = SecondBrain{TurnVets: 2, TimedOutTurns: 1, TimeoutRatePct: ptrF(50.0)}
		if rep := BuildDiagnosticReport(*underRuns, nil, "global", ""); len(rep.Signals) != 0 {
			t.Fatalf("under minDiagnosticRuns triggered signal: %+v", rep.Signals)
		}
		underPct := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
		underPct.E = SecondBrain{TurnVets: 20, TimedOutTurns: 1, TimeoutRatePct: &pLow}
		if rep := BuildDiagnosticReport(*underPct, nil, "global", ""); len(rep.Signals) != 0 {
			t.Fatalf("below 5%% timeout triggered signal: %+v", rep.Signals)
		}

		med := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
		med.E = SecondBrain{TurnVets: 10, TimedOutTurns: 1, TimeoutRatePct: ptrF(10.0)}
		repMed := BuildDiagnosticReport(*med, nil, "global", "")
		if len(repMed.Signals) != 1 || repMed.Signals[0].Rule != "pre_invocation_timeouts" ||
			repMed.Signals[0].Severity != "medium" || repMed.Signals[0].ActionScope != scopeProposeOnly ||
			!strings.Contains(repMed.Signals[0].RecommendedAction, "internal/search/") {
			t.Fatalf("medium timeout signal = %+v", repMed.Signals)
		}

		// Single-window high timeout rate downgrades to medium under 2-day hysteresis.
		high := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
		high.E = SecondBrain{TurnVets: 10, TimedOutTurns: 2, TimeoutRatePct: ptrF(20.0)}
		repDay1 := BuildDiagnosticReport(*high, nil, "global", "")
		if len(repDay1.Signals) != 1 || repDay1.Signals[0].Severity != "medium" {
			t.Fatalf("day1 timeout signal = %+v, want medium under hysteresis", repDay1.Signals)
		}
		// Sustained timeout regression across prior window promotes to high.
		high.Deltas = &WindowDeltas{
			PreviousSessions: 5,
			Metrics: map[string]MetricDelta{
				"E.turn_vets":        {Current: 10, Previous: 10},
				"E.timeout_rate_pct": {Current: 20.0, Previous: 10.0},
			},
		}
		repHigh := BuildDiagnosticReport(*high, nil, "global", "")
		if len(repHigh.Signals) != 1 || repHigh.Signals[0].Severity != "high" {
			t.Fatalf("sustained high timeout signal = %+v", repHigh.Signals)
		}
	})

	t.Run("bash_read_bypass thresholds", func(t *testing.T) {
		underSess := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
		underSess.Sessions["agy"] = 4
		underSess.C.BashReadSkips = map[string]int{"agy": 5}
		if rep := BuildDiagnosticReport(*underSess, nil, "global", ""); len(rep.Signals) != 0 {
			t.Fatalf("under minDiagnosticSessions triggered signal: %+v", rep.Signals)
		}
		underSkips := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
		underSkips.Sessions["agy"] = 6
		underSkips.C.BashReadSkips = map[string]int{"agy": 2}
		if rep := BuildDiagnosticReport(*underSkips, nil, "global", ""); len(rep.Signals) != 0 {
			t.Fatalf("below 3 skips triggered signal: %+v", rep.Signals)
		}

		med := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
		med.Sessions["agy"] = 5
		med.C.BashReadSkips = map[string]int{"agy": 3}
		repMed := BuildDiagnosticReport(*med, nil, "global", "")
		if len(repMed.Signals) != 1 || repMed.Signals[0].Rule != "bash_read_bypass" ||
			repMed.Signals[0].Severity != "medium" || repMed.Signals[0].ActionScope != scopeProposeOnly ||
			!strings.Contains(repMed.Signals[0].RecommendedAction, "PreToolUse") {
			t.Fatalf("medium bash_read_bypass signal = %+v", repMed.Signals)
		}

		// Single-window high rate downgrades to medium; sustained prior-window rate promotes to high.
		high := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
		high.Sessions["agy"] = 5
		high.C.BashReadSkips = map[string]int{"agy": 10}
		repDay1 := BuildDiagnosticReport(*high, nil, "global", "")
		if len(repDay1.Signals) != 1 || repDay1.Signals[0].Severity != "medium" {
			t.Fatalf("day1 bash_read_bypass signal = %+v, want medium under hysteresis", repDay1.Signals)
		}
		high.Deltas = &WindowDeltas{
			PreviousSessions: 5,
			Metrics: map[string]MetricDelta{
				"C.bash_read_skips.agy": {Current: 10, Previous: 4},
			},
		}
		repHigh := BuildDiagnosticReport(*high, nil, "global", "")
		if len(repHigh.Signals) != 1 || repHigh.Signals[0].Severity != "high" {
			t.Fatalf("sustained high bash_read_bypass signal = %+v", repHigh.Signals)
		}
	})

	t.Run("structural_regression multi-factor and deltas plus ActionScope", func(t *testing.T) {
		// Single-factor ProdPkgCycles without worsening delta must emit only prod_pkg_cycles, NOT structural_regression.
		single := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
		single.Health.Structural = search.StructuralSignals{ProdPkgCycles: 1, TotalProdPkgs: 4}
		repSingle := BuildDiagnosticReport(*single, nil, "project", "/work/svc")
		if len(repSingle.Signals) != 1 || repSingle.Signals[0].Rule != "prod_pkg_cycles" || repSingle.Signals[0].ActionScope != scopeProposeOnly {
			t.Fatalf("single-factor cycle signal = %+v", repSingle.Signals)
		}

		// Multi-factor (cycles + coupling) triggers structural_regression too.
		multi := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
		multi.Health.Structural = search.StructuralSignals{
			ProdPkgCycles: 1, TotalProdPkgs: 4, HiddenCouplingEditPairs: 3, TotalCodeFiles: 10,
		}
		repMulti := BuildDiagnosticReport(*multi, nil, "project", "/work/svc")
		if !hasDiagRule(repMulti.Signals, "structural_regression") {
			t.Fatalf("multi-factor structural_regression missing: %+v", repMulti.Signals)
		}

		// Single risk (large files) WITH positive delta triggers structural_regression.
		deltaOnly := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
		deltaOnly.Health.Structural = search.StructuralSignals{TotalCodeFiles: 8, LargeFilesOver350: 3}
		deltaOnly.Deltas = &WindowDeltas{Metrics: map[string]MetricDelta{
			"H.structural.large_files_over_350": {Current: 3, Previous: 1, Delta: 2},
		}}
		repDelta := BuildDiagnosticReport(*deltaOnly, nil, "project", "/work/svc")
		if !hasDiagRule(repDelta.Signals, "structural_regression") {
			t.Fatalf("delta-driven structural_regression missing: %+v", repDelta.Signals)
		}

		// Auto-skill scope vs propose_only scope checks.
		sk := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
		sk.Sessions["agy"] = 6
		sk.C.GrepCalls["agy"] = 10
		sk.C.SearchGrepRatio["agy"] = ptrF(0.2)
		sk.C.RedundantReads = map[string]int{"agy": 12}
		repSk := BuildDiagnosticReport(*sk, []EvalRow{{Kind: "capability", N: 4, C: 1}}, "global", "")
		for _, rule := range []string{"search_grep_low", "redundant_reads_elevated", "eval_pass_rate_low"} {
			sig, ok := findDiagSignal(repSk.Signals, rule)
			if !ok || sig.ActionScope != scopeAutoSkill || sig.RecommendedAction == "" {
				t.Fatalf("rule %s missing auto_skill action: %+v", rule, sig)
			}
		}
	})
}

func ptrF(v float64) *float64 { return &v }

func hasDiagRule(sigs []DiagnosticSignal, rule string) bool {
	_, ok := findDiagSignal(sigs, rule)
	return ok
}

func findDiagSignal(sigs []DiagnosticSignal, rule string) (DiagnosticSignal, bool) {
	for _, s := range sigs {
		if s.Rule == rule {
			return s, true
		}
	}
	return DiagnosticSignal{}, false
}

func TestV910DiagnosticsRatesDigestAndDeltas(t *testing.T) {
	t.Run("F056_small_counts_across_5000_sessions_zero_high", testV910SmallCountsZeroHigh)
	t.Run("F056_two_day_hysteresis_and_static_invariants", testV910TwoDayHysteresis)
	t.Run("F006_structured_auto_applied_marker", testV910StructuredAutoApplied)
	t.Run("F018_structural_window_deltas_from_daily_digests", testV910StructuralDeltas)
	t.Run("F013_sandbox_and_labeled_turn_vets_filtered", testV910SandboxTurnVetsFilter)
	t.Run("F036_subagent_caps_and_exempt_wrapup_steps", testV910SubagentCapsAndExemptSteps)
}

func testV910SmallCountsZeroHigh(t *testing.T) {
	sum := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
	sum.Sessions["agy"], sum.Health.MainSessions = 5000, 5000
	sum.Health.SubagentsOverBudget, sum.Health.GuardedDowngrades = 5, 3
	sum.A.EscapedBugs, sum.A.Corrections = 4, 6
	sum.C.BashReadSkips = map[string]int{"agy": 10}
	sum.D.RetryLoops, sum.C.RedundantReads = map[string]int{"agy": 10}, map[string]int{"agy": 10}
	sum.Health.Structural = search.StructuralSignals{GodNodeClampHits: 10, GodNodeRepeatReads: 5, GodNodeChurnRate: 0.01}
	sum.Deltas = &WindowDeltas{
		PreviousSessions: 5000,
		Metrics: map[string]MetricDelta{
			"H.subagents_over_budget": {Current: 5, Previous: 5}, "H.guarded_downgrades": {Current: 3, Previous: 3},
			"A.escaped_bugs": {Current: 4, Previous: 4}, "A.corrections": {Current: 6, Previous: 6},
			"C.bash_read_skips.agy": {Current: 10, Previous: 10},
		},
	}
	rep := BuildDiagnosticReport(*sum, nil, "global", "")
	if len(rep.Signals) != 0 {
		t.Fatalf("expected 0 signals (0 HIGH) for 5/3/4/6/10 across 5000 sessions, got %+v", rep.Signals)
	}
	// Sustained high rates across N=10 sessions: Day 1 downgrades to medium; confirmed Day 2 emits HIGH.
	hi := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
	hi.Sessions["agy"], hi.Health.MainSessions = 10, 10
	hi.Health.SubagentsOverBudget, hi.Health.GuardedDowngrades = 3, 2
	hi.A.EscapedBugs, hi.A.Corrections = 2, 3
	hi.C.BashReadSkips = map[string]int{"agy": 10}
	for _, sig := range BuildDiagnosticReport(*hi, nil, "global", "").Signals {
		if sig.Severity == "high" {
			t.Fatalf("day1 unconfirmed high-rate signal should downgrade to medium: %+v", sig)
		}
	}
	hi.Deltas = &WindowDeltas{
		PreviousSessions: 10,
		Metrics: map[string]MetricDelta{
			"H.subagents_over_budget": {Current: 3, Previous: 2}, "H.guarded_downgrades": {Current: 2, Previous: 1},
			"A.escaped_bugs": {Current: 2, Previous: 2}, "A.corrections": {Current: 3, Previous: 3},
			"C.bash_read_skips.agy": {Current: 10, Previous: 4},
		},
	}
	repHi := BuildDiagnosticReport(*hi, nil, "global", "")
	for _, rule := range []string{"subagents_over_budget", "guarded_downgrades", "escaped_bugs_present", "corrections_elevated", "bash_read_bypass"} {
		if sig, ok := findDiagSignal(repHi.Signals, rule); !ok || sig.Severity != "high" {
			t.Fatalf("sustained high-rate %s = (%+v, %v), want high", rule, sig, ok)
		}
	}
}

func testV910TwoDayHysteresis(t *testing.T) {
	home := t.TempDir()
	s := openT(t, Path(home))
	day1 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	day2 := day1.Add(24 * time.Hour)
	seedHighRateDayT(t, s, "/work/app", "d1", day1.Add(-2*time.Hour).UnixMilli())

	// Day 1 without prior digest or prior window: high-rate spikes downgrade to medium.
	sum1, err := s.Show(ShowOptions{Home: home, Since: day1.Add(-24 * time.Hour), Now: day1})
	if err != nil {
		t.Fatalf("Show day1: %v", err)
	}
	for _, rule := range []string{"bash_read_bypass", "pre_invocation_timeouts"} {
		if sig, ok := findDiagSignal(sum1.Diagnostics.Signals, rule); !ok || sig.Severity != "medium" {
			t.Fatalf("day1 %s = (%+v, %v), want medium under 2-day hysteresis", rule, sig, ok)
		}
	}
	// Stale digest (3 days earlier) must NOT confirm Day 1.
	if err := SaveDailyDigest(s, DailyDigest{
		Day: "2026-11-18", Verdict: VerdictRegress,
		Signals: []DigestAction{{SignalID: "sig:global:bash_read_bypass", Severity: "medium", Rule: "bash_read_bypass"}},
	}); err != nil {
		t.Fatal(err)
	}
	// Build & save Day 1 digest; same-day Show must STILL not self-confirm to HIGH.
	d1, err := BuildDailyDigest(s, home, "", day1)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveDailyDigest(s, d1); err != nil {
		t.Fatal(err)
	}
	sum1Same, err := s.Show(ShowOptions{Home: home, Since: day1.Add(-24 * time.Hour), Now: day1})
	if err != nil {
		t.Fatal(err)
	}
	if sig, ok := findDiagSignal(sum1Same.Diagnostics.Signals, "bash_read_bypass"); !ok || sig.Severity != "medium" {
		t.Fatalf("stale/same-day digest falsely confirmed day1 HIGH: %+v", sig)
	}

	// Day 2 with sustained high rates and consecutive Day 1 digest promotes cleanly to HIGH.
	seedHighRateDayT(t, s, "/work/app", "d2", day2.Add(-2*time.Hour).UnixMilli())
	sum2, err := s.Show(ShowOptions{Home: home, Since: day2.Add(-24 * time.Hour), Now: day2})
	if err != nil {
		t.Fatalf("Show day2: %v", err)
	}
	for _, rule := range []string{"bash_read_bypass", "pre_invocation_timeouts"} {
		if sig, ok := findDiagSignal(sum2.Diagnostics.Signals, rule); !ok || sig.Severity != "high" {
			t.Fatalf("day2 sustained %s = (%+v, %v), want high", rule, sig, ok)
		}
	}
}

func seedHighRateDayT(t *testing.T, s *Store, root, prefix string, ts int64) {
	t.Helper()
	for i := 0; i < 6; i++ {
		sid := fmt.Sprintf("%s-%d", prefix, i)
		if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES (?, 'agy', ?, ?, ?)`,
			sid, root, ts+int64(i)*1000, ts+int64(i)*1000+500); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 2; j++ {
			ext := fmt.Sprintf("%s:b:%d", sid, j)
			if _, err := s.db.Exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES (?, ?, ?, 'tool', 'Bash')`, sid, ext, ts+10+int64(j)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES (?, ?, 0, 'cat')`, sid, ext); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.db.Exec(`INSERT INTO turn_vets(project_root, conversation_id, turn_num, intent, gate, score, duration_ms, timed_out, created_at) VALUES (?, ?, 1, 'TASK', 'PROCEED', 80, 260, 1, ?)`, root, sid, ts+20); err != nil {
			t.Fatal(err)
		}
	}
}

func testV910StructuredAutoApplied(t *testing.T) {
	home := t.TempDir()
	bdb := filepath.Join(home, harness.StateDir, "brain.db")
	if err := os.MkdirAll(filepath.Dir(bdb), 0o700); err != nil {
		t.Fatal(err)
	}
	sqliteT(t, bdb,
		`CREATE TABLE records(rid INTEGER PRIMARY KEY, scope TEXT NOT NULL, id TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL, date TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, project TEXT NOT NULL DEFAULT '', updated TEXT NOT NULL, provenance TEXT NOT NULL DEFAULT '', UNIQUE(scope, id))`,
		`INSERT INTO records VALUES (1, '', 'REVIEW-2026-11-21-preflight', 'review', 'closed', '2026-11-21', 'preflight review', 'Applied fix from RecordDigestAppliedAction. See revert: git revert abc1234 and inline auto-applied: mention.', '/work/app', '2026-11-21T09:00:00Z', '')`,
		"INSERT INTO records VALUES (2, '', 'REVIEW-2026-11-21-fenced', 'review', 'closed', '2026-11-21', 'code review', 'Example:\n```\nauto-applied: feed123\n```\n', '/work/app', '2026-11-21T09:05:00Z', '')",
		"INSERT INTO records VALUES (3, '', 'REVIEW-2026-11-21-auto', 'review', 'closed', '2026-11-21', 'auto fix skills/golang', 'auto-applied: deadbee\nsig:global:search_grep_low install pending\nrevert: git revert deadbee', '/work/app', '2026-11-21T09:10:00Z', 'derived')",
	)
	got := readBrainAppliedActions(home, "/work/app", "2026-11-20")
	if len(got) != 1 || got[0].SignalID != "sig:global:search_grep_low" || got[0].AppliedSHA != "deadbee" {
		t.Fatalf("readBrainAppliedActions = %+v, want single structured auto-applied action", got)
	}
}

func testV910StructuralDeltas(t *testing.T) {
	home := t.TempDir()
	proj := seedStructuralRepoWithSearchDB(t, home)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	since := now.Add(-24 * time.Hour)
	s := openT(t, Path(home))
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES ('s-cur', 'agy', ?, ?, ?)`,
		proj, now.Add(-2*time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := SaveDailyDigest(s, DailyDigest{
		Day: "2026-11-20", Verdict: VerdictSteady,
		ASTSnapshot: search.StructuralSignals{HiddenCouplingPairs: 1, HiddenCouplingReadPairs: 1, TotalCodeFiles: 2, TotalProdPkgs: 2},
	}); err != nil {
		t.Fatal(err)
	}
	sum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil || sum.Deltas == nil {
		t.Fatalf("Show: err=%v deltas=%v", err, sum)
	}
	md, ok := sum.Deltas.Metrics["H.structural.hidden_coupling_edit_pairs"]
	if !ok || md.Previous != 0 || md.Current != 1 || md.Delta != 1 {
		t.Fatalf("H.structural.hidden_coupling_edit_pairs = (%+v, %v), want cur=1 prev=0 delta=+1", md, ok)
	}
	if clampMD, ok := sum.Deltas.Metrics["H.structural.god_node_clamp_hits"]; !ok || clampMD.Delta <= 0 {
		t.Fatalf("H.structural.god_node_clamp_hits = (%+v, %v), want positive delta", clampMD, ok)
	}
	projSum, err := s.Show(ShowOptions{Home: home, Project: proj, Since: since, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := projSum.Deltas.Metrics["H.structural.hidden_coupling_edit_pairs"]; leaked {
		t.Fatalf("project-scoped Show leaked global daily_digests AST delta: %+v", projSum.Deltas.Metrics)
	}
}

func testV910SandboxTurnVetsFilter(t *testing.T) {
	home := t.TempDir()
	s := openT(t, Path(home))
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	since, ts := now.Add(-24*time.Hour), now.Add(-time.Hour).UnixMilli()
	if _, err := s.db.Exec(`INSERT INTO sessions(id, vendor, project_root, label, started_at, ended_at) VALUES ('eval-s1', 'agy', '/work/real-app', 'eval=smoke', ?, ?)`, ts, ts+500); err != nil {
		t.Fatal(err)
	}
	sandboxRoots := []string{
		"/tmp/fyr-eval/s1", "/work/proj/.agents/scratch/sub", "/work/real-home-smoke-1", "/work/fyr-smoke-9",
		"/Users/alice", "/", home, "/tmp", filepath.Join(home, harness.StateDir, "dev", "fyr-eval"),
	}
	for i, root := range sandboxRoots {
		if _, err := s.db.Exec(`INSERT INTO turn_vets(project_root, conversation_id, turn_num, intent, gate, score, created_at) VALUES (?, ?, 1, 'TASK', 'PROCEED', 85, ?)`,
			root, fmt.Sprintf("sb-conv-%d", i), ts+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO turn_vets(project_root, conversation_id, turn_num, intent, gate, score, created_at) VALUES ('/work/real-app', 'eval-s1', 1, 'TASK', 'PROCEED', 90, ?)`, ts+100); err != nil {
		t.Fatal(err)
	}
	if defSum, err := s.Show(ShowOptions{Home: home, Since: since, Now: now}); err != nil || defSum.Sessions["agy"] != 0 || defSum.E.TurnVets != 0 {
		t.Fatalf("default 0-session Show leaked turn_vets: err=%v sessions=%v E.TurnVets=%d", err, defSum.Sessions, defSum.E.TurnVets)
	}
	if withSB, err := s.Show(ShowOptions{Home: home, Since: since, Now: now, IncludeSandboxes: true}); err != nil || withSB.E.TurnVets != len(sandboxRoots) {
		t.Fatalf("IncludeSandboxes E.TurnVets = %d (err=%v), want %d", withSB.E.TurnVets, err, len(sandboxRoots))
	}
	if allLbl, err := s.Show(ShowOptions{Home: home, Since: since, Now: now, AllLabels: true}); err != nil || allLbl.E.TurnVets != 1 {
		t.Fatalf("AllLabels E.TurnVets = %d (err=%v), want 1", allLbl.E.TurnVets, err)
	}
	if explicit, err := s.Show(ShowOptions{Home: home, Since: since, Now: now, Project: "/tmp/fyr-eval/s1"}); err != nil || explicit.E.TurnVets != 1 {
		t.Fatalf("explicit Project E.TurnVets = %d (err=%v), want 1", explicit.E.TurnVets, err)
	}
}

func testV910SubagentCapsAndExemptSteps(t *testing.T) {
	for role, want := range map[string]int{
		"workflow-builder-8a62522f": 120, "coder-1": 120,
		"worker-2": 40, "worker-x": 40, "implementer": 40,
		"workflow-reviewer-1234": 40, "security-audit": 40, "sub-extra": 40,
	} {
		if got := subagentCapForRole(role); got != want {
			t.Errorf("subagentCapForRole(%q) = %d, want %d", role, got, want)
		}
	}
	home := t.TempDir()
	s := openT(t, Path(home))
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	ts := now.Add(-time.Hour).UnixMilli()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO sessions(id, vendor, project_root, started_at, ended_at) VALUES ('M1', 'agy', '/work/app', ?, ?)`, ts, ts+50000)
	exec(`INSERT INTO sessions(id, vendor, project_root, parent_id, agent_type, started_at, ended_at) VALUES ('R_EX', 'agy', '/work/app', 'M1', 'reviewer', ?, ?), ('B_WK', 'agy', '/work/app', 'M1', 'workflow-builder-8a62522f', ?, ?)`, ts+100, ts+5000, ts+6000, ts+20000)
	for i := 0; i < 40; i++ {
		exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('R_EX', ?, ?, 'tool', 'Read')`, fmt.Sprintf("r:%d", i), ts+200+int64(i))
	}
	exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool, denied, target_kind, cmd_hash) VALUES
		('R_EX', 'r:den', ?, 'tool', 'Read', 1, '', ''), ('R_EX', 'r:fin', ?, 'tool', 'agy:finish', 0, '', ''),
		('R_EX', 'r:msg', ?, 'tool', 'SendMessage', 0, '', ''), ('R_EX', 'r:art', ?, 'tool', 'Write', 0, 'scratch_agent', '.agents/scratch/agents/reviewer-1.md'),
		('R_EX', 'r:git', ?, 'tool', 'Bash', 0, '', '')`, ts+300, ts+301, ts+302, ts+303, ts+304)
	exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES ('R_EX', 'r:git', 0, 'git add'), ('R_EX', 'r:git', 1, 'git commit -m'), ('R_EX', 'r:git', 2, 'git checkout --detach')`)
	for i := 0; i < 120; i++ {
		exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('B_WK', ?, ?, 'tool', 'Read')`, fmt.Sprintf("b:%d", i), ts+7000+int64(i))
	}
	exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('B_WK', 'b:fin', ?, 'tool', 'finish')`, ts+8000)
	if sum, err := s.Show(ShowOptions{Home: home, Since: now.Add(-24 * time.Hour), Now: now}); err != nil || sum.Health.SubagentsOverBudget != 0 {
		t.Fatalf("SubagentsOverBudget = %d (err=%v), want 0", sum.Health.SubagentsOverBudget, err)
	}
	exec(`INSERT INTO steps(session_id, ext_id, ts, kind, tool) VALUES ('R_EX', 'r:over', ?, 'tool', 'Bash')`, ts+305)
	exec(`INSERT INTO step_verbs(session_id, ext_id, n, verb) VALUES ('R_EX', 'r:over', 0, 'go test')`)
	if sum2, err := s.Show(ShowOptions{Home: home, Since: now.Add(-24 * time.Hour), Now: now}); err != nil || sum2.Health.SubagentsOverBudget != 1 {
		t.Fatalf("SubagentsOverBudget after non-exempt step = %d (err=%v), want 1", sum2.Health.SubagentsOverBudget, err)
	}
}

func TestEvalPassRateWindowDeltaAndHysteresis(t *testing.T) {
	home := t.TempDir()
	s := openT(t, Path(home))
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	since := now.Add(-24 * time.Hour)

	// Current window: 1/4 pass (0.25 < 0.60 -> candidate high).
	// Prior window:   2/4 pass (0.50 < 0.85 -> confirms 2-window persistence -> HIGH).
	for i, st := range []string{"pass", "fail", "fail", "fail"} {
		cause := ""
		if st == "fail" {
			cause = "check"
		}
		if err := s.RecordTrial(Trial{
			Run: fmt.Sprintf("eval/cur/%d", i), Label: "eval=smoke", Kind: "capability",
			Harness: "v9.10.0", Status: st, Cause: cause, StartedAt: now.Add(-time.Duration(i+1) * time.Hour), Seconds: 5,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// First verify single-window eval_pass_rate_low downgrades to medium.
	sumSingle, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if sig, ok := findDiagSignal(sumSingle.Diagnostics.Signals, "eval_pass_rate_low"); !ok || sig.Severity != "medium" {
		t.Fatalf("single-window eval signal got (%+v, %v), want medium", sig, ok)
	}

	// Seed prior window with 2/4 pass (0.50 < 0.85) -> WindowDeltas["eval.pass_rate"] populated and promotes to HIGH.
	for i, st := range []string{"pass", "pass", "fail", "fail"} {
		cause := ""
		if st == "fail" {
			cause = "check"
		}
		if err := s.RecordTrial(Trial{
			Run: fmt.Sprintf("eval/prev/%d", i), Label: "eval=smoke", Kind: "capability",
			Harness: "v9.10.0", Status: st, Cause: cause, StartedAt: since.Add(-time.Duration(i+1) * time.Hour), Seconds: 5,
		}); err != nil {
			t.Fatal(err)
		}
	}
	sumSustained, err := s.Show(ShowOptions{Home: home, Since: since, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	md, ok := sumSustained.Deltas.Metrics["eval.pass_rate"]
	if !ok || md.Current != 0.25 || md.Previous != 0.5 || md.Delta != -0.25 {
		t.Fatalf("WindowDeltas.Metrics[eval.pass_rate] = (%+v, %v), want cur=0.25 prev=0.5 delta=-0.25", md, ok)
	}
	if sig, ok := findDiagSignal(sumSustained.Diagnostics.Signals, "eval_pass_rate_low"); !ok || sig.Severity != "high" {
		t.Fatalf("sustained 2-window eval signal got (%+v, %v), want high", sig, ok)
	}
}
