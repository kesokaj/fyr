package brain

import (
	"encoding/json"
	"regexp"
	"time"
)

const (
	simSchemaVersion           = 5
	maxSIMAliasDepth           = 16
	maxSIMRootCauseBytes       = 240
	maxSIMWhatBytes            = 400
	defaultSIMListLimit        = 20
	defaultSIMMaxBytes         = 6144
	defaultSIMMaxProblems      = 8
	defaultSIMMaxAttempts      = 4
	defaultSIMRecentFailedDays = 30
)

var (
	simProblemIDRe = regexp.MustCompile(`^SIM-P([0-9]{3,9})$`)
	simHexSHARe    = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// SIMModelStatus captures per-model stratified Stage B recovery for one attempt.
type SIMModelStatus struct {
	Model    string `json:"model"`
	Status   string `json:"status"` // recovered | still_regressing | pending_data
	Sessions int    `json:"sessions"`
}

// SIMProblem is one root-cause entry in the global brain.db v5 SIM ledger.
type SIMProblem struct {
	ID             string `json:"id"`
	RootCause      string `json:"root_cause"`
	NormRootCause  string `json:"norm_root_cause,omitempty"`
	MechanismClass string `json:"mechanism_class,omitempty"`
	SignalID       string `json:"signal_id,omitempty"`
	TargetPaths    string `json:"target_paths,omitempty"`
	Status         string `json:"status"`
	Provenance     string `json:"provenance"`
	OpenedAt       string `json:"opened_at"`
	UpdatedAt      string `json:"updated_at"`
	OpenedHarness  string `json:"opened_harness,omitempty"`
	OpenedModel    string `json:"opened_model,omitempty"`
}

// SIMAttempt is one recorded harness-edit attempt linked to a SIMProblem.
type SIMAttempt struct {
	ID               int64            `json:"id"`
	ProblemID        string           `json:"problem_id"`
	SourceKind       string           `json:"source_kind"`
	ProposalID       string           `json:"proposal_id,omitempty"`
	CommitSHA        string           `json:"commit_sha,omitempty"`
	ParentSHA        string           `json:"parent_sha,omitempty"`
	HarnessVersion   string           `json:"harness_version,omitempty"`
	ProposerModel    string           `json:"proposer_model,omitempty"`
	PolicyModel      string           `json:"policy_model,omitempty"`
	ProjectScope     string           `json:"project_scope,omitempty"`
	Files            []string         `json:"files,omitempty"`
	What             string           `json:"what"`
	PredictedRows    []string         `json:"predicted_rows,omitempty"`
	PredictedSignals []string         `json:"predicted_signals,omitempty"`
	EvalVerdict      string           `json:"eval_verdict"`
	FixedRows        []string         `json:"fixed_rows,omitempty"`
	MissedRows       []string         `json:"missed_rows,omitempty"`
	RegressedRows    []string         `json:"regressed_rows,omitempty"`
	EvalPassed       int              `json:"eval_passed"`
	EvalTotal        int              `json:"eval_total"`
	FieldStatus      string           `json:"field_status"`
	FieldMetric      string           `json:"field_metric,omitempty"`
	FieldBaseline    *float64         `json:"field_baseline,omitempty"`
	FieldObserved    *float64         `json:"field_observed,omitempty"`
	FieldModels      []SIMModelStatus `json:"field_models,omitempty"`
	Provenance       string           `json:"provenance"`
	CreatedAt        string           `json:"created_at"`
	UpdatedAt        string           `json:"updated_at"`
}

// UnmarshalJSON decodes SIMAttempt while accepting either []SIMModelStatus or
// legacy []string for field_models.
func (a *SIMAttempt) UnmarshalJSON(data []byte) error {
	type alias SIMAttempt
	aux := struct {
		*alias
		FieldModels json.RawMessage `json:"field_models,omitempty"`
	}{alias: (*alias)(a)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.FieldModels) > 0 {
		ms, err := parseSIMFieldModels(aux.FieldModels)
		if err != nil {
			return err
		}
		a.FieldModels = ms
	}
	return nil
}

// SIMAlias maps a merged SIM problem ID to its canonical target problem ID.
type SIMAlias struct {
	OldID    string `json:"old_id"`
	TargetID string `json:"target_id"`
	MergedAt string `json:"merged_at"`
}

// SIMRecordInput is the input payload for RecordSIMAttempt.
type SIMRecordInput struct {
	ProblemID, RootCause, MechanismClass, ProblemStatus string
	SignalID, SourceKind, ProposalID                    string
	CommitSHA, ParentSHA, HarnessVersion                string
	ProposerModel, PolicyModel, ProjectScope            string
	Files, PredictedRows, PredictedSignals              []string
	What, EvalVerdict                                   string
	FixedRows, MissedRows, RegressedRows                []string
	EvalPassed, EvalTotal                               int
	FieldStatus, FieldMetric                            string
	FieldBaseline                                       *float64
	Provenance                                          string
	Now                                                 time.Time
}

// SIMRecordResult summarizes one RecordSIMAttempt write.
type SIMRecordResult struct {
	ProblemID      string `json:"problem_id"`
	AttemptID      int64  `json:"attempt_id"`
	CreatedProblem bool   `json:"created_problem"`
	ResolvedAlias  bool   `json:"resolved_alias"`
}

// SIMListFilter filters ListSIMProblems.
type SIMListFilter struct {
	SignalID, PathPrefix, Status string
	Limit                        int
}

// SIMProblemSummary pairs one SIMProblem with aggregate attempt metadata.
type SIMProblemSummary struct {
	Problem      SIMProblem `json:"problem"`
	AttemptCount int        `json:"attempt_count"`
	LastVerdict  string     `json:"last_verdict,omitempty"`
	LastField    string     `json:"last_field_status,omitempty"`
	LastModel    string     `json:"last_policy_model,omitempty"`
}

// SIMProblemDetail holds one canonical SIMProblem, its aliases, and attempts.
type SIMProblemDetail struct {
	Problem  SIMProblem   `json:"problem"`
	Aliases  []string     `json:"aliases,omitempty"`
	Attempts []SIMAttempt `json:"attempts"`
}

// SIMMergeResult reports one MergeSIMProblem transition.
type SIMMergeResult struct {
	OldID    string `json:"old_id"`
	TargetID string `json:"target_id"`
	MergedAt string `json:"merged_at"`
}

// SIMFieldUpdate updates Stage B field status and stratified models on an attempt.
type SIMFieldUpdate struct {
	AttemptID     int64
	CommitSHA     string
	ProposalID    string
	FieldStatus   string
	FieldMetric   string
	FieldObserved *float64
	FieldModels   []SIMModelStatus
	Now           time.Time
}

// SIMQuery configures bounded read-only retrieval via RetrieveSIMSlice.
type SIMQuery struct {
	Signals               []string
	TargetPaths           []string
	Terms                 []string
	MaxBytes              int // default 6144 (6 KiB)
	MaxProblems           int // default 8
	MaxAttemptsPerProblem int // default 4
	RecentFailedDays      int // default 30
	Now                   time.Time
}

// SIMSlice is the bounded, redacted text slice returned by RetrieveSIMSlice.
type SIMSlice struct {
	Text         string
	ProblemCount int
	AttemptCount int
	Truncated    bool
}

// SIMStateExport holds portable SIM ledger rows for offline state bundles.
type SIMStateExport struct {
	Problems []SIMProblem `json:"problems"`
	Attempts []SIMAttempt `json:"attempts"`
	Aliases  []SIMAlias   `json:"aliases"`
}

// SIMStateImportOpts configures ImportStateSIM.
type SIMStateImportOpts struct {
	DryRun bool
}
