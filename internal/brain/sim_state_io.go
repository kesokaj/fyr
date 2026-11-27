package brain

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ExportStateSIM exports SIM ledger rows from brain.db read-only without
// migrating a pre-v5 store. When since is non-zero, only rows updated/created
// at or after since (plus parent problems referenced by them) are returned.
func ExportStateSIM(home string, since time.Time) (SIMStateExport, error) {
	st, err := openSIMReadOnly(home)
	if err != nil || st == nil {
		return SIMStateExport{}, err
	}
	defer st.close()
	ctx := context.Background()
	probs, err := exportAllSIMProblems(ctx, st.db)
	if err != nil {
		return SIMStateExport{}, err
	}
	atts, err := exportAllSIMAttempts(ctx, st.db)
	if err != nil {
		return SIMStateExport{}, err
	}
	aliases, err := exportAllSIMAliases(ctx, st.db)
	if err != nil {
		return SIMStateExport{}, err
	}
	if since.IsZero() {
		return SIMStateExport{Problems: probs, Attempts: atts, Aliases: aliases}, nil
	}
	return filterSIMExportSince(probs, atts, aliases, since), nil
}

func exportAllSIMProblems(ctx context.Context, q sqlQuerier) ([]SIMProblem, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+simProblemCols+` FROM sim_problems ORDER BY pid ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SIMProblem
	for rows.Next() {
		p, err := scanSIMProblem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func exportAllSIMAttempts(ctx context.Context, q sqlQuerier) ([]SIMAttempt, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+simAttemptCols+` FROM sim_attempts ORDER BY created_at ASC, id ASC`)
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

func exportAllSIMAliases(ctx context.Context, q sqlQuerier) ([]SIMAlias, error) {
	rows, err := q.QueryContext(ctx, `SELECT old_id, target_id, merged_at FROM sim_aliases ORDER BY old_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SIMAlias
	for rows.Next() {
		var al SIMAlias
		if err := rows.Scan(&al.OldID, &al.TargetID, &al.MergedAt); err != nil {
			return nil, err
		}
		out = append(out, al)
	}
	return out, rows.Err()
}

