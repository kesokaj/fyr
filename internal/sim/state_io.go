package sim

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ExportState exports SIM ledger rows read-only without migrating a pre-v5
// store. When since is non-zero, only rows updated/created at or after since
// (plus parent problems referenced by them) are returned.
func ExportState(deps StoreDeps, home string, since time.Time) (StateExport, error) {
	r := runner{d: deps}
	q, closeFn, err := r.openRO(home)
	if err != nil || q == nil {
		return StateExport{}, err
	}
	defer closeFn()
	ctx := context.Background()
	probs, err := simExportAllProblems(ctx, q)
	if err != nil {
		return StateExport{}, err
	}
	atts, err := simExportAllAttempts(ctx, q)
	if err != nil {
		return StateExport{}, err
	}
	aliases, err := simExportAllAliases(ctx, q)
	if err != nil {
		return StateExport{}, err
	}
	if since.IsZero() {
		return StateExport{Problems: probs, Attempts: atts, Aliases: aliases}, nil
	}
	return simFilterExportSince(probs, atts, aliases, since), nil
}

func simExportAllProblems(ctx context.Context, q Querier) ([]Problem, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+simProblemCols+` FROM sim_problems ORDER BY pid ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Problem
	for rows.Next() {
		p, err := simScanProblem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func simExportAllAttempts(ctx context.Context, q Querier) ([]Attempt, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+simAttemptCols+` FROM sim_attempts ORDER BY created_at ASC, id ASC`)
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

func simExportAllAliases(ctx context.Context, q Querier) ([]Alias, error) {
	rows, err := q.QueryContext(ctx, `SELECT old_id, target_id, merged_at FROM sim_aliases ORDER BY old_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alias
	for rows.Next() {
		var al Alias
		if err := rows.Scan(&al.OldID, &al.TargetID, &al.MergedAt); err != nil {
			return nil, err
		}
		out = append(out, al)
	}
	return out, rows.Err()
}

func simFilterExportSince(probs []Problem, atts []Attempt, aliases []Alias, since time.Time) StateExport {
	needed := map[string]bool{}
	includeAlias := map[string]bool{}
	outAtts := simSelectAttemptsSince(atts, since, needed)
	simMarkExportSeeds(probs, aliases, since, needed, includeAlias)
	simExpandExportAliases(aliases, needed, includeAlias)
	return simPickNeededExport(probs, outAtts, aliases, needed, includeAlias)
}

func simSelectAttemptsSince(atts []Attempt, since time.Time, needed map[string]bool) []Attempt {
	var out []Attempt
	for _, a := range atts {
		if simTimeAtOrAfter(a.UpdatedAt, since) || simTimeAtOrAfter(a.CreatedAt, since) {
			out = append(out, a)
			needed[a.ProblemID] = true
		}
	}
	return out
}

func simMarkExportSeeds(probs []Problem, aliases []Alias, since time.Time, needed, includeAlias map[string]bool) {
	for _, al := range aliases {
		if simTimeAtOrAfter(al.MergedAt, since) {
			includeAlias[al.OldID] = true
			needed[al.OldID], needed[al.TargetID] = true, true
		}
	}
	for _, p := range probs {
		if simTimeAtOrAfter(p.UpdatedAt, since) || simTimeAtOrAfter(p.OpenedAt, since) {
			needed[p.ID] = true
		}
	}
}

