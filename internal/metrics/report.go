package metrics

import (
	"database/sql"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/metrics/report"
	"github.com/kesokaj/fyr/internal/search"
)

const (
	VerdictImproving = report.VerdictImproving
	VerdictSteady    = report.VerdictSteady
	VerdictRegress   = report.VerdictRegress

	StatusPendingData     = report.StatusPendingData
	StatusRecovered       = report.StatusRecovered
	StatusStillRegressing = report.StatusStillRegressing

	DefaultTrajectoryLimit = report.DefaultTrajectoryLimit

	minRecoverySessions = report.MinRecoverySessions
)

type (
	DailyDigest           = report.DailyDigest
	RollingKPIDeltas      = report.RollingKPIDeltas
	ASTStructuralDiff     = report.ASTStructuralDiff
	PreInvocationSnapshot = report.PreInvocationSnapshot
	PreInvocationDiff     = report.PreInvocationDiff
	DigestAppliedAction   = report.DigestAppliedAction
	DigestAction          = report.DigestAction
	TrajectoryOptions     = report.TrajectoryOptions
	SessionTrajectory     = report.SessionTrajectory
	JSONScorecard         = report.JSONScorecard
	JSONAutonomy          = report.JSONAutonomy
	digestASTCol          = report.DigestASTCol
)

// JSONReport is the JSON payload written by RenderJSONReport and WriteJSONReport.
type JSONReport struct {
	GeneratedAt string `json:"generated_at"`
	*Summary
	Scorecard          JSONScorecard `json:"scorecard"`
	BackgroundAutonomy JSONAutonomy  `json:"background_autonomy"`
	Evals              *EvalRows     `json:"evals,omitempty"`
}

// renderReport is RenderReport; a test replaces it with one that fails.
var renderReport = RenderReport

// ReportPath is the default report file under home, stamped to the minute.
func ReportPath(home string, now time.Time) string { return report.ReportPath(home, now) }

// DailyReportPath returns <home>/.config/fyr/reports/YYYY-MM-DD-daily-metrics.html.
func DailyReportPath(home string, now time.Time) string { return report.DailyReportPath(home, now) }

// WriteDailyReport writes the daily HTML report once per calendar day (0600, O_EXCL).
func WriteDailyReport(sum *Summary, evals *EvalRows, home string, now time.Time) error {
	return report.WriteDailyReportWith(adaptRender(sum, evals), toReportSummary(sum), toReportEvalView(evals), home, now)
}

// WriteReport renders sum and evals (nil for none) to file (mode 0600, O_EXCL).
func WriteReport(sum *Summary, evals *EvalRows, file string, now time.Time) error {
	return report.WriteReportWith(adaptRender(sum, evals), toReportSummary(sum), toReportEvalView(evals), file, now)
}

// RenderReport writes the report's HTML for sum and evals (nil for none).
func RenderReport(w io.Writer, sum *Summary, evals *EvalRows, now time.Time) error {
	return report.RenderReport(w, toReportSummary(sum), toReportEvalView(evals), now)
}

// RenderJSONReport writes the JSON usage report for sum and evals to w.
func RenderJSONReport(w io.Writer, sum *Summary, evals *EvalRows, now time.Time) error {
	return report.RenderJSONReport(w, toReportSummary(sum), toReportEvalView(evals), now)
}

// WriteJSONReport renders sum and evals as JSON to file (mode 0600, O_EXCL).
func WriteJSONReport(sum *Summary, evals *EvalRows, file string, now time.Time) error {
	return report.WriteJSONReport(toReportSummary(sum), toReportEvalView(evals), file, now)
}

// RenderMarkdownReport writes the Markdown usage report to w.
func RenderMarkdownReport(w io.Writer, sum *Summary, evals *EvalRows, now time.Time) error {
	return report.RenderMarkdownReport(w, toReportSummary(sum), toReportEvalView(evals), now)
}

