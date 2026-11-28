package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

const maxSIMJSONIn = 1 << 20

type simRecordJSONDoc struct {
	ProblemID        string   `json:"problem_id"`
	RootCause        string   `json:"root_cause"`
	MechanismClass   string   `json:"mechanism_class"`
	Status           string   `json:"status"`
	SignalID         string   `json:"signal_id"`
	SourceKind       string   `json:"source_kind"`
	ProposalID       string   `json:"proposal_id"`
	CommitSHA        string   `json:"commit_sha"`
	ParentSHA        string   `json:"parent_sha"`
	HarnessVersion   string   `json:"harness_version"`
	ProposerModel    string   `json:"proposer_model"`
	PolicyModel      string   `json:"policy_model"`
	ProjectScope     string   `json:"project_scope"`
	Files            []string `json:"files"`
	What             string   `json:"what"`
	PredictedRows    []string `json:"predicted_rows"`
	PredictedSignals []string `json:"predicted_signals"`
	EvalVerdict      string   `json:"eval_verdict"`
	FixedRows        []string `json:"fixed_rows"`
	MissedRows       []string `json:"missed_rows"`
	RegressedRows    []string `json:"regressed_rows"`
	EvalOK           int      `json:"eval_passed"`
	EvalTotal        int      `json:"eval_total"`
	FieldStatus      string   `json:"field_status"`
	FieldMetric      string   `json:"field_metric"`
	FieldBaseline    *float64 `json:"field_baseline"`
	Provenance       string   `json:"provenance"`
}

func visitedFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

func validSIMListStatus(s string) bool {
	switch s {
	case "active", "resolved", "merged", "obsolete", "all":
		return true
	}
	return false
}

func splitSIMCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func applySIMRecordCSVs(in *brain.SIMRecordInput, set map[string]bool, files, predRows, predSigs, fixed, missed, reg string) {
	if set["files"] {
		in.Files = splitSIMCSV(files)
	}
	if set["predicted-rows"] {
		in.PredictedRows = splitSIMCSV(predRows)
	}
	if set["predicted-signals"] {
		in.PredictedSignals = splitSIMCSV(predSigs)
	}
	if set["fixed-rows"] {
		in.FixedRows = splitSIMCSV(fixed)
	}
	if set["missed-rows"] {
		in.MissedRows = splitSIMCSV(missed)
	}
	if set["regressed-rows"] {
		in.RegressedRows = splitSIMCSV(reg)
	}
}

func decodeAutoSIMRecordJSON(r io.Reader) (simRecordJSONDoc, error) {
	if r == nil {
		return simRecordJSONDoc{}, errors.New("invalid --json-in payload: empty input")
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxSIMJSONIn+1))
	if err != nil {
		return simRecordJSONDoc{}, err
	}
	if len(raw) > maxSIMJSONIn {
		return simRecordJSONDoc{}, fmt.Errorf("invalid --json-in payload: exceeds %d bytes", maxSIMJSONIn)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return simRecordJSONDoc{}, errors.New("invalid --json-in payload: expected a JSON object")
	}
	var doc simRecordJSONDoc
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return simRecordJSONDoc{}, fmt.Errorf("invalid --json-in payload: %w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return simRecordJSONDoc{}, errors.New("invalid --json-in payload: trailing tokens after JSON object")
	}
	return doc, nil
}

