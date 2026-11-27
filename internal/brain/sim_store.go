package brain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
)

func openSIMReadOnly(home string) (*store, error) {
	st, ver, err := openStoreReadOnly(home)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if ver < simSchemaVersion {
		_ = st.close()
		return nil, nil
	}
	return st, nil
}

// RecordSIMAttempt records one attempt inside BEGIN IMMEDIATE, opening or
// deterministically merging a SIMProblem, resolving alias chains, and
// enforcing operator provenance immutability.
func RecordSIMAttempt(home string, in SIMRecordInput) (SIMRecordResult, error) {
	prep, err := prepareSIMRecordInput(in)
	if err != nil {
		return SIMRecordResult{}, err
	}
	st, err := openStore(home)
	if err != nil {
		return SIMRecordResult{}, err
	}
	defer st.close()
	var res SIMRecordResult
	err = st.tx(func(c *sql.Conn) error {
		ctx := context.Background()
		probID, created, resolvedAlias, err := resolveOrCreateSIMProblem(ctx, c, prep)
		if err != nil {
			return err
		}
		attID, err := insertSIMAttempt(ctx, c, probID, prep)
		if err != nil {
			return err
		}
		res = SIMRecordResult{
			ProblemID:      probID,
			AttemptID:      attID,
			CreatedProblem: created,
			ResolvedAlias:  resolvedAlias,
		}
		return nil
	})
	return res, err
}

func resolveOrCreateSIMProblem(ctx context.Context, c *sql.Conn, p preparedSIMInput) (string, bool, bool, error) {
	if p.ProblemID != "" {
		return resolveExplicitSIMProblem(ctx, c, p)
	}
	cur, ok, err := lookupSIMProblemByNorm(ctx, c, p.normRootCause)
	if err != nil {
		return "", false, false, err
	}
	if ok {
		return mergeNormSIMProblem(ctx, c, cur, p)
	}
	id, err := createSIMProblem(ctx, c, p)
	return id, err == nil, false, err
}

func resolveExplicitSIMProblem(ctx context.Context, c *sql.Conn, p preparedSIMInput) (string, bool, bool, error) {
	canonID, resolved, err := resolveSIMAlias(ctx, c, p.ProblemID)
	if err != nil {
		return "", false, false, err
	}
	cur, ok, err := lookupSIMProblem(ctx, c, canonID)
	if err != nil {
		return "", false, false, err
	}
	if !ok {
		return "", false, false, refuse("sim: problem %s not found", p.ProblemID)
	}
	if err := applySIMProblemUpdate(ctx, c, cur, p, true); err != nil {
		return "", false, false, err
	}
	return cur.ID, false, resolved, nil
}

func mergeNormSIMProblem(ctx context.Context, c *sql.Conn, cur SIMProblem, p preparedSIMInput) (string, bool, bool, error) {
	resolved := false
	if cur.Status == "merged" {
		canonID, r, err := resolveSIMAlias(ctx, c, cur.ID)
		if err != nil {
			return "", false, false, err
		}
		var ok bool
		cur, ok, err = lookupSIMProblem(ctx, c, canonID)
		if err != nil {
			return "", false, false, err
		}
		if !ok {
			return "", false, false, refuse("sim: problem %s not found", canonID)
		}
		resolved = r
	}
	if err := applySIMProblemUpdate(ctx, c, cur, p, false); err != nil {
		return "", false, false, err
	}
	return cur.ID, false, resolved, nil
}

func applySIMProblemUpdate(ctx context.Context, c *sql.Conn, cur SIMProblem, p preparedSIMInput, explicitID bool) error {
	if cur.Provenance == "operator" && p.Provenance != "operator" {
		if p.ProblemStatus != "" || (explicitID && p.RootCause != "" && p.normRootCause != cur.NormRootCause) {
			return refuse("sim: operator-provenance problem %s is immutable to derived writes", cur.ID)
		}
		return nil
	}
	next := cur
	next.TargetPaths = mergeSIMTargetPaths(cur.TargetPaths, p.Files)
	if p.Provenance == "operator" {
		applyOperatorProblemFields(&next, p, explicitID)
	} else {
		applyDerivedProblemFields(&next, p)
	}
	_, err := c.ExecContext(ctx, `UPDATE sim_problems
		SET root_cause = ?, norm_root_cause = ?, mechanism_class = ?, signal_id = ?,
		    target_paths = ?, status = ?, provenance = ?, updated_at = ?
		WHERE id = ?`,
		next.RootCause, next.NormRootCause, next.MechanismClass, next.SignalID,
		next.TargetPaths, next.Status, next.Provenance, p.stamp, cur.ID)
	return err
}

