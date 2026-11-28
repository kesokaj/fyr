package metrics

import r "github.com/kesokaj/fyr/internal/metrics/report"

// Summary is `show --json` (spec § Output). A metric the CLI cannot produce
// is nil for its column and named in Notes, never 0.
type Summary struct {
	Window map[string]string `json:"window"`
	// Columns are the summary's columns: "agy", then "claude" only when the
	// window holds historical rows.
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

	accs map[string]*acc
	// seen holds the vendor and id of every tool call and message already
	// counted: a resumed or forked session replays earlier history into a
	// new transcript, and the replay is not new work.
	seen           map[string]bool
	readCalls      map[string]int
	rangedReads    map[string]int
	redundantReads map[string]int
	evalRows       []EvalRow
}

type (
	Outcome              = r.Outcome
	Closed               = r.Closed
	PassK                = r.P
	Tokens               = r.Tok
	Efficiency           = r.Efficiency
	SessionPeak          = r.SessionPeak
	Adherence            = r.Adherence
	VerbCount            = r.VerbCount
	Reliability          = r.Reliability
	SecondBrain          = r.SecondBrain
	CohortMetrics        = r.CohortMetrics
	TagCohort            = r.TagCohort
	AgentRoleMetrics     = r.AgentRoleMetrics
	WorkSummary          = r.WorkSummary
	ProjectRow           = r.ProjectRow
	PoolQuota            = r.PoolQuota
	HealthSignals        = r.HealthSignals
	Day                  = r.Day
	OrchestrationSummary = r.OrchestrationSummary
	SIMSummary           = r.SIMSummary
	SkillTelemetry       = r.SkillTelemetry
	RuleTelemetry        = r.RuleTelemetry
)

type sess struct {
	id, vendor, root, parent, agent, model, label, outcome, run string
	start, end                                                  int64
	cost                                                        *float64
}

type stp struct {
	ext, kind, tool, skill, kindOf, hash, denialKind string
	ts, started, ended, wait                         int64
	isErr, denied                                    bool
	in, out, cr, cw, ctx                             int64
	verbs                                            []string
	verbNs                                           []int
}

func (st stp) verbN(idx int) int {
	if idx < len(st.verbNs) {
		return st.verbNs[idx]
	}
	return idx
}

// firstNonCDVerbIdx returns the slice index of the first non-`cd` Bash verb
// starting from verb index 0 (matching firstNonCDBashVerb), or -1 when verb 0
// is missing or every verb is `cd`.
func (st stp) firstNonCDVerbIdx() int {
	for i, v := range st.verbs {
		if st.verbN(i) != i {
			return -1
		}
		if isCDBashVerb(v) {
			continue
		}
		return i
	}
	return -1
}

type sessEvent struct {
	kind, ref, value string
	ts               int64
}