func filterSIMExportSince(probs []SIMProblem, atts []SIMAttempt, aliases []SIMAlias, since time.Time) SIMStateExport {
	needed := map[string]bool{}
	var outAtts []SIMAttempt
	for _, a := range atts {
		if timeAtOrAfter(a.UpdatedAt, since) || timeAtOrAfter(a.CreatedAt, since) {
			outAtts = append(outAtts, a)
			needed[a.ProblemID] = true
		}
	}
	includeAlias := map[string]bool{}
	for _, al := range aliases {
		if timeAtOrAfter(al.MergedAt, since) {
			includeAlias[al.OldID] = true
			needed[al.OldID], needed[al.TargetID] = true, true
		}
	}
	for _, p := range probs {
		if timeAtOrAfter(p.UpdatedAt, since) || timeAtOrAfter(p.OpenedAt, since) {
			needed[p.ID] = true
		}
	}
	for step := 0; step < maxSIMAliasDepth; step++ {
		changed := false
		for _, al := range aliases {
			if needed[al.OldID] && (!includeAlias[al.OldID] || !needed[al.TargetID]) {
				includeAlias[al.OldID] = true
				needed[al.TargetID] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	var outAliases []SIMAlias
	for _, al := range aliases {
		if includeAlias[al.OldID] {
			outAliases = append(outAliases, al)
		}
	}
	var outProbs []SIMProblem
	for _, p := range probs {
		if needed[p.ID] {
			outProbs = append(outProbs, p)
		}
	}
	return SIMStateExport{Problems: outProbs, Attempts: outAtts, Aliases: outAliases}
}

// ImportStateSIM idempotently merges SIM problems, aliases, and attempts into
// brain.db under home. Local operator provenance is preserved unconditionally;
// opts.DryRun simulates against a read-only snapshot without mutating disk.
func ImportStateSIM(home string, exp SIMStateExport, opts SIMStateImportOpts) (StateImportStats, error) {
	if strings.TrimSpace(home) == "" {
		return StateImportStats{}, refuse("brain: home directory is required")
	}
	clean, err := validateSIMStateExport(exp)
	if err != nil {
		return StateImportStats{}, err
	}
	total := len(clean.Problems) + len(clean.Attempts) + len(clean.Aliases)
	if total == 0 {
		return StateImportStats{}, nil
	}
	if opts.DryRun {
		return importSIMStateDryRun(home, clean, total)
	}
	st, err := openStore(home)
	if err != nil {
		return StateImportStats{}, err
	}
	defer st.close()
	var stats StateImportStats
	err = st.tx(func(c *sql.Conn) error {
		var txErr error
		stats, txErr = applySIMStateImportTx(context.Background(), c, clean, total)
		return txErr
	})
	return stats, err
}

func importSIMStateDryRun(home string, exp SIMStateExport, total int) (StateImportStats, error) {
	probs := map[string]SIMProblem{}
	atts := map[simAttemptNatKey]SIMAttempt{}
	aliases := map[string]string{}
	st, err := openSIMReadOnly(home)
	if err != nil {
		return StateImportStats{}, err
	}
	if st != nil {
		defer st.close()
		if err := loadSIMSnapshot(context.Background(), st.db, probs, atts, aliases); err != nil {
			return StateImportStats{}, err
		}
	}
	return simulateSIMImport(exp, total, probs, atts, aliases)
}

func loadSIMSnapshot(ctx context.Context, q sqlQuerier, probs map[string]SIMProblem, atts map[simAttemptNatKey]SIMAttempt, aliases map[string]string) error {
	ps, err := exportAllSIMProblems(ctx, q)
	if err != nil {
		return err
	}
	for _, p := range ps {
		probs[p.ID] = p
	}
	as, err := exportAllSIMAttempts(ctx, q)
	if err != nil {
		return err
	}
	for _, a := range as {
		atts[makeSIMAttemptNatKey(a)] = a
	}
	als, err := exportAllSIMAliases(ctx, q)
	if err != nil {
		return err
	}
	for _, al := range als {
		aliases[al.OldID] = al.TargetID
	}
	return nil
}

func applySIMStateImportTx(ctx context.Context, c *sql.Conn, exp SIMStateExport, total int) (StateImportStats, error) {
	stats := StateImportStats{Total: total}
	for _, p := range exp.Problems {
		cur, exists, err := lookupSIMProblem(ctx, c, p.ID)
		if err != nil {
			return StateImportStats{}, err
		}
		if apply, isInsert := decideSIMProblemMerge(cur, exists, p, &stats); apply {
			if err := writeSIMImportProblem(ctx, c, isInsert, p); err != nil {
				return StateImportStats{}, err
			}
		}
	}
	if err := applySIMImportAliases(ctx, c, exp.Aliases, &stats); err != nil {
		return StateImportStats{}, err
	}
	if err := applySIMImportAttempts(ctx, c, exp.Attempts, &stats); err != nil {
		return StateImportStats{}, err
	}
	return stats, nil
}

func applySIMImportAliases(ctx context.Context, c *sql.Conn, incoming []SIMAlias, stats *StateImportStats) error {
	aliasMap, err := loadSIMAliasMap(ctx, c)
	if err != nil {
		return err
	}
	for _, al := range incoming {
		probs := map[string]SIMProblem{}
		if p, ok, err := lookupSIMProblem(ctx, c, al.OldID); err != nil {
			return err
		} else if ok {
			probs[al.OldID] = p
		}
		apply, canonTarget, err := decideSIMAliasMerge(al, aliasMap, probs, stats)
		if err != nil {
			return err
		}
		if !apply {
			continue
		}
		if _, err := c.ExecContext(ctx, `INSERT INTO sim_aliases(old_id, target_id, merged_at) VALUES (?, ?, ?)`, al.OldID, canonTarget, al.MergedAt); err != nil {
			return err
		}
		if p, ok := probs[al.OldID]; ok && p.Status != "merged" && p.Provenance != "operator" {
			if _, err := c.ExecContext(ctx, `UPDATE sim_problems SET status = 'merged', updated_at = ? WHERE id = ?`, al.MergedAt, al.OldID); err != nil {
				return err
			}
		}
		aliasMap[al.OldID] = canonTarget
	}
	return nil
}

func applySIMImportAttempts(ctx context.Context, c *sql.Conn, incoming []SIMAttempt, stats *StateImportStats) error {
	for _, a := range incoming {
		cur, exists, err := lookupSIMAttemptByNatKey(ctx, c, a)
		if err != nil {
			return err
		}
		if apply, isInsert := decideSIMAttemptMerge(cur, exists, a, stats); apply {
			if err := writeSIMImportAttempt(ctx, c, cur.ID, isInsert, a); err != nil {
				return err
			}
		}
	}
	return nil
}

func lookupSIMAttemptByNatKey(ctx context.Context, q sqlQuerier, a SIMAttempt) (SIMAttempt, bool, error) {
	cur, err := scanSIMAttempt(q.QueryRowContext(ctx, `SELECT `+simAttemptCols+` FROM sim_attempts
		WHERE problem_id = ? AND source_kind = ? AND proposal_id = ? AND commit_sha = ? AND created_at = ?
		LIMIT 1`, a.ProblemID, a.SourceKind, a.ProposalID, a.CommitSHA, a.CreatedAt))
	if errors.Is(err, sql.ErrNoRows) {
		return SIMAttempt{}, false, nil
	}
	if err != nil {
		return SIMAttempt{}, false, err
	}
	return cur, true, nil
}

func writeSIMImportProblem(ctx context.Context, c *sql.Conn, isInsert bool, p SIMProblem) error {
	if isInsert {
		_, err := c.ExecContext(ctx, `INSERT INTO sim_problems(
			id, root_cause, norm_root_cause, mechanism_class, signal_id, target_paths,
			status, provenance, opened_at, updated_at, opened_harness, opened_model)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.ID, p.RootCause, p.NormRootCause, p.MechanismClass, p.SignalID, p.TargetPaths,
			p.Status, p.Provenance, p.OpenedAt, p.UpdatedAt, p.OpenedHarness, p.OpenedModel)
		return err
	}
	_, err := c.ExecContext(ctx, `UPDATE sim_problems
		SET root_cause = ?, norm_root_cause = ?, mechanism_class = ?, signal_id = ?,
		    target_paths = ?, status = ?, provenance = ?, updated_at = ?,
		    opened_harness = ?, opened_model = ?
		WHERE id = ?`,
		p.RootCause, p.NormRootCause, p.MechanismClass, p.SignalID,
		p.TargetPaths, p.Status, p.Provenance, p.UpdatedAt,
		p.OpenedHarness, p.OpenedModel, p.ID)
	return err
}

func writeSIMImportAttempt(ctx context.Context, c *sql.Conn, curID int64, isInsert bool, a SIMAttempt) error {
	base, obs := resolveSIMObserved(a.FieldBaseline, nil), resolveSIMObserved(a.FieldObserved, nil)
	if isInsert {
		_, err := c.ExecContext(ctx, `INSERT INTO sim_attempts(
			problem_id, source_kind, proposal_id, commit_sha, parent_sha,
			harness_version, proposer_model, policy_model, project_scope, files_json,
			what, predicted_rows, predicted_signals, eval_verdict,
			fixed_rows, missed_rows, regressed_rows, eval_passed, eval_total,
			field_status, field_metric, field_baseline, field_observed, field_models_json,
			provenance, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.ProblemID, a.SourceKind, a.ProposalID, a.CommitSHA, a.ParentSHA,
			a.HarnessVersion, a.ProposerModel, a.PolicyModel, a.ProjectScope, marshalSIMJSON(a.Files),
			a.What, marshalSIMJSON(a.PredictedRows), marshalSIMJSON(a.PredictedSignals), a.EvalVerdict,
			marshalSIMJSON(a.FixedRows), marshalSIMJSON(a.MissedRows), marshalSIMJSON(a.RegressedRows), a.EvalPassed, a.EvalTotal,
			a.FieldStatus, a.FieldMetric, base, obs, marshalSIMJSON(a.FieldModels),
			a.Provenance, a.CreatedAt, a.UpdatedAt)
		return err
	}
	_, err := c.ExecContext(ctx, `UPDATE sim_attempts
		SET parent_sha = ?, harness_version = ?, proposer_model = ?, policy_model = ?,
		    project_scope = ?, files_json = ?, what = ?, predicted_rows = ?, predicted_signals = ?,
		    eval_verdict = ?, fixed_rows = ?, missed_rows = ?, regressed_rows = ?,
		    eval_passed = ?, eval_total = ?, field_status = ?, field_metric = ?,
		    field_baseline = ?, field_observed = ?, field_models_json = ?,
		    provenance = ?, updated_at = ?
		WHERE id = ?`,
		a.ParentSHA, a.HarnessVersion, a.ProposerModel, a.PolicyModel,
		a.ProjectScope, marshalSIMJSON(a.Files), a.What, marshalSIMJSON(a.PredictedRows), marshalSIMJSON(a.PredictedSignals),
		a.EvalVerdict, marshalSIMJSON(a.FixedRows), marshalSIMJSON(a.MissedRows), marshalSIMJSON(a.RegressedRows),
		a.EvalPassed, a.EvalTotal, a.FieldStatus, a.FieldMetric,
		base, obs, marshalSIMJSON(a.FieldModels),
		a.Provenance, a.UpdatedAt, curID)
	return err
}
