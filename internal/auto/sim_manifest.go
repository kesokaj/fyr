package auto

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const (
	simManifestMaxRationaleBytes = 400
	simManifestMaxRootCauseBytes = 240
	simManifestMaxPredictedRows  = 16
	simManifestMaxPredictedSigs  = 8
)

var (
	simManifestProblemIDRe = regexp.MustCompile(`^SIM-P[0-9]{3,9}$`)
	simManifestRowIDRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

// simManifest holds a validated pre-eval SIM prediction line from auto-drafts.jsonl.
type simManifest struct {
	ProblemID        string
	RootCause        string
	MechanismClass   string
	Rationale        string
	PredictedRows    []string
	PredictedSignals []string
}

func setSIMDraftField(d *draftLine, key, val string) bool {
	switch key {
	case "problem_id":
		d.ProblemID = val
	case "root_cause":
		d.RootCause = val
	case "mechanism_class":
		d.MechanismClass = val
	case "rationale":
		d.Rationale = val
	case "predicted_rows":
		d.PredictedRows = val
	case "predicted_signals":
		d.PredictedSignals = val
	default:
		return false
	}
	d.hasSIMField = true
	return true
}

func hasSIMManifestFields(d draftLine) bool {
	return d.hasSIMField || d.ProblemID != "" || d.RootCause != "" ||
		d.MechanismClass != "" || d.Rationale != "" ||
		d.PredictedRows != "" || d.PredictedSignals != ""
}

func hasNonSIMManifestFields(d draftLine) bool {
	return d.hasOtherField || d.Action != "" || d.ID != "" || d.Target != "" ||
		d.Title != "" || d.Body != "" || d.Quote != "" || d.MemoryType != "" ||
		d.Scope != "" || d.Sessions != "" || hasProposalMetaFields(d)
}

func hasSIMControlChar(d draftLine) bool {
	for _, fld := range []string{
		d.ProblemID, d.RootCause, d.MechanismClass,
		d.Rationale, d.PredictedRows, d.PredictedSignals,
	} {
		for _, r := range fld {
			if r < 0x20 || r == 0x7f {
				return true
			}
		}
	}
	return false
}

func validateDraftKindFields(d draftLine) error {
	if d.Kind == "sim_manifest" {
		if hasNonSIMManifestFields(d) {
			return errors.New("sim_manifest takes only problem_id, root_cause, mechanism_class, rationale, predicted_rows and predicted_signals")
		}
		return nil
	}
	if hasSIMManifestFields(d) {
		return errors.New("sim_manifest fields are only valid on kind sim_manifest")
	}
	if len(d.Body) >= draftMaxBytes {
		return errors.New("draft body is 4 KiB or larger")
	}
	if strings.TrimSpace(d.Body) == "" {
		return errors.New("draft body is empty")
	}
	return nil
}

func handleSIMManifestDraft(class string, lineNum int, d draftLine, dst **simManifest) (Finding, bool) {
	if d.Kind != "sim_manifest" {
		return Finding{}, false
	}
	if *dst != nil {
		return Finding{"draft", "invalid", class, fmt.Sprintf("line %d: duplicate sim_manifest (at most one per run)", lineNum)}, true
	}
	m, err := parseSIMManifestDraft(class, d)
	if err != nil {
		return Finding{"draft", "invalid", class, fmt.Sprintf("line %d: %s", lineNum, oneLine(err.Error()))}, true
	}
	*dst = m
	return Finding{}, true
}

func parseSIMManifestDraft(class string, d draftLine) (*simManifest, error) {
	if class != ClassHarnessEdit && class != ClassDistill {
		return nil, fmt.Errorf("sim_manifest is not allowed in class %q", class)
	}
	rationale := strings.TrimSpace(d.Rationale)
	if rationale == "" || len(rationale) > simManifestMaxRationaleBytes {
		return nil, fmt.Errorf("sim_manifest rationale must be 1..%d bytes", simManifestMaxRationaleBytes)
	}
	problemID, rootCause, err := parseSIMTargetIdentity(d.ProblemID, d.RootCause)
	if err != nil {
		return nil, err
	}
	mech, err := parseSIMMechanismClass(d.MechanismClass)
	if err != nil {
		return nil, err
	}
	rows, err := parseSIMPredictedRows(d.PredictedRows)
	if err != nil {
		return nil, err
	}
	sigs, err := parseSIMPredictedSignals(d.PredictedSignals)
	if err != nil {
		return nil, err
	}
	return &simManifest{
		ProblemID:        problemID,
		RootCause:        rootCause,
		MechanismClass:   mech,
		Rationale:        rationale,
		PredictedRows:    rows,
		PredictedSignals: sigs,
	}, nil
}

func parseSIMTargetIdentity(rawID, rawCause string) (string, string, error) {
	id := strings.TrimSpace(rawID)
	cause := strings.TrimSpace(rawCause)
	if id == "" && cause == "" {
		return "", "", errors.New("sim_manifest requires problem_id or root_cause")
	}
	if id != "" && !simManifestProblemIDRe.MatchString(id) {
		return "", "", fmt.Errorf("sim_manifest problem_id %q must match SIM-PNNN", id)
	}
	if cause != "" {
		if len(cause) > simManifestMaxRootCauseBytes {
			return "", "", fmt.Errorf("sim_manifest root_cause exceeds %d bytes", simManifestMaxRootCauseBytes)
		}
		if !hasSIMAlnum(cause) {
			return "", "", errors.New("sim_manifest root_cause must contain alphanumeric characters")
		}
	}
	return id, cause, nil
}

func parseSIMMechanismClass(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	switch v {
	case "":
		return "prompt_guidance", nil
	case "structural", "threshold", "model_workaround", "prompt_guidance":
		return v, nil
	default:
		return "", fmt.Errorf("sim_manifest mechanism_class invalid: %q", v)
	}
}

func parseSIMPredictedRows(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	parts := strings.Split(trimmed, ",")
	if len(parts) > simManifestMaxPredictedRows {
		return nil, fmt.Errorf("sim_manifest predicted_rows exceeds max %d", simManifestMaxPredictedRows)
	}
	var out []string
	for _, part := range parts {
		tok := strings.TrimSpace(part)
		if !simManifestRowIDRe.MatchString(tok) {
			return nil, fmt.Errorf("sim_manifest predicted_rows invalid row %q", tok)
		}
		if !slices.Contains(out, tok) {
			out = append(out, tok)
		}
	}
	return out, nil
}

func parseSIMPredictedSignals(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	parts := strings.Split(trimmed, ",")
	if len(parts) > simManifestMaxPredictedSigs {
		return nil, fmt.Errorf("sim_manifest predicted_signals exceeds max %d", simManifestMaxPredictedSigs)
	}
	var out []string
	for _, part := range parts {
		tok := strings.TrimSpace(part)
		if !isValidSignalID(tok) {
			return nil, fmt.Errorf("sim_manifest predicted_signals invalid signal %q", tok)
		}
		if !slices.Contains(out, tok) {
			out = append(out, tok)
		}
	}
	return out, nil
}

func hasSIMAlnum(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
