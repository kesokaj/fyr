package metrics

import (
	r "github.com/kesokaj/fyr/internal/metrics/report"
	"github.com/kesokaj/fyr/internal/metrics/show"
)

const (
	// DefaultContextTarget is the context target when none is given.
	DefaultContextTarget = show.DefaultContextTarget
	activeGap            = show.ActiveGap
	historical           = show.Historical
	showBatchChunk       = show.ShowBatchChunk
)

type (
	// Summary is `show --json` (spec § Output).
	Summary = show.Summary
	// ShowOptions filter what show and report read (spec § Verbs).
	ShowOptions = show.Options

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

	sess       = show.Sess
	stp        = show.Stp
	sessEvent  = show.SessEvent
	trial      = show.Trial
	showData   = show.ShowData
	waveRow    = show.WaveRow
	projVetAcc = show.ProjVetAcc
)
