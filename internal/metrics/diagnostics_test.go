package metrics

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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

		high := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
		high.E = SecondBrain{TurnVets: 10, TimedOutTurns: 2, TimeoutRatePct: ptrF(20.0)}
		repHigh := BuildDiagnosticReport(*high, nil, "global", "")
		if len(repHigh.Signals) != 1 || repHigh.Signals[0].Severity != "high" {
			t.Fatalf("high timeout signal = %+v", repHigh.Signals)
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

		high := newSummary(time.Unix(0, 0), time.Unix(86400, 0))
		high.Sessions["agy"] = 5
		high.C.BashReadSkips = map[string]int{"agy": 10}
		repHigh := BuildDiagnosticReport(*high, nil, "global", "")
		if len(repHigh.Signals) != 1 || repHigh.Signals[0].Severity != "high" {
			t.Fatalf("high bash_read_bypass signal = %+v", repHigh.Signals)
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
