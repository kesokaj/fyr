package sim

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const simProblemCols = `id, root_cause, norm_root_cause, mechanism_class, signal_id, target_paths,
	status, provenance, opened_at, updated_at, opened_harness, opened_model`

const simAttemptCols = `id, problem_id, source_kind, proposal_id, commit_sha, parent_sha,
	harness_version, proposer_model, policy_model, project_scope, files_json,
	what, predicted_rows, predicted_signals, eval_verdict,
	fixed_rows, missed_rows, regressed_rows, eval_passed, eval_total,
	field_status, field_metric, field_baseline, field_observed, field_models_json,
	provenance, created_at, updated_at`

type simRowScanner interface {
	Scan(dest ...any) error
}

func simScanIDRows(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func simScanProblem(sc simRowScanner) (Problem, error) {
	var p Problem
	err := sc.Scan(&p.ID, &p.RootCause, &p.NormRootCause, &p.MechanismClass, &p.SignalID, &p.TargetPaths,
		&p.Status, &p.Provenance, &p.OpenedAt, &p.UpdatedAt, &p.OpenedHarness, &p.OpenedModel)
	return p, err
}

func simScanAttempt(sc simRowScanner) (Attempt, error) {
	var a Attempt
	var filesJSON, predRows, predSigs, fixedRows, missedRows, regRows, modelsJSON string
	var base, obs sql.NullFloat64
	err := sc.Scan(&a.ID, &a.ProblemID, &a.SourceKind, &a.ProposalID, &a.CommitSHA, &a.ParentSHA,
		&a.HarnessVersion, &a.ProposerModel, &a.PolicyModel, &a.ProjectScope, &filesJSON,
		&a.What, &predRows, &predSigs, &a.EvalVerdict,
		&fixedRows, &missedRows, &regRows, &a.EvalPassed, &a.EvalTotal,
		&a.FieldStatus, &a.FieldMetric, &base, &obs, &modelsJSON,
		&a.Provenance, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return Attempt{}, err
	}
	a.Files = simUnmarshalStringSlice(filesJSON)
	a.PredictedRows = simUnmarshalStringSlice(predRows)
	a.PredictedSignals = simUnmarshalStringSlice(predSigs)
	a.FixedRows = simUnmarshalStringSlice(fixedRows)
	a.MissedRows = simUnmarshalStringSlice(missedRows)
	a.RegressedRows = simUnmarshalStringSlice(regRows)
	if base.Valid {
		v := base.Float64
		a.FieldBaseline = &v
	}
	if obs.Valid {
		v := obs.Float64
		a.FieldObserved = &v
	}
	a.FieldModels, _ = ParseFieldModels([]byte(modelsJSON))
	return a, nil
}

func simLookupProblem(ctx context.Context, q Querier, id string) (Problem, bool, error) {
	p, err := simScanProblem(q.QueryRowContext(ctx, `SELECT `+simProblemCols+` FROM sim_problems WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Problem{}, false, nil
	}
	if err != nil {
		return Problem{}, false, err
	}
	return p, true, nil
}

func simLookupProblemByNorm(ctx context.Context, q Querier, norm string) (Problem, bool, error) {
	p, err := simScanProblem(q.QueryRowContext(ctx, `SELECT `+simProblemCols+` FROM sim_problems
		WHERE norm_root_cause = ? AND status != 'obsolete'
		ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'resolved' THEN 1 WHEN 'merged' THEN 2 ELSE 3 END, pid ASC
		LIMIT 1`, norm))
	if errors.Is(err, sql.ErrNoRows) {
		return Problem{}, false, nil
	}
	if err != nil {
		return Problem{}, false, err
	}
	return p, true, nil
}

func simQueryAttemptsForIDs(ctx context.Context, q Querier, ids []string) ([]Attempt, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := q.QueryContext(ctx, `SELECT `+simAttemptCols+` FROM sim_attempts
		WHERE problem_id IN (`+ph+`) ORDER BY created_at DESC, id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		a, err := simScanAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListProblems lists SIM problems filtered by signal, path prefix, status,
// and limit, tolerating missing or pre-v5 stores read-only.
func ListProblems(deps StoreDeps, home string, f ListFilter) ([]ProblemSummary, error) {
	r := runner{d: deps}
	status, err := r.validateEnum("status", f.Status, "active", "active", "resolved", "merged", "obsolete", "all")
	if err != nil {
		return nil, err
	}
	if f.Limit < 0 {
		return nil, r.usage("sim: limit must be >= 1")
	}
	limit := f.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	q, closeFn, err := r.openRO(home)
	if err != nil || q == nil {
		return nil, err
	}
	defer closeFn()
	return simListProblemsRead(context.Background(), q, f, status, limit)
}

func simListProblemsRead(ctx context.Context, q Querier, f ListFilter, status string, limit int) ([]ProblemSummary, error) {
	sqlq := `SELECT ` + simProblemCols + ` FROM sim_problems WHERE 1 = 1`
	var args []any
	if status != "all" {
		sqlq += ` AND status = ?`
		args = append(args, status)
	}
	if sig := strings.TrimSpace(f.SignalID); sig != "" {
		sqlq += ` AND signal_id = ?`
		args = append(args, sig)
	}
	rows, err := q.QueryContext(ctx, sqlq+` ORDER BY updated_at DESC, pid DESC`, args...)
	if err != nil {
		return nil, err
	}
	probs, err := simScanFilteredProblems(rows, strings.TrimSpace(f.PathPrefix), limit)
	if err != nil {
		return nil, err
	}
	return simSummarizeProblems(ctx, q, probs)
}

func simScanFilteredProblems(rows *sql.Rows, pathPrefix string, limit int) ([]Problem, error) {
	defer rows.Close()
	var probs []Problem
	for rows.Next() {
		p, err := simScanProblem(rows)
		if err != nil {
			return nil, err
		}
		if pathPrefix != "" && !simTargetPathsMatchPrefix(p.TargetPaths, pathPrefix) {
			continue
		}
		probs = append(probs, p)
		if len(probs) >= limit {
			break
		}
	}
	return probs, rows.Err()
}

func simSummarizeProblems(ctx context.Context, q Querier, probs []Problem) ([]ProblemSummary, error) {
	aliases, err := simLoadAliasMap(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]ProblemSummary, 0, len(probs))
	for _, p := range probs {
		ids := []string{p.ID}
		if p.Status != "merged" {
			ids = append(ids, simAliasesForCanonical(aliases, p.ID)...)
		}
		atts, err := simQueryAttemptsForIDs(ctx, q, ids)
		if err != nil {
			return nil, err
		}
		sum := ProblemSummary{Problem: p, AttemptCount: len(atts)}
		if len(atts) > 0 {
			sum.LastVerdict = atts[0].EvalVerdict
			sum.LastField = atts[0].FieldStatus
			sum.LastModel = atts[0].PolicyModel
		}
		out = append(out, sum)
	}
	return out, nil
}

// GetProblem returns the canonical Problem (resolving any alias chain with a
// depth-16 cycle guard), its merged aliases, and all attempts.
func GetProblem(deps StoreDeps, home, id string) (ProblemDetail, error) {
	r := runner{d: deps}
	id = strings.TrimSpace(id)
	if !ValidProblemID(id) {
		return ProblemDetail{}, r.usage("sim: invalid problem id %q (want SIM-PNNN)", id)
	}
	q, closeFn, err := r.openRO(home)
	if err != nil {
		return ProblemDetail{}, err
	}
	if q == nil {
		return ProblemDetail{}, r.refuse("sim: problem %s not found", id)
	}
	defer closeFn()
	return r.getProblemRead(context.Background(), q, id)
}

func (r runner) getProblemRead(ctx context.Context, q Querier, id string) (ProblemDetail, error) {
	canonID, _, err := r.resolveAlias(ctx, q, id)
	if err != nil {
		return ProblemDetail{}, err
	}
	prob, ok, err := simLookupProblem(ctx, q, canonID)
	if err != nil {
		return ProblemDetail{}, err
	}
	if !ok {
		return ProblemDetail{}, r.refuse("sim: problem %s not found", id)
	}
	aliasMap, err := simLoadAliasMap(ctx, q)
	if err != nil {
		return ProblemDetail{}, err
	}
	aliases := simAliasesForCanonical(aliasMap, canonID)
	atts, err := simQueryAttemptsForIDs(ctx, q, append([]string{canonID}, aliases...))
	if err != nil {
		return ProblemDetail{}, err
	}
	return ProblemDetail{Problem: prob, Aliases: aliases, Attempts: atts}, nil
}

// FindAttemptByProposalOrSHA looks up the newest matching SIM attempt
// read-only, matching exact proposal_id first and falling back to >=7-hex
// commit_sha prefix matching. Pre-v5 or missing stores return (nil, nil).
func FindAttemptByProposalOrSHA(deps StoreDeps, home, proposalID, commitSHA string) (*Attempt, error) {
	r := runner{d: deps}
	proposalID = strings.TrimSpace(proposalID)
	commitSHA = strings.ToLower(strings.TrimSpace(commitSHA))
	if proposalID == "" && !hexSHARe.MatchString(commitSHA) {
		return nil, nil
	}
	q, closeFn, err := r.openRO(home)
	if err != nil || q == nil {
		return nil, err
	}
	defer closeFn()
	return simFindAttemptForUpdate(context.Background(), q, 0, proposalID, commitSHA)
}

func simFindAttemptForUpdate(ctx context.Context, q Querier, attemptID int64, proposalID, commitSHA string) (*Attempt, error) {
	if attemptID > 0 {
		a, err := simScanAttempt(q.QueryRowContext(ctx, `SELECT `+simAttemptCols+` FROM sim_attempts WHERE id = ?`, attemptID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return &a, err
	}
	if proposalID != "" {
		a, err := simScanAttempt(q.QueryRowContext(ctx, `SELECT `+simAttemptCols+` FROM sim_attempts WHERE proposal_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, proposalID))
		if err == nil {
			return &a, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return simFindAttemptBySHAPrefix(ctx, q, commitSHA)
}

func simFindAttemptBySHAPrefix(ctx context.Context, q Querier, commitSHA string) (*Attempt, error) {
	if !hexSHARe.MatchString(commitSHA) {
		return nil, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT `+simAttemptCols+` FROM sim_attempts
		WHERE length(commit_sha) >= 7 AND lower(substr(commit_sha, 1, 7)) = ?
		ORDER BY created_at DESC, id DESC`, commitSHA[:7])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := simScanAttempt(rows)
		if err != nil {
			return nil, err
		}
		if SHAPrefixMatch(a.CommitSHA, commitSHA) {
			return &a, nil
		}
	}
	return nil, rows.Err()
}

// FindAttemptsForFieldSync returns non-reverted attempts eligible for Stage B
// field outcome synchronization, tolerating missing or pre-v5 stores.
func FindAttemptsForFieldSync(deps StoreDeps, home string) ([]Attempt, error) {
	r := runner{d: deps}
	q, closeFn, err := r.openRO(home)
	if err != nil || q == nil {
		return nil, err
	}
	defer closeFn()
	rows, err := q.QueryContext(context.Background(), `SELECT `+simAttemptCols+` FROM sim_attempts
		WHERE field_status != 'reverted'
		  AND (field_status IN ('pending_data', 'recovered', 'still_regressing')
		       OR (eval_verdict IN ('kept', 'operator_verified') AND commit_sha != ''))
		ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		a, err := simScanAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
