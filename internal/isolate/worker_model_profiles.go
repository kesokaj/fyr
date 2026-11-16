package isolate

import (
	"context"
	"errors"
	"strings"

	"github.com/kesokaj/fyr/internal/models"
)

// ResolveAutoRoleAndTier determines the best (role, tier) for a Kickoff based
// on its Files scope and Prompt complexity.
func ResolveAutoRoleAndTier(k Kickoff) (role, tier, rationale string) {
	return models.ResolveScopeRoleAndTier(k.Files, k.Text)
}

// resolveAutoModels resolves Kickoffs with Model: "auto" (or empty when
// default/auto-pick applies) and Judge-Model: "auto" before preflight model
// verification, and rejects interactive-only orchestrator IDs in headless work.
func resolveAutoModels(_ context.Context, o *WorkerOptions, ks []Kickoff, listed map[string]bool, ids []string) error {
	for i := range ks {
		if models.IsInteractiveOnlyID(ks[i].Model) {
			return fmtErrInteractiveModel(ks[i].Path, ks[i].Model)
		}
		if strings.EqualFold(strings.TrimSpace(ks[i].Model), "auto") {
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
	if models.IsInteractiveOnlyID(o.Model) {
		return fmtErrInteractiveModel("--model", o.Model)
	}
	if strings.EqualFold(strings.TrimSpace(o.Model), "auto") {
		var sample Kickoff
		if len(ks) > 0 {
			sample = ks[0]
		}
		role, tier, _ := ResolveAutoRoleAndTier(sample)
		if picked := models.PickFromListedIDs(o.Home, listed, ids, role, tier); picked != "" {
			o.Model = picked
		}
	}
	return nil
}

func fmtErrInteractiveModel(where, model string) error {
	if where == "--model" {
		return errors.New("--model " + model + ": interactive orchestrator cannot run headless; use --model auto or a concrete builder model")
	}
	return errors.New("the Kickoff " + where + " names " + model + ", which is an interactive orchestrator and cannot run headless; use Model: auto or a concrete builder model")
}
