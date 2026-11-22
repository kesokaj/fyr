package brain

import (
	"reflect"
	"strings"
)

type simAttemptNatKey struct {
	problemID, sourceKind, proposalID, commitSHA, createdAt string
}

func makeSIMAttemptNatKey(a SIMAttempt) simAttemptNatKey {
	return simAttemptNatKey{a.ProblemID, a.SourceKind, a.ProposalID, a.CommitSHA, a.CreatedAt}
}

func validateSIMStateExport(exp SIMStateExport) (SIMStateExport, error) {
	out := SIMStateExport{
		Problems: make([]SIMProblem, 0, len(exp.Problems)),
		Attempts: make([]SIMAttempt, 0, len(exp.Attempts)),
		Aliases:  make([]SIMAlias, 0, len(exp.Aliases)),
	}
	for _, p := range exp.Problems {
		cp, err := validateSIMImportProblem(p)
		if err != nil {
			return SIMStateExport{}, err
		}
		out.Problems = append(out.Problems, cp)
	}
	for _, a := range exp.Attempts {
		ca, err := validateSIMImportAttempt(a)
		if err != nil {
			return SIMStateExport{}, err
		}
		out.Attempts = append(out.Attempts, ca)
	}
	for _, al := range exp.Aliases {
		al.OldID, al.TargetID = strings.TrimSpace(al.OldID), strings.TrimSpace(al.TargetID)
		if !validSIMProblemID(al.OldID) || !validSIMProblemID(al.TargetID) || al.OldID == al.TargetID || strings.TrimSpace(al.MergedAt) == "" {
			return SIMStateExport{}, refuse("sim state import: invalid alias %q -> %q", al.OldID, al.TargetID)
		}
		out.Aliases = append(out.Aliases, al)
	}
	return out, nil
}

func validateSIMImportProblem(p SIMProblem) (SIMProblem, error) {
	p.ID = strings.TrimSpace(p.ID)
	if !validSIMProblemID(p.ID) {
		return SIMProblem{}, refuse("sim state import: invalid problem id %q", p.ID)
	}
	var err error
	if p.RootCause, err = cleanSIMText("root_cause", p.RootCause, maxSIMRootCauseBytes, true); err != nil {
		return SIMProblem{}, refuse("sim state import %s: %v", p.ID, err)
	}
	if p.NormRootCause = strings.TrimSpace(p.NormRootCause); p.NormRootCause == "" {
		p.NormRootCause = normalizeRootCause(p.RootCause)
	}
	if p.MechanismClass, err = validateSIMEnum("mechanism_class", p.MechanismClass, "", "structural", "threshold", "model_workaround", "prompt_guidance"); err != nil {
		return SIMProblem{}, refuse("sim state import %s: %v", p.ID, err)
	}
	if p.Status, err = validateSIMEnum("status", p.Status, "active", "active", "resolved", "merged", "obsolete"); err != nil {
		return SIMProblem{}, refuse("sim state import %s: %v", p.ID, err)
	}
	if p.Provenance, err = validateSIMEnum("provenance", p.Provenance, "derived", "operator", "derived"); err != nil {
		return SIMProblem{}, refuse("sim state import %s: %v", p.ID, err)
	}
	if strings.TrimSpace(p.OpenedAt) == "" || strings.TrimSpace(p.UpdatedAt) == "" {
		return SIMProblem{}, refuse("sim state import %s: timestamps required", p.ID)
	}
	return p, nil
}

func validateSIMImportAttempt(a SIMAttempt) (SIMAttempt, error) {
	a.ProblemID = strings.TrimSpace(a.ProblemID)
	if !validSIMProblemID(a.ProblemID) {
		return SIMAttempt{}, refuse("sim state import: invalid attempt problem_id %q", a.ProblemID)
	}
	var err error
	if a.What, err = cleanSIMText("what", a.What, maxSIMWhatBytes, true); err != nil {
		return SIMAttempt{}, refuse("sim state import attempt: %v", err)
	}
	if err := validateSIMImportAttemptEnums(&a); err != nil {
		return SIMAttempt{}, err
	}
	if err := validateSIMImportAttemptDetails(&a); err != nil {
		return SIMAttempt{}, err
	}
	return a, nil
}

