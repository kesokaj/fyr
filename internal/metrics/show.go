package metrics

import (
	"database/sql"
	"time"

	"github.com/kesokaj/fyr/internal/metrics/report"
	"github.com/kesokaj/fyr/internal/metrics/show"
)

// Show computes the summary for the options' window and filters.
func (s *Store) Show(o ShowOptions) (*Summary, error) {
	return show.Show(s.showDeps(), o)
}

func showEst() show.Estimators {
	return show.Estimators{AtK: PassAtK, PowK: PassPowK, Wilson: Wilson}
}

func showRootFuncs() show.RootFuncs {
	return show.RootFuncs{
		ResolveRepoID:            ResolveRepoID,
		IsSandboxProjectRoot:     isSandboxProjectRoot,
		IsNonCollapsibleRoot:     isNonCollapsibleRoot,
		IsTempRoot:               isTempRoot,
		IsAgyWorktreePath:        isAgyWorktreePath,
		ResolveGitWorktreeOnDisk: resolveGitWorktreeOnDisk,
		ResolveFallbackRoot:      resolveFallbackRoot,
		ResolveSessionRoots:      resolveSessionRoots,
		DecodeWaveStamp:          decodeWaveStamp,
	}
}

func (s *Store) showDeps() show.Deps {
	mpk := mergeDevEvalPassK
	return show.Deps{
		DB:                          s.db,
		StorePath:                   s.path,
		Est:                         showEst(),
		Roots:                       showRootFuncs(),
		TrialsIn:                    s.trialsIn,
		TrialRuns:                   s.trialRuns,
		BrainHasSIMTables:           brainHasSIMTables,
		ScanOrchestrationRuns:       scanShowOrchestrationRuns,
		SessionRatings:              s.showSessionRatings,
		MergeDevEvalPassK:           mpk,
		PopulateJudgeVerdictsHealth: s.populateJudgeVerdictsHealth,
		PopulateAutonomySignals:     populateAutonomySignals,
		PopulateTriggerCoverage:     populateTriggerCoverage,
		PopulateSessionRatings:      s.populateSessionRatings,
		PopulateBackgroundRuns:      s.populateBackgroundRuns,
		LoadEvalRows:                s.loadShowEvalRows,
		PopulateWindowDeltas:        s.populateWindowDeltas,
		PopulateDiagnostics:         s.populateDiagnostics,
	}
}

func scanShowOrchestrationRuns(db *sql.DB, q string, args []any) ([]show.OrchestrationRunRow, error) {
	rows, err := scanOrchestrationRuns(db, q, args)
	if err != nil {
		return nil, err
	}
	out := make([]show.OrchestrationRunRow, len(rows))
	for i, row := range rows {
		ok := row.Passed
		out[i] = show.OrchestrationRunRow{
			Kind: row.Kind, Status: row.Status, HaltCode: row.HaltCode, Verdict: row.Verdict,
			Units: row.Units, DefectRounds: row.DefectRounds, Passed: ok, Failed: row.Failed,
			DurationMS: row.DurationMS,
		}
	}
	return out, nil
}

func (s *Store) showSessionRatings(project string, since, now time.Time) ([]show.RatingRow, error) {
	rows, err := s.SessionRatings(project, since, now)
	if err != nil {
		return nil, err
	}
	out := make([]show.RatingRow, len(rows))
	for i, row := range rows {
		out[i] = show.RatingRow{
			SessionID: row.SessionID, OutcomeScore: row.OutcomeScore,
			SkillAction: row.SkillAction, TargetSkill: row.TargetSkill,
		}
	}
	return out, nil
}

func (s *Store) loadShowEvalRows(home string, since, now time.Time) (any, error) {
	ev, err := ReportEvalsWithDevFallback(s, home, ShowOptions{Since: since, Now: now})
	if err != nil || ev == nil {
		return nil, err
	}
	return ev.Rows, nil
}

func resolveMetricsHome(o ShowOptions, storePath string) string {
	return show.ResolveMetricsHome(o, storePath)
}

func showQuery(o ShowOptions, since, now time.Time) (string, []any) {
	return show.ShowQuery(o, since, now)
}

func windowAndProjectWhere(tsCol string, o ShowOptions, since, now time.Time) (string, []any) {
	return show.WindowAndProjectWhere(tsCol, o, since, now)
}

func (s *Store) sessionsIn(q string, args []any) ([]sess, error) {
	return show.SessionsIn(s.db, q, args)
}

func (s *Store) stepsOf(id string) ([]stp, error) {
	return show.StepsOf(s.db, id)
}

func (s *Store) loadShowData(all []sess) (*showData, error) {
	return show.LoadShowData(s.db, all)
}

func newSummary(since, now time.Time) *Summary { return show.NewSummary(since, now) }
func round4(v float64) float64                 { return report.Round4(v) }
func normalizeMilli(ts int64) int64            { return show.NormalizeMilli(ts) }

func parseFileHashRange(h string) (bool, string, int, int) { return show.ParseFileHashRange(h) }
func recordReadInterval(seen map[string][][2]int, base string, start, end int) bool {
	return show.RecordReadInterval(seen, base, start, end)
}

func subagentCapForRole(agent string) int { return show.SubagentCapForRole(agent) }
func isSQLiteVerb(v string) bool          { return show.IsSQLiteVerb(v) }
func sameModelFamily(workerModel, judgeModel string) bool {
	return show.SameModelFamily(workerModel, judgeModel)
}
func distinctSessionIDs(all []sess) []string      { return show.DistinctSessionIDs(all) }
func inPlaceholders(ids []string) (string, []any) { return show.InPlaceholders(ids) }

func distinctProjectRoots(o ShowOptions, home string, all []sess) []string {
	return show.DistinctProjectRoots(o, home, all, isSandboxProjectRoot)
}

func populateAdherenceVerbs(sum *Summary, all []sess, stepsByID map[string][]stp) {
	show.PopulateAdherenceVerbs(sum, all, stepsByID)
}

func collectStepHealth(h *HealthSignals, sum *Summary, all []sess, stepsByID map[string][]stp, home string) map[string]bool {
	return show.CollectStepHealth(h, sum, all, stepsByID, home)
}

func buildRootCanonicalizerWithRoots(all []sess, vetsByRoot map[string]*projVetAcc, byID, wtMap map[string]string) func(string) string {
	return show.BuildRootCanonicalizerWithRoots(all, vetsByRoot, byID, wtMap, showRootFuncs())
}
