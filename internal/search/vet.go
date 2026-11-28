package search

import (
	"context"
	"database/sql"

	"github.com/kesokaj/fyr/internal/hooks/telemetry"
	"github.com/kesokaj/fyr/internal/vet"
)

// IntentMode is one of the 9 deterministic turn intents classified by ClassifyIntent.
type IntentMode = vet.IntentMode

const (
	IntentApproval   = vet.IntentApproval
	IntentCorrection = vet.IntentCorrection
	IntentQuestion   = vet.IntentQuestion
	IntentTrivial    = vet.IntentTrivial
	IntentTask       = vet.IntentTask
	IntentBug        = vet.IntentBug
	IntentFeature    = vet.IntentFeature
	IntentReview     = vet.IntentReview
	IntentRelease    = vet.IntentRelease
)

// VetGate is the execution gate assigned by VetPrompt.
type VetGate = vet.VetGate

const (
	GatePassThrough      VetGate = vet.GatePassThrough
	GateEnrichAndProceed VetGate = vet.GateEnrichAndProceed
	GateVetFirst         VetGate = vet.GateVetFirst
)

const maxSkillDigestBytes = vet.MaxSkillDigestBytes

// SkillMeta describes a harness skill candidate for pre-invocation prefetching.
type SkillMeta = vet.SkillMeta

// MatchedRecord is a project or global record matched for the current turn.
type MatchedRecord = vet.MatchedRecord

// TurnBrainContext holds memories, open bug rules, ADRs, active plan, and conflicts for a turn.
type TurnBrainContext = vet.TurnBrainContext

// SliceResult holds the AST slice capsule and metadata for prompt vetting.
type SliceResult = vet.SliceResult

// SubtaskSlice is one deterministic, pairwise-disjoint unit of work emitted on PreInvocation.
type SubtaskSlice = vet.SubtaskSlice

// VetResult is the structured output of VetPrompt and BuildTurnEnvelope.
type VetResult = vet.Result

// VetHistoryRow is one turn's persisted vetting state in .agents/scratch/search.db.
type VetHistoryRow = vet.HistoryRow

// VetHistoryIngestRow is one row read from search.db vet_history for downstream metrics ingest.
type VetHistoryIngestRow = vet.HistoryIngestRow

// Fence is a per-turn cryptographic boundary wrapping untrusted reference data.
type Fence = vet.Fence

// New generates a collision-free 18-hex-char cryptographic fence boundary.
func New(texts ...string) Fence { return vet.New(texts...) }

// EvaluateAnchors checks the 6 prompt anchors and returns present and missing lists.
func EvaluateAnchors(prompt string, slice SliceResult, brainCtx TurnBrainContext) ([]string, []string) {
	return vet.EvaluateAnchors(prompt, slice, brainCtx)
}

// CheckADRConflict reports a conflict string when lowPrompt matches a rejected phrase in adr.
func CheckADRConflict(lowPrompt string, adr MatchedRecord) string {
	return vet.CheckADRConflict(lowPrompt, adr)
}

// ClassifyIntent deterministically classifies prompt into one of the 9 IntentModes.
func ClassifyIntent(prompt string, symbolHits int) IntentMode {
	return vet.ClassifyIntent(prompt, symbolHits)
}

// RouteFeature returns a concise advisory fyr feature route for prompt and intent.
func RouteFeature(prompt string, intent IntentMode, skills []SkillMeta) string {
	return vet.RouteFeature(prompt, intent, skills)
}

// DecomposeSubtasks deterministically splits multi-clause or multi-package prompts.
func DecomposeSubtasks(cleanPrompt string, intent IntentMode, slice SliceResult, files []string, catalog []SkillMeta) []SubtaskSlice {
	return vet.DecomposeSubtasks(cleanPrompt, intent, slice, files, catalog)
}

// MatchSkills scores the installed skill catalog and sets the security subagent flag.
func MatchSkills(prompt string, intent IntentMode, files []string, catalog []SkillMeta) ([]SkillMeta, bool) {
	return vet.MatchSkills(prompt, intent, files, catalog)
}

// ComputeVetScore computes a deterministic 0..100 prompt quality score with carry-forward.
func ComputeVetScore(prompt string, intent IntentMode, slice SliceResult, brainCtx TurnBrainContext, prev *VetHistoryRow) (int, []string, []string) {
	return vet.ComputeScore(prompt, intent, slice, brainCtx, prev)
}

// SynthesizeImprovedPrompt generates a compact delta-only [VET_IMPROVED_PROMPT] record.
func SynthesizeImprovedPrompt(prompt string, res VetResult, slice SliceResult, brainCtx TurnBrainContext, prev *VetHistoryRow) string {
	return vet.SynthesizeImprovedPrompt(prompt, res, slice, brainCtx, prev)
}

