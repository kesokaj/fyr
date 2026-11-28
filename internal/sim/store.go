package sim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// RecordAttempt records one attempt inside a write transaction, opening or
// deterministically merging a Problem, resolving alias chains, and enforcing
// operator provenance immutability.
func RecordAttempt(deps StoreDeps, home string, in RecordInput) (RecordResult, error) {
	r := runner{d: deps}
	prep, err := r.prepareRecordInput(in)
	if err != nil {
		return RecordResult{}, err
	}
	var res RecordResult
	err = r.withTx(home, func(c *sql.Conn) error {
		ctx := context.Background()
		probID, created, resolvedAlias, err := r.resolveOrCreateProblem(ctx, c, prep)
		if err != nil {
			return err
		}
		attID, err := simInsertAttempt(ctx, c, probID, prep)
		if err != nil {
			return err
		}
		res = RecordResult{
			ProblemID:      probID,
			AttemptID:      attID,
			CreatedProblem: created,
			ResolvedAlias:  resolvedAlias,
		}
		return nil
	})
	return res, err
}

func (r runner) resolveOrCreateProblem(ctx context.Context, c *sql.Conn, p preparedInput) (string, bool, bool, error) {
	if p.ProblemID != "" {
		return r.resolveExplicitProblem(ctx, c, p)
	}
	cur, ok, err := simLookupProblemByNorm(ctx, c, p.normRootCause)
	if err != nil {
		return "", false, false, err
	}
	if ok {
		return r.mergeNormProblem(ctx, c, cur, p)
	}
	id, err := simCreateProblem(ctx, c, p)
	return id, err == nil, false, err
}

func (r runner) resolveExplicitProblem(ctx context.Context, c *sql.Conn, p preparedInput) (string, bool, bool, error) {
	canonID, resolved, err := r.resolveAlias(ctx, c, p.ProblemID)
	if err != nil {
		return "", false, false, err
	}
	cur, ok, err := simLookupProblem(ctx, c, canonID)
	if err != nil {
		return "", false, false, err
	}
	if !ok {
		return "", false, false, r.refuse("sim: problem %s not found", p.ProblemID)
	}
	if err := r.applyProblemUpdate(ctx, c, cur, p, true); err != nil {
		return "", false, false, err
	}
	return cur.ID, false, resolved, nil
}

func (r runner) mergeNormProblem(ctx context.Context, c *sql.Conn, cur Problem, p preparedInput) (string, bool, bool, error) {
	resolved := false
	if cur.Status == "merged" {
		canonID, res, err := r.resolveAlias(ctx, c, cur.ID)
		if err != nil {
			return "", false, false, err
		}
		var ok bool
		cur, ok, err = simLookupProblem(ctx, c, canonID)
		if err != nil {
			return "", false, false, err
		}
		if !ok {
			return "", false, false, r.refuse("sim: problem %s not found", canonID)
		}
		resolved = res
	}
	if err := r.applyProblemUpdate(ctx, c, cur, p, false); err != nil {
		return "", false, false, err
	}
	return cur.ID, false, resolved, nil
}

func (r runner) applyProblemUpdate(ctx context.Context, c *sql.Conn, cur Problem, p preparedInput, explicitID bool) error {
	if cur.Provenance == "operator" && p.Provenance != "operator" {
		if p.ProblemStatus != "" || (explicitID && p.RootCause != "" && p.normRootCause != cur.NormRootCause) {
			return r.refuse("sim: operator-provenance problem %s is immutable to derived writes", cur.ID)
		}
		return nil
	}
	next := cur
	next.TargetPaths = r.mergeTargetPaths(cur.TargetPaths, p.Files)
	if p.Provenance == "operator" {
		simApplyOperatorFields(&next, p, explicitID)
	} else {
		simApplyDerivedFields(&next, p)
	}
	_, err := c.ExecContext(ctx, `UPDATE sim_problems
		SET root_cause = ?, norm_root_cause = ?, mechanism_class = ?, signal_id = ?,
		    target_paths = ?, status = ?, provenance = ?, updated_at = ?
		WHERE id = ?`,
		next.RootCause, next.NormRootCause, next.MechanismClass, next.SignalID,
		next.TargetPaths, next.Status, next.Provenance, p.stamp, cur.ID)
	return err
}

