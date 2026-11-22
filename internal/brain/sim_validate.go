package brain

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type preparedSIMInput struct {
	SIMRecordInput
	normRootCause string
	stamp         string
}

func validSIMProblemID(id string) bool {
	return simProblemIDRe.MatchString(id)
}

func parseSIMNumericSuffix(id string) (int64, bool) {
	m := simProblemIDRe.FindStringSubmatch(id)
	if len(m) != 2 {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	return n, err == nil
}

func normalizeRootCause(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSep := true
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			inSep = false
			continue
		}
		if !inSep {
			b.WriteByte(' ')
			inSep = true
		}
	}
	return strings.TrimSpace(b.String())
}

func cleanSIMText(field, raw string, maxBytes int, required bool) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		if required {
			return "", usage("sim: %s is required", field)
		}
		return "", nil
	}
	if !titleText(s) {
		return "", usage("sim: %s must be single-line UTF-8 without control characters", field)
	}
	if len(s) > maxBytes {
		return "", usage("sim: %s exceeds %d bytes", field, maxBytes)
	}
	return s, nil
}

func cleanSIMSHA(field, raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", nil
	}
	if !simHexSHARe.MatchString(s) {
		return "", usage("sim: invalid %s %q (want 7..40 hex chars)", field, raw)
	}
	return s, nil
}

