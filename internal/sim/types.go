package sim

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/trail"
)

// SchemaVersion is the minimum brain.db user_version carrying SIM tables.
const SchemaVersion = 5

const (
	MaxRootCauseBytes       = 240
	MaxWhatBytes            = 400
	MaxMetaBytes            = 128
	maxAliasDepth           = 16
	maxScopeBytes           = 256
	maxTargetPathsBytes     = 1024
	maxSliceItems           = 64
	maxSliceItemBytes       = 256
	defaultListLimit        = 20
	defaultMaxBytes         = 6144
	defaultMaxProblems      = 8
	defaultMaxAttempts      = 4
	defaultRecentFailedDays = 30
)

var (
	problemIDRe = regexp.MustCompile(`^SIM-P([0-9]{3,9})$`)
	hexSHARe    = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// Querier executes read-only SQL queries against the SIM tables.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// StoreDeps injects database session, error classification, and text helpers
// from the brain store so internal/sim stays decoupled from internal/brain and
// modernc.org/sqlite.
type StoreDeps struct {
	OpenReadOnly func(home string) (Querier, func() error, error)
	WithTx       func(home string, fn func(c *sql.Conn) error) error
	StorePath    func(home string) string
	FTSQuery     func(words []string, op string) string
	Printable    func(s string) string
	TitleText    func(s string) bool
	Refuse       func(format string, a ...any) error
	Usage        func(format string, a ...any) error
}

type runner struct{ d StoreDeps }

func (r runner) refuse(format string, a ...any) error {
	if r.d.Refuse != nil {
		return r.d.Refuse(format, a...)
	}
	return fmt.Errorf(format, a...)
}

func (r runner) usage(format string, a ...any) error {
	if r.d.Usage != nil {
		return r.d.Usage(format, a...)
	}
	return fmt.Errorf(format, a...)
}

func (r runner) titleText(s string) bool {
	if r.d.TitleText != nil {
		return r.d.TitleText(s)
	}
	return simDefaultTitleText(s)
}

func (r runner) ftsQuery(words []string, op string) string {
	if r.d.FTSQuery != nil {
		return r.d.FTSQuery(words, op)
	}
	return simDefaultFTSQuery(words, op)
}

func (r runner) printable(s string) string {
	if r.d.Printable != nil {
		return r.d.Printable(s)
	}
	return simDefaultPrintable(s)
}

func (r runner) openRO(home string) (Querier, func() error, error) {
	if r.d.OpenReadOnly == nil {
		return nil, nil, r.refuse("sim: read-only store opener not configured")
	}
	return r.d.OpenReadOnly(home)
}

func (r runner) withTx(home string, fn func(c *sql.Conn) error) error {
	if r.d.WithTx == nil {
		return r.refuse("sim: transaction opener not configured")
	}
	return r.d.WithTx(home, fn)
}

// ModelStatus captures per-model stratified Stage B recovery for one attempt.
type ModelStatus struct {
	Model    string `json:"model"`
	Status   string `json:"status"` // recovered | still_regressing | pending_data
	Sessions int    `json:"sessions"`
}

// Problem is one root-cause entry in the global brain.db v5 SIM ledger.
type Problem struct {
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

// Attempt is one recorded harness-edit attempt linked to a Problem.
type Attempt struct {
	ID               int64         `json:"id"`
	ProblemID        string        `json:"problem_id"`
	SourceKind       string        `json:"source_kind"`
	ProposalID       string        `json:"proposal_id,omitempty"`
	CommitSHA        string        `json:"commit_sha,omitempty"`
	ParentSHA        string        `json:"parent_sha,omitempty"`
	HarnessVersion   string        `json:"harness_version,omitempty"`
	ProposerModel    string        `json:"proposer_model,omitempty"`
	PolicyModel      string        `json:"policy_model,omitempty"`
	ProjectScope     string        `json:"project_scope,omitempty"`
	Files            []string      `json:"files,omitempty"`
	What             string        `json:"what"`
	PredictedRows    []string      `json:"predicted_rows,omitempty"`
	PredictedSignals []string      `json:"predicted_signals,omitempty"`
	EvalVerdict      string        `json:"eval_verdict"`
	FixedRows        []string      `json:"fixed_rows,omitempty"`
	MissedRows       []string      `json:"missed_rows,omitempty"`
	RegressedRows    []string      `json:"regressed_rows,omitempty"`
	EvalPassed       int           `json:"eval_passed"`
	EvalTotal        int           `json:"eval_total"`
	FieldStatus      string        `json:"field_status"`
	FieldMetric      string        `json:"field_metric,omitempty"`
	FieldBaseline    *float64      `json:"field_baseline,omitempty"`
	FieldObserved    *float64      `json:"field_observed,omitempty"`
	FieldModels      []ModelStatus `json:"field_models,omitempty"`
	Provenance       string        `json:"provenance"`
	CreatedAt        string        `json:"created_at"`
	UpdatedAt        string        `json:"updated_at"`
}

// UnmarshalJSON decodes Attempt while accepting either []ModelStatus or
// legacy []string for field_models.
func (a *Attempt) UnmarshalJSON(data []byte) error {
	type alias Attempt
	aux := struct {
		*alias
		FieldModels json.RawMessage `json:"field_models,omitempty"`
	}{alias: (*alias)(a)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.FieldModels) > 0 {
		ms, err := ParseFieldModels(aux.FieldModels)
		if err != nil {
			return err
		}
		a.FieldModels = ms
	}
	return nil
}

// Alias maps a merged SIM problem ID to its canonical target problem ID.
type Alias struct {
	OldID    string `json:"old_id"`
	TargetID string `json:"target_id"`
	MergedAt string `json:"merged_at"`
}

// RecordInput is the input payload for RecordAttempt.
type RecordInput struct {
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

// RecordResult summarizes one RecordAttempt write.
type RecordResult struct {
	ProblemID      string `json:"problem_id"`
	AttemptID      int64  `json:"attempt_id"`
	CreatedProblem bool   `json:"created_problem"`
	ResolvedAlias  bool   `json:"resolved_alias"`
}

// ListFilter filters ListProblems.
type ListFilter struct {
	SignalID, PathPrefix, Status string
	Limit                        int
}

// ProblemSummary pairs one Problem with aggregate attempt metadata.
type ProblemSummary struct {
	Problem      Problem `json:"problem"`
	AttemptCount int     `json:"attempt_count"`
	LastVerdict  string  `json:"last_verdict,omitempty"`
	LastField    string  `json:"last_field_status,omitempty"`
	LastModel    string  `json:"last_policy_model,omitempty"`
}

// ProblemDetail holds one canonical Problem, its aliases, and attempts.
type ProblemDetail struct {
	Problem  Problem   `json:"problem"`
	Aliases  []string  `json:"aliases,omitempty"`
	Attempts []Attempt `json:"attempts"`
}

// MergeResult reports one MergeProblem transition.
type MergeResult struct {
	OldID    string `json:"old_id"`
	TargetID string `json:"target_id"`
	MergedAt string `json:"merged_at"`
}

// FieldUpdate updates Stage B field status and stratified models on an attempt.
type FieldUpdate struct {
	AttemptID     int64
	CommitSHA     string
	ProposalID    string
	FieldStatus   string
	FieldMetric   string
	FieldObserved *float64
	FieldModels   []ModelStatus
	Now           time.Time
}

// Query configures bounded read-only retrieval via RetrieveSlice.
type Query struct {
	Signals               []string
	TargetPaths           []string
	Terms                 []string
	MaxBytes              int // default 6144 (6 KiB)
	MaxProblems           int // default 8
	MaxAttemptsPerProblem int // default 4
	RecentFailedDays      int // default 30
	Now                   time.Time
}

// Slice is the bounded, redacted text slice returned by RetrieveSlice.
type Slice struct {
	Text         string
	ProblemCount int
	AttemptCount int
	Truncated    bool
}

// StateExport holds portable SIM ledger rows for offline state bundles.
type StateExport struct {
	Problems []Problem `json:"problems"`
	Attempts []Attempt `json:"attempts"`
	Aliases  []Alias   `json:"aliases"`
}

// StateImportOpts configures ImportState.
type StateImportOpts struct {
	DryRun bool
}

// ImportStats summarizes one ImportState call.
type ImportStats struct {
	Total             int `json:"total"`
	Inserted          int `json:"inserted"`
	Updated           int `json:"updated"`
	Skipped           int `json:"skipped"`
	Conflicts         int `json:"conflicts"`
	OperatorPreserved int `json:"operator_preserved"`
}

// ValidProblemID reports whether id matches SIM-PNNN..SIM-PNNNNNNNNN.
func ValidProblemID(id string) bool {
	return problemIDRe.MatchString(id)
}

// SHAPrefixMatch reports whether a and b are valid 7..40-char hex SHAs where
// one is a prefix of the other (case-insensitive).
func SHAPrefixMatch(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if !hexSHARe.MatchString(a) || !hexSHARe.MatchString(b) {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// HasAlnum reports whether s contains at least one Unicode letter or digit.
func HasAlnum(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// IsNotSignalRune reports whether r delimits signal tokens.
func IsNotSignalRune(r rune) bool {
	return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != ':' && r != '_' && r != '-'
}

// ExtractSignalTokens scans text and invokes push for every sig:* token.
func ExtractSignalTokens(text string, push func(string)) {
	if text == "" {
		return
	}
	for _, tok := range strings.FieldsFunc(text, IsNotSignalRune) {
		if strings.HasPrefix(tok, "sig:") {
			push(tok)
		}
	}
}

// CollectTargetPaths returns deduplicated skills/* paths collected from
// installed skill names and free-form texts.
func CollectTargetPaths(skillNames, texts []string) []string {
	seen := map[string]bool{}
	var out []string
	push := func(p string) {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, name := range skillNames {
		if n := strings.TrimSpace(name); n != "" {
			push("skills/" + n + "/")
		}
	}
	for _, text := range texts {
		simExtractSkillPaths(text, push)
	}
	return out
}

func simExtractSkillPaths(text string, push func(string)) {
	for _, f := range strings.Fields(text) {
		tok := strings.Trim(f, "`\"'.,;:()[]")
		if strings.HasPrefix(tok, "skills/") && len(tok) > len("skills/") {
			push(tok)
		}
	}
}

// CollectTerms returns up to maxTerms deduplicated FTS query terms built from
// an initial seed, signal suffixes, and leading lines of texts.
func CollectTerms(seed string, signals, texts []string, maxTerms int) []string {
	seen := map[string]bool{}
	var out []string
	push := func(term string) {
		term = strings.TrimSpace(term)
		if term == "" || seen[term] || (maxTerms > 0 && len(out) >= maxTerms) {
			return
		}
		seen[term] = true
		out = append(out, term)
	}
	push(seed)
	for _, sig := range signals {
		if i := strings.LastIndexByte(sig, ':'); i >= 0 && i+1 < len(sig) {
			push(strings.NewReplacer("_", " ", "-", " ").Replace(sig[i+1:]))
		}
	}
	for _, text := range texts {
		first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
		push(trail.TruncateUTF8(first, 80))
	}
	return out
}

func simMarshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return "[]"
	}
	return string(b)
}

func simUnmarshalStringSlice(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "[]" || trimmed == "null" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil || len(out) == 0 {
		return nil
	}
	return out
}

func simParseTime(raw string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
			return t
		}
	}
	return time.Time{}
}

func simTimeAtOrAfter(raw string, since time.Time) bool {
	ts := simParseTime(raw)
	return !ts.IsZero() && !ts.Before(since)
}

func simDefaultTitleText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func simDefaultFTSQuery(words []string, op string) string {
	var parts []string
	for _, w := range words {
		q := strings.ReplaceAll(strings.TrimSpace(w), `"`, `""`)
		if q != "" {
			parts = append(parts, `"`+q+`"`)
		}
	}
	return strings.Join(parts, op)
}

func simDefaultPrintable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}