func simApplyOperatorFields(next *Problem, p preparedInput, explicitID bool) {
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

func simApplyDerivedFields(next *Problem, p preparedInput) {
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

func simCreateProblem(ctx context.Context, c *sql.Conn, p preparedInput) (string, error) {
	id, err := simAllocateProblemID(ctx, c)
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

func simAllocateProblemID(ctx context.Context, q Querier) (string, error) {
	var maxN int64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(pid), 0) FROM sim_problems`).Scan(&maxN); err != nil {
		return "", err
	}
	for _, sqlq := range []string{`SELECT id FROM sim_problems`, `SELECT old_id FROM sim_aliases`} {
		rows, err := q.QueryContext(ctx, sqlq)
		if err != nil {
			return "", err
		}
		ids, err := simScanIDRows(rows)
		if err != nil {
			return "", err
		}
		for _, id := range ids {
			if n, ok := simParseNumericSuffix(id); ok && n > maxN {
				maxN = n
			}
		}
	}
	return fmt.Sprintf("SIM-P%03d", maxN+1), nil
}

func simInsertAttempt(ctx context.Context, c *sql.Conn, problemID string, p preparedInput) (int64, error) {
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
		p.HarnessVersion, p.ProposerModel, p.PolicyModel, p.ProjectScope, simMarshalJSON(p.Files),
		p.What, simMarshalJSON(p.PredictedRows), simMarshalJSON(p.PredictedSignals), p.EvalVerdict,
		simMarshalJSON(p.FixedRows), simMarshalJSON(p.MissedRows), simMarshalJSON(p.RegressedRows), p.EvalPassed, p.EvalTotal,
		p.FieldStatus, p.FieldMetric, base, p.Provenance, p.stamp, p.stamp)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r runner) resolveAlias(ctx context.Context, q Querier, id string) (string, bool, error) {
	cur := id
	visited := map[string]bool{id: true}
	for depth := 0; depth < maxAliasDepth; depth++ {
		var next string
		err := q.QueryRowContext(ctx, `SELECT target_id FROM sim_aliases WHERE old_id = ?`, cur).Scan(&next)
		if errors.Is(err, sql.ErrNoRows) {
			return cur, cur != id, nil
		}
		if err != nil {
			return "", false, err
		}
		if visited[next] {
			return "", false, r.refuse("sim: merge cycle refused")
		}
		visited[next] = true
		cur = next
	}
	var extra string
	if err := q.QueryRowContext(ctx, `SELECT target_id FROM sim_aliases WHERE old_id = ?`, cur).Scan(&extra); err == nil {
		return "", false, r.refuse("sim: merge cycle refused")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	return cur, cur != id, nil
}

func simLoadAliasMap(ctx context.Context, q Querier) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT old_id, target_id FROM sim_aliases`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var oldID, targetID string
		if err := rows.Scan(&oldID, &targetID); err != nil {
			return nil, err
		}
		out[oldID] = targetID
	}
	return out, rows.Err()
}

func simResolveAliasInMap(m map[string]string, id string) (string, bool) {
	cur := id
	visited := map[string]bool{id: true}
	for depth := 0; depth < maxAliasDepth; depth++ {
		next, ok := m[cur]
		if !ok {
			return cur, true
		}
		if visited[next] {
			return "", false
		}
		visited[next] = true
		cur = next
	}
	if _, more := m[cur]; more {
		return "", false
	}
	return cur, true
}

func simAliasesForCanonical(m map[string]string, canonID string) []string {
	var out []string
	for oldID := range m {
		if target, ok := simResolveAliasInMap(m, oldID); ok && target == canonID {
			out = append(out, oldID)
		}
	}
	sort.Strings(out)
	return out
}

// MergeProblem marks oldID as merged and inserts an alias to targetID's
// canonical problem, refusing cycles and derived merges of operator problems.
func MergeProblem(deps StoreDeps, home, oldID, targetID, provenance string, now time.Time) (MergeResult, error) {
	r := runner{d: deps}
	oldID, targetID = strings.TrimSpace(oldID), strings.TrimSpace(targetID)
	if !ValidProblemID(oldID) || !ValidProblemID(targetID) {
		return MergeResult{}, r.usage("sim: invalid problem id (want SIM-PNNN)")
	}
	prov, err := r.validateEnum("provenance", provenance, "derived", "operator", "derived")
	if err != nil {
		return MergeResult{}, err
	}
	if oldID == targetID {
		return MergeResult{}, r.refuse("sim: merge cycle refused")
	}
	if now.IsZero() {
		now = time.Now()
	}
	var res MergeResult
	err = r.withTx(home, func(c *sql.Conn) error {
		var txErr error
		res, txErr = r.mergeProblemTx(context.Background(), c, oldID, targetID, prov, now.UTC().Format(time.RFC3339))
		return txErr
	})
	return res, err
}

func (r runner) mergeProblemTx(ctx context.Context, c *sql.Conn, oldID, targetID, prov, stamp string) (MergeResult, error) {
	canonTarget, err := r.checkMergeAllowed(ctx, c, oldID, targetID, prov)
	if err != nil {
		return MergeResult{}, err
	}
	var curTarget, curMerged string
	err = c.QueryRowContext(ctx, `SELECT target_id, merged_at FROM sim_aliases WHERE old_id = ?`, oldID).Scan(&curTarget, &curMerged)
	if err == nil {
		if curTarget == canonTarget {
			return MergeResult{OldID: oldID, TargetID: canonTarget, MergedAt: curMerged}, nil
		}
		return MergeResult{}, r.refuse("sim: problem %s is already merged into %s", oldID, curTarget)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return MergeResult{}, err
	}
	if _, err := c.ExecContext(ctx, `UPDATE sim_problems SET status = 'merged', updated_at = ? WHERE id = ?`, stamp, oldID); err != nil {
		return MergeResult{}, err
	}
	if _, err := c.ExecContext(ctx, `INSERT INTO sim_aliases(old_id, target_id, merged_at) VALUES (?, ?, ?)`, oldID, canonTarget, stamp); err != nil {
		return MergeResult{}, err
	}
	return MergeResult{OldID: oldID, TargetID: canonTarget, MergedAt: stamp}, nil
}

func (r runner) checkMergeAllowed(ctx context.Context, c *sql.Conn, oldID, targetID, prov string) (string, error) {
	oldProb, ok, err := simLookupProblem(ctx, c, oldID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", r.refuse("sim: problem %s not found", oldID)
	}
	if oldProb.Provenance == "operator" && prov != "operator" {
		return "", r.refuse("sim: operator-provenance problem %s is immutable to derived writes", oldID)
	}
	if _, ok, err := simLookupProblem(ctx, c, targetID); err != nil {
		return "", err
	} else if !ok {
		return "", r.refuse("sim: problem %s not found", targetID)
	}
	canonTarget, _, err := r.resolveAlias(ctx, c, targetID)
	if err != nil {
		return "", err
	}
	if canonTarget == oldID {
		return "", r.refuse("sim: merge cycle refused")
	}
	return canonTarget, nil
}

// UpdateAttemptFieldOutcome updates Stage B field status and stratified model
// recovery on one attempt matched by AttemptID, ProposalID, or >=7-hex
// CommitSHA prefix. It never overwrites field_status='reverted' and only
// transitions problem status on derived problems.
func UpdateAttemptFieldOutcome(deps StoreDeps, home string, upd FieldUpdate) (bool, error) {
	r := runner{d: deps}
	if strings.TrimSpace(home) == "" {
		return false, r.refuse("brain: home directory is required")
	}
	status, sha, err := r.validateFieldUpdate(upd)
	if err != nil {
		return false, err
	}
	if r.d.StorePath != nil {
		if _, err := os.Lstat(r.d.StorePath(home)); errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
	}
	now := upd.Now
	if now.IsZero() {
		now = time.Now()
	}
	stamp := now.UTC().Format(time.RFC3339)
	var updated bool
	err = r.withTx(home, func(c *sql.Conn) error {
		var txErr error
		updated, txErr = r.updateFieldOutcomeTx(context.Background(), c, upd, status, sha, stamp)
		return txErr
	})
	return updated, err
}

func (r runner) updateFieldOutcomeTx(ctx context.Context, c *sql.Conn, upd FieldUpdate, status, sha, stamp string) (bool, error) {
	att, err := simFindAttemptForUpdate(ctx, c, upd.AttemptID, strings.TrimSpace(upd.ProposalID), sha)
	if err != nil || att == nil {
		return false, err
	}
	if att.FieldStatus == "reverted" {
		return false, nil
	}
	metric := att.FieldMetric
	if m := strings.TrimSpace(upd.FieldMetric); m != "" {
		metric = m
	}
	obs := simResolveObserved(upd.FieldObserved, att.FieldObserved)
	modelsJSON := simMarshalJSON(att.FieldModels)
	if upd.FieldModels != nil {
		modelsJSON = simMarshalJSON(upd.FieldModels)
	}
	if _, err := c.ExecContext(ctx, `UPDATE sim_attempts
		SET field_status = ?, field_metric = ?, field_observed = ?, field_models_json = ?, updated_at = ?
		WHERE id = ?`, status, metric, obs, modelsJSON, stamp, att.ID); err != nil {
		return false, err
	}
	return true, r.transitionDerivedProblemFieldStatus(ctx, c, att.ProblemID, status, stamp)
}

func simResolveObserved(incoming, existing *float64) sql.NullFloat64 {
	if incoming != nil {
		return sql.NullFloat64{Float64: *incoming, Valid: true}
	}
	if existing != nil {
		return sql.NullFloat64{Float64: *existing, Valid: true}
	}
	return sql.NullFloat64{}
}

func (r runner) transitionDerivedProblemFieldStatus(ctx context.Context, c *sql.Conn, problemID, fieldStatus, stamp string) error {
	canonID, _, err := r.resolveAlias(ctx, c, problemID)
	if err != nil {
		return err
	}
	prob, ok, err := simLookupProblem(ctx, c, canonID)
	if err != nil || !ok || prob.Provenance != "derived" {
		return err
	}
	next := prob.Status
	switch {
	case fieldStatus == "recovered" && prob.Status == "active":
		next = "resolved"
	case (fieldStatus == "still_regressing" || fieldStatus == "reverted") && prob.Status == "resolved":
		next = "active"
	}
	if next == prob.Status {
		return nil
	}
	_, err = c.ExecContext(ctx, `UPDATE sim_problems SET status = ?, updated_at = ? WHERE id = ?`, next, stamp, prob.ID)
	return err
}
