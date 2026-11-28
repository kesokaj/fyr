package sim

import (
	"reflect"
	"strings"
)

type simAttemptNatKey struct {
	problemID, sourceKind, proposalID, commitSHA, createdAt string
}

func simMakeAttemptNatKey(a Attempt) simAttemptNatKey {
	return simAttemptNatKey{a.ProblemID, a.SourceKind, a.ProposalID, a.CommitSHA, a.CreatedAt}
}

func (r runner) validateStateExport(exp StateExport) (StateExport, error) {
	out := StateExport{
		Problems: make([]Problem, 0, len(exp.Problems)),
		Attempts: make([]Attempt, 0, len(exp.Attempts)),
		Aliases:  make([]Alias, 0, len(exp.Aliases)),
	}
	for _, p := range exp.Problems {
		cp, err := r.validateImportProblem(p)
		if err != nil {
			return StateExport{}, err
		}
		out.Problems = append(out.Problems, cp)
	}
	for _, a := range exp.Attempts {
		ca, err := r.validateImportAttempt(a)
		if err != nil {
			return StateExport{}, err
		}
		out.Attempts = append(out.Attempts, ca)
	}
	for _, al := range exp.Aliases {
		al.OldID, al.TargetID = strings.TrimSpace(al.OldID), strings.TrimSpace(al.TargetID)
		mergedAt, err := r.cleanTimestamp("merged_at", al.MergedAt)
		if !ValidProblemID(al.OldID) || !ValidProblemID(al.TargetID) || al.OldID == al.TargetID || err != nil {
			return StateExport{}, r.refuse("sim state import: invalid alias %q -> %q", al.OldID, al.TargetID)
		}
		al.MergedAt = mergedAt
		out.Aliases = append(out.Aliases, al)
	}
	return out, nil
}

func (r runner) validateImportProblem(p Problem) (Problem, error) {
	p.ID = strings.TrimSpace(p.ID)
	if !ValidProblemID(p.ID) {
		return Problem{}, r.refuse("sim state import: invalid problem id %q", p.ID)
	}
	var err error
	if p.RootCause, err = r.cleanText("root_cause", p.RootCause, MaxRootCauseBytes, true); err != nil {
		return Problem{}, r.refuse("sim state import %s: %v", p.ID, err)
	}
	if p.NormRootCause = simNormalizeRootCause(p.RootCause); p.NormRootCause == "" {
		return Problem{}, r.refuse("sim state import %s: root_cause must contain alphanumeric characters", p.ID)
	}
	if err := r.validateImportProblemFields(&p); err != nil {
		return Problem{}, r.refuse("sim state import %s: %v", p.ID, err)
	}
	return p, nil
}

func (r runner) validateImportProblemFields(p *Problem) error {
	var err error
	if p.MechanismClass, err = r.validateEnum("mechanism_class", p.MechanismClass, "", "structural", "threshold", "model_workaround", "prompt_guidance"); err != nil {
		return err
	}
	if p.Status, err = r.validateEnum("status", p.Status, "active", "active", "resolved", "merged", "obsolete"); err != nil {
		return err
	}
	if p.Provenance, err = r.validateEnum("provenance", p.Provenance, "derived", "operator", "derived"); err != nil {
		return err
	}
	if p.OpenedAt, err = r.cleanTimestamp("opened_at", p.OpenedAt); err != nil {
		return err
	}
	if p.UpdatedAt, err = r.cleanTimestamp("updated_at", p.UpdatedAt); err != nil {
		return err
	}
	if err := r.cleanMetaFields([]simMetaField{
		{"signal_id", &p.SignalID, MaxMetaBytes},
		{"opened_harness", &p.OpenedHarness, MaxMetaBytes},
		{"opened_model", &p.OpenedModel, MaxMetaBytes},
	}); err != nil {
		return err
	}
	if strings.TrimSpace(p.TargetPaths) != "" {
		files, err := r.cleanFiles(strings.Split(p.TargetPaths, ","))
		if err != nil {
			return err
		}
		joined := strings.Join(files, ",")
		if len(joined) > maxTargetPathsBytes {
			return r.usage("sim: target_paths exceeds %d bytes", maxTargetPathsBytes)
		}
		p.TargetPaths = joined
	} else {
		p.TargetPaths = ""
	}
	return nil
}