func applySIMRecordJSON(in *brain.SIMRecordInput, doc simRecordJSONDoc, set map[string]bool) {
	pickStr := func(dst *string, key, val string) {
		if !set[key] && val != "" {
			*dst = val
		}
	}
	pickSlice := func(dst *[]string, key string, val []string) {
		if !set[key] && len(val) > 0 {
			*dst = val
		}
	}
	pickStr(&in.ProblemID, "problem", doc.ProblemID)
	pickStr(&in.RootCause, "root-cause", doc.RootCause)
	pickStr(&in.MechanismClass, "mechanism", doc.MechanismClass)
	pickStr(&in.ProblemStatus, "status", doc.Status)
	pickStr(&in.SignalID, "signal", doc.SignalID)
	pickStr(&in.SourceKind, "source", doc.SourceKind)
	pickStr(&in.ProposalID, "proposal", doc.ProposalID)
	pickStr(&in.CommitSHA, "commit", doc.CommitSHA)
	pickStr(&in.ParentSHA, "parent", doc.ParentSHA)
	pickStr(&in.HarnessVersion, "harness", doc.HarnessVersion)
	pickStr(&in.ProposerModel, "proposer-model", doc.ProposerModel)
	pickStr(&in.PolicyModel, "policy-model", doc.PolicyModel)
	pickStr(&in.What, "what", doc.What)
	pickStr(&in.EvalVerdict, "verdict", doc.EvalVerdict)
	pickStr(&in.FieldStatus, "field-status", doc.FieldStatus)
	pickStr(&in.Provenance, "provenance", doc.Provenance)
	pickSlice(&in.Files, "files", doc.Files)
	pickSlice(&in.PredictedRows, "predicted-rows", doc.PredictedRows)
	pickSlice(&in.PredictedSignals, "predicted-signals", doc.PredictedSignals)
	pickSlice(&in.FixedRows, "fixed-rows", doc.FixedRows)
	pickSlice(&in.MissedRows, "missed-rows", doc.MissedRows)
	pickSlice(&in.RegressedRows, "regressed-rows", doc.RegressedRows)
	in.ProjectScope = doc.ProjectScope
	evalOKDst := &in.EvalPassed
	*evalOKDst = doc.EvalOK
	in.EvalTotal = doc.EvalTotal
	in.FieldMetric = doc.FieldMetric
	in.FieldBaseline = doc.FieldBaseline
}

func applySIMRecordDefaults(in *brain.SIMRecordInput, set map[string]bool) error {
	for _, chk := range []struct{ key, val string }{
		{"mechanism", in.MechanismClass},
		{"status", in.ProblemStatus},
		{"source", in.SourceKind},
		{"verdict", in.EvalVerdict},
		{"field-status", in.FieldStatus},
		{"provenance", in.Provenance},
	} {
		if set[chk.key] && strings.TrimSpace(chk.val) == "" {
			return fmt.Errorf("--%s must not be empty", chk.key)
		}
	}
	if strings.TrimSpace(in.SourceKind) == "" {
		in.SourceKind = "operator_commit"
	}
	if strings.TrimSpace(in.EvalVerdict) == "" {
		in.EvalVerdict = "operator_verified"
	}
	if strings.TrimSpace(in.FieldStatus) == "" {
		in.FieldStatus = "n/a"
	}
	if strings.TrimSpace(in.Provenance) == "" {
		in.Provenance = "operator"
	}
	if strings.TrimSpace(in.MechanismClass) == "" && strings.TrimSpace(in.ProblemID) == "" {
		in.MechanismClass = "prompt_guidance"
	}
	return nil
}

func autoSIMErr(c *runtimeCtx, err error) error {
	switch {
	case err == nil:
		return nil
	case brain.IsUsage(err):
		fmt.Fprintln(c.out, search.Printable("fyr auto sim: "+err.Error()))
		return exitCode(2)
	case brain.IsRefusal(err):
		fmt.Fprintln(c.out, search.Printable("fyr auto sim: "+err.Error()))
		return exitCode(1)
	}
	return errors.New(search.Printable(err.Error()))
}

func autoSIMUsageErr(out io.Writer, reason string) error {
	if reason != "" {
		fmt.Fprintln(out, search.Printable(reason))
	}
	fmt.Fprintln(out, "usage: fyr auto sim list [--signal SIG] [--path PREFIX] [--status S] [--limit N] [--json]")
	fmt.Fprintln(out, "       fyr auto sim show <SIM-ID> [--json]")
	fmt.Fprintln(out, "       fyr auto sim record --what TEXT (--problem ID | --root-cause TEXT) [flags]")
	fmt.Fprintln(out, "       fyr auto sim merge <OLD-ID> <TARGET-ID> [--provenance P] [--json]")
	fmt.Fprintln(out, "run 'fyr auto sim -h' for flags")
	return exitCode(2)
}

func autoSIMHelp() string {
	return `usage: fyr auto sim list [--signal SIG] [--path PREFIX] [--status S]
                         [--limit N] [--json]
       fyr auto sim show <SIM-ID> [--json]
       fyr auto sim record --what TEXT (--problem ID | --root-cause TEXT)
                           [--mechanism M] [--status S] [--signal SIG]
                           [--files CSV] [--proposal ID] [--commit SHA]
                           [--parent SHA] [--harness VER]
                           [--proposer-model ID] [--policy-model ID]
                           [--source K] [--verdict V] [--field-status S]
                           [--predicted-rows CSV] [--predicted-signals CSV]
                           [--fixed-rows CSV] [--missed-rows CSV]
                           [--regressed-rows CSV] [--provenance P]
                           [--json-in] [--json]
       fyr auto sim merge <OLD-ID> <TARGET-ID> [--provenance P] [--json]

Manage the global Self-Improvement Memory (SIM) ledger in ~/.config/fyr/brain.db.
list and show are read-only; record and merge mutate brain.db.

` + helpLast + "\n"
}

func writePrettyJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

func renderAutoSIMList(w io.Writer, items []brain.SIMProblemSummary, asJSON bool) error {
	if asJSON {
		if items == nil {
			items = []brain.SIMProblemSummary{}
		}
		return writePrettyJSON(w, items)
	}
	if len(items) == 0 {
		fmt.Fprintln(w, "(none)")
		return nil
	}
	for _, it := range items {
		model := it.LastModel
		if model == "" {
			model = it.Problem.OpenedModel
		}
		fmt.Fprintf(w, "%s  %s  %d  %s/%s  %s  %s\n",
			it.Problem.ID, it.Problem.Status, it.AttemptCount,
			orDash(it.LastVerdict), orDash(it.LastField),
			search.Printable(orDash(model)), search.Printable(it.Problem.RootCause))
	}
	return nil
}

func renderAutoSIMShow(w io.Writer, d brain.SIMProblemDetail, asJSON bool) error {
	if asJSON {
		if d.Attempts == nil {
			d.Attempts = []brain.SIMAttempt{}
		}
		return writePrettyJSON(w, d)
	}
	p := d.Problem
	fmt.Fprintf(w, "%s  %s  mechanism=%s  signal=%s  provenance=%s\n",
		p.ID, p.Status, orDash(p.MechanismClass), search.Printable(orDash(p.SignalID)), p.Provenance)
	fmt.Fprintf(w, "root_cause: %s\n", search.Printable(p.RootCause))
	if p.TargetPaths != "" {
		fmt.Fprintf(w, "target_paths: %s\n", search.Printable(p.TargetPaths))
	}
	fmt.Fprintf(w, "opened: %s  updated: %s  harness=%s  model=%s\n",
		search.Printable(p.OpenedAt), search.Printable(p.UpdatedAt), search.Printable(orDash(p.OpenedHarness)), search.Printable(orDash(p.OpenedModel)))
	if len(d.Aliases) > 0 {
		fmt.Fprintf(w, "aliases: %s\n", strings.Join(d.Aliases, ", "))
	}
	fmt.Fprintf(w, "attempts (%d):\n", len(d.Attempts))
	for _, a := range d.Attempts {
		renderAutoSIMAttempt(w, a)
	}
	return nil
}

func renderAutoSIMAttempt(w io.Writer, a brain.SIMAttempt) {
	fmt.Fprintf(w, "  #%d  %s  source=%s  verdict=%s  field=%s  provenance=%s\n",
		a.ID, search.Printable(a.CreatedAt), a.SourceKind, a.EvalVerdict, a.FieldStatus, a.Provenance)
	fmt.Fprintf(w, "    what: %s\n", search.Printable(a.What))
	if a.ProposerModel != "" || a.PolicyModel != "" {
		fmt.Fprintf(w, "    models: proposer=%s policy=%s\n",
			search.Printable(orDash(a.ProposerModel)), search.Printable(orDash(a.PolicyModel)))
	}
	if a.CommitSHA != "" || a.ParentSHA != "" || a.ProposalID != "" || a.HarnessVersion != "" {
		fmt.Fprintf(w, "    commit=%s parent=%s proposal=%s harness=%s\n",
			orDash(a.CommitSHA), orDash(a.ParentSHA), search.Printable(orDash(a.ProposalID)), search.Printable(orDash(a.HarnessVersion)))
	}
	if len(a.FixedRows) > 0 || len(a.MissedRows) > 0 || len(a.RegressedRows) > 0 {
		fmt.Fprintf(w, "    rows: fixed=[%s] missed=[%s] regressed=[%s]\n",
			search.Printable(strings.Join(a.FixedRows, ",")),
			search.Printable(strings.Join(a.MissedRows, ",")),
			search.Printable(strings.Join(a.RegressedRows, ",")))
	}
	if len(a.FieldModels) > 0 {
		parts := make([]string, 0, len(a.FieldModels))
		for _, m := range a.FieldModels {
			parts = append(parts, fmt.Sprintf("%s:%s(%d)", search.Printable(m.Model), orDash(m.Status), m.Sessions))
		}
		fmt.Fprintf(w, "    field_models: %s\n", strings.Join(parts, ", "))
	}
}