// WriteMarkdownReport renders sum and evals as Markdown to file (mode 0600, O_EXCL).
func WriteMarkdownReport(sum *Summary, evals *EvalRows, file string, now time.Time) error {
	return report.WriteMarkdownReport(toReportSummary(sum), toReportEvalView(evals), file, now)
}

// ReportEvalsWithDevFallback returns eval rows from s, falling back read-only to
// <home>/<StateDir>/dev/fyr-eval/<StateDir>/metrics.db when empty.
func ReportEvalsWithDevFallback(s *Store, home string, o ShowOptions) (*EvalRows, error) {
	if o.Project != "" || o.Label != "" {
		return nil, nil
	}
	ev, err := s.Evals(EvalOptions{Since: o.Since, Now: o.Now})
	if err != nil || len(ev.Rows) > 0 {
		return ev, err
	}
	h := resolveMetricsHome(ShowOptions{Home: home}, s.path)
	if h == "" {
		return ev, nil
	}
	devStore, cleanup, ok := OpenDevEvalFallbackRO(h)
	if !ok {
		return ev, nil
	}
	defer cleanup()
	devEv, derr := devStore.Evals(EvalOptions{Since: o.Since, Now: o.Now})
	if derr != nil || len(devEv.Rows) == 0 {
		return ev, nil
	}
	return devEv, nil
}

// OpenDevEvalFallbackRO opens <home>/<StateDir>/dev/fyr-eval/<StateDir>/metrics.db
// via a symlink-safe read-only copy when present and schema >= 5.
func OpenDevEvalFallbackRO(home string) (*Store, func(), bool) {
	db, devPath, cleanup, ok := report.OpenDevEvalDB(home, len(migrations))
	if !ok {
		return nil, nil, false
	}
	return &Store{db: db, path: devPath}, cleanup, true
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
	prevSum, err := store.Show(ShowOptions{Home: home, Project: repoRoot, Since: since7.Add(-7 * 24 * time.Hour), Now: since7, SkipDeltas: true})
	if err != nil {
		return DailyDigest{}, err
	}
	prevDigest, todayDigest, err := loadPriorDigests(store, repoRoot, day)
	if err != nil {
		return DailyDigest{}, err
	}
	var extra []DigestAction
	if shift := detectModelShiftAudit(store, home, repoRoot, nowUTC); shift != nil {
		extra = append(extra, *shift)
	}
	return report.AssembleDailyDigest(report.DigestInput{
		Day: day, NowUTC: nowUTC, Home: home, RepoRoot: repoRoot,
		CurSum: toReportSummary(curSum), PrevSum: toReportSummary(prevSum),
		PrevDigest: prevDigest, TodayDigest: todayDigest, ExtraSignals: extra,
		Hooks: digestSignalHooks(),
	}), nil
}

// SaveDailyDigest persists d into the daily_digests table (upserting by day).
func SaveDailyDigest(s *Store, d DailyDigest) error { return report.SaveDailyDigest(storeDB(s), d) }

// LatestDailyDigest loads the most recent DailyDigest from daily_digests.
func LatestDailyDigest(s *Store) (*DailyDigest, error) { return report.LatestDailyDigest(storeDB(s)) }

// PreviousDailyDigest loads the newest DailyDigest strictly before beforeDay.
func PreviousDailyDigest(s *Store, beforeDay string) (*DailyDigest, error) {
	return report.PreviousDailyDigest(storeDB(s), beforeDay)
}

// RecordDigestAppliedAction persists one applied-action checkpoint into today's row.
func RecordDigestAppliedAction(store *Store, home, repoRoot string, now time.Time, act DigestAppliedAction) error {
	return report.RecordDigestAppliedAction(storeDB(store), home, now, act, func(t time.Time) (DailyDigest, error) {
		return BuildDailyDigest(store, home, "", t)
	})
}

// LatestDailyDigestPath returns the path of the fail-open digest brief under home.
func LatestDailyDigestPath(home string) string { return report.LatestDailyDigestPath(home) }