func (r runner) validateImportAttempt(a Attempt) (Attempt, error) {
	a.ProblemID = strings.TrimSpace(a.ProblemID)
	if !ValidProblemID(a.ProblemID) {
		return Attempt{}, r.refuse("sim state import: invalid attempt problem_id %q", a.ProblemID)
	}
	var err error
	if a.What, err = r.cleanText("what", a.What, MaxWhatBytes, true); err != nil {
		return Attempt{}, r.refuse("sim state import attempt: %v", err)
	}
	if err := r.validateImportAttemptEnums(&a); err != nil {
		return Attempt{}, err
	}
	if err := r.validateImportAttemptDetails(&a); err != nil {
		return Attempt{}, err
	}
	return a, nil
}

func (r runner) validateImportAttemptEnums(a *Attempt) error {
	var err error
	if a.SourceKind, err = r.validateEnum("source_kind", a.SourceKind, "operator_commit", "auto_harness_edit", "auto_distill", "retro_harness", "operator_commit"); err != nil {
		return r.refuse("sim state import attempt: %v", err)
	}
	if a.EvalVerdict, err = r.validateEnum("eval_verdict", a.EvalVerdict, "none", "kept", "not_kept", "skipped", "operator_verified", "none"); err != nil {
		return r.refuse("sim state import attempt: %v", err)
	}
	if a.FieldStatus, err = r.validateEnum("field_status", a.FieldStatus, "n/a", "n/a", "pending_data", "recovered", "still_regressing", "reverted"); err != nil {
		return r.refuse("sim state import attempt: %v", err)
	}
	if a.Provenance, err = r.validateEnum("provenance", a.Provenance, "derived", "operator", "derived"); err != nil {
		return r.refuse("sim state import attempt: %v", err)
	}
	return nil
}

func (r runner) validateImportAttemptDetails(a *Attempt) error {
	var err error
	if a.EvalPassed < 0 || a.EvalTotal < 0 {
		return r.refuse("sim state import attempt: eval counts must be >= 0")
	}
	if a.CommitSHA, err = r.cleanSHA("commit_sha", a.CommitSHA); err != nil {
		return r.refuse("sim state import attempt: %v", err)
	}
	if a.ParentSHA, err = r.cleanSHA("parent_sha", a.ParentSHA); err != nil {
		return r.refuse("sim state import attempt: %v", err)
	}
	if a.CreatedAt, err = r.cleanTimestamp("created_at", a.CreatedAt); err != nil {
		return r.refuse("sim state import attempt: %v", err)
	}
	if a.UpdatedAt, err = r.cleanTimestamp("updated_at", a.UpdatedAt); err != nil {
		return r.refuse("sim state import attempt: %v", err)
	}
	if err := r.cleanMetaFields([]simMetaField{
		{"proposal_id", &a.ProposalID, MaxMetaBytes},
		{"harness_version", &a.HarnessVersion, MaxMetaBytes},
		{"proposer_model", &a.ProposerModel, MaxMetaBytes},
		{"policy_model", &a.PolicyModel, MaxMetaBytes},
		{"project_scope", &a.ProjectScope, maxScopeBytes},
		{"field_metric", &a.FieldMetric, MaxMetaBytes},
	}); err != nil {
		return r.refuse("sim state import attempt: %v", err)
	}
	if err := r.validateImportAttemptSlices(a); err != nil {
		return r.refuse("sim state import attempt: %v", err)
	}
	simNormalizeEmptyAttemptSlices(a)
	return nil
}

func (r runner) validateImportAttemptSlices(a *Attempt) error {
	var err error
	if a.Files, err = r.cleanFiles(a.Files); err != nil {
		return err
	}
	if a.PredictedRows, err = r.cleanStringSlice("predicted_rows", a.PredictedRows); err != nil {
		return err
	}
	if a.PredictedSignals, err = r.cleanStringSlice("predicted_signals", a.PredictedSignals); err != nil {
		return err
	}
	if a.FixedRows, err = r.cleanStringSlice("fixed_rows", a.FixedRows); err != nil {
		return err
	}
	if a.MissedRows, err = r.cleanStringSlice("missed_rows", a.MissedRows); err != nil {
		return err
	}
	if a.RegressedRows, err = r.cleanStringSlice("regressed_rows", a.RegressedRows); err != nil {
		return err
	}
	return r.validateFieldModels(a.FieldModels)
}