func applyOperatorProblemFields(next *SIMProblem, p preparedSIMInput, explicitID bool) {
	mutated := false
	if p.ProblemStatus != "" {
		next.Status = p.ProblemStatus
		mutated = true
	}
	if explicitID && p.RootCause != "" && p.RootCause != next.RootCause {
		next.RootCause = p.RootCause
		next.NormRootCause = p.normRootCause
		mutated = true
	}
	if p.MechanismClass != "" {
		next.MechanismClass = p.MechanismClass
		mutated = true
	}
	if p.SignalID != "" {
		next.SignalID = p.SignalID
		mutated = true
	}
	if mutated {
		next.Provenance = "operator"
	}
}

func applyDerivedProblemFields(next *SIMProblem, p preparedSIMInput) {
	if next.MechanismClass == "" && p.MechanismClass != "" {
		next.MechanismClass = p.MechanismClass
	}
	if next.SignalID == "" && p.SignalID != "" {
		next.SignalID = p.SignalID
	}
	switch {
	case p.ProblemStatus != "":
		next.Status = p.ProblemStatus
	case next.Status == "resolved" && (p.EvalVerdict == "not_kept" || p.FieldStatus == "still_regressing" || p.FieldStatus == "reverted"):
		next.Status = "active"
	case next.Status == "active" && p.FieldStatus == "recovered":
		next.Status = "resolved"
	}
}

func createSIMProblem(ctx context.Context, c *sql.Conn, p preparedSIMInput) (string, error) {
	id, err := allocateSIMProblemID(ctx, c)
	if err != nil {
		return "", err
	}
	mech := p.MechanismClass
	if mech == "" {
		mech = "prompt_guidance"
	}
	status := p.ProblemStatus
	if status == "" {
		status = "active"
		if p.Provenance == "derived" && p.FieldStatus == "recovered" {
			status = "resolved"
		}
	}
	openedModel := p.PolicyModel
	if openedModel == "" {
		openedModel = p.ProposerModel
	}
	_, err = c.ExecContext(ctx, `INSERT INTO sim_problems(
		id, root_cause, norm_root_cause, mechanism_class, signal_id, target_paths,
		status, provenance, opened_at, updated_at, opened_harness, opened_model)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, p.RootCause, p.normRootCause, mech, p.SignalID, strings.Join(p.Files, ","),
		status, p.Provenance, p.stamp, p.stamp, p.HarnessVersion, openedModel)
	return id, err
}

func allocateSIMProblemID(ctx context.Context, q sqlQuerier) (string, error) {
	var maxN int64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(pid), 0) FROM sim_problems`).Scan(&maxN); err != nil {
		return "", err
	}
	for _, sqlq := range []string{`SELECT id FROM sim_problems`, `SELECT old_id FROM sim_aliases`} {
		rows, err := q.QueryContext(ctx, sqlq)
		if err != nil {
			return "", err
		}
		ids, err := scanIDRows(rows)
		if err != nil {
			return "", err
		}
		for _, id := range ids {
			if n, ok := parseSIMNumericSuffix(id); ok && n > maxN {
				maxN = n
			}
		}
	}
	return fmt.Sprintf("SIM-P%03d", maxN+1), nil
}

func insertSIMAttempt(ctx context.Context, c *sql.Conn, problemID string, p preparedSIMInput) (int64, error) {
	var base sql.NullFloat64
	if p.FieldBaseline != nil {
		base = sql.NullFloat64{Float64: *p.FieldBaseline, Valid: true}
	}
	res, err := c.ExecContext(ctx, `INSERT INTO sim_attempts(
		problem_id, source_kind, proposal_id, commit_sha, parent_sha,
		harness_version, proposer_model, policy_model, project_scope, files_json,
		what, predicted_rows, predicted_signals, eval_verdict,
		fixed_rows, missed_rows, regressed_rows, eval_passed, eval_total,
		field_status, field_metric, field_baseline, field_models_json,
		provenance, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '[]', ?, ?, ?)`,
		problemID, p.SourceKind, p.ProposalID, p.CommitSHA, p.ParentSHA,
		p.HarnessVersion, p.ProposerModel, p.PolicyModel, p.ProjectScope, marshalSIMJSON(p.Files),
		p.What, marshalSIMJSON(p.PredictedRows), marshalSIMJSON(p.PredictedSignals), p.EvalVerdict,
		marshalSIMJSON(p.FixedRows), marshalSIMJSON(p.MissedRows), marshalSIMJSON(p.RegressedRows), p.EvalPassed, p.EvalTotal,
		p.FieldStatus, p.FieldMetric, base, p.Provenance, p.stamp, p.stamp)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