// WriteLatestDailyDigest atomically writes d as compact 0600 JSON under home.
func WriteLatestDailyDigest(home string, d DailyDigest) error {
	return report.WriteLatestDailyDigest(home, d)
}

// ReadLatestDailyDigest reads latest-daily-digest.json fail-open.
func ReadLatestDailyDigest(home string) (*DailyDigest, error) {
	return report.ReadLatestDailyDigest(home)
}

// EvaluateActionRecovery determines post-fix recovery status for one applied action.
func EvaluateActionRecovery(act DigestAppliedAction, curSum *Summary, prev *DailyDigest, day string, postSessions int, activeSignals []DigestAction) string {
	return report.EvaluateActionRecovery(act, toReportSummary(curSum), prev, day, postSessions, activeSignals)
}

// RenderDailyDigestJSON writes d as indented JSON to w.
func RenderDailyDigestJSON(w io.Writer, d DailyDigest) error {
	return report.RenderDailyDigestJSON(w, d)
}

// RenderDailyDigestText renders a human-readable summary of d.
func RenderDailyDigestText(d DailyDigest) string { return report.RenderDailyDigestText(d) }

// Trajectories returns session trajectories matching o, newest first.
func (s *Store) Trajectories(o TrajectoryOptions) ([]SessionTrajectory, error) {
	deps := report.TrajectoryDeps{
		Home: resolveMetricsHome(ShowOptions{}, s.path),
		StepsOf: func(sessionID string) ([]report.TrajectoryStep, error) {
			steps, err := s.stepsOf(sessionID)
			if err != nil {
				return nil, err
			}
			out := make([]report.TrajectoryStep, len(steps))
			for i, st := range steps {
				out[i] = report.TrajectoryStep{
					Kind: st.Kind, Tool: st.Tool, Hash: st.Hash,
					IsErr: st.IsErr, Denied: st.Denied,
					In: st.In, CR: st.CR, CW: st.CW, Ctx: st.Ctx,
				}
			}
			return out, nil
		},
		ParseFileHashRange: parseFileHashRange,
		RecordReadInterval: recordReadInterval,
	}
	return report.Trajectories(s.db, o, deps)
}

// WriteTrajectoriesJSON encodes trajectories as indented JSON to w.
func WriteTrajectoriesJSON(w io.Writer, list []SessionTrajectory) error {
	return report.WriteTrajectoriesJSON(w, list)
}

// WriteTrajectoriesText renders trajectories in human-readable form to w.
func WriteTrajectoriesText(w io.Writer, list []SessionTrajectory) {
	report.WriteTrajectoriesText(w, list)
}

func storeDB(s *Store) *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

func adaptRender(sum *Summary, evals *EvalRows) report.RenderFunc {
	return func(w io.Writer, _ *report.Summary, _ *report.EvalView, now time.Time) error {
		return renderReport(w, sum, evals, now)
	}
}

func toReportSummary(s *Summary) *report.Summary {
	if s == nil {
		return nil
	}
	return &report.Summary{
		Window: s.Window, Columns: s.Columns, Sessions: s.Sessions, Subagents: s.Subagents,
		A: s.A, B: s.B, C: s.C, D: s.D, E: s.E, F: s.F,
		TriggerCoverage: s.TriggerCoverage, Health: s.Health,
		Orchestration: s.Orchestration, SIM: s.SIM,
		SkillTelemetry: s.SkillTelemetry, RuleTelemetry: s.RuleTelemetry,
		TagCohort: s.TagCohort, AgentRoles: s.AgentRoles,
		Deltas: s.Deltas, Diagnostics: s.Diagnostics, DailyDigest: s.DailyDigest,
		Projects: s.Projects, Daily: s.Daily, Notes: s.Notes,
		ReadCalls: s.ReadCallsMap(), NumbersFn: s.Numbers,
	}
}

