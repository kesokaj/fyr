package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type autonomyDocs struct {
	context       string
	feature       string
	work          string
	tasks         string
	sessionSizing string
	rows          map[string]Row
}

func checkDocPhrases(name, text string, must, forbid []string) []string {
	var errs []string
	for _, want := range must {
		if !strings.Contains(text, want) {
			errs = append(errs, name+" missing required phrase "+want)
		}
	}
	for _, bad := range forbid {
		if strings.Contains(text, bad) {
			errs = append(errs, name+" still contains build-time wait-point "+bad)
		}
	}
	return errs
}

func autonomyProblems(d autonomyDocs) []string {
	var errs []string
	errs = append(errs, checkDocPhrases("context/AGENTS.md", d.context,
		[]string{"decides in the pre-build interview; never asked during build"},
		[]string{"wants to be asked often"},
	)...)
	errs = append(errs, checkDocPhrases("skills/fyr-feature/SKILL.md", d.feature,
		[]string{
			"pre-build interview",
			"grilling",
			"never stops to ask",
			"by: implementer",
			"closing handoff",
		},
		[]string{
			"Three points always wait for the user",
			"Confirm or correct.",
			"At a cap, ask",
			"the question is asked then, as this step's gate",
		},
	)...)
	errs = append(errs, checkDocPhrases("skills/fyr-work/SKILL.md", d.work,
		[]string{
			"never asked during the loop",
			"ADR-517",
			"Base-Check:",
			"Judge:",
			"Judge-Model:",
			"(`files:`, `model:`, `base-check:`, `judge:`, `judge-model:`, `after:`, `who:`, `#`)",
			"fyr metrics trajectory --project . --limit 1",
			"KNEE_EXCEEDED",
			"180,000",
			"safety FAIL",
		},
		[]string{
			"waits for the operator's `go`",
			"stop and wait for the operator's `go`",
			"has no context-based stop",
		},
	)...)
	errs = append(errs, checkDocPhrases("skills/fyr-work/references/tasks.md", d.tasks,
		[]string{"ADR-517", "fyr metrics trajectory", "KNEE_EXCEEDED"},
		[]string{"that table always waits for the go"},
	)...)
	errs = append(errs, checkDocPhrases("rules/session-sizing.md", d.sessionSizing,
		[]string{"fyr metrics trajectory", "KNEE_EXCEEDED", "180,000", "zero build-time questions"},
		[]string{"the model cannot see its own fill"},
	)...)
	if r, ok := d.rows["autonomy"]; !ok || r.Check == "" || r.Prompt2 != "" {
		errs = append(errs, "GUIDE-eval.md missing single-turn capability row autonomy")
	}
	if r, ok := d.rows["work-start-host"]; !ok || strings.HasPrefix(strings.TrimSpace(r.Prompt2), "go.") {
		errs = append(errs, "GUIDE-eval.md work-start-host still pauses mid-build for a bare go. turn")
	}
	return errs
}

func loadAutonomyDocs(t *testing.T, root string) autonomyDocs {
	t.Helper()
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}
	list, found, err := ReadTrackedSuite(root, false)
	if err != nil || !found {
		t.Fatalf("ReadTrackedSuite: found=%v err=%v", found, err)
	}
	rows := make(map[string]Row, len(list))
	for _, r := range list {
		rows[r.ID] = r
	}
	return autonomyDocs{
		context:       read("context/AGENTS.md"),
		feature:       read("skills/fyr-feature/SKILL.md"),
		work:          read("skills/fyr-work/SKILL.md"),
		tasks:         read("skills/fyr-work/references/tasks.md"),
		sessionSizing: read("rules/session-sizing.md"),
		rows:          rows,
	}
}

func assertAutonomyMustFailMutations(t *testing.T, good autonomyDocs) {
	t.Helper()
	cloneRows := func(skip string, prompt2 string) map[string]Row {
		m := make(map[string]Row, len(good.rows))
		for k, v := range good.rows {
			if k != skip {
				m[k] = v
			}
		}
		if prompt2 != "" {
			r := m["work-start-host"]
			r.Prompt2 = prompt2
			m["work-start-host"] = r
		}
		return m
	}
	mutations := []struct {
		name string
		mut  func(d *autonomyDocs)
	}{
		{"context asks often", func(d *autonomyDocs) { d.context += "\nwants to be asked often" }},
		{"feature three gates", func(d *autonomyDocs) { d.feature += "\nThree points always wait for the user" }},
		{"feature cap ask", func(d *autonomyDocs) { d.feature += "\nAt a cap, ask" }},
		{"work host go wait", func(d *autonomyDocs) { d.work += "\nstop and wait for the operator's `go`" }},
		{"work step 8 prefix guard", func(d *autonomyDocs) {
			d.work = strings.ReplaceAll(d.work, "`base-check:`, `judge:`, `judge-model:`, ", "")
		}},
		{"work missing knee check", func(d *autonomyDocs) { d.work = strings.ReplaceAll(d.work, "KNEE_EXCEEDED", "") }},
		{"tasks host go wait", func(d *autonomyDocs) { d.tasks += "\nthat table always waits for the go" }},
		{"session sizing missing knee", func(d *autonomyDocs) { d.sessionSizing = strings.ReplaceAll(d.sessionSizing, "180,000", "") }},
		{"missing autonomy row", func(d *autonomyDocs) { d.rows = cloneRows("autonomy", "") }},
		{"work-start-host bare go", func(d *autonomyDocs) { d.rows = cloneRows("", "go. Run the wave.") }},
	}
	for _, tc := range mutations {
		cp := good
		tc.mut(&cp)
		if len(autonomyProblems(cp)) == 0 {
			t.Errorf("must-fail mutation %q unexpectedly passed autonomyProblems", tc.name)
		}
	}
}

// TestAutonomyNoBuildTimeQuestions pins ADR-517 across context, workflow skills,
// session-sizing, and GUIDE-eval: questions happen only in the pre-build
// interview, and no build-time wait-point remains except a safety FAIL.
func TestAutonomyNoBuildTimeQuestions(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	docs := loadAutonomyDocs(t, root)
	if errs := autonomyProblems(docs); len(errs) > 0 {
		for _, e := range errs {
			t.Error(e)
		}
		return
	}
	assertAutonomyMustFailMutations(t, docs)
}
