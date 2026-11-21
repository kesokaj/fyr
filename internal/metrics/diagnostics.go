package metrics

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	minDiagnosticSessions  = 5
	minDiagnosticRuns      = 3
	minHiddenCouplingPairs = 3
	minProdPkgCycles       = 1
)

// DiagnosticSignal is one strictly shape-only ranked diagnostic finding.
type DiagnosticSignal struct {
	SignalID   string  `json:"signal_id"`
	Severity   string  `json:"severity"`
	Rule       string  `json:"rule"`
	Metric     string  `json:"metric"`
	Current    float64 `json:"current"`
	Previous   float64 `json:"previous"`
	Delta      float64 `json:"delta"`
	SampleSize int     `json:"sample_size"`
	Summary    string  `json:"summary"`
}

// DiagnosticReport holds ranked, deduplicated, shape-only diagnostic signals.
type DiagnosticReport struct {
	Scope       string             `json:"scope"`
	GeneratedAt string             `json:"generated_at"`
	Signals     []DiagnosticSignal `json:"signals"`
}

// BuildDiagnosticReport computes a deterministic, deduplicated, strictly
// shape-only DiagnosticReport with zero raw paths, prose, labels, or tool args.
func BuildDiagnosticReport(sum Summary, evals []EvalRow, scope, projectRoot string) DiagnosticReport {
	normScope, idPrefix := resolveDiagnosticScope(scope, projectRoot)
	var raw []DiagnosticSignal
	raw = appendSessionSignals(raw, sum, idPrefix)
	raw = appendAdherenceSignals(raw, sum, idPrefix)
	raw = appendStructuralSignals(raw, sum, idPrefix)
	raw = appendRunAndEvalSignals(raw, sum, evals, normScope, idPrefix)
	signals := dedupAndSortSignals(raw)
	genAt := sum.Window["until"]
	if genAt == "" {
		genAt = "1970-01-01"
	}
	return DiagnosticReport{
		Scope:       normScope,
		GeneratedAt: printable(genAt),
		Signals:     signals,
	}
}

func (s *Store) populateDiagnostics(sum *Summary, o ShowOptions, since, now time.Time) error {
	if o.skipDeltas || o.Label != "" {
		return nil
	}
	scope := "global"
	if strings.TrimSpace(o.Project) != "" {
		scope = "project"
	}
	var evalRows []EvalRow
	if scope == "global" {
		ev, err := ReportEvalsWithDevFallback(s, resolveMetricsHome(o, s.path), ShowOptions{
			Since:   since,
			Now:     now,
			Project: o.Project,
			Label:   o.Label,
		})
		if err != nil {
			return err
		}
		if ev != nil {
			evalRows = ev.Rows
		}
	}
	rep := BuildDiagnosticReport(*sum, evalRows, scope, o.Project)
	rep.GeneratedAt = now.UTC().Format(time.RFC3339)
	sum.Diagnostics = &rep
	return nil
}

func resolveDiagnosticScope(scope, projectRoot string) (string, string) {
	sc := strings.ToLower(strings.TrimSpace(scope))
	root := strings.TrimSpace(projectRoot)
	if sc == "global" || root == "" {
		return "global", "sig:global:"
	}
	h := sha256.Sum256([]byte(filepath.Clean(root)))
	return "project", "sig:proj-" + hex.EncodeToString(h[:])[:8] + ":"
}

