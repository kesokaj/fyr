package sim

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type preparedInput struct {
	RecordInput
	normRootCause string
	stamp         string
}

type simMetaField struct {
	name string
	dst  *string
	max  int
}

func simParseNumericSuffix(id string) (int64, bool) {
	m := problemIDRe.FindStringSubmatch(id)
	if len(m) != 2 {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	return n, err == nil
}

func simNormalizeRootCause(s string) string {
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

func (r runner) cleanText(field, raw string, maxBytes int, required bool) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		if required {
			return "", r.usage("sim: %s is required", field)
		}
		return "", nil
	}
	if !r.titleText(s) {
		return "", r.usage("sim: %s must be single-line UTF-8 without control characters", field)
	}
	if len(s) > maxBytes {
		return "", r.usage("sim: %s exceeds %d bytes", field, maxBytes)
	}
	return s, nil
}

func (r runner) cleanSHA(field, raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", nil
	}
	if !hexSHARe.MatchString(s) {
		return "", r.usage("sim: invalid %s %q (want 7..40 hex chars)", field, raw)
	}
	return s, nil
}

func (r runner) cleanTimestamp(field, raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" || !r.titleText(s) {
		return "", r.usage("sim: invalid %s timestamp %q", field, raw)
	}
	if _, err := time.Parse(time.RFC3339, s); err != nil {
		if _, err2 := time.Parse(time.RFC3339Nano, s); err2 != nil {
			return "", r.usage("sim: invalid %s RFC3339 timestamp %q", field, raw)
		}
	}
	return s, nil
}

func (r runner) cleanMetaFields(fields []simMetaField) error {
	for _, f := range fields {
		v, err := r.cleanText(f.name, *f.dst, f.max, false)
		if err != nil {
			return err
		}
		*f.dst = v
	}
	return nil
}