func shaPrefixMatch(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if !simHexSHARe.MatchString(a) || !simHexSHARe.MatchString(b) {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func cleanSIMFiles(files []string) ([]string, error) {
	seen := make(map[string]bool, len(files))
	var out []string
	for _, raw := range files {
		s := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(raw)), "./")
		if s == "" {
			continue
		}
		if !titleText(s) || len(s) > maxSIMSliceItemBytes {
			return nil, usage("sim: file path %q is invalid or exceeds %d bytes", raw, maxSIMSliceItemBytes)
		}
		if !seen[s] {
			if len(out) >= maxSIMSliceItems {
				return nil, usage("sim: files exceeds %d items", maxSIMSliceItems)
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out, nil
}

func cleanSIMStringSlice(field string, items []string) ([]string, error) {
	seen := make(map[string]bool, len(items))
	var out []string
	for _, raw := range items {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if !titleText(s) || len(s) > maxSIMSliceItemBytes {
			return nil, usage("sim: %s item is invalid or exceeds %d bytes", field, maxSIMSliceItemBytes)
		}
		if !seen[s] {
			if len(out) >= maxSIMSliceItems {
				return nil, usage("sim: %s exceeds %d items", field, maxSIMSliceItems)
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}

func mergeSIMTargetPaths(existing string, files []string) string {
	var all []string
	if strings.TrimSpace(existing) != "" {
		all = append(all, strings.Split(existing, ",")...)
	}
	all = append(all, files...)
	cleaned, _ := cleanSIMFiles(all)
	return strings.Join(cleaned, ",")
}

func targetPathsMatchPrefix(targetPaths, prefix string) bool {
	q := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(prefix)), "./")
	qp := strings.TrimSuffix(q, "/")
	if qp == "" {
		return true
	}
	for _, raw := range strings.Split(targetPaths, ",") {
		t := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(raw)), "./")
		tp := strings.TrimSuffix(t, "/")
		if tp == "" {
			continue
		}
		if tp == qp || strings.HasPrefix(tp, qp+"/") || (strings.HasSuffix(t, "/") && strings.HasPrefix(qp, tp+"/")) {
			return true
		}
	}
	return false
}

func parseSIMFieldModels(raw []byte) ([]SIMModelStatus, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "[]" || trimmed == "null" {
		return nil, nil
	}
	var objs []SIMModelStatus
	if err := json.Unmarshal([]byte(trimmed), &objs); err == nil {
		if len(objs) == 0 {
			return nil, nil
		}
		return objs, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(trimmed), &names); err == nil {
		var out []SIMModelStatus
		for _, n := range names {
			if m := strings.TrimSpace(n); m != "" {
				out = append(out, SIMModelStatus{Model: m})
			}
		}
		return out, nil
	}
	return nil, usage("sim: invalid field_models JSON")
}

func validateSIMEnum(field, val, def string, allowed ...string) (string, error) {
	v := strings.TrimSpace(val)
	if v == "" {
		return def, nil
	}
	for _, ok := range allowed {
		if v == ok {
			return v, nil
		}
	}
	return "", usage("sim: invalid %s %q", field, val)
}

func validateSIMFieldModels(models []SIMModelStatus) error {
	if len(models) > maxSIMSliceItems {
		return usage("sim: field_models exceeds %d items", maxSIMSliceItems)
	}
	for _, m := range models {
		name := strings.TrimSpace(m.Model)
		if name == "" || !titleText(name) || len(name) > maxSIMMetaBytes {
			return usage("sim: invalid field_models model %q", m.Model)
		}
		if m.Sessions < 0 {
			return usage("sim: field_models sessions must be >= 0")
		}
		if m.Status != "" {
			if _, err := validateSIMEnum("field_models.status", m.Status, "", "recovered", "still_regressing", "pending_data"); err != nil {
				return err
			}
		}
	}
	return nil
}

func prepareSIMRecordInput(in SIMRecordInput) (preparedSIMInput, error) {
	var err error
	out := preparedSIMInput{SIMRecordInput: in}
	out.ProblemID = strings.TrimSpace(in.ProblemID)
	if out.ProblemID != "" && !validSIMProblemID(out.ProblemID) {
		return out, usage("sim: invalid problem_id %q (want SIM-PNNN)", in.ProblemID)
	}
	if out.RootCause, err = cleanSIMText("root_cause", in.RootCause, maxSIMRootCauseBytes, false); err != nil {
		return out, err
	}
	if out.RootCause != "" {
		if out.normRootCause = normalizeRootCause(out.RootCause); out.normRootCause == "" {
			return out, usage("sim: root_cause must contain alphanumeric characters")
		}
	}
	if out.ProblemID == "" && out.RootCause == "" {
		return out, usage("sim: problem_id or root_cause is required")
	}
	if out.What, err = cleanSIMText("what", in.What, maxSIMWhatBytes, true); err != nil {
		return out, err
	}
	if err := validateSIMRecordEnums(&out); err != nil {
		return out, err
	}
	if err := cleanSIMRecordDetails(&out); err != nil {
		return out, err
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	out.stamp = now.UTC().Format(time.RFC3339)
	return out, nil
}

func validateSIMRecordEnums(out *preparedSIMInput) error {
	var err error
	if out.MechanismClass, err = validateSIMEnum("mechanism_class", out.MechanismClass, "", "structural", "threshold", "model_workaround", "prompt_guidance"); err != nil {
		return err
	}
	if out.ProblemStatus, err = validateSIMEnum("status", out.ProblemStatus, "", "active", "resolved", "obsolete"); err != nil {
		return err
	}
	if out.SourceKind, err = validateSIMEnum("source_kind", out.SourceKind, "operator_commit", "auto_harness_edit", "auto_distill", "retro_harness", "operator_commit"); err != nil {
		return err
	}
	if out.EvalVerdict, err = validateSIMEnum("eval_verdict", out.EvalVerdict, "none", "kept", "not_kept", "skipped", "operator_verified", "none"); err != nil {
		return err
	}
	if out.FieldStatus, err = validateSIMEnum("field_status", out.FieldStatus, "n/a", "n/a", "pending_data", "recovered", "still_regressing", "reverted"); err != nil {
		return err
	}
	if out.Provenance, err = validateSIMEnum("provenance", out.Provenance, "derived", "operator", "derived"); err != nil {
		return err
	}
	if out.EvalPassed < 0 || out.EvalTotal < 0 {
		return usage("sim: eval counts must be >= 0")
	}
	return nil
}

func cleanSIMRecordDetails(out *preparedSIMInput) error {
	var err error
	if out.CommitSHA, err = cleanSIMSHA("commit_sha", out.CommitSHA); err != nil {
		return err
	}
	if out.ParentSHA, err = cleanSIMSHA("parent_sha", out.ParentSHA); err != nil {
		return err
	}
	if out.Files, err = cleanSIMFiles(out.Files); err != nil {
		return err
	}
	if out.PredictedRows, err = cleanSIMStringSlice("predicted_rows", out.PredictedRows); err != nil {
		return err
	}
	if out.PredictedSignals, err = cleanSIMStringSlice("predicted_signals", out.PredictedSignals); err != nil {
		return err
	}
	if out.FixedRows, err = cleanSIMStringSlice("fixed_rows", out.FixedRows); err != nil {
		return err
	}
	if out.MissedRows, err = cleanSIMStringSlice("missed_rows", out.MissedRows); err != nil {
		return err
	}
	if out.RegressedRows, err = cleanSIMStringSlice("regressed_rows", out.RegressedRows); err != nil {
		return err
	}
	return cleanSIMMetaFields([]simMetaField{
		{"signal_id", &out.SignalID, maxSIMMetaBytes},
		{"proposal_id", &out.ProposalID, maxSIMMetaBytes},
		{"harness_version", &out.HarnessVersion, maxSIMMetaBytes},
		{"proposer_model", &out.ProposerModel, maxSIMMetaBytes},
		{"policy_model", &out.PolicyModel, maxSIMMetaBytes},
		{"project_scope", &out.ProjectScope, maxSIMScopeBytes},
		{"field_metric", &out.FieldMetric, maxSIMMetaBytes},
	})
}

func validateSIMFieldUpdate(upd SIMFieldUpdate) (string, string, error) {
	if strings.TrimSpace(upd.FieldStatus) == "" {
		return "", "", usage("sim: field_status is required")
	}
	status, err := validateSIMEnum("field_status", upd.FieldStatus, "", "n/a", "pending_data", "recovered", "still_regressing", "reverted")
	if err != nil {
		return "", "", err
	}
	sha, err := cleanSIMSHA("commit_sha", upd.CommitSHA)
	if err != nil {
		return "", "", err
	}
	if _, err := cleanSIMText("proposal_id", upd.ProposalID, maxSIMMetaBytes, false); err != nil {
		return "", "", err
	}
	if _, err := cleanSIMText("field_metric", upd.FieldMetric, maxSIMMetaBytes, false); err != nil {
		return "", "", err
	}
	if upd.AttemptID <= 0 && strings.TrimSpace(upd.ProposalID) == "" && sha == "" {
		return "", "", usage("sim: attempt_id, proposal_id, or commit_sha is required")
	}
	if err := validateSIMFieldModels(upd.FieldModels); err != nil {
		return "", "", err
	}
	return status, sha, nil
}