func appendSessionSignals(out []DiagnosticSignal, sum Summary, pfx string) []DiagnosticSignal {
	sessions := max(sum.Sessions["agy"], sum.Health.MainSessions)
	if sessions >= minDiagnosticSessions {
		kneeRate := round4(float64(sum.Health.SessionsOverKnee) / float64(sessions))
		if kneeRate >= 0.25 {
			prev := priorKneeRate(sum)
			sev := pickSev(kneeRate >= 0.50, "high", "medium")
			out = append(out, makeSignal(pfx, "context_knee_rate", sev, "H.sessions_over_knee",
				kneeRate, prev, sessions, fmt.Sprintf("%d of %d sessions reached >=60%% model context knee (%.1f%%)", sum.Health.SessionsOverKnee, sessions, kneeRate*100)))
		}
		if er := sum.D.ToolErrorRate["agy"]; er != nil && sum.B.ToolCalls["agy"] >= 10 && *er >= 0.08 {
			prev := priorMetricVal(sum, "D.tool_error_rate.agy")
			sev := pickSev(*er >= 0.15, "high", "medium")
			out = append(out, makeSignal(pfx, "tool_error_rate_high", sev, "D.tool_error_rate.agy",
				round4(*er), prev, sessions, fmt.Sprintf("tool error rate %.2f%% across %d sessions (%d tool calls)", *er*100, sessions, sum.B.ToolCalls["agy"])))
		}
		if sum.Health.SubagentsOverBudget >= 2 {
			prev := priorMetricVal(sum, "H.subagents_over_budget")
			sev := pickSev(sum.Health.SubagentsOverBudget >= 5, "high", "medium")
			out = append(out, makeSignal(pfx, "subagents_over_budget", sev, "H.subagents_over_budget",
				float64(sum.Health.SubagentsOverBudget), prev, sessions, fmt.Sprintf("%d subagent runs exceeded budget across %d sessions", sum.Health.SubagentsOverBudget, sessions)))
		}
		if sum.Health.GuardedDowngrades >= 1 {
			prev := priorMetricVal(sum, "H.guarded_downgrades")
			sev := pickSev(sum.Health.GuardedDowngrades >= 3, "high", "medium")
			out = append(out, makeSignal(pfx, "guarded_downgrades", sev, "H.guarded_downgrades",
				float64(sum.Health.GuardedDowngrades), prev, sessions, fmt.Sprintf("%d guarded subagent model downgrades across %d sessions", sum.Health.GuardedDowngrades, sessions)))
		}
	}
	if sum.A.RatingsCount >= minDiagnosticSessions && sum.A.MeanOutcomeScore != nil && *sum.A.MeanOutcomeScore < 3.5 {
		prev := priorMetricVal(sum, "A.mean_outcome_score")
		sev := pickSev(*sum.A.MeanOutcomeScore < 2.5, "high", "medium")
		out = append(out, makeSignal(pfx, "low_session_outcome", sev, "A.mean_outcome_score",
			round4(*sum.A.MeanOutcomeScore), prev, sum.A.RatingsCount, fmt.Sprintf("mean rated session outcome %.2f/5 across %d rated sessions", *sum.A.MeanOutcomeScore, sum.A.RatingsCount)))
	}
	return out
}

func appendAdherenceSignals(out []DiagnosticSignal, sum Summary, pfx string) []DiagnosticSignal {
	sessions := max(sum.Sessions["agy"], sum.Health.MainSessions)
	if sessions < minDiagnosticSessions {
		return out
	}
	if r := sum.C.SearchGrepRatio["agy"]; r != nil && sum.C.GrepCalls["agy"] >= 5 && *r < 1.0 {
		prev := priorMetricVal(sum, "C.search_grep_ratio.agy")
		sev := pickSev(*r < 0.5, "high", "medium")
		out = append(out, makeSignal(pfx, "search_grep_low", sev, "C.search_grep_ratio.agy",
			round4(*r), prev, sessions, fmt.Sprintf("search-to-grep ratio %.2f across %d sessions (%d search vs %d grep)", *r, sessions, sum.C.SQLiteVerbs["agy"], sum.C.GrepCalls["agy"])))
	}
	if retries := sum.D.RetryLoops["agy"]; retries >= 5 {
		prev := priorMetricVal(sum, "D.retry_loops.agy")
		sev := pickSev(retries >= sessions*2, "high", "medium")
		out = append(out, makeSignal(pfx, "retry_loops_elevated", sev, "D.retry_loops.agy",
			float64(retries), prev, sessions, fmt.Sprintf("%d retry loops across %d sessions", retries, sessions)))
	}
	if red := sum.C.RedundantReads["agy"]; red >= 5 {
		prev := priorMetricVal(sum, "C.redundant_reads.agy")
		sev := "low"
		if red >= sessions*3 {
			sev = "high"
		} else if red >= sessions {
			sev = "medium"
		}
		out = append(out, makeSignal(pfx, "redundant_reads_elevated", sev, "C.redundant_reads.agy",
			float64(red), prev, sessions, fmt.Sprintf("%d redundant file reads across %d sessions", red, sessions)))
	}
	if sum.A.Corrections >= 3 {
		prev := priorMetricVal(sum, "A.corrections")
		sev := pickSev(sum.A.Corrections >= 6, "high", "medium")
		out = append(out, makeSignal(pfx, "corrections_elevated", sev, "A.corrections",
			float64(sum.A.Corrections), prev, sessions, fmt.Sprintf("%d corrections recorded across %d sessions", sum.A.Corrections, sessions)))
	}
	if sum.A.EscapedBugs >= 2 {
		prev := priorMetricVal(sum, "A.escaped_bugs")
		sev := pickSev(sum.A.EscapedBugs >= 4, "high", "medium")
		out = append(out, makeSignal(pfx, "escaped_bugs_present", sev, "A.escaped_bugs",
			float64(sum.A.EscapedBugs), prev, sessions, fmt.Sprintf("%d escaped bugs recorded across %d sessions", sum.A.EscapedBugs, sessions)))
	}
	return out
}

