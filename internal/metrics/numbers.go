package metrics

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kesokaj/fyr/internal/search"
)

// Snapshot opens a read-only copy of the store at path, made in tmp: a dry
// run and the dream's prompt read it without creating -wal or -shm beside
// the store or running a migration. A missing store is os.ErrNotExist.
func Snapshot(path, tmp string) (*Store, error) {
	if _, err := os.Lstat(path); err != nil {
		return nil, err
	}
	db, err := search.OpenCopy(filepath.Dir(path), filepath.Base(path), tmp)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, path: path}
	var v string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&v); err != nil {
		db.Close()
		return nil, fmt.Errorf("metrics: %s: %w", path, err)
	}
	if v != fmt.Sprint(len(migrations)) {
		db.Close()
		return nil, fmt.Errorf("metrics.db schema %s, this binary reads %d; run fyr metrics ingest first", v, len(migrations))
	}
	return s, nil
}

// Numbers is the summary as metric keys and numbers only: every key is one of
// this package's own names (no tool, skill, agent, label or path), the form
// the dream's prompt carries (R3, ADR-055a). A metric a CLI cannot produce is
// absent, never 0.
func (s *Summary) Numbers() map[string]float64 {
	n := map[string]float64{}
	put := func(k string, v float64) { n[k] = v }
	for _, v := range s.columns() {
		s.putColumn(put, v)
	}
	for _, st := range []string{"PASS", "FAIL", "BLOCKED"} {
		put("A.verifier."+st, float64(s.A.Verifier[st]))
	}
	for _, sev := range []string{"high", "medium", "low"} {
		put("A.review_findings."+sev, float64(s.A.ReviewFindings[sev]))
	}
	put("B.context_target", float64(s.B.ContextTarget))
	put("A.escaped_bugs", float64(s.A.EscapedBugs))
	put("A.corrections", float64(s.A.Corrections))
	put("A.ratings_count", float64(s.A.RatingsCount))
	if s.A.MeanOutcomeScore != nil {
		put("A.mean_outcome_score", *s.A.MeanOutcomeScore)
	}
	if s.A.MeanTokenEfficiency != nil {
		put("A.mean_token_efficiency", *s.A.MeanTokenEfficiency)
	}
	for _, k := range forbiddenKeys {
		put("C.forbidden."+k[1], float64(s.C.Forbidden[k[0]]))
	}
	s.putSecondBrainAndWaves(put)
	s.putHealthNumbers(put)
	s.putStructuralNumbers(put)
	s.putBackgroundRunNumbers(put)
	return n
}

func (s *Summary) putSecondBrainAndWaves(put func(string, float64)) {
	put("E.turn_vets", float64(s.E.TurnVets))
	if s.E.MeanVetScore != nil {
		put("E.mean_vet_score", *s.E.MeanVetScore)
	}
	put("E.vet_first_turns", float64(s.E.VetFirstTurns))
	put("E.bypassed_turns", float64(s.E.BypassedTurns))
	put("E.pivots_seeded", float64(s.E.PivotsSeeded))
	put("E.ledger_saved", float64(s.E.LedgerSaved))
	put("F.waves", float64(s.F.Waves))
	put("F.workers", float64(s.F.Workers))
	put("F.passed", float64(s.F.Passed))
	put("F.failed", float64(s.F.Failed))
	put("F.judge_passed", float64(s.F.JudgePassed))
	put("F.judge_failed", float64(s.F.JudgeFailed))
	put("F.judge_fallback", float64(s.F.JudgeFallback))
}

func (s *Summary) putHealthNumbers(put func(string, float64)) {
	h := s.Health
	put("H.sessions_over_knee", float64(h.SessionsOverKnee))
	put("H.main_sessions", float64(h.MainSessions))
	put("H.subagents_over_budget", float64(h.SubagentsOverBudget))
	put("H.hook_denials", float64(h.HookDenials))
	put("H.clamped_saves", float64(h.ClampedSaves))
	put("H.hook_blocks_ledger", float64(h.HookBlocksLedger))
	put("H.hook_blocks_guards", float64(h.HookBlocksGuards))
	put("H.guarded_downgrades", float64(h.GuardedDowngrades))
	put("H.search_db_compactions", float64(h.SearchDBCompactions))
	put("H.step_compactions", float64(h.StepCompactions))
}

func (s *Summary) putStructuralNumbers(put func(string, float64)) {
	st := s.Health.Structural
	if st.TotalCodeFiles == 0 && st.TotalProdPkgs == 0 && st.HiddenCouplingPairs == 0 &&
		st.HiddenCouplingEditPairs == 0 && st.HiddenCouplingReadPairs == 0 &&
		st.GodNodeClampHits == 0 && st.GodNodeRepeatReads == 0 && st.ProdPkgCycles == 0 && st.LargeFilesOver350 == 0 {
		return
	}
	put("H.structural.hidden_coupling_pairs", float64(st.HiddenCouplingPairs))
	put("H.structural.hidden_coupling_edit_pairs", float64(st.HiddenCouplingEditPairs))
	put("H.structural.hidden_coupling_read_pairs", float64(st.HiddenCouplingReadPairs))
	put("H.structural.hidden_coupling_rate", st.HiddenCouplingRate)
	put("H.structural.god_node_clamp_hits", float64(st.GodNodeClampHits))
	put("H.structural.god_node_repeat_reads", float64(st.GodNodeRepeatReads))
	put("H.structural.god_node_churn_rate", st.GodNodeChurnRate)
	put("H.structural.prod_pkg_cycles", float64(st.ProdPkgCycles))
	put("H.structural.hub_concentration", st.HubConcentration)
	put("H.structural.large_files_over_350", float64(st.LargeFilesOver350))
	put("H.structural.total_code_files", float64(st.TotalCodeFiles))
	put("H.structural.total_prod_pkgs", float64(st.TotalProdPkgs))
}

