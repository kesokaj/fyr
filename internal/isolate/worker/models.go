package worker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/kesokaj/fyr/internal/models"
)

// checkModels is refusal 21: when the run names a model, in a
// Kickoff's Model: line or --model, every one must be an ID the host's agy
// models lists. A failed call or an empty list refuses too: fail closed
// (D103 item 3). A run naming none makes no call.
func checkModels(ctx context.Context, o *Options, ks []Kickoff) error {
	if !hasNamedModel(o, ks) {
		return nil
	}
	agy, err := exec.LookPath("agy")
	if err != nil {
		return errors.New("the model check runs the host's agy: agy not found on PATH")
	}
	ids, err := models.ListModels(ctx, agy)
	if err != nil {
		return fmt.Errorf("the model check: agy models: %v", err)
	}
	if len(ids) == 0 {
		return errors.New("the model check: agy models listed no model")
	}
	listed := make(map[string]bool, len(ids))
	for _, id := range ids {
		listed[id] = true
	}
	if err := resolveAutoModels(o, ks, listed, ids); err != nil {
		return err
	}
	return validateListedModels(o, ks, listed, ids)
}

func hasNamedModel(o *Options, ks []Kickoff) bool {
	if o.Model != "" {
		return true
	}
	for _, k := range ks {
		if k.Model != "" || k.JudgeModel != "" {
			return true
		}
	}
	return false
}

func validateListedModels(o *Options, ks []Kickoff, listed map[string]bool, ids []string) error {
	all := strings.Join(ids, ", ")
	for _, k := range ks {
		if k.Model != "" && !listed[k.Model] {
			return fmt.Errorf("the Kickoff %s names the model %s, which agy models does not list: %s", k.Path, k.Model, all)
		}
		if k.JudgeModel != "" && !listed[k.JudgeModel] {
			return fmt.Errorf("the Kickoff %s names the judge model %s, which agy models does not list: %s", k.Path, k.JudgeModel, all)
		}
	}
	if o.Model != "" && !listed[o.Model] {
		return fmt.Errorf("--model %s: agy models does not list it: %s", o.Model, all)
	}
	return nil
}

// resolveAutoModels resolves Kickoffs with Model: "auto" (or empty when
// default/auto-pick applies) and Judge-Model: "auto" before preflight model
// verification, and rejects interactive-only orchestrator IDs in headless work.
func resolveAutoModels(o *Options, ks []Kickoff, listed map[string]bool, ids []string) error {
	wantAuto := strings.EqualFold(strings.TrimSpace(o.Model), "auto")
	for i := range ks {
		if models.IsInteractiveOnlyID(ks[i].Model) {
			return fmtErrInteractiveModel(ks[i].Path, ks[i].Model)
		}
		if models.IsInteractiveOnlyID(ks[i].JudgeModel) {
			return fmtErrInteractiveModel(ks[i].Path, ks[i].JudgeModel)
		}
		if strings.EqualFold(strings.TrimSpace(ks[i].Model), "auto") || (wantAuto && ks[i].Model == "") {
			picked, _, err := models.PickAutoModelForScope(o.Home, ks[i].Files, ks[i].Text, listed, ids)
			if err != nil {
				return err
			}
			ks[i].Model = picked
		}
		if strings.EqualFold(strings.TrimSpace(ks[i].JudgeModel), "auto") {
			ks[i].JudgeModel = "" // let runJudge pick cross-family dynamically
		}
	}
	return resolveOptionAutoModel(o, ks, listed, ids)
}

func resolveOptionAutoModel(o *Options, ks []Kickoff, listed map[string]bool, ids []string) error {
	if models.IsInteractiveOnlyID(o.Model) {
		return fmtErrInteractiveModel("--model", o.Model)
	}
	if !strings.EqualFold(strings.TrimSpace(o.Model), "auto") {
		return nil
	}
	var sample Kickoff
	if len(ks) > 0 {
		sample = ks[0]
	}
	picked, _, err := models.PickAutoModelForScope(o.Home, sample.Files, sample.Text, listed, ids)
	if err != nil {
		return err
	}
	o.Model = picked
	return nil
}

func fmtErrInteractiveModel(where, model string) error {
	if where == "--model" {
		return errors.New("--model " + model + ": interactive orchestrator cannot run headless; use --model auto or a concrete builder model")
	}
	return errors.New("the Kickoff " + where + " names " + model + ", which is an interactive orchestrator and cannot run headless; use Model: auto or a concrete builder model")
}