func appendStructuralSignals(out []DiagnosticSignal, sum Summary, pfx string) []DiagnosticSignal {
	st := sum.Health.Structural
	if st.ProdPkgCycles >= minProdPkgCycles {
		out = append(out, makeSignal(pfx, "prod_pkg_cycles", "high", "H.structural.prod_pkg_cycles",
			float64(st.ProdPkgCycles), 0, st.ProdPkgCycles, fmt.Sprintf("%d production package dependency cycle(s) across %d packages", st.ProdPkgCycles, st.TotalProdPkgs)))
	}
	if st.HiddenCouplingEditPairs >= minHiddenCouplingPairs {
		sev := pickSev(st.HiddenCouplingEditPairs >= 8 || st.HiddenCouplingRate >= 0.40, "high", "medium")
		out = append(out, makeSignal(pfx, "hidden_coupling", sev, "H.structural.hidden_coupling_edit_pairs",
			float64(st.HiddenCouplingEditPairs), 0, st.HiddenCouplingEditPairs, fmt.Sprintf("%d co-edited file pairs lack AST edges (co-read %d, rate %.1f%%)", st.HiddenCouplingEditPairs, st.HiddenCouplingReadPairs, st.HiddenCouplingRate*100)))
	}
	sessions := max(sum.Sessions["agy"], sum.Health.MainSessions)
	churnHits := st.GodNodeClampHits + st.GodNodeRepeatReads
	if sessions >= minDiagnosticSessions && churnHits >= 5 {
		sev := pickSev(st.GodNodeChurnRate >= 0.35 || churnHits >= 15, "high", "medium")
		out = append(out, makeSignal(pfx, "god_node_churn", sev, "H.structural.god_node_churn_rate",
			st.GodNodeChurnRate, 0, sessions, fmt.Sprintf("top-10 hub files drew %d clamp hits and %d repeat reads (churn %.1f%%)", st.GodNodeClampHits, st.GodNodeRepeatReads, st.GodNodeChurnRate*100)))
	}
	if st.TotalCodeFiles >= minDiagnosticSessions && st.LargeFilesOver350 >= 3 && float64(st.LargeFilesOver350)/float64(st.TotalCodeFiles) >= 0.25 {
		sev := pickSev(float64(st.LargeFilesOver350)/float64(st.TotalCodeFiles) >= 0.50, "medium", "low")
		out = append(out, makeSignal(pfx, "large_files_concentration", sev, "H.structural.large_files_over_350",
			float64(st.LargeFilesOver350), 0, st.TotalCodeFiles, fmt.Sprintf("%d of %d production code files exceed 350 lines", st.LargeFilesOver350, st.TotalCodeFiles)))
	}
	return out
}

func appendRunAndEvalSignals(out []DiagnosticSignal, sum Summary, evals []EvalRow, normScope, pfx string) []DiagnosticSignal {
	if jTotal := sum.Health.JudgePassed + sum.Health.JudgeFailed; jTotal >= minDiagnosticRuns {
		failRate := round4(float64(sum.Health.JudgeFailed) / float64(jTotal))
		if failRate >= 0.33 {
			sev := pickSev(failRate >= 0.50, "high", "medium")
			out = append(out, makeSignal(pfx, "judge_fail_rate", sev, "F.judge_failed",
				failRate, 0, jTotal, fmt.Sprintf("%d of %d judge verdicts failed (%.1f%%)", sum.Health.JudgeFailed, jTotal, failRate*100)))
		}
	}
	if sum.F.Workers >= minDiagnosticRuns && float64(sum.F.Failed)/float64(sum.F.Workers) >= 0.33 {
		failRate := round4(float64(sum.F.Failed) / float64(sum.F.Workers))
		sev := pickSev(failRate >= 0.50, "high", "medium")
		out = append(out, makeSignal(pfx, "work_waves_failing", sev, "F.failed",
			failRate, 0, sum.F.Workers, fmt.Sprintf("%d of %d worker runs failed (%.1f%%)", sum.F.Failed, sum.F.Workers, failRate*100)))
	}
	if normScope == "global" {
		out = appendBackgroundRunSignals(out, sum, pfx)
	}
	return appendEvalSignals(out, evals, pfx)
}

