package isolate

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
func checkModels(ctx context.Context, o *WorkerOptions, ks []Kickoff) error {
	named := o.Model != ""
	for _, k := range ks {
		named = named || k.Model != "" || k.JudgeModel != ""
	}
	if !named {
		return nil
	}
	// With --runner docker refusal 10 was not made: the check is the host's.
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
	listed := map[string]bool{}
	for _, id := range ids {
		listed[id] = true
	}
	if err := resolveAutoModels(o, ks, listed, ids); err != nil {
		return err
	}
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
