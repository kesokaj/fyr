package isolate

import (
	"errors"
	"strings"

	"github.com/kesokaj/fyr/internal/models"
)

// resolveAutoModels resolves Kickoffs with Model: "auto" (or empty when
// default/auto-pick applies) and Judge-Model: "auto" before preflight model
// verification, and rejects interactive-only orchestrator IDs in headless work.
func resolveAutoModels(o *WorkerOptions, ks []Kickoff, listed map[string]bool, ids []string) error {
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

func resolveOptionAutoModel(o *WorkerOptions, ks []Kickoff, listed map[string]bool, ids []string) error {
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
