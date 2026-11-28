package brain

import (
	"database/sql"
	"errors"
	"os"
	"time"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/sim"
)

const (
	simSchemaVersion = sim.SchemaVersion
	maxSIMMetaBytes  = sim.MaxMetaBytes
)

// SIMModelStatus captures per-model stratified Stage B recovery for one attempt.
type SIMModelStatus = sim.ModelStatus

// SIMProblem is one root-cause entry in the global brain.db v5 SIM ledger.
type SIMProblem = sim.Problem

// SIMAttempt is one recorded harness-edit attempt linked to a SIMProblem.
type SIMAttempt = sim.Attempt

// SIMAlias maps a merged SIM problem ID to its canonical target problem ID.
type SIMAlias = sim.Alias

// SIMRecordInput is the input payload for RecordSIMAttempt.
type SIMRecordInput = sim.RecordInput

// SIMRecordResult summarizes one RecordSIMAttempt write.
type SIMRecordResult = sim.RecordResult

// SIMListFilter filters ListSIMProblems.
type SIMListFilter = sim.ListFilter

// SIMProblemSummary pairs one SIMProblem with aggregate attempt metadata.
type SIMProblemSummary = sim.ProblemSummary

// SIMProblemDetail holds one canonical SIMProblem, its aliases, and attempts.
type SIMProblemDetail = sim.ProblemDetail

// SIMMergeResult reports one MergeSIMProblem transition.
type SIMMergeResult = sim.MergeResult

// SIMFieldUpdate updates Stage B field status and stratified models on an attempt.
type SIMFieldUpdate = sim.FieldUpdate

// SIMQuery configures bounded read-only retrieval via RetrieveSIMSlice.
type SIMQuery = sim.Query

// SIMSlice is the bounded, redacted text slice returned by RetrieveSIMSlice.
type SIMSlice = sim.Slice

// SIMStateExport holds portable SIM ledger rows for offline state bundles.
type SIMStateExport = sim.StateExport

// SIMStateImportOpts configures ImportStateSIM.
type SIMStateImportOpts = sim.StateImportOpts

func simDeps() sim.StoreDeps {
	return sim.StoreDeps{
		OpenReadOnly: openSIMReadOnlyDeps,
		WithTx:       withSIMTx,
		StorePath:    Path,
		FTSQuery:     search.FTSQuery,
		Printable:    search.Printable,
		TitleText:    titleText,
		Refuse:       refuse,
		Usage:        usage,
	}
}

func openSIMReadOnlyDeps(home string) (sim.Querier, func() error, error) {
	st, ver, err := openStoreReadOnly(home)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if ver < simSchemaVersion {
		_ = st.close()
		return nil, nil, nil
	}
	return st.db, st.close, nil
}

func withSIMTx(home string, fn func(c *sql.Conn) error) error {
	st, err := openStore(home)
	if err != nil {
		return err
	}
	defer st.close()
	return st.tx(fn)
}

// RecordSIMAttempt records one attempt inside BEGIN IMMEDIATE, opening or
// deterministically merging a SIMProblem, resolving alias chains, and
// enforcing operator provenance immutability.
func RecordSIMAttempt(home string, in SIMRecordInput) (SIMRecordResult, error) {
	return sim.RecordAttempt(simDeps(), home, in)
}

// ListSIMProblems lists SIM problems filtered by signal, path prefix, status,
// and limit, tolerating missing or pre-v5 brain stores read-only.
func ListSIMProblems(home string, f SIMListFilter) ([]SIMProblemSummary, error) {
	return sim.ListProblems(simDeps(), home, f)
}

// GetSIMProblem returns the canonical SIMProblem (resolving any alias chain
// with a depth-16 cycle guard), its merged aliases, and all attempts.
func GetSIMProblem(home, id string) (SIMProblemDetail, error) {
	return sim.GetProblem(simDeps(), home, id)
}

// MergeSIMProblem marks oldID as merged and inserts an alias to targetID's
// canonical problem, refusing cycles and derived merges of operator problems.
func MergeSIMProblem(home, oldID, targetID, provenance string, now time.Time) (SIMMergeResult, error) {
	return sim.MergeProblem(simDeps(), home, oldID, targetID, provenance, now)
}

// UpdateSIMAttemptFieldOutcome updates Stage B field status and stratified
// model recovery on one attempt matched by AttemptID, ProposalID, or >=7-hex
// CommitSHA prefix.
func UpdateSIMAttemptFieldOutcome(home string, upd SIMFieldUpdate) (bool, error) {
	return sim.UpdateAttemptFieldOutcome(simDeps(), home, upd)
}

// FindSIMAttemptByProposalOrSHA looks up the newest matching SIM attempt
// read-only, matching exact proposal_id first and falling back to >=7-hex
// commit_sha prefix matching.
func FindSIMAttemptByProposalOrSHA(home, proposalID, commitSHA string) (*SIMAttempt, error) {
	return sim.FindAttemptByProposalOrSHA(simDeps(), home, proposalID, commitSHA)
}

// FindSIMAttemptsForFieldSync returns non-reverted attempts eligible for Stage
// B field outcome synchronization, tolerating missing or pre-v5 stores.
func FindSIMAttemptsForFieldSync(home string) ([]SIMAttempt, error) {
	return sim.FindAttemptsForFieldSync(simDeps(), home)
}

// RetrieveSIMSlice runs read-only 4-tier priority retrieval over the global
// SIM ledger, returning a bounded, redacted text slice for optimizer prompts.
func RetrieveSIMSlice(home string, q SIMQuery) (SIMSlice, error) {
	return sim.RetrieveSlice(simDeps(), home, q)
}

// ExportStateSIM exports SIM ledger rows from brain.db read-only without
// migrating a pre-v5 store.
func ExportStateSIM(home string, since time.Time) (SIMStateExport, error) {
	return sim.ExportState(simDeps(), home, since)
}

// ImportStateSIM idempotently merges SIM problems, aliases, and attempts into
// brain.db under home.
func ImportStateSIM(home string, exp SIMStateExport, opts SIMStateImportOpts) (StateImportStats, error) {
	stats, err := sim.ImportState(simDeps(), home, exp, opts)
	return StateImportStats(stats), err
}

// SIMValidProblemID reports whether id matches the SIM-PNNN problem ID format.
func SIMValidProblemID(id string) bool {
	return sim.ValidProblemID(id)
}

// SIMSHAPrefixMatch reports whether a and b are valid 7..40-char hex SHAs
// where one is a prefix of the other.
func SIMSHAPrefixMatch(a, b string) bool {
	return sim.SHAPrefixMatch(a, b)
}

// SIMHasAlnum reports whether s contains at least one letter or digit.
func SIMHasAlnum(s string) bool {
	return sim.HasAlnum(s)
}

func redactSIMLine(line, cleanHome string) string {
	return sim.RedactLine(simDeps(), line, cleanHome)
}
