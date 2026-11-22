package brain

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"time"
)

// UpdateSIMAttemptFieldOutcome updates Stage B field status and stratified
// model recovery on one attempt matched by AttemptID, ProposalID, or >=7-hex
// CommitSHA prefix. It never overwrites field_status='reverted' and only
// transitions problem status on derived problems.
func UpdateSIMAttemptFieldOutcome(home string, upd SIMFieldUpdate) (bool, error) {
	if strings.TrimSpace(home) == "" {
		return false, refuse("brain: home directory is required")
	}
	status, sha, err := validateSIMFieldUpdate(upd)
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(Path(home)); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	now := upd.Now
	if now.IsZero() {
		now = time.Now()
	}
	stamp := now.UTC().Format(time.RFC3339)
	st, err := openStore(home)
	if err != nil {
		return false, err
	}
	defer st.close()
	var updated bool
	err = st.tx(func(c *sql.Conn) error {
		var txErr error
		updated, txErr = updateSIMFieldOutcomeTx(context.Background(), c, upd, status, sha, stamp)
		return txErr
	})
	return updated, err
}

func updateSIMFieldOutcomeTx(ctx context.Context, c *sql.Conn, upd SIMFieldUpdate, status, sha, stamp string) (bool, error) {
	att, err := findSIMAttemptForUpdate(ctx, c, upd.AttemptID, strings.TrimSpace(upd.ProposalID), sha)
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
	obs := resolveSIMObserved(upd.FieldObserved, att.FieldObserved)
	modelsJSON := marshalSIMJSON(att.FieldModels)
	if upd.FieldModels != nil {
		modelsJSON = marshalSIMJSON(upd.FieldModels)
	}
	if _, err := c.ExecContext(ctx, `UPDATE sim_attempts
		SET field_status = ?, field_metric = ?, field_observed = ?, field_models_json = ?, updated_at = ?
		WHERE id = ?`, status, metric, obs, modelsJSON, stamp, att.ID); err != nil {
		return false, err
	}
	return true, transitionDerivedProblemFieldStatus(ctx, c, att.ProblemID, status, stamp)
}

func resolveSIMObserved(incoming, existing *float64) sql.NullFloat64 {
	if incoming != nil {
		return sql.NullFloat64{Float64: *incoming, Valid: true}
	}
	if existing != nil {
		return sql.NullFloat64{Float64: *existing, Valid: true}
	}
	return sql.NullFloat64{}
}

func transitionDerivedProblemFieldStatus(ctx context.Context, c *sql.Conn, problemID, fieldStatus, stamp string) error {
	canonID, _, err := resolveSIMAlias(ctx, c, problemID)
	if err != nil {
		return err
	}
	prob, ok, err := lookupSIMProblem(ctx, c, canonID)
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

// FindSIMAttemptsForFieldSync returns non-reverted attempts eligible for Stage
// B field outcome synchronization, tolerating missing or pre-v5 stores.
func FindSIMAttemptsForFieldSync(home string) ([]SIMAttempt, error) {
	st, _, err := openSIMReadOnly(home)
	if err != nil || st == nil {
		return nil, err
	}
	defer st.close()
	rows, err := st.db.Query(`SELECT ` + simAttemptCols + ` FROM sim_attempts
		WHERE field_status != 'reverted'
		  AND (field_status IN ('pending_data', 'recovered', 'still_regressing')
		       OR (eval_verdict IN ('kept', 'operator_verified') AND commit_sha != ''))
		ORDER BY created_at DESC, id DESC`)
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