func validateSIMImportAttemptEnums(a *SIMAttempt) error {
	var err error
	if a.SourceKind, err = validateSIMEnum("source_kind", a.SourceKind, "operator_commit", "auto_harness_edit", "auto_distill", "retro_harness", "operator_commit"); err != nil {
		return refuse("sim state import attempt: %v", err)
	}
	if a.EvalVerdict, err = validateSIMEnum("eval_verdict", a.EvalVerdict, "none", "kept", "not_kept", "skipped", "operator_verified", "none"); err != nil {
		return refuse("sim state import attempt: %v", err)
	}
	if a.FieldStatus, err = validateSIMEnum("field_status", a.FieldStatus, "n/a", "n/a", "pending_data", "recovered", "still_regressing", "reverted"); err != nil {
		return refuse("sim state import attempt: %v", err)
	}
	if a.Provenance, err = validateSIMEnum("provenance", a.Provenance, "derived", "operator", "derived"); err != nil {
		return refuse("sim state import attempt: %v", err)
	}
	return nil
}

func validateSIMImportAttemptDetails(a *SIMAttempt) error {
	var err error
	if a.CommitSHA, err = cleanSIMSHA("commit_sha", a.CommitSHA); err != nil {
		return refuse("sim state import attempt: %v", err)
	}
	if a.ParentSHA, err = cleanSIMSHA("parent_sha", a.ParentSHA); err != nil {
		return refuse("sim state import attempt: %v", err)
	}
	if err := validateSIMFieldModels(a.FieldModels); err != nil {
		return refuse("sim state import attempt: %v", err)
	}
	if strings.TrimSpace(a.CreatedAt) == "" || strings.TrimSpace(a.UpdatedAt) == "" {
		return refuse("sim state import attempt: timestamps required")
	}
	normalizeEmptySIMAttemptSlices(a)
	return nil
}

func normalizeEmptySIMAttemptSlices(a *SIMAttempt) {
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

func simulateSIMImport(exp SIMStateExport, total int, probs map[string]SIMProblem, atts map[simAttemptNatKey]SIMAttempt, aliases map[string]string) (StateImportStats, error) {
	stats := StateImportStats{Total: total}
	for _, p := range exp.Problems {
		cur, exists := probs[p.ID]
		if apply, _ := decideSIMProblemMerge(cur, exists, p, &stats); apply {
			probs[p.ID] = p
		}
	}
	for _, al := range exp.Aliases {
		apply, err := decideSIMAliasMerge(al, aliases, probs, &stats)
		if err != nil {
			return StateImportStats{}, err
		}
		if apply {
			aliases[al.OldID] = al.TargetID
		}
	}
	for _, a := range exp.Attempts {
		k := makeSIMAttemptNatKey(a)
		cur, exists := atts[k]
		if apply, _ := decideSIMAttemptMerge(cur, exists, a, &stats); apply {
			atts[k] = a
		}
	}
	return stats, nil
}

func decideSIMProblemMerge(cur SIMProblem, exists bool, inc SIMProblem, stats *StateImportStats) (bool, bool) {
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
	tCur, tInc := parseTimeString(cur.UpdatedAt), parseTimeString(inc.UpdatedAt)
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

func decideSIMAliasMerge(al SIMAlias, aliases map[string]string, probs map[string]SIMProblem, stats *StateImportStats) (bool, error) {
	if curTarget, exists := aliases[al.OldID]; exists {
		if curTarget != al.TargetID {
			stats.Conflicts++
		}
		stats.Skipped++
		return false, nil
	}
	if p, ok := probs[al.OldID]; ok && p.Provenance == "operator" && p.Status != "merged" {
		stats.OperatorPreserved++
		stats.Conflicts++
		stats.Skipped++
		return false, nil
	}
	canonTarget, ok := resolveAliasInMap(aliases, al.TargetID)
	if !ok || canonTarget == al.OldID {
		return false, refuse("sim: merge cycle refused")
	}
	stats.Inserted++
	return true, nil
}

func decideSIMAttemptMerge(cur SIMAttempt, exists bool, inc SIMAttempt, stats *StateImportStats) (bool, bool) {
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
	tCur, tInc := parseTimeString(cur.UpdatedAt), parseTimeString(inc.UpdatedAt)
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

func simAttemptContentEqual(a, b SIMAttempt) bool {
	a.ID, b.ID = 0, 0
	return reflect.DeepEqual(a, b)
}