func simExpandExportAliases(aliases []Alias, needed, includeAlias map[string]bool) {
	for step := 0; step < maxAliasDepth; step++ {
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
}

func simPickNeededExport(probs []Problem, outAtts []Attempt, aliases []Alias, needed, includeAlias map[string]bool) StateExport {
	var outAliases []Alias
	for _, al := range aliases {
		if includeAlias[al.OldID] {
			outAliases = append(outAliases, al)
		}
	}
	var outProbs []Problem
	for _, p := range probs {
		if needed[p.ID] {
			outProbs = append(outProbs, p)
		}
	}
	return StateExport{Problems: outProbs, Attempts: outAtts, Aliases: outAliases}
}

// ImportState idempotently merges SIM problems, aliases, and attempts into
// brain.db under home. Local operator provenance is preserved unconditionally;
// opts.DryRun simulates against a read-only snapshot without mutating disk.
func ImportState(deps StoreDeps, home string, exp StateExport, opts StateImportOpts) (ImportStats, error) {
	r := runner{d: deps}
	if strings.TrimSpace(home) == "" {
		return ImportStats{}, r.refuse("brain: home directory is required")
	}
	clean, err := r.validateStateExport(exp)
	if err != nil {
		return ImportStats{}, err
	}
	total := len(clean.Problems) + len(clean.Attempts) + len(clean.Aliases)
	if total == 0 {
		return ImportStats{}, nil
	}
	if opts.DryRun {
		return r.importStateDryRun(home, clean, total)
	}
	var stats ImportStats
	err = r.withTx(home, func(c *sql.Conn) error {
		var txErr error
		stats, txErr = r.applyStateImportTx(context.Background(), c, clean, total)
		return txErr
	})
	return stats, err
}

func (r runner) importStateDryRun(home string, exp StateExport, total int) (ImportStats, error) {
	probs := map[string]Problem{}
	atts := map[simAttemptNatKey]Attempt{}
	aliases := map[string]string{}
	q, closeFn, err := r.openRO(home)
	if err != nil {
		return ImportStats{}, err
	}
	if q != nil {
		defer closeFn()
		if err := simLoadSnapshot(context.Background(), q, probs, atts, aliases); err != nil {
			return ImportStats{}, err
		}
	}
	return r.simulateImport(exp, total, probs, atts, aliases)
}

func simLoadSnapshot(ctx context.Context, q Querier, probs map[string]Problem, atts map[simAttemptNatKey]Attempt, aliases map[string]string) error {
	ps, err := simExportAllProblems(ctx, q)
	if err != nil {
		return err
	}
	for _, p := range ps {
		probs[p.ID] = p
	}
	as, err := simExportAllAttempts(ctx, q)
	if err != nil {
		return err
	}
	for _, a := range as {
		atts[simMakeAttemptNatKey(a)] = a
	}
	als, err := simExportAllAliases(ctx, q)
	if err != nil {
		return err
	}
	for _, al := range als {
		aliases[al.OldID] = al.TargetID
	}
	return nil
}

func (r runner) applyStateImportTx(ctx context.Context, c *sql.Conn, exp StateExport, total int) (ImportStats, error) {
	stats := ImportStats{Total: total}
	for _, p := range exp.Problems {
		cur, exists, err := simLookupProblem(ctx, c, p.ID)
		if err != nil {
			return ImportStats{}, err
		}
		if apply, isInsert := simDecideProblemMerge(cur, exists, p, &stats); apply {
			if err := simWriteImportProblem(ctx, c, isInsert, p); err != nil {
				return ImportStats{}, err
			}
		}
	}
	if err := r.applyImportAliases(ctx, c, exp.Aliases, &stats); err != nil {
		return ImportStats{}, err
	}
	if err := simApplyImportAttempts(ctx, c, exp.Attempts, &stats); err != nil {
		return ImportStats{}, err
	}
	return stats, nil
}

func (r runner) applyImportAliases(ctx context.Context, c *sql.Conn, incoming []Alias, stats *ImportStats) error {
	aliasMap, err := simLoadAliasMap(ctx, c)
	if err != nil {
		return err
	}
	for _, al := range incoming {
		if err := r.applyOneImportAlias(ctx, c, al, aliasMap, stats); err != nil {
			return err
		}
	}
	return nil
}

func (r runner) applyOneImportAlias(ctx context.Context, c *sql.Conn, al Alias, aliasMap map[string]string, stats *ImportStats) error {
	probs := map[string]Problem{}
	p, ok, err := simLookupProblem(ctx, c, al.OldID)
	if err != nil {
		return err
	}
	if ok {
		probs[al.OldID] = p
	}
	apply, canonTarget, err := r.decideAliasMerge(al, aliasMap, probs, stats)
	if err != nil || !apply {
		return err
	}
	if _, err := c.ExecContext(ctx, `INSERT INTO sim_aliases(old_id, target_id, merged_at) VALUES (?, ?, ?)`, al.OldID, canonTarget, al.MergedAt); err != nil {
		return err
	}
	if ok && p.Status != "merged" && p.Provenance != "operator" {
		if _, err := c.ExecContext(ctx, `UPDATE sim_problems SET status = 'merged', updated_at = ? WHERE id = ?`, al.MergedAt, al.OldID); err != nil {
			return err
		}
	}
	aliasMap[al.OldID] = canonTarget
	return nil
}

func simApplyImportAttempts(ctx context.Context, c *sql.Conn, incoming []Attempt, stats *ImportStats) error {
	for _, a := range incoming {
		cur, exists, err := simLookupAttemptByNatKey(ctx, c, a)
		if err != nil {
			return err
		}
		if apply, isInsert := simDecideAttemptMerge(cur, exists, a, stats); apply {
			if err := simWriteImportAttempt(ctx, c, cur.ID, isInsert, a); err != nil {
				return err
			}
		}
	}
	return nil
}

func simLookupAttemptByNatKey(ctx context.Context, q Querier, a Attempt) (Attempt, bool, error) {
	cur, err := simScanAttempt(q.QueryRowContext(ctx, `SELECT `+simAttemptCols+` FROM sim_attempts
		WHERE problem_id = ? AND source_kind = ? AND proposal_id = ? AND commit_sha = ? AND created_at = ?
		LIMIT 1`, a.ProblemID, a.SourceKind, a.ProposalID, a.CommitSHA, a.CreatedAt))
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, false, nil
	}
	if err != nil {
		return Attempt{}, false, err
	}
	return cur, true, nil
}

func simWriteImportProblem(ctx context.Context, c *sql.Conn, isInsert bool, p Problem) error {
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

func simWriteImportAttempt(ctx context.Context, c *sql.Conn, curID int64, isInsert bool, a Attempt) error {
	base, obs := simResolveObserved(a.FieldBaseline, nil), simResolveObserved(a.FieldObserved, nil)
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
			a.HarnessVersion, a.ProposerModel, a.PolicyModel, a.ProjectScope, simMarshalJSON(a.Files),
			a.What, simMarshalJSON(a.PredictedRows), simMarshalJSON(a.PredictedSignals), a.EvalVerdict,
			simMarshalJSON(a.FixedRows), simMarshalJSON(a.MissedRows), simMarshalJSON(a.RegressedRows), a.EvalPassed, a.EvalTotal,
			a.FieldStatus, a.FieldMetric, base, obs, simMarshalJSON(a.FieldModels),
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
		a.ProjectScope, simMarshalJSON(a.Files), a.What, simMarshalJSON(a.PredictedRows), simMarshalJSON(a.PredictedSignals),
		a.EvalVerdict, simMarshalJSON(a.FixedRows), simMarshalJSON(a.MissedRows), simMarshalJSON(a.RegressedRows),
		a.EvalPassed, a.EvalTotal, a.FieldStatus, a.FieldMetric,
		base, obs, simMarshalJSON(a.FieldModels),
		a.Provenance, a.UpdatedAt, curID)
	return err
}