func simNormalizeEmptyAttemptSlices(a *Attempt) {
	if len(a.Files) == 0 {
		a.Files = nil
	}
	if len(a.PredictedRows) == 0 {
		a.PredictedRows = nil
	}
	if len(a.PredictedSignals) == 0 {
		a.PredictedSignals = nil
	}
	if len(a.FixedRows) == 0 {
		a.FixedRows = nil
	}
	if len(a.MissedRows) == 0 {
		a.MissedRows = nil
	}
	if len(a.RegressedRows) == 0 {
		a.RegressedRows = nil
	}
	if len(a.FieldModels) == 0 {
		a.FieldModels = nil
	}
}

func (r runner) simulateImport(exp StateExport, total int, probs map[string]Problem, atts map[simAttemptNatKey]Attempt, aliases map[string]string) (ImportStats, error) {
	stats := ImportStats{Total: total}
	for _, p := range exp.Problems {
		cur, exists := probs[p.ID]
		if apply, _ := simDecideProblemMerge(cur, exists, p, &stats); apply {
			probs[p.ID] = p
		}
	}
	for _, al := range exp.Aliases {
		apply, canonTarget, err := r.decideAliasMerge(al, aliases, probs, &stats)
		if err != nil {
			return ImportStats{}, err
		}
		if apply {
			aliases[al.OldID] = canonTarget
		}
	}
	for _, a := range exp.Attempts {
		nk := simMakeAttemptNatKey(a)
		cur, exists := atts[nk]
		if apply, _ := simDecideAttemptMerge(cur, exists, a, &stats); apply {
			atts[nk] = a
		}
	}
	return stats, nil
}

func simDecideProblemMerge(cur Problem, exists bool, inc Problem, stats *ImportStats) (bool, bool) {
	if !exists {
		stats.Inserted++
		return true, true
	}
	if cur == inc {
		stats.Skipped++
		return false, false
	}
	if cur.Provenance == "operator" {
		stats.OperatorPreserved++
		stats.Conflicts++
		stats.Skipped++
		return false, false
	}
	tCur, tInc := simParseTime(cur.UpdatedAt), simParseTime(inc.UpdatedAt)
	if !tInc.IsZero() && (tCur.IsZero() || tInc.After(tCur)) {
		stats.Updated++
		return true, false
	}
	if !tInc.IsZero() && !tCur.IsZero() && tInc.Equal(tCur) {
		stats.Conflicts++
	}
	stats.Skipped++
	return false, false
}

func (r runner) decideAliasMerge(al Alias, aliases map[string]string, probs map[string]Problem, stats *ImportStats) (bool, string, error) {
	canonTarget, ok := simResolveAliasInMap(aliases, al.TargetID)
	if curTarget, exists := aliases[al.OldID]; exists {
		curCanon, curOK := simResolveAliasInMap(aliases, curTarget)
		same := curTarget == al.TargetID || (ok && curTarget == canonTarget) || (ok && curOK && curCanon == canonTarget)
		if !same {
			stats.Conflicts++
		}
		stats.Skipped++
		return false, "", nil
	}
	if p, has := probs[al.OldID]; has && p.Provenance == "operator" && p.Status != "merged" {
		stats.OperatorPreserved++
		stats.Conflicts++
		stats.Skipped++
		return false, "", nil
	}
	if !ok || canonTarget == al.OldID {
		return false, "", r.refuse("sim: merge cycle refused")
	}
	stats.Inserted++
	return true, canonTarget, nil
}

func simDecideAttemptMerge(cur Attempt, exists bool, inc Attempt, stats *ImportStats) (bool, bool) {
	if !exists {
		stats.Inserted++
		return true, true
	}
	if simAttemptContentEqual(cur, inc) {
		stats.Skipped++
		return false, false
	}
	if cur.Provenance == "operator" {
		stats.OperatorPreserved++
		stats.Conflicts++
		stats.Skipped++
		return false, false
	}
	if cur.FieldStatus == "reverted" && inc.FieldStatus != "reverted" {
		stats.Conflicts++
		stats.Skipped++
		return false, false
	}
	tCur, tInc := simParseTime(cur.UpdatedAt), simParseTime(inc.UpdatedAt)
	if !tInc.IsZero() && (tCur.IsZero() || tInc.After(tCur)) {
		stats.Updated++
		return true, false
	}
	if !tInc.IsZero() && !tCur.IsZero() && tInc.Equal(tCur) {
		stats.Conflicts++
	}
	stats.Skipped++
	return false, false
}

func simAttemptContentEqual(a, b Attempt) bool {
	a.ID, b.ID = 0, 0
	return reflect.DeepEqual(a, b)
}
