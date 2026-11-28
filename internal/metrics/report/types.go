package report

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

const (
	// DefaultContextTarget is the default context-window threshold for reports.
	DefaultContextTarget = 300000
	// HistoricalVendor identifies historical Claude Code columns.
	HistoricalVendor = "claude"
	// SeverityInfo marks informational notices excluded from regression transitions.
	SeverityInfo = "info"
	// RuleDirectShellRead identifies direct shell file-read bypass signals.
	RuleDirectShellRead = "bash_read_bypass"
	// ScopeAutoSkill identifies skill-scoped auto-applied actions.
	ScopeAutoSkill = "auto_skill"
	// ScopeProposeOnly identifies proposal-only action scopes.
	ScopeProposeOnly = "propose_only"
	// MinHiddenCouplingPairs is the threshold for hidden-coupling regressions.
	MinHiddenCouplingPairs = 3
	// MinDirectShellReads is the threshold for shell-read bypass regressions.
	MinDirectShellReads = 3
	// DirectShellReadMedRate is the medium per-session shell-read bypass rate.
	DirectShellReadMedRate = 0.25
	// MinTimeoutRatePct is the threshold for PreInvocation timeout regressions.
	MinTimeoutRatePct = 5.0
	// TrialTextLimit bounds printable shape-only labels.
	TrialTextLimit = 120
)

// Summary carries the shape-only metrics summary rendered by HTML, Markdown,
// JSON reports, and daily digests.
type Summary struct {
	Window          map[string]string         `json:"window"`
	Columns         []string                  `json:"columns"`
	Sessions        map[string]int            `json:"sessions"`
	Subagents       map[string]map[string]int `json:"subagents"`
	A               Outcome                   `json:"A"`
	B               Efficiency                `json:"B"`
	C               Adherence                 `json:"C"`
	D               Reliability               `json:"D"`
	E               SecondBrain               `json:"E"`
	F               WorkSummary               `json:"F"`
	TriggerCoverage TriggerCoverage           `json:"trigger_coverage"`
	Health          HealthSignals             `json:"health"`
	Orchestration   *OrchestrationSummary     `json:"orchestration,omitempty"`
	SIM             *SIMSummary               `json:"sim,omitempty"`
	SkillTelemetry  []SkillTelemetry          `json:"skill_telemetry,omitempty"`
	RuleTelemetry   []RuleTelemetry           `json:"rule_telemetry,omitempty"`
	TagCohort       *TagCohort                `json:"tag_cohort,omitempty"`
	AgentRoles      []AgentRoleMetrics        `json:"agent_roles,omitempty"`
	Deltas          *WindowDeltas             `json:"deltas,omitempty"`
	Diagnostics     *DiagnosticReport         `json:"diagnostics,omitempty"`
	DailyDigest     *DailyDigest              `json:"daily_digest,omitempty"`
	Projects        []ProjectRow              `json:"projects,omitempty"`
	Daily           []Day                     `json:"daily"`
	Notes           []string                  `json:"notes"`

	ReadCalls map[string]int            `json:"-"`
	NumbersFn func() map[string]float64 `json:"-"`
}

// ColumnsList returns s.Columns, defaulting to ["agy"] when empty.
func (s *Summary) ColumnsList() []string {
	if s == nil || len(s.Columns) == 0 {
		return []string{"agy"}
	}
	return s.Columns
}

// OverTarget formats the sessions-over-context-target ratio for vendor v.
func (s *Summary) OverTarget(v string) string {
	p := s.B.SessionsOverContextTarget[v]
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d of %d", *p, len(s.B.SessionPeaks[v]))
}

// EvalView supplies pre-formatted eval rows and suite lines to report renderers.
type EvalView struct {
	Head     []string
	Rows     [][]string
	Lines    []string
	RowCount int
	Trials   int
	RawJSON  any
}

type Outcome struct {
	DoDClosed           map[string]Closed `json:"dod_closed"`
	Verifier            map[string]int    `json:"verifier"`
	ReviewFindings      map[string]int    `json:"review_findings"`
	EscapedBugs         int               `json:"escaped_bugs"`
	Corrections         int               `json:"corrections"`
	PassK               map[string]PassK  `json:"pass_k"`
	RatingsCount        int               `json:"ratings_count"`
	MeanOutcomeScore    *float64          `json:"mean_outcome_score"`
	MeanTokenEfficiency *float64          `json:"mean_token_efficiency"`
}