func appendBackgroundRunSignals(out []DiagnosticSignal, sum Summary, pfx string) []DiagnosticSignal {
	if dr := sum.Health.DreamRuns; dr.Runs >= minDiagnosticRuns && float64(dr.Failed)/float64(dr.Runs) >= 0.33 {
		rate := round4(float64(dr.Failed) / float64(dr.Runs))
		sev := pickSev(rate >= 0.50, "high", "medium")
		out = append(out, makeSignal(pfx, "dream_runs_failing", sev, "H.background_runs.dream.failed",
			float64(dr.Failed), priorMetricVal(sum, "H.background_runs.dream.failed"), dr.Runs,
			fmt.Sprintf("%d of %d background dream runs failed (%.1f%%)", dr.Failed, dr.Runs, rate*100)))
	}
	if ar := sum.Health.AutoRuns; ar.Runs >= minDiagnosticRuns && float64(ar.Failed)/float64(ar.Runs) >= 0.33 {
		rate := round4(float64(ar.Failed) / float64(ar.Runs))
		sev := pickSev(rate >= 0.50, "high", "medium")
		out = append(out, makeSignal(pfx, "auto_runs_failing", sev, "H.background_runs.auto.failed",
			float64(ar.Failed), priorMetricVal(sum, "H.background_runs.auto.failed"), ar.Runs,
			fmt.Sprintf("%d of %d background auto runs failed (%.1f%%)", ar.Failed, ar.Runs, rate*100)))
	}
	return out
}

func appendEvalSignals(out []DiagnosticSignal, evals []EvalRow, pfx string) []DiagnosticSignal {
	totalRuns, totalPassed, failingRows := 0, 0, 0
	for _, r := range evals {
		if strings.EqualFold(r.Kind, "indicator") || r.N <= 0 {
			continue
		}
		totalRuns += r.N
		totalPassed += r.C
		if r.C < r.N {
			failingRows++
		}
	}
	if totalRuns >= minDiagnosticRuns && totalPassed < totalRuns {
		passRate := round4(float64(totalPassed) / float64(totalRuns))
		if passRate < 0.85 {
			sev := pickSev(passRate < 0.60, "high", "medium")
			out = append(out, makeSignal(pfx, "eval_pass_rate_low", sev, "eval.pass_rate",
				passRate, 0, totalRuns, fmt.Sprintf("eval pass rate %.1f%% across %d graded runs (%d failing rows)", passRate*100, totalRuns, failingRows)))
		}
	}
	return out
}

func makeSignal(pfx, rule, sev, metric string, cur, prev float64, sample int, summary string) DiagnosticSignal {
	c, p := round4(cur), round4(prev)
	return DiagnosticSignal{
		SignalID:   pfx + rule,
		Severity:   sev,
		Rule:       rule,
		Metric:     metric,
		Current:    c,
		Previous:   p,
		Delta:      round4(c - p),
		SampleSize: sample,
		Summary:    summary,
	}
}

func dedupAndSortSignals(in []DiagnosticSignal) []DiagnosticSignal {
	byID := make(map[string]DiagnosticSignal, len(in))
	for _, sig := range in {
		if sig.SignalID == "" {
			continue
		}
		if existing, ok := byID[sig.SignalID]; !ok || severityRank(sig.Severity) > severityRank(existing.Severity) {
			byID[sig.SignalID] = sig
		}
	}
	out := make([]DiagnosticSignal, 0, len(byID))
	for _, sig := range byID {
		out = append(out, sig)
	}
	sort.Slice(out, func(i, j int) bool {
		ri, rj := severityRank(out[i].Severity), severityRank(out[j].Severity)
		if ri != rj {
			return ri > rj
		}
		return out[i].SignalID < out[j].SignalID
	})
	return out
}

func severityRank(sev string) int {
	switch sev {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func pickSev(cond bool, t, f string) string {
	if cond {
		return t
	}
	return f
}

func priorMetricVal(sum Summary, key string) float64 {
	if sum.Deltas != nil && sum.Deltas.PreviousSessions >= minDiagnosticSessions {
		if md, ok := sum.Deltas.Metrics[key]; ok {
			return md.Previous
		}
	}
	return 0
}

func priorKneeRate(sum Summary) float64 {
	if sum.Deltas != nil && sum.Deltas.PreviousSessions >= minDiagnosticSessions && sum.Deltas.KneeRatePct != nil {
		return round4(sum.Deltas.KneeRatePct.Previous / 100.0)
	}
	return 0
}
