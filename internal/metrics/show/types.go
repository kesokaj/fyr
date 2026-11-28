package show

import (
	"database/sql"
	"time"

	r "github.com/kesokaj/fyr/internal/metrics/report"
)

const (
	// DefaultContextTarget is the context target when none is given.
	DefaultContextTarget = r.DefaultContextTarget
	// ActiveGap is the longest pause still counted as active time (spec B2).
	ActiveGap = 5 * time.Minute
	// Historical is the historical Claude Code vendor column name.
	Historical = r.HistoricalVendor
	// ShowBatchChunk is the SQL IN(...) chunk size for batched show loads.
	ShowBatchChunk = 200
)

// Options filter what show and report read (spec § Verbs).
type Options struct {
	Home             string
	Since            time.Time // zero: 30 days before Now
	Now              time.Time // zero: time.Now()
	SinceTag         string
	Project          string
	Label            string
	AllLabels        bool
	IncludeSandboxes bool
	ContextTarget    int64
	SkipDeltas       bool
}

// Summary is `show --json` (spec § Output).
type Summary struct {
	Window          map[string]string         `json:"window"`
	Columns         []string                  `json:"columns"`
	Sessions        map[string]int            `json:"sessions"`
	Subagents       map[string]map[string]int `json:"subagents"`
	A               r.Outcome                 `json:"A"`
	B               r.Efficiency              `json:"B"`
	C               r.Adherence               `json:"C"`
	D               r.Reliability             `json:"D"`
	E               r.SecondBrain             `json:"E"`
	F               r.WorkSummary             `json:"F"`
	TriggerCoverage r.TriggerCoverage         `json:"trigger_coverage"`
	Health          r.HealthSignals           `json:"health"`
	Orchestration   *r.OrchestrationSummary   `json:"orchestration,omitempty"`
	SIM             *r.SIMSummary             `json:"sim,omitempty"`
	SkillTelemetry  []r.SkillTelemetry        `json:"skill_telemetry,omitempty"`
	RuleTelemetry   []r.RuleTelemetry         `json:"rule_telemetry,omitempty"`
	TagCohort       *r.TagCohort              `json:"tag_cohort,omitempty"`
	AgentRoles      []r.AgentRoleMetrics      `json:"agent_roles,omitempty"`
	Deltas          *r.WindowDeltas           `json:"deltas,omitempty"`
	Diagnostics     *r.DiagnosticReport       `json:"diagnostics,omitempty"`
	DailyDigest     *r.DailyDigest            `json:"daily_digest,omitempty"`
	Projects        []r.ProjectRow            `json:"projects,omitempty"`
	Daily           []r.Day                   `json:"daily"`
	Notes           []string                  `json:"notes"`

	EvalRows any `json:"-"`

	accs           map[string]*acc
	seen           map[string]bool
	readCalls      map[string]int
	rangedReads    map[string]int
	redundantReads map[string]int
}

// ReadCallsMap returns per-vendor Read tool call counts collected during Add.
func (s *Summary) ReadCallsMap() map[string]int {
	if s == nil {
		return nil
	}
	return s.readCalls
}

// Sess is one loaded sessions table row.
type Sess struct {
	ID, Vendor, Root, Parent, Agent, Model, Label, Outcome, Run string
	Start, End                                                  int64
	Cost                                                        *float64
}

// Stp is one loaded steps table row with attached verbs and denial metadata.
type Stp struct {
	Ext, Kind, Tool, Skill, KindOf, Hash, DenialKind string
	TS, Started, Ended, Wait                         int64
	IsErr, Denied                                    bool
	In, Out, CR, CW, Ctx                             int64
	Verbs                                            []string
	VerbNs                                           []int
}

// VerbN returns the stored verb ordinal for Verbs[idx], falling back to idx.
func (st Stp) VerbN(idx int) int {
	if idx < len(st.VerbNs) {
		return st.VerbNs[idx]
	}
	return idx
}

// FirstNonCDVerbIdx returns the slice index of the first non-`cd` Bash verb
// starting from verb index 0, or -1 when verb 0 is missing or every verb is `cd`.
func (st Stp) FirstNonCDVerbIdx() int {
	for i, v := range st.Verbs {
		if st.VerbN(i) != i {
			return -1
		}
		if IsCDBashVerb(v) {
			continue
		}
		return i
	}
	return -1
}