func (r runner) cleanFiles(files []string) ([]string, error) {
	seen := make(map[string]bool, len(files))
	var out []string
	for _, raw := range files {
		s := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(raw)), "./")
		if s == "" {
			continue
		}
		if !r.titleText(s) || len(s) > maxSliceItemBytes {
			return nil, r.usage("sim: file path %q is invalid or exceeds %d bytes", raw, maxSliceItemBytes)
		}
		if !seen[s] {
			if len(out) >= maxSliceItems {
				return nil, r.usage("sim: files exceeds %d items", maxSliceItems)
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (r runner) cleanStringSlice(field string, items []string) ([]string, error) {
	seen := make(map[string]bool, len(items))
	var out []string
	for _, raw := range items {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if !r.titleText(s) || len(s) > maxSliceItemBytes {
			return nil, r.usage("sim: %s item is invalid or exceeds %d bytes", field, maxSliceItemBytes)
		}
		if !seen[s] {
			if len(out) >= maxSliceItems {
				return nil, r.usage("sim: %s exceeds %d items", field, maxSliceItems)
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, nil
}

func (r runner) mergeTargetPaths(existing string, files []string) string {
	var all []string
	if strings.TrimSpace(existing) != "" {
		all = append(all, strings.Split(existing, ",")...)
	}
	all = append(all, files...)
	cleaned, _ := r.cleanFiles(all)
	return strings.Join(cleaned, ",")
}

func simTargetPathsMatchPrefix(targetPaths, prefix string) bool {
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

// ParseFieldModels decodes field_models JSON from either []ModelStatus or
// legacy []string form.
func ParseFieldModels(raw []byte) ([]ModelStatus, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "[]" || trimmed == "null" {
		return nil, nil
	}
	var objs []ModelStatus
	if err := json.Unmarshal([]byte(trimmed), &objs); err == nil {
		if len(objs) == 0 {
			return nil, nil
		}
		return objs, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(trimmed), &names); err == nil {
		var out []ModelStatus
		for _, n := range names {
			if m := strings.TrimSpace(n); m != "" {
				out = append(out, ModelStatus{Model: m})
			}
		}
		return out, nil
	}
	return nil, errors.New("sim: invalid field_models JSON")
}

func (r runner) validateEnum(field, val, def string, allowed ...string) (string, error) {
	v := strings.TrimSpace(val)
	if v == "" {
		return def, nil
	}
	for _, ok := range allowed {
		if v == ok {
			return v, nil
		}
	}
	return "", r.usage("sim: invalid %s %q", field, val)
}

func (r runner) validateFieldModels(models []ModelStatus) error {
	if len(models) > maxSliceItems {
		return r.usage("sim: field_models exceeds %d items", maxSliceItems)
	}
	for _, m := range models {
		name := strings.TrimSpace(m.Model)
		if name == "" || !r.titleText(name) || len(name) > MaxMetaBytes {
			return r.usage("sim: invalid field_models model %q", m.Model)
		}
		if m.Sessions < 0 {
			return r.usage("sim: field_models sessions must be >= 0")
		}
		if m.Status != "" {
			if _, err := r.validateEnum("field_models.status", m.Status, "", "recovered", "still_regressing", "pending_data"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r runner) prepareRecordInput(in RecordInput) (preparedInput, error) {
	var err error
	out := preparedInput{RecordInput: in}
	out.ProblemID = strings.TrimSpace(in.ProblemID)
	if out.ProblemID != "" && !ValidProblemID(out.ProblemID) {
		return out, r.usage("sim: invalid problem_id %q (want SIM-PNNN)", in.ProblemID)
	}
	if out.RootCause, err = r.cleanText("root_cause", in.RootCause, MaxRootCauseBytes, false); err != nil {
		return out, err
	}
	if out.RootCause != "" {
		if out.normRootCause = simNormalizeRootCause(out.RootCause); out.normRootCause == "" {
			return out, r.usage("sim: root_cause must contain alphanumeric characters")
		}
	}
	if out.ProblemID == "" && out.RootCause == "" {
		return out, r.usage("sim: problem_id or root_cause is required")
	}
	if out.What, err = r.cleanText("what", in.What, MaxWhatBytes, true); err != nil {
		return out, err
	}
	if err := r.validateRecordEnums(&out); err != nil {
		return out, err
	}
	if err := r.cleanRecordDetails(&out); err != nil {
		return out, err
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	out.stamp = now.UTC().Format(time.RFC3339)
	return out, nil
}

func (r runner) validateRecordEnums(out *preparedInput) error {
	var err error
	if out.MechanismClass, err = r.validateEnum("mechanism_class", out.MechanismClass, "", "structural", "threshold", "model_workaround", "prompt_guidance"); err != nil {
		return err
	}
	if out.ProblemStatus, err = r.validateEnum("status", out.ProblemStatus, "", "active", "resolved", "obsolete"); err != nil {
		return err
	}
	if out.SourceKind, err = r.validateEnum("source_kind", out.SourceKind, "operator_commit", "auto_harness_edit", "auto_distill", "retro_harness", "operator_commit"); err != nil {
		return err
	}
	if out.EvalVerdict, err = r.validateEnum("eval_verdict", out.EvalVerdict, "none", "kept", "not_kept", "skipped", "operator_verified", "none"); err != nil {
		return err
	}
	if out.FieldStatus, err = r.validateEnum("field_status", out.FieldStatus, "n/a", "n/a", "pending_data", "recovered", "still_regressing", "reverted"); err != nil {
		return err
	}
	if out.Provenance, err = r.validateEnum("provenance", out.Provenance, "derived", "operator", "derived"); err != nil {
		return err
	}
	if out.EvalPassed < 0 || out.EvalTotal < 0 {
		return r.usage("sim: eval counts must be >= 0")
	}
	return nil
}

func (r runner) cleanRecordDetails(out *preparedInput) error {
	var err error
	if out.CommitSHA, err = r.cleanSHA("commit_sha", out.CommitSHA); err != nil {
		return err
	}
	if out.ParentSHA, err = r.cleanSHA("parent_sha", out.ParentSHA); err != nil {
		return err
	}
	if out.Files, err = r.cleanFiles(out.Files); err != nil {
		return err
	}
	if out.PredictedRows, err = r.cleanStringSlice("predicted_rows", out.PredictedRows); err != nil {
		return err
	}
	if out.PredictedSignals, err = r.cleanStringSlice("predicted_signals", out.PredictedSignals); err != nil {
		return err
	}
	if out.FixedRows, err = r.cleanStringSlice("fixed_rows", out.FixedRows); err != nil {
		return err
	}
	if out.MissedRows, err = r.cleanStringSlice("missed_rows", out.MissedRows); err != nil {
		return err
	}
	if out.RegressedRows, err = r.cleanStringSlice("regressed_rows", out.RegressedRows); err != nil {
		return err
	}
	return r.cleanMetaFields([]simMetaField{
		{"signal_id", &out.SignalID, MaxMetaBytes},
		{"proposal_id", &out.ProposalID, MaxMetaBytes},
		{"harness_version", &out.HarnessVersion, MaxMetaBytes},
		{"proposer_model", &out.ProposerModel, MaxMetaBytes},
		{"policy_model", &out.PolicyModel, MaxMetaBytes},
		{"project_scope", &out.ProjectScope, maxScopeBytes},
		{"field_metric", &out.FieldMetric, MaxMetaBytes},
	})
}

func (r runner) validateFieldUpdate(upd FieldUpdate) (string, string, error) {
	if strings.TrimSpace(upd.FieldStatus) == "" {
		return "", "", r.usage("sim: field_status is required")
	}
	status, err := r.validateEnum("field_status", upd.FieldStatus, "", "n/a", "pending_data", "recovered", "still_regressing", "reverted")
	if err != nil {
		return "", "", err
	}
	sha, err := r.cleanSHA("commit_sha", upd.CommitSHA)
	if err != nil {
		return "", "", err
	}
	if _, err := r.cleanText("proposal_id", upd.ProposalID, MaxMetaBytes, false); err != nil {
		return "", "", err
	}
	if _, err := r.cleanText("field_metric", upd.FieldMetric, MaxMetaBytes, false); err != nil {
		return "", "", err
	}
	if upd.AttemptID <= 0 && strings.TrimSpace(upd.ProposalID) == "" && sha == "" {
		return "", "", r.usage("sim: attempt_id, proposal_id, or commit_sha is required")
	}
	if err := r.validateFieldModels(upd.FieldModels); err != nil {
		return "", "", err
	}
	return status, sha, nil
}