// ReadVetHistoryForIngest reads all vet_history rows from db in deterministic order.
func ReadVetHistoryForIngest(db *sql.DB) ([]VetHistoryIngestRow, error) {
	return vet.ReadHistoryForIngest(db)
}

// FormatVetResult renders a structured VetResult for `fyr search --vet`.
func FormatVetResult(res VetResult) string { return vet.FormatResult(res) }

// BuildTurnEnvelope formats turn metadata, contract, advisory route, and
// delta-only enrichment into a fenced text key-value card via renderPreInvocationBanner.
func BuildTurnEnvelope(res *VetResult, slice SliceResult, brainCtx TurnBrainContext, extraSections []string, extraBanners []string) (string, string) {
	return BuildTurnEnvelopeWithMeta(res, slice, brainCtx, turnBannerMetaFromExtras(extraSections, extraBanners))
}

// BuildTurnEnvelopeWithMeta serializes turn metadata, contract, advisory route,
// and delta-only enrichment using typed TurnBannerMeta without stringly round-tripping.
func BuildTurnEnvelopeWithMeta(res *VetResult, slice SliceResult, brainCtx TurnBrainContext, meta TurnBannerMeta) (string, string) {
	return telemetry.BuildTurnEnvelopeWithMeta(res, slice, brainCtx, meta)
}

func vetPromptCoreWithHistory(
	prompt string,
	intent IntentMode,
	slice SliceResult,
	brainCtx TurnBrainContext,
	catalog []SkillMeta,
	prev *VetHistoryRow,
	recentSkills map[string]bool,
	recentPivots []string,
) VetResult {
	return vet.VetPromptCoreWithHistory(prompt, intent, slice, brainCtx, catalog, prev, recentSkills, recentPivots)
}

func collectMatchedRecords(brainCtx TurnBrainContext) []MatchedRecord {
	return vet.CollectMatchedRecords(brainCtx)
}

func isWorkerBriefPrompt(prompt string) bool { return vet.IsWorkerBriefPrompt(prompt) }

func filterSuggestedSkills(skills []SkillMeta, curFiles []string, recentSkills map[string]bool, recentPivots []string) []SkillMeta {
	return vet.FilterSuggestedSkills(skills, curFiles, recentSkills, recentPivots)
}

type vetTurnMetrics struct {
	durationMS    int
	timedOut      bool
	injectedBytes int
}

func (st *store) recordVetHistory(convID string, turnNum int, rawPrompt string, res VetResult, pivotFiles []string) error {
	return st.recordVetHistoryWithMetrics(convID, turnNum, rawPrompt, res, pivotFiles, vetTurnMetrics{})
}

func (st *store) recordVetHistoryWithMetrics(convID string, turnNum int, rawPrompt string, res VetResult, pivotFiles []string, m vetTurnMetrics) error {
	return st.recordVetHistoryWithMetricsCtx(context.Background(), convID, turnNum, rawPrompt, res, pivotFiles, m)
}

func (st *store) recordVetHistoryWithMetricsCtx(ctx context.Context, convID string, turnNum int, rawPrompt string, res VetResult, pivotFiles []string, m vetTurnMetrics) error {
	if st == nil || st.db == nil {
		return ctx.Err()
	}
	return vet.RecordHistoryCtx(ctx, st.db, nowFunc().Unix(), convID, turnNum, rawPrompt, res, pivotFiles, vet.TurnMetrics{
		DurationMS:    m.durationMS,
		TimedOut:      m.timedOut,
		InjectedBytes: m.injectedBytes,
	})
}

func (st *store) lastVetHistory(convID string, beforeTurn int) (*VetHistoryRow, error) {
	return st.lastVetHistoryCtx(context.Background(), convID, beforeTurn)
}

func (st *store) lastVetHistoryCtx(ctx context.Context, convID string, beforeTurn int) (*VetHistoryRow, error) {
	if st == nil || st.db == nil {
		return nil, nil
	}
	return vet.LastHistoryCtx(ctx, st.db, convID, beforeTurn)
}

func (st *store) recentSuggestedSkills(convID string, beforeTurn int) (map[string]bool, []string, error) {
	if st == nil || st.db == nil {
		return nil, nil, nil
	}
	return vet.RecentSuggestedSkills(st.db, convID, beforeTurn)
}

func (st *store) listConversationVetHistory(convID string, limit int) ([]VetHistoryRow, error) {
	if st == nil || st.db == nil {
		return nil, nil
	}
	return vet.ListConversationHistory(st.db, convID, limit)
}
