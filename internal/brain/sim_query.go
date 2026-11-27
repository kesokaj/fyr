package brain

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

func scanIDRows(rows *sql.Rows) ([]string, error) {
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

func scanSIMProblem(sc simRowScanner) (SIMProblem, error) {
	var p SIMProblem
	err := sc.Scan(&p.ID, &p.RootCause, &p.NormRootCause, &p.MechanismClass, &p.SignalID, &p.TargetPaths,
		&p.Status, &p.Provenance, &p.OpenedAt, &p.UpdatedAt, &p.OpenedHarness, &p.OpenedModel)
	return p, err
}

func scanSIMAttempt(sc simRowScanner) (SIMAttempt, error) {
	var a SIMAttempt
	var filesJSON, predRows, predSigs, fixedRows, missedRows, regRows, modelsJSON string
	var base, obs sql.NullFloat64
	err := sc.Scan(&a.ID, &a.ProblemID, &a.SourceKind, &a.ProposalID, &a.CommitSHA, &a.ParentSHA,
		&a.HarnessVersion, &a.ProposerModel, &a.PolicyModel, &a.ProjectScope, &filesJSON,
		&a.What, &predRows, &predSigs, &a.EvalVerdict,
		&fixedRows, &missedRows, &regRows, &a.EvalPassed, &a.EvalTotal,
		&a.FieldStatus, &a.FieldMetric, &base, &obs, &modelsJSON,
		&a.Provenance, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return SIMAttempt{}, err
	}
	a.Files = unmarshalSIMStringSlice(filesJSON)
	a.PredictedRows = unmarshalSIMStringSlice(predRows)
	a.PredictedSignals = unmarshalSIMStringSlice(predSigs)
	a.FixedRows = unmarshalSIMStringSlice(fixedRows)
	a.MissedRows = unmarshalSIMStringSlice(missedRows)
	a.RegressedRows = unmarshalSIMStringSlice(regRows)
	if base.Valid {
		v := base.Float64
		a.FieldBaseline = &v
	}
	if obs.Valid {
		v := obs.Float64
		a.FieldObserved = &v
	}
	a.FieldModels, _ = parseSIMFieldModels([]byte(modelsJSON))
	return a, nil
}

func lookupSIMProblem(ctx context.Context, q sqlQuerier, id string) (SIMProblem, bool, error) {
	p, err := scanSIMProblem(q.QueryRowContext(ctx, `SELECT `+simProblemCols+` FROM sim_problems WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SIMProblem{}, false, nil
	}
	if err != nil {
		return SIMProblem{}, false, err
	}
	return p, true, nil
}

func lookupSIMProblemByNorm(ctx context.Context, q sqlQuerier, norm string) (SIMProblem, bool, error) {
	p, err := scanSIMProblem(q.QueryRowContext(ctx, `SELECT `+simProblemCols+` FROM sim_problems
		WHERE norm_root_cause = ? AND status != 'obsolete'
		ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'resolved' THEN 1 WHEN 'merged' THEN 2 ELSE 3 END, pid ASC
		LIMIT 1`, norm))
	if errors.Is(err, sql.ErrNoRows) {
		return SIMProblem{}, false, nil
	}
	if err != nil {
		return SIMProblem{}, false, err
	}
	return p, true, nil
}

func querySIMAttemptsForIDs(ctx context.Context, q sqlQuerier, ids []string) ([]SIMAttempt, error) {
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
	var out []SIMAttempt
	for rows.Next() {
		a, err := scanSIMAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListSIMProblems lists SIM problems filtered by signal, path prefix, status,
// and limit, tolerating missing or pre-v5 brain stores read-only.
func ListSIMProblems(home string, f SIMListFilter) ([]SIMProblemSummary, error) {
	status, err := validateSIMEnum("status", f.Status, "active", "active", "resolved", "merged", "obsolete", "all")
	if err != nil {
		return nil, err
	}
	if f.Limit < 0 {
		return nil, usage("sim: limit must be >= 1")
	}
	limit := f.Limit
	if limit == 0 {
		limit = defaultSIMListLimit
	}
	st, err := openSIMReadOnly(home)
	if err != nil || st == nil {
		return nil, err
	}
	defer st.close()
	return listSIMProblemsRead(context.Background(), st.db, f, status, limit)
}

func listSIMProblemsRead(ctx context.Context, q sqlQuerier, f SIMListFilter, status string, limit int) ([]SIMProblemSummary, error) {
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
	var probs []SIMProblem
	for rows.Next() {
		p, err := scanSIMProblem(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if strings.TrimSpace(f.PathPrefix) != "" && !targetPathsMatchPrefix(p.TargetPaths, f.PathPrefix) {
			continue
		}
		probs = append(probs, p)
		if len(probs) >= limit {
			break
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return summarizeSIMProblems(ctx, q, probs)
}

func summarizeSIMProblems(ctx context.Context, q sqlQuerier, probs []SIMProblem) ([]SIMProblemSummary, error) {
	aliases, err := loadSIMAliasMap(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]SIMProblemSummary, 0, len(probs))
	for _, p := range probs {
		ids := []string{p.ID}
		if p.Status != "merged" {
			ids = append(ids, aliasesForCanonical(aliases, p.ID)...)
		}
		atts, err := querySIMAttemptsForIDs(ctx, q, ids)
		if err != nil {
			return nil, err
		}
		sum := SIMProblemSummary{Problem: p, AttemptCount: len(atts)}
		if len(atts) > 0 {
			sum.LastVerdict = atts[0].EvalVerdict
			sum.LastField = atts[0].FieldStatus
			sum.LastModel = atts[0].PolicyModel
		}
		out = append(out, sum)
	}
	return out, nil
}

// GetSIMProblem returns the canonical SIMProblem (resolving any alias chain
// with a depth-16 cycle guard), its merged aliases, and all attempts.
func GetSIMProblem(home, id string) (SIMProblemDetail, error) {
	id = strings.TrimSpace(id)
	if !validSIMProblemID(id) {
		return SIMProblemDetail{}, usage("sim: invalid problem id %q (want SIM-PNNN)", id)
	}
	st, err := openSIMReadOnly(home)
	if err != nil {
		return SIMProblemDetail{}, err
	}
	if st == nil {
		return SIMProblemDetail{}, refuse("sim: problem %s not found", id)
	}
	defer st.close()
	return getSIMProblemRead(context.Background(), st.db, id)
}

func getSIMProblemRead(ctx context.Context, q sqlQuerier, id string) (SIMProblemDetail, error) {
	canonID, _, err := resolveSIMAlias(ctx, q, id)
	if err != nil {
		return SIMProblemDetail{}, err
	}
	prob, ok, err := lookupSIMProblem(ctx, q, canonID)
	if err != nil {
		return SIMProblemDetail{}, err
	}
	if !ok {
		return SIMProblemDetail{}, refuse("sim: problem %s not found", id)
	}
	aliasMap, err := loadSIMAliasMap(ctx, q)
	if err != nil {
		return SIMProblemDetail{}, err
	}
	aliases := aliasesForCanonical(aliasMap, canonID)
	atts, err := querySIMAttemptsForIDs(ctx, q, append([]string{canonID}, aliases...))
	if err != nil {
		return SIMProblemDetail{}, err
	}
	return SIMProblemDetail{Problem: prob, Aliases: aliases, Attempts: atts}, nil
}

// FindSIMAttemptByProposalOrSHA looks up the newest matching SIM attempt
// read-only, matching exact proposal_id first and falling back to >=7-hex
// commit_sha prefix matching. Pre-v5 or missing stores return (nil, nil).
func FindSIMAttemptByProposalOrSHA(home, proposalID, commitSHA string) (*SIMAttempt, error) {
	proposalID = strings.TrimSpace(proposalID)
	commitSHA = strings.ToLower(strings.TrimSpace(commitSHA))
	if proposalID == "" && !simHexSHARe.MatchString(commitSHA) {
		return nil, nil
	}
	st, err := openSIMReadOnly(home)
	if err != nil || st == nil {
		return nil, err
	}
	defer st.close()
	return findSIMAttemptForUpdate(context.Background(), st.db, 0, proposalID, commitSHA)
}

func findSIMAttemptForUpdate(ctx context.Context, q sqlQuerier, attemptID int64, proposalID, commitSHA string) (*SIMAttempt, error) {
	if attemptID > 0 {
		a, err := scanSIMAttempt(q.QueryRowContext(ctx, `SELECT `+simAttemptCols+` FROM sim_attempts WHERE id = ?`, attemptID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return &a, err
	}
	if proposalID != "" {
		a, err := scanSIMAttempt(q.QueryRowContext(ctx, `SELECT `+simAttemptCols+` FROM sim_attempts WHERE proposal_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, proposalID))
		if err == nil {
			return &a, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return findSIMAttemptBySHAPrefix(ctx, q, commitSHA)
}

func findSIMAttemptBySHAPrefix(ctx context.Context, q sqlQuerier, commitSHA string) (*SIMAttempt, error) {
	if !simHexSHARe.MatchString(commitSHA) {
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
		a, err := scanSIMAttempt(rows)
		if err != nil {
			return nil, err
		}
		if shaPrefixMatch(a.CommitSHA, commitSHA) {
			return &a, nil
		}
	}
	return nil, rows.Err()
}