func (s *Summary) putBackgroundRunNumbers(put func(string, float64)) {
	dr, ar := s.Health.DreamRuns, s.Health.AutoRuns
	if dr.Runs+ar.Runs == 0 {
		return
	}
	put("H.background_runs.total", float64(dr.Runs+ar.Runs))
	put("H.background_runs.failed", float64(dr.Failed+ar.Failed))
	if dr.Runs > 0 {
		put("H.background_runs.dream.runs", float64(dr.Runs))
		put("H.background_runs.dream.passed", float64(dr.Passed))
		put("H.background_runs.dream.failed", float64(dr.Failed))
		put("H.background_runs.dream.mean_duration_ms", dr.MeanDurationMS)
		put("H.background_runs.dream.projects_checked", float64(dr.ProjectsChecked))
		put("H.background_runs.dream.proposals_written", float64(dr.ProposalsWritten))
		put("H.background_runs.dream.ratings_written", float64(dr.RatingsWritten))
	}
	if ar.Runs > 0 {
		put("H.background_runs.auto.runs", float64(ar.Runs))
		put("H.background_runs.auto.passed", float64(ar.Passed))
		put("H.background_runs.auto.failed", float64(ar.Failed))
		put("H.background_runs.auto.mean_duration_ms", ar.MeanDurationMS)
		put("H.background_runs.auto.projects_checked", float64(ar.ProjectsChecked))
		put("H.background_runs.auto.proposals_written", float64(ar.ProposalsWritten))
		put("H.background_runs.auto.ratings_written", float64(ar.RatingsWritten))
		put("H.background_runs.auto.workers_ran", float64(ar.WorkersRan))
		put("H.background_runs.auto.applied", float64(ar.Applied))
	}
}

// putColumn puts column v's metrics, each key suffixed with the column's
// name; a metric the column's CLI cannot produce is not put.
func (s *Summary) putColumn(put func(string, float64), v string) {
	put("sessions."+v, float64(s.Sessions[v]))
	sub := 0
	for _, c := range s.Subagents[v] {
		sub += c
	}
	put("subagents."+v, float64(sub))
	c := s.A.DoDClosed[v]
	put("A.dod_closed."+v, float64(c.Closed))
	put("A.dod_ambiguous."+v, float64(c.Ambiguous))
	put("A.dod_of."+v, float64(c.Of))
	put("B.tool_calls."+v, float64(s.B.ToolCalls[v]))
	put("B.turns."+v, float64(s.B.Turns[v]))
	if t := s.B.Tokens[v]; t != nil {
		put("B.tokens_in."+v, float64(t.In))
		put("B.tokens_out."+v, float64(t.Out))
		put("B.cache_read."+v, float64(t.CacheRead))
	}
	if p := s.B.CostUSD[v]; p != nil {
		put("B.cost_usd."+v, *p)
	}
	put("B.wall_min."+v, s.B.WallMin[v])
	put("B.active_min."+v, s.B.ActiveMin[v])
	put("B.model_min."+v, s.B.ModelMin[v])
	put("B.tool_min."+v, s.B.ToolMin[v])
	put("B.wait_min."+v, s.B.WaitMin[v])
	put("B.idle_min."+v, s.B.IdleMin[v])
	put("B.peak_context."+v, float64(s.B.PeakContext[v]))
	if p := s.B.SessionsOverContextTarget[v]; p != nil {
		put("B.sessions_over_context_target."+v, float64(*p))
	}
	if p := s.B.Compactions[v]; p != nil {
		put("B.compactions."+v, float64(*p))
	}
	s.putColumnAdherenceAndReliability(put, v)
}

func (s *Summary) putColumnAdherenceAndReliability(put func(string, float64), v string) {
	put("C.sqlite_verbs."+v, float64(s.C.SQLiteVerbs[v]))
	put("C.grep_calls."+v, float64(s.C.GrepCalls[v]))
	if p := s.C.SearchGrepRatio[v]; p != nil {
		put("C.search_grep_ratio."+v, *p)
	}
	if s.C.RedundantReads != nil {
		if c, ok := s.C.RedundantReads[v]; ok {
			put("C.redundant_reads."+v, float64(c))
		}
	}
	if p := s.C.RangedReadPct[v]; p != nil {
		put("C.ranged_read_pct."+v, *p)
	}
	if p := s.D.ToolErrorRate[v]; p != nil {
		put("D.tool_error_rate."+v, *p)
	}
	if p := s.D.Denials[v]; p != nil {
		put("D.denials."+v, float64(*p))
	}
	if p := s.D.Interrupts[v]; p != nil {
		put("D.interrupts."+v, float64(*p))
	}
	put("D.retry_loops."+v, float64(s.D.RetryLoops[v]))
}

// forbiddenKeys maps each forbidden shape to its key in Numbers.
var forbiddenKeys = [][2]string{
	{"git add -A", "git_add_all"}, {"git add .", "git_add_dot"}, {"git push --force", "push_force"},
	{"--no-verify", "no_verify"}, {"commit chained to test", "commit_chained_to_test"},
}