// SessEvent is one loaded events table row.
type SessEvent struct {
	Kind, Ref, Value string
	TS               int64
}

// Trial is one loaded trials table row for pass^k and auto-run accounting.
type Trial struct {
	Label, Run, Status string
	Start              int64
}

// WaveRow is one loaded work_waves row.
type WaveRow struct {
	Stamp, Worker, ProjectRoot, State, Model, BaseCheck, JudgeModel, JudgeVerdict string
	UpdatedAt                                                                     int64
}

// ProjVetAcc aggregates turn_vets metrics per project root.
type ProjVetAcc struct {
	Turns, ScoreSum int
	LastMS          int64
}

// SecondBrainAcc holds turn_vets summary totals and per-root/per-conversation rollups.
type SecondBrainAcc struct {
	SB               r.SecondBrain
	scoreSum         int
	durations        []int
	injSum, injCount int
	ByRoot           map[string]*ProjVetAcc
	ByConv           map[string][2]int
}

// ShowData bundles batched steps, events, waves, and second-brain aggregates for Show.
type ShowData struct {
	Steps  map[string][]Stp
	Events map[string][]SessEvent
	Waves  []WaveRow
	SBAcc  *SecondBrainAcc
}

// OrchestrationRunRow is the subset of orchestration_runs fields needed by Show.
type OrchestrationRunRow struct {
	Kind, Status, HaltCode, Verdict string
	Units, DefectRounds             int
	Passed, Failed                  int
	DurationMS                      int64
}

// RatingRow is the subset of session_ratings fields needed for skill telemetry.
type RatingRow struct {
	SessionID    string
	OutcomeScore int
	SkillAction  string
	TargetSkill  string
}

// Estimators supplies statistical estimators without cyclic imports.
type Estimators struct {
	AtK    func(n, c, k int) (float64, bool)
	PowK   func(n, c, k int) (float64, bool)
	Wilson func(n, c int, z float64) (float64, float64)
}

// RootFuncs provides project-root classification and worktree resolution callbacks.
type RootFuncs struct {
	ResolveRepoID            func(string) string
	IsSandboxProjectRoot     func(root, home string) bool
	IsNonCollapsibleRoot     func(string) bool
	IsTempRoot               func(string) bool
	IsAgyWorktreePath        func(string) bool
	ResolveGitWorktreeOnDisk func(string) (string, bool)
	ResolveFallbackRoot      func(id, rawRoot, parent string, byID, parents map[string]string, knownReal []string) string
	ResolveSessionRoots      func(all []Sess) (byID, wtMap map[string]string)
	DecodeWaveStamp          func(stored string) (label, raw string)
}

// Deps supplies store handles and parent-package callbacks to Show without import cycles.
type Deps struct {
	DB        *sql.DB
	StorePath string
	Est       Estimators
	Roots     RootFuncs

	TrialsIn                    func(o Options, since, now time.Time) ([]Trial, error)
	TrialRuns                   func() (map[string]bool, error)
	BrainHasSIMTables           func(db *sql.DB) bool
	ScanOrchestrationRuns       func(db *sql.DB, q string, args []any) ([]OrchestrationRunRow, error)
	SessionRatings              func(project string, since, now time.Time) ([]RatingRow, error)
	MergeDevEvalPassK           func(sum *Summary, home string, since, now time.Time)
	PopulateJudgeVerdictsHealth func(sum *Summary, o Options, waves []WaveRow, since, now time.Time) error
	PopulateAutonomySignals     func(sum *Summary, home string, o Options, all []Sess)
	PopulateTriggerCoverage     func(sum *Summary, o Options, all []Sess, trials []Trial, data *ShowData, since, now time.Time)
	PopulateSessionRatings      func(sum *Summary, o Options, since, now time.Time) error
	PopulateBackgroundRuns      func(sum *Summary, o Options, since, now time.Time) error
	LoadEvalRows                func(home string, since, now time.Time) (any, error)
	PopulateWindowDeltas        func(sum *Summary, o Options, since, now time.Time) error
	PopulateDiagnostics         func(sum *Summary, o Options, now time.Time) error
}