type Closed struct {
	Closed    int `json:"closed"`
	Ambiguous int `json:"ambiguous"`
	Of        int `json:"of"`
}

type PassK struct {
	K        int                `json:"k"`
	Passed   int                `json:"passed"`
	PassK    int                `json:"pass_k"`
	N        int                `json:"n"`
	C        int                `json:"c"`
	Errors   int                `json:"errors"`
	PassAt   map[string]float64 `json:"pass_at,omitempty"`
	PassPow  map[string]float64 `json:"pass_pow,omitempty"`
	Wilson95 [2]float64         `json:"wilson95"`
	Flip     int                `json:"flip"`
}

type Tokens struct {
	In         int64 `json:"in"`
	Out        int64 `json:"out"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

// Short aliases for facade re-exports.
type (
	P   = PassK
	Tok = Tokens
)

type Efficiency struct {
	ToolCalls                 map[string]int            `json:"tool_calls"`
	ToolCallsByTool           map[string]map[string]int `json:"tool_calls_by_tool"`
	Turns                     map[string]int            `json:"turns"`
	Tokens                    map[string]*Tokens        `json:"tokens"`
	CostUSD                   map[string]*float64       `json:"cost_usd"`
	WallMin                   map[string]float64        `json:"wall_min"`
	ActiveMin                 map[string]float64        `json:"active_min"`
	ModelMin                  map[string]float64        `json:"model_min"`
	ToolMin                   map[string]float64        `json:"tool_min"`
	WaitMin                   map[string]float64        `json:"wait_min"`
	IdleMin                   map[string]float64        `json:"idle_min"`
	PeakContext               map[string]int64          `json:"peak_context"`
	Compactions               map[string]*int           `json:"compactions"`
	ContextTarget             int64                     `json:"context_target"`
	SessionsOverContextTarget map[string]*int           `json:"sessions_over_context_target"`
	SessionPeaks              map[string][]SessionPeak  `json:"session_peaks"`
}

type SessionPeak struct {
	Session string `json:"session"`
	Date    string `json:"date"`
	Peak    int64  `json:"peak"`
}

type Adherence struct {
	SQLiteVerbs     map[string]int            `json:"sqlite_verbs"`
	GrepCalls       map[string]int            `json:"grep_calls"`
	SearchGrepRatio map[string]*float64       `json:"search_grep_ratio"`
	RedundantReads  map[string]int            `json:"redundant_reads"`
	RangedReadPct   map[string]*float64       `json:"ranged_read_pct"`
	Forbidden       map[string]int            `json:"forbidden"`
	FirstReads      map[string]int            `json:"first_reads"`
	Skills          map[string]map[string]int `json:"skills"`
	BashReadSkips   map[string]int            `json:"bash_read_bypasses,omitempty"`
	TopBashVerbs    []VerbCount               `json:"top_bash_verbs,omitempty"`
}

type VerbCount struct {
	Verb  string `json:"verb"`
	Count int    `json:"count"`
}

type Reliability struct {
	ToolErrorRate     map[string]*float64       `json:"tool_error_rate"`
	ToolErrors        map[string]int            `json:"tool_errors,omitempty"`
	Denials           map[string]*int           `json:"denials"`
	HookDenials       map[string]int            `json:"hook_denials,omitempty"`
	Interrupts        map[string]*int           `json:"interrupts"`
	RetryLoops        map[string]int            `json:"retry_loops"`
	SubagentToolCalls map[string]map[string]int `json:"subagent_tool_calls"`
}

type SecondBrain struct {
	TurnVets          int      `json:"turn_vets"`
	MeanVetScore      *float64 `json:"mean_vet_score"`
	VetFirstTurns     int      `json:"vet_first_turns"`
	BypassedTurns     int      `json:"bypassed_turns"`
	PivotsSeeded      int      `json:"pivots_seeded"`
	LedgerSaved       int      `json:"ledger_saved"`
	TimedOutTurns     int      `json:"timed_out_turns"`
	TimeoutRatePct    *float64 `json:"timeout_rate_pct"`
	P50DurationMS     *float64 `json:"p50_duration_ms"`
	P95DurationMS     *float64 `json:"p95_duration_ms"`
	MeanInjectedBytes *float64 `json:"mean_injected_bytes"`
	PivotsUsed        int      `json:"pivots_used"`
	SkillsLoaded      int      `json:"skills_loaded"`
}

type CohortMetrics struct {
	Since                   string   `json:"since,omitempty"`
	Until                   string   `json:"until,omitempty"`
	Sessions                int      `json:"sessions"`
	ToolCalls               int      `json:"tool_calls"`
	SQLiteVerbs             int      `json:"sqlite_verbs"`
	GrepCalls               int      `json:"grep_calls"`
	SearchGrepRatio         *float64 `json:"search_grep_ratio"`
	BashReadSkips           int      `json:"bash_read_bypasses"`
	TurnVets                int      `json:"turn_vets"`
	PreInvocationTimedOut   int      `json:"pre_invocation_timed_out"`
	PreInvocationTimeoutPct *float64 `json:"pre_invocation_timeout_pct"`
	PreInvocationP50MS      *float64 `json:"pre_invocation_p50_ms"`
	PreInvocationP95MS      *float64 `json:"pre_invocation_p95_ms"`
}

type TagCohort struct {
	Tag    string        `json:"tag"`
	Cutoff string        `json:"cutoff"`
	Pre    CohortMetrics `json:"pre"`
	Post   CohortMetrics `json:"post"`
}

type AgentRoleMetrics struct {
	Role            string   `json:"role"`
	Sessions        int      `json:"sessions"`
	ToolCalls       int      `json:"tool_calls"`
	ToolErrors      int      `json:"tool_errors"`
	HookDenials     int      `json:"hook_denials,omitempty"`
	ToolErrorRate   *float64 `json:"tool_error_rate"`
	SQLiteVerbs     int      `json:"sqlite_verbs"`
	GrepCalls       int      `json:"grep_calls"`
	SearchGrepRatio *float64 `json:"search_grep_ratio"`
	TurnVets        int      `json:"turn_vets"`
	TimedOutTurns   int      `json:"timed_out_turns"`
	TimeoutRatePct  *float64 `json:"timeout_rate_pct"`
}

type WorkSummary struct {
	Waves           int `json:"waves"`
	Workers         int `json:"workers"`
	Passed          int `json:"passed"`
	Failed          int `json:"failed"`
	BaseCheckPassed int `json:"base_check_passed"`
	BaseCheckFailed int `json:"base_check_failed"`
	JudgePassed     int `json:"judge_passed"`
	JudgeFailed     int `json:"judge_failed"`
	JudgeFallback   int `json:"judge_fallback"`
}

type ProjectRow struct {
	Project         string   `json:"project"`
	Sessions        int      `json:"sessions"`
	ActiveMin       float64  `json:"active_min"`
	Tokens          int64    `json:"tokens"`
	SearchGrepRatio *float64 `json:"search_grep_ratio"`
	MeanVetScore    *float64 `json:"mean_vet_score"`
	Corrections     int      `json:"corrections"`
	EscapedBugs     int      `json:"escaped_bugs"`
	LastActive      string   `json:"last_active"`
	AutoFindings    int      `json:"auto_findings"`
	HandoffLagDays  *int     `json:"handoff_lag_days,omitempty"`
}

type PoolQuota struct {
	Name string `json:"name"`
	Five int    `json:"five"`
	Week int    `json:"week"`
}

type BackgroundKindStats struct {
	Runs             int     `json:"runs"`
	Passed           int     `json:"passed"`
	Failed           int     `json:"failed"`
	MeanDurationMS   float64 `json:"mean_duration_ms,omitempty"`
	ProjectsChecked  int     `json:"projects_checked,omitempty"`
	ProposalsWritten int     `json:"proposals_written,omitempty"`
	RatingsWritten   int     `json:"ratings_written,omitempty"`
	WorkersRan       int     `json:"workers_ran,omitempty"`
	Applied          int     `json:"applied,omitempty"`
}

type HealthSignals struct {
	OrchestrationCalls  int                      `json:"orchestration_calls"`
	SubagentDispatches  int                      `json:"subagent_dispatches"`
	WorkflowCalls       int                      `json:"workflow_calls"`
	SwarmCalls          int                      `json:"swarm_calls"`
	DeterministicGates  int                      `json:"deterministic_gates"`
	JudgePassed         int                      `json:"judge_passed"`
	JudgeFailed         int                      `json:"judge_failed"`
	JudgeFallback       int                      `json:"judge_fallback"`
	AutoRunsPresent     bool                     `json:"auto_runs_present,omitempty"`
	AutoModelRuns       int                      `json:"auto_model_runs"`
	AutoOpenAsks        int                      `json:"auto_open_asks"`
	AutoLastVerdict     string                   `json:"auto_last_verdict,omitempty"`
	AutoProjectCount    int                      `json:"auto_project_count"`
	SearchDBCompactions int                      `json:"search_db_compactions"`
	StepCompactions     int                      `json:"step_compactions"`
	MainSessions        int                      `json:"main_sessions"`
	SessionsOverKnee    int                      `json:"sessions_over_knee"`
	SubagentsOverBudget int                      `json:"subagents_over_budget"`
	HookDenials         int                      `json:"hook_denials"`
	ClampedSaves        int                      `json:"clamped_saves"`
	HookBlocksLedger    int                      `json:"hook_blocks_ledger"`
	HookBlocksGuards    int                      `json:"hook_blocks_guards"`
	ModelFamilyCounts   map[string]int           `json:"model_family_counts,omitempty"`
	ModelFamilyShare    map[string]float64       `json:"model_family_share,omitempty"`
	GuardedDowngrades   int                      `json:"guarded_downgrades"`
	PoolQuotas          []PoolQuota              `json:"pool_quotas,omitempty"`
	QuotaUpdatedAt      string                   `json:"quota_updated_at,omitempty"`
	Structural          search.StructuralSignals `json:"structural"`
	DreamRuns           BackgroundKindStats      `json:"dream_runs"`
	AutoRuns            BackgroundKindStats      `json:"auto_runs"`
	KeptAutoBranches    int                      `json:"kept_auto_branches"`
	ProposalsQueued     int                      `json:"proposals_queued"`
	ProposalsClosed     int                      `json:"proposals_closed"`
}

type Day struct {
	Date        string             `json:"date"`
	Sessions    map[string]int     `json:"sessions"`
	ToolCalls   map[string]int     `json:"tool_calls"`
	Tokens      map[string]int64   `json:"tokens"`
	CostUSD     map[string]float64 `json:"cost_usd"`
	SQLiteVerbs map[string]int     `json:"sqlite_verbs"`
	GrepCalls   map[string]int     `json:"grep_calls"`
	Errors      map[string]int     `json:"errors"`
	Denials     map[string]int     `json:"denials,omitempty"`
}

type OrchestrationSummary struct {
	Runs           int            `json:"runs"`
	ByKind         map[string]int `json:"by_kind,omitempty"`
	ByStatus       map[string]int `json:"by_status,omitempty"`
	Units          int            `json:"units"`
	DefectRounds   int            `json:"defect_rounds"`
	Succeeded      int            `json:"succeeded"`
	Failed         int            `json:"failed"`
	MeanDurationMS float64        `json:"mean_duration_ms,omitempty"`
	JudgeOK        int            `json:"judge_ok,omitempty"`
	JudgeFailed    int            `json:"judge_failed,omitempty"`
	Halts          map[string]int `json:"halts,omitempty"`
}

type SIMSummary struct {
	Available         bool           `json:"available"`
	ActiveProblems    int            `json:"active_problems"`
	ResolvedProblems  int            `json:"resolved_problems"`
	MergedProblems    int            `json:"merged_problems,omitempty"`
	Workarounds       int            `json:"workarounds"`
	ActiveWorkarounds int            `json:"active_workarounds"`
	Attempts          int            `json:"attempts"`
	AttemptsByVerdict map[string]int `json:"attempts_by_verdict,omitempty"`
	FieldStatusCounts map[string]int `json:"field_status_counts,omitempty"`
}

type SkillTelemetry struct {
	Skill             string   `json:"skill"`
	Suggested         int      `json:"suggested"`
	Loaded            int      `json:"loaded"`
	Converted         int      `json:"converted"`
	ConversionRatePct *float64 `json:"conversion_rate_pct,omitempty"`
	Sessions          int      `json:"sessions"`
	RatedSessions     int      `json:"rated_sessions,omitempty"`
	MeanOutcomeScore  *float64 `json:"mean_outcome_score,omitempty"`
	ToolCalls         int      `json:"tool_calls"`
	ToolErrors        int      `json:"tool_errors"`
	ToolErrorRatePct  *float64 `json:"tool_error_rate_pct,omitempty"`
	RewriteSkillCount int      `json:"rewrite_skill_count"`
	NewSkillCount     int      `json:"new_skill_count,omitempty"`
}

type RuleTelemetry struct {
	Rule     string `json:"rule"`
	Kind     string `json:"kind"`
	Count    int    `json:"count"`
	Severity string `json:"severity,omitempty"`
}

type TriggerCoverage struct {
	WorkerPhases              int      `json:"worker_phases"`
	QualifyingPhases          int      `json:"qualifying_phases"`
	QualifyingWorkerPhasePct  *float64 `json:"qualifying_worker_phase_pct"`
	ConfiguredWorkerPhases    int      `json:"configured_worker_phases"`
	ConfiguredWorkerPhasePct  *float64 `json:"configured_worker_phase_pct"`
	WorkerPassed              int      `json:"worker_passed"`
	BaseCheckPassed           int      `json:"base_check_passed"`
	BaseCheckFailed           int      `json:"base_check_failed"`
	JudgePassed               int      `json:"judge_passed"`
	JudgeFailed               int      `json:"judge_failed"`
	BuildTimeQuestions        int      `json:"build_time_questions"`
	SearchCalls               int      `json:"search_calls"`
	GrepCalls                 int      `json:"grep_calls"`
	SearchGrepRatio           *float64 `json:"search_grep_ratio"`
	ReadLedgerBlocks          int      `json:"read_ledger_blocks"`
	AutoModelRuns             int      `json:"auto_model_runs"`
	WindowWeeks               float64  `json:"window_weeks"`
	AutoModelRunsPerWeek      float64  `json:"auto_model_runs_per_week"`
	DoDClosedSessions         int      `json:"dod_closed_sessions"`
	BuildSessions             int      `json:"build_sessions"`
	DoDClosedPct              *float64 `json:"dod_closed_pct"`
	SessionsOverKnee          int      `json:"sessions_over_knee"`
	MainSessions              int      `json:"main_sessions"`
	OrchestrationCalls        int      `json:"orchestration_calls"`
	SwarmSkillLoads           int      `json:"swarm_skill_loads"`
	GateVerbs                 int      `json:"gate_verbs"`
	LandVerbs                 int      `json:"land_verbs"`
	DeterministicGates        int      `json:"deterministic_gates"`
	DeterministicGateSessions int      `json:"deterministic_gate_sessions"`
	DeterministicGatePct      *float64 `json:"deterministic_gate_pct"`
	PreInvocationTurnVets     int      `json:"pre_invocation_turn_vets"`
	PreInvocationTimedOut     int      `json:"pre_invocation_timed_out"`
	PreInvocationTimeoutPct   *float64 `json:"pre_invocation_timeout_pct"`
	PreInvocationP50MS        *float64 `json:"pre_invocation_p50_ms"`
	PreInvocationP95MS        *float64 `json:"pre_invocation_p95_ms"`
	InstalledSkillsCount      int      `json:"installed_skills_count,omitempty"`
	TriggeredSkillsCount      int      `json:"triggered_skills_count,omitempty"`
	SkillCoveragePct          *float64 `json:"skill_coverage_pct,omitempty"`
	UntriggeredSkills         []string `json:"untriggered_skills,omitempty"`
}

type MetricDelta struct {
	Current  float64 `json:"current"`
	Previous float64 `json:"previous"`
	Delta    float64 `json:"delta"`
}

type WindowDeltas struct {
	PreviousWindow   map[string]string      `json:"previous_window"`
	PreviousSessions int                    `json:"previous_sessions"`
	Sessions         *MetricDelta           `json:"sessions,omitempty"`
	Turns            *MetricDelta           `json:"turns,omitempty"`
	ToolCalls        *MetricDelta           `json:"tool_calls,omitempty"`
	KneeRatePct      *MetricDelta           `json:"knee_rate_pct,omitempty"`
	OverTargetPct    *MetricDelta           `json:"over_target_pct,omitempty"`
	SearchGrepRatio  *MetricDelta           `json:"search_grep_ratio,omitempty"`
	RangedReadPct    *MetricDelta           `json:"ranged_read_pct,omitempty"`
	ToolErrorRatePct *MetricDelta           `json:"tool_error_rate_pct,omitempty"`
	RetryLoops       *MetricDelta           `json:"retry_loops,omitempty"`
	MeanOutcomeScore *MetricDelta           `json:"mean_outcome_score,omitempty"`
	Metrics          map[string]MetricDelta `json:"metrics,omitempty"`
}

type DiagnosticSignal struct {
	SignalID          string  `json:"signal_id"`
	Severity          string  `json:"severity"`
	Rule              string  `json:"rule"`
	Metric            string  `json:"metric"`
	Current           float64 `json:"current"`
	Previous          float64 `json:"previous"`
	Delta             float64 `json:"delta"`
	SampleSize        int     `json:"sample_size"`
	Summary           string  `json:"summary"`
	ActionScope       string  `json:"action_scope,omitempty"`
	RecommendedAction string  `json:"recommended_action,omitempty"`
}

type DiagnosticReport struct {
	Scope       string             `json:"scope"`
	GeneratedAt string             `json:"generated_at"`
	Signals     []DiagnosticSignal `json:"signals"`
}

// Round4 rounds v to four decimal places.
func Round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

// KneePct computes the percentage of main sessions reaching >= 60% context knee.
func KneePct(s *Summary) (float64, bool) {
	if s.Health.MainSessions <= 0 {
		return 0, false
	}
	return Round4(float64(s.Health.SessionsOverKnee) * 100.0 / float64(s.Health.MainSessions)), true
}

// OverTargetPct computes the percentage of agy main sessions over context target.
func OverTargetPct(s *Summary) (float64, bool) {
	over := s.B.SessionsOverContextTarget["agy"]
	n := len(s.B.SessionPeaks["agy"])
	if over == nil || n <= 0 {
		return 0, false
	}
	return Round4(float64(*over) * 100.0 / float64(n)), true
}

// NewMetricDelta constructs a rounded MetricDelta from current and previous values.
func NewMetricDelta(cur, prev float64) MetricDelta {
	return MetricDelta{Current: Round4(cur), Previous: Round4(prev), Delta: Round4(cur - prev)}
}

// PtrMetricDelta returns a pointer to NewMetricDelta(cur, prev).
func PtrMetricDelta(cur, prev float64) *MetricDelta {
	d := NewMetricDelta(cur, prev)
	return &d
}

// PtrOptionalDelta scales and compares optional metric pointers.
func PtrOptionalDelta(cur, prev *float64, scale float64) *MetricDelta {
	if cur == nil || prev == nil {
		return nil
	}
	return PtrMetricDelta(*cur*scale, *prev*scale)
}

// SafeMap returns an empty non-nil map when m is nil.
func SafeMap(m map[string]int) map[string]int {
	if m == nil {
		return map[string]int{}
	}
	return m
}

// WilsonText formats a Wilson 95% confidence interval.
func WilsonText(w [2]float64) string { return fmt.Sprintf("[%.4f, %.4f]", w[0], w[1]) }

// SortedKeys returns the keys of m sorted lexicographically.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// PickSev returns t when cond holds, otherwise f.
func PickSev(cond bool, t, f string) string {
	if cond {
		return t
	}
	return f
}

// ClipPrintable trims, strips control characters, and truncates s to maxLen UTF-8 bytes.
func ClipPrintable(s string, maxLen int) string {
	return trail.TruncateUTF8(search.Printable(strings.TrimSpace(s)), maxLen)
}