func toReportEvalView(e *EvalRows) *report.EvalView {
	if e == nil || len(e.Rows) == 0 {
		return nil
	}
	return &report.EvalView{
		Head: evalHead, Rows: e.Table(), Lines: e.Suite.lines(),
		RowCount: e.Suite.Rows, Trials: e.Suite.Trials, RawJSON: e,
	}
}

func loadPriorDigests(store *Store, repoRoot, day string) (*DailyDigest, *DailyDigest, error) {
	if strings.TrimSpace(repoRoot) != "" {
		return nil, nil, nil
	}
	prev, err := PreviousDailyDigest(store, day)
	if err != nil {
		return nil, nil, err
	}
	today, _ := report.DailyDigestByDay(store.db, day)
	return prev, today, nil
}

func digestSignalHooks() report.DigestSignalHooks {
	return report.DigestSignalHooks{
		MakeStructuralSignal: func(astSnap search.StructuralSignals, _ ASTStructuralDiff, repoRoot, msg, sev string) DiagnosticSignal {
			scope := pickSev(strings.TrimSpace(repoRoot) != "", "project", "global")
			_, pfx := resolveDiagnosticScope(scope, repoRoot)
			return makeSignal(pfx, "structural_regression", sev, "H.structural.regression",
				float64(astSnap.ProdPkgCycles+astSnap.HiddenCouplingEditPairs+astSnap.LargeFilesOver350), 0, max(1, astSnap.TotalCodeFiles), msg)
		},
		FinalizeSignals: func(raw []DiagnosticSignal) []DiagnosticSignal {
			dedup := dedupAndSortSignals(raw)
			assignDiagnosticActions(dedup)
			return dedup
		},
	}
}

func populateAutonomySignals(sum *Summary, home string, o ShowOptions, all []sess) {
	sum.Health.KeptAutoBranches = report.CountKeptAutoBranches(home, o.Project, o.IncludeSandboxes, distinctProjectRoots(o, home, all), isSandboxProjectRoot)
	if home != "" {
		q, c := report.CountProposalsFromBrainDB(home, o.Project)
		sum.Health.ProposalsQueued = q
		sum.Health.ProposalsClosed = c
	}
}

func mergeDevEvalPassK(sum *Summary, home string, since, now time.Time) {
	devStore, cleanup, ok := OpenDevEvalFallbackRO(home)
	if !ok {
		return
	}
	defer cleanup()
	devOpt := ShowOptions{Since: since, Now: now, AllLabels: true}
	devAll, err := devStore.sessionsIn(showQuery(devOpt, since, now))
	if err != nil {
		return
	}
	devTrials, err := devStore.trialsIn(devOpt, since, now)
	if err != nil {
		return
	}
	devClaimed, err := devStore.trialRuns()
	if err != nil {
		return
	}
	m := map[string]PassK{}
	tmp := &Summary{A: Outcome{PassK: m}}
	tmp.PassK(devAll, devTrials, devClaimed, showEst())
	merged, changed := report.MergeUnseenPassK(sum.A.PassK, tmp.A.PassK, evalPrefix)
	m = merged
	sum.A.PassK = m
	if changed {
		sum.Notes = append(sum.Notes, "evals: merged pass^k from dev/fyr-eval store")
	}
}

func openCopySQLiteFile(scratchHome, base string, relParts ...string) (*sql.DB, func(), bool) {
	return report.OpenCopySQLiteFile(scratchHome, base, relParts...)
}

func readBoundedRegularFile(base string, relParts ...string) ([]byte, bool) {
	return report.ReadBoundedRegularFile(base, relParts...)
}

func tableCols(db *sql.DB, table string) map[string]bool { return report.TableCols(db, table) }

func priorDayString(day string) string { return report.PriorDayString(day) }

func isMetricBetter(metric string, cur, base float64) bool {
	return report.IsMetricBetter(metric, cur, base)
}

func lookupSummaryMetric(sum *Summary, metric string) (float64, bool) {
	return report.LookupSummaryMetric(toReportSummary(sum), metric)
}
