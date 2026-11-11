package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type featureWorkersDocs struct {
	feature string
	rows    map[string]Row
}

func cutSectionBetween(text, start, end string) (string, bool) {
	_, after, ok := strings.Cut(text, start)
	if !ok {
		return "", false
	}
	if end == "" {
		return after, true
	}
	sec, _, ok := strings.Cut(after, end)
	return sec, ok
}

func checkTemplateWorkersByDefault(feature string) []string {
	tmpl, ok := cutSectionBetween(feature, "```markdown\n# Plan —", "\n```")
	if !ok {
		return []string{"skills/fyr-feature/SKILL.md missing Plan markdown template block"}
	}
	errs := checkDocPhrases("skills/fyr-feature/SKILL.md#template", tmpl,
		[]string{
			"files: <required on multi-phase plans",
			"fyr models --pick builder",
			"base-check: <required on multi-phase `workers: yes`",
			"judge: <required on multi-phase `workers: yes`",
			"fyr models --pick judge",
		},
		[]string{
			"files: <optional:",
			"<optional workers-only",
			"agy models",
		},
	)
	if strings.Count(tmpl, "\nfiles:") < 2 {
		errs = append(errs, "skills/fyr-feature/SKILL.md#template must show files: on both Phase 1 and Phase 2")
	}
	return errs
}

func checkStep2AndStep3WorkersByDefault(feature string) []string {
	var errs []string
	step2, ok := cutSectionBetween(feature, "## Step 2:", "\n## Step 3:")
	if !ok {
		return []string{"skills/fyr-feature/SKILL.md missing ## Step 2: .. ## Step 3: section"}
	}
	errs = append(errs, checkDocPhrases("skills/fyr-feature/SKILL.md#Step2", step2,
		[]string{
			"fyr models --pick builder",
			"fyr models --pick judge",
			"do not run `agy models`",
		},
		[]string{
			"A phase may carry one `files:` line",
			"may also carry optional worker header lines",
		},
	)...)
	step3, ok := cutSectionBetween(feature, "## Step 3:", "\n## Step 4:")
	if !ok {
		return append(errs, "skills/fyr-feature/SKILL.md missing ## Step 3: .. ## Step 4: section")
	}
	errs = append(errs, checkDocPhrases("skills/fyr-feature/SKILL.md#Step3", step3,
		[]string{
			"D<n> — <date> — workers: yes | no, <why> — by: planner",
			"defaults to `workers: yes`",
			"concrete technical reason",
		},
		[]string{
			"yes when two or more phases have only `who: AI` boxes, depend on no unsettled `Open:` row, and carry pairwise disjoint `files:` lines",
		},
	)...)
	return errs
}

func featureWorkersProblems(d featureWorkersDocs) []string {
	errs := checkTemplateWorkersByDefault(d.feature)
	errs = append(errs, checkStep2AndStep3WorkersByDefault(d.feature)...)
	r, ok := d.rows["feature"]
	if !ok {
		return append(errs, "GUIDE-eval.md missing row feature")
	}
	if _, wide := r.Wide(); !wide {
		errs = append(errs, "GUIDE-eval.md row feature must use wide grants for multi-phase fyr models planning")
	}
	errs = append(errs, checkDocPhrases("GUIDE-eval.md#feature.Check", r.Check,
		[]string{"^files:", "^model:", "^base-check:", "^judge:", "^judge-model:", "workers: yes", "models.json"},
		nil,
	)...)
	return errs
}

func assertFeatureWorkersMustFailMutations(t *testing.T, good featureWorkersDocs) {
	t.Helper()
	cloneFeatureRow := func(fn func(*Row)) map[string]Row {
		m := make(map[string]Row, len(good.rows))
		for k, v := range good.rows {
			m[k] = v
		}
		r := m["feature"]
		fn(&r)
		m["feature"] = r
		return m
	}
	mutations := []struct {
		name string
		mut  func(*featureWorkersDocs)
	}{
		{"files optional in template", func(d *featureWorkersDocs) {
			d.feature = strings.Replace(d.feature, "files: <required on multi-phase plans", "files: <optional:", 1)
		}},
		{"missing builder pick", func(d *featureWorkersDocs) {
			d.feature = strings.ReplaceAll(d.feature, "fyr models --pick builder", "builder-model")
		}},
		{"missing judge pick", func(d *featureWorkersDocs) {
			d.feature = strings.ReplaceAll(d.feature, "fyr models --pick judge", "judge-model")
		}},
		{"base-check marked optional", func(d *featureWorkersDocs) {
			d.feature = strings.Replace(d.feature, "base-check: <required on multi-phase `workers: yes`", "base-check: <optional workers-only", 1)
		}},
		{"agy models in template", func(d *featureWorkersDocs) {
			d.feature = strings.Replace(d.feature, "### Phase 2", "model: agy models\n### Phase 2", 1)
		}},
		{"missing agy models prohibition", func(d *featureWorkersDocs) {
			d.feature = strings.ReplaceAll(d.feature, "do not run `agy models`", "skip model lookup")
		}},
		{"missing technical-reason rule", func(d *featureWorkersDocs) {
			d.feature = strings.ReplaceAll(d.feature, "concrete technical reason", "preference")
		}},
		{"feature row named grants", func(d *featureWorkersDocs) {
			d.rows = cloneFeatureRow(func(r *Row) { r.Grants = "named" })
		}},
		{"feature check omits workers: yes", func(d *featureWorkersDocs) {
			d.rows = cloneFeatureRow(func(r *Row) { r.Check = strings.ReplaceAll(r.Check, "workers: yes", "decisions") })
		}},
	}
	for _, tc := range mutations {
		cp := good
		tc.mut(&cp)
		if len(featureWorkersProblems(cp)) == 0 {
			t.Errorf("must-fail mutation %q unexpectedly passed featureWorkersProblems", tc.name)
		}
	}
}

// TestFeaturePlanWorkersByDefault pins Phase 3 of PLAN-2026-11-11-0640-plan-fyr-autonomy-and-full-feature-use:
// multi-phase plans require files:, default workers to yes (with a concrete
// technical reason required for no), and populate model:, base-check:, judge:,
// and judge-model: per worker phase via fyr models --pick.
func TestFeaturePlanWorkersByDefault(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "skills", "fyr-feature", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	list, found, err := ReadTrackedSuite(root, false)
	if err != nil || !found {
		t.Fatalf("ReadTrackedSuite: found=%v err=%v", found, err)
	}
	rows := make(map[string]Row, len(list))
	for _, r := range list {
		rows[r.ID] = r
	}
	docs := featureWorkersDocs{feature: string(b), rows: rows}
	if errs := featureWorkersProblems(docs); len(errs) > 0 {
		for _, e := range errs {
			t.Error(e)
		}
		return
	}
	assertFeatureWorkersMustFailMutations(t, docs)
}
